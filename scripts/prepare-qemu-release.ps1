#requires -Version 7.0
[CmdletBinding()]
param([string]$MsysDirectory = $env:CAGE_MSYS2_ROOT)
. (Join-Path $PSScriptRoot 'windows-common.ps1')
if (-not $IsWindows) { throw 'Prepare the corresponding runtime sources on Windows.' }
$repository = Split-Path $PSScriptRoot -Parent
if (-not $MsysDirectory) {
    $MsysDirectory = @('C:\msys64', 'D:\msys64') | Where-Object { Test-Path (Join-Path $_ 'ucrt64/bin/pkg-config.exe') } | Select-Object -First 1
}
if (-not $MsysDirectory) { throw 'Set CAGE_MSYS2_ROOT to the MSYS2 UCRT64 build environment.' }
$runtime = Join-Path $repository '_output/windows-deps/qemu-cage'
& (Join-Path $PSScriptRoot 'build-qemu-windows.ps1') -OutputDirectory $runtime -MsysDirectory $MsysDirectory
$runtimeIdentity = Get-Content (Join-Path $runtime 'cage-build.json') -Raw | ConvertFrom-Json
$runtimeManifestHash = (Get-FileHash -LiteralPath (Join-Path $runtime 'cage-build.json') -Algorithm SHA256).Hash
$collectorHash = (Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'bundle-qemu-sources.sh') -Algorithm SHA256).Hash + (Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash
$directory = Join-Path $repository '_output/windows-qemu-sources'
$archive = Join-Path $directory 'full-source.tar.gz'
$record = Join-Path $directory 'source-identity.json'
if ((Test-Path -LiteralPath $record) -and (Test-Path -LiteralPath $archive)) {
    $identity = Get-Content -LiteralPath $record -Raw | ConvertFrom-Json
    if ($identity.schema -ne 2 -or $identity.fingerprint -ne $runtimeIdentity.fingerprint -or $identity.runtimeManifestHash -ne $runtimeManifestHash -or $identity.collectorHash -ne $collectorHash -or $identity.sha256 -ne (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash) {
        throw 'Cached QEMU sources differ from their verified runtime identity.'
    }
    Write-Host 'Verified cached QEMU corresponding sources.'
    return
}
New-Item -ItemType Directory -Path $directory -Force | Out-Null
$savedMsystem, $savedChere, $savedNode = $env:MSYSTEM, $env:CHERE_INVOKING, $env:CAGE_NODE
try {
    $env:MSYSTEM = 'UCRT64'; $env:CHERE_INVOKING = '1'
    $env:CAGE_NODE = Get-BuildCommand @('node.exe', 'node')
    Invoke-BuildTool (Join-Path $MsysDirectory 'usr/bin/bash.exe') @('-l',
        (Join-Path $PSScriptRoot 'bundle-qemu-sources.sh'), $repository, $runtime, $archive)
} finally { $env:MSYSTEM = $savedMsystem; $env:CHERE_INVOKING = $savedChere; $env:CAGE_NODE = $savedNode }
$identity = @{ schema = 2; fingerprint = $runtimeIdentity.fingerprint; runtimeManifestHash = $runtimeManifestHash; collectorHash = $collectorHash; sha256 = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash }
$identity | ConvertTo-Json | Set-Content -LiteralPath "$record.partial" -Encoding utf8
Move-Item -LiteralPath "$record.partial" -Destination $record -Force
Write-Host 'Collected and verified QEMU corresponding sources before application signing.'
