#requires -Version 7.0
[CmdletBinding()]
param(
    [switch]$Release,
    [switch]$Sign,
    [switch]$SkipBuild,
    [string]$AppDirectory,
    [string]$OutputDirectory,
    [string]$QemuSourceArchive
)
. (Join-Path $PSScriptRoot 'windows-common.ps1')
$repository = Split-Path $PSScriptRoot -Parent
$version = Get-CageVersion $repository
$signed = $Release -or $Sign
if (-not $AppDirectory) { $AppDirectory = Join-Path $repository '_output/windows-amd64/app' }
if (-not $OutputDirectory) { $OutputDirectory = Join-Path $repository "_output/releases/$version/windows" }
$AppDirectory = [IO.Path]::GetFullPath($AppDirectory)
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
if (Test-Path -LiteralPath (Join-Path $AppDirectory 'CageData')) { throw 'App directory contains CageData user data. Package a clean build directory; do not package an installed application.' }
if (-not $SkipBuild) { & (Join-Path $PSScriptRoot 'build-windows.ps1') -Release:$Release -Sign:$Sign -OutputDirectory $AppDirectory }
if ((Get-Content -LiteralPath (Join-Path $AppDirectory 'Cage.install-id') -Raw) -ne 'io.github.lee-xeo.claude-cage') { throw 'Invalid Cage app directory.' }
if ((Get-Content -LiteralPath (Join-Path $AppDirectory 'version.txt') -Raw).Trim() -ne $version) { throw 'App version differs from VERSION; rebuild before packaging.' }
$makeNSIS = Get-BuildCommand @('makensis.exe', 'makensis')
$sevenZip = Get-BuildCommand @('7z.exe', '7zz', '7z')
if ($IsWindows) {
    if ([Diagnostics.FileVersionInfo]::GetVersionInfo((Join-Path $AppDirectory 'Cage.exe')).ProductVersion -ne $version) { throw 'Cage.exe version differs from VERSION; rebuild before packaging.' }
    Invoke-BuildTool (Join-Path $AppDirectory 'resources/cage.exe') @('verify-vm-assets')
    Invoke-BuildTool (Join-Path $AppDirectory 'resources/cage.exe') @('verify-web-assets')
    if ($SkipBuild) { Assert-WindowsWorkbench $AppDirectory }
} else {
    $metadata = & $sevenZip 'l' '-tPE' (Join-Path $AppDirectory 'Cage.exe')
    if ($LASTEXITCODE -ne 0 -or -not ($metadata -match ('^ProductVersion: ' + [regex]::Escape($version) + '$'))) { throw 'Cage.exe version differs from VERSION; rebuild before packaging.' }
}
$signTool = $null
if ($signed) {
    Assert-WindowsSigningConfiguration
    if ($env:CAGE_WINDOWS_CERT_SHA1 -notmatch '^[0-9A-Fa-f]{40}$') { throw 'Build and verify the signed app before packaging.' }
    if (Test-Path -LiteralPath (Join-Path $AppDirectory 'DEVELOPMENT-BUILD.txt')) { throw 'Rebuild with build-windows.ps1 -Sign or -Release before packaging a signed installer.' }
    if ($Release -and (-not $QemuSourceArchive -or -not (Test-Path -LiteralPath $QemuSourceArchive -PathType Leaf))) {
        throw 'Release packaging requires -QemuSourceArchive containing the exact QEMU, firmware and linked library sources. Generate it from the built runtime with bundle-qemu-sources.sh.'
    }
    if ($Release) { Invoke-BuildTool $sevenZip @('t', '-bso0', '-bsp0', $QemuSourceArchive) }
    $signTool = Get-SigningTool
    foreach ($file in @('Cage.exe', 'resources/cage.exe', 'resources/mihomo.exe')) {
        $signature = Get-AuthenticodeSignature -LiteralPath (Join-Path $AppDirectory $file)
        if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $env:CAGE_WINDOWS_CERT_SHA1) { throw "Invalid release signature: $file" }
    }
    $webview = Get-AuthenticodeSignature -LiteralPath (Join-Path $AppDirectory 'resources/runtime/MicrosoftEdgeWebView2RuntimeInstallerX64.exe')
    if ($webview.Status -ne 'Valid' -or $webview.SignerCertificate.Subject -notmatch 'O=Microsoft Corporation') { throw 'WebView2 installer must have a valid Microsoft signature.' }
}
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
# Verify source material before the expensive installer compression.
$dependencies = Get-Content -LiteralPath (Join-Path $repository 'packaging/windows/dependencies.json') -Raw | ConvertFrom-Json
$qemuTar = Get-VerifiedGitArchive $dependencies.qemuSource (Join-Path $repository '_output/windows-deps')
$suffix = if ($Release) { '' } elseif ($Sign) { '-test-signed' } else { '-unsigned' }
$artifact = Join-Path $OutputDirectory "Cage-$version-windows-amd64-setup$suffix.exe"
$manifest = Join-Path $AppDirectory 'Cage.install-files.json'
$files = @(Get-ChildItem -LiteralPath $AppDirectory -File -Recurse -Force | Where-Object FullName -ne $manifest)
$ownership = [Collections.Generic.List[object]]::new()
foreach ($file in $files) {
    $relative = [IO.Path]::GetRelativePath($AppDirectory, $file.FullName).Replace('\', '/')
    $ownership.Add(@{ path = $relative; sha256 = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant() })
}
@{ version = 1; files = @($ownership | Sort-Object path) } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $manifest -Encoding utf8
$numericVersion = ($version -split '-')[0] + '.0'
$prefix = if ($IsWindows) { '/' } else { '-' }
# Source files are UTF-8; Windows ACP must not reinterpret Chinese UI text.
$arguments = @("${prefix}V3", "${prefix}INPUTCHARSET", 'UTF8', "${prefix}DAPP_DIR=$AppDirectory", "${prefix}DVERSION=$version", "${prefix}DVERSION_NUMERIC=$numericVersion", "${prefix}DOUTPUT_FILE=$artifact", "${prefix}DFILE_MANIFEST=$manifest")
if ($signed) {
    $arguments += @("${prefix}DSIGN_PWSH=$((Get-Process -Id $PID).Path)", "${prefix}DSIGN_SCRIPT=$(Join-Path $PSScriptRoot 'sign-windows-file.ps1')")
}
$arguments += (Join-Path $repository 'packaging/windows/Cage.nsi')
Invoke-BuildTool $makeNSIS $arguments
Invoke-BuildTool (Get-BuildCommand @('node.exe', 'node')) @(
    (Join-Path $PSScriptRoot 'verify-windows-installer-icon.mjs'),
    $artifact, (Join-Path $repository 'packaging/windows/Cage.ico'), $sevenZip
)
if ($signed) { Sign-CageFile $artifact $signTool }
if ((Get-Item -LiteralPath $artifact).Length -gt 512MB) { throw 'Installer exceeds the application updater 512 MiB limit.' }

# Source materials travel beside the binary, not inside the application directory.
$coreSource = Join-Path $OutputDirectory "mihomo-$($dependencies.mihomo.version)-source.tar.gz"
$verifiedCoreSource = Get-VerifiedDependency $dependencies.mihomoSource (Join-Path $repository '_output/windows-deps')
Copy-Item -LiteralPath $verifiedCoreSource -Destination $coreSource -Force
Invoke-BuildTool $sevenZip @('t', '-bso0', '-bsp0', $coreSource)
$releaseFiles = @($artifact, $coreSource)
$qemuSource = Join-Path $OutputDirectory ($dependencies.qemuSource.file + '.gz')
if (Test-Path -LiteralPath $qemuSource) { Remove-Item -LiteralPath $qemuSource }
Invoke-BuildTool $sevenZip @('a', '-tgzip', '-mx=9', '-bso0', '-bsp0', $qemuSource, $qemuTar)
Invoke-BuildTool $sevenZip @('t', '-bso0', '-bsp0', $qemuSource)
$releaseFiles += $qemuSource
$qemuRuntime = Join-Path $AppDirectory 'resources/runtime/qemu'
if (Test-Path (Join-Path $qemuRuntime 'cage-build.json')) {
    $keymapArchive = Get-VerifiedDependency $dependencies.qemuKeycodemap (Join-Path $repository '_output/windows-deps')
    $keymapSource = Join-Path $OutputDirectory ('qemu-keycodemapdb-' + $dependencies.qemuKeycodemap.version + '-source.tar.gz')
    Copy-Item $keymapArchive $keymapSource -Force
    $changesSource = Join-Path $OutputDirectory ('qemu-' + $dependencies.qemu.version + '-cage-changes-source.tar.gz')
    Invoke-BuildTool 'tar' @('-czf', $changesSource, '-C', $repository, 'patches/qemu', 'scripts/build-qemu-windows.ps1',
        'scripts/build-qemu-windows.sh', '-C', $qemuRuntime, 'cage-build.json', 'build-packages.txt')
    $releaseFiles += @($keymapSource, $changesSource)
}
if ($Release) {
    $sourceExtension = if ($QemuSourceArchive.EndsWith('.tar.gz', [StringComparison]::OrdinalIgnoreCase)) { '.tar.gz' } else { [IO.Path]::GetExtension($QemuSourceArchive) }
    $qemuSourceTarget = Join-Path $OutputDirectory ('qemu-' + $dependencies.qemu.version + '-windows-full-source' + $sourceExtension)
    Copy-Item -LiteralPath $QemuSourceArchive -Destination $qemuSourceTarget -Force
    $releaseFiles += $qemuSourceTarget
}
$sums = foreach ($file in $releaseFiles) { (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant() + '  ' + [IO.Path]::GetFileName($file) }
[IO.File]::WriteAllLines((Join-Path $OutputDirectory 'SHA256SUMS'), $sums)
Write-Host "Windows installer: $artifact"
if (-not $signed) { Write-Warning 'Unsigned development installer. Do not publish it as an automatic update; Windows VM and installation acceptance are still required.' }
