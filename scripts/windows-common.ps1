# Shared build helpers. PowerShell 7 runs these scripts on Windows, macOS and Linux.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true

function Invoke-BuildTool {
    param([string]$Tool, [string[]]$Arguments)
    & $Tool @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Tool failed with exit code $LASTEXITCODE" }
}

function Get-CageVersion {
    param([string]$Repository)
    $value = (Get-Content -LiteralPath (Join-Path $Repository 'VERSION') -Raw).Trim()
    if ($value -notmatch '^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$') { throw 'Invalid VERSION.' }
    return $value
}

function Get-BuildCommand {
    param([string[]]$Names)
    foreach ($name in $Names) {
        $command = Get-Command $name -ErrorAction SilentlyContinue
        if ($command) { return $command.Source }
    }
    throw "Missing build tool: $($Names -join ' or ')"
}

function Get-VerifiedDependency {
    param($Dependency, [string]$Cache)
    if ($Dependency.url -notlike 'https://*') { throw 'Build dependencies must use HTTPS.' }
    $path = Join-Path $Cache $Dependency.file
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        New-Item -ItemType Directory -Path $Cache -Force | Out-Null
        $partial = "$path.partial"
        try {
            Invoke-WebRequest -Uri $Dependency.url -OutFile $partial -TimeoutSec 900 -MaximumRedirection 5
            Move-Item -LiteralPath $partial -Destination $path -Force
        } finally {
            if (Test-Path -LiteralPath $partial) { Remove-Item -LiteralPath $partial -Force }
        }
    }
    $actual = (Get-FileHash -LiteralPath $path -Algorithm $Dependency.algorithm).Hash
    if ($actual -ne $Dependency.hash) { throw "Dependency checksum mismatch: $($Dependency.file). Remove the cached file and retry." }
    return $path
}

function Expand-BuildArchive {
    param([string]$Archive, [string]$Destination, [string]$SevenZip)
    New-Item -ItemType Directory -Path $Destination -Force | Out-Null
    Invoke-BuildTool $SevenZip @('x', '-y', '-bso0', '-bsp0', "-o$Destination", $Archive)
}

function Get-VerifiedGitArchive {
    param($Dependency, [string]$Cache)
    $archive = Join-Path $Cache $Dependency.file
    if (-not (Test-Path -LiteralPath $archive)) {
        $git = Get-BuildCommand @('git.exe', 'git')
        $checkout = Join-Path $Cache ('source-' + [guid]::NewGuid().ToString('N'))
        $partial = "$archive.partial"
        try {
            Invoke-BuildTool $git @('init', '--bare', '--quiet', $checkout)
            Invoke-BuildTool $git @('--git-dir', $checkout, 'fetch', '--depth=1', $Dependency.repository, $Dependency.commit)
            $actualCommit = & $git '--git-dir' $checkout 'rev-parse' 'FETCH_HEAD'
            if ($LASTEXITCODE -ne 0 -or $actualCommit -ne $Dependency.commit) { throw 'Fetched source commit does not match the pinned QEMU build.' }
            # Git for Windows defaults can change archive bytes. Keep the
            # pinned source identical to the reviewed POSIX-generated archive.
            Invoke-BuildTool $git @('--git-dir', $checkout, '-c', 'core.autocrlf=false', '-c', 'tar.umask=0002', 'archive', '--format=tar', "--prefix=$($Dependency.prefix)", "--output=$partial", $Dependency.commit)
            $actualHash = (Get-FileHash -LiteralPath $partial -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($actualHash -ne $Dependency.hash) { throw "Source archive checksum mismatch: $($Dependency.file); expected $($Dependency.hash), actual $actualHash" }
            Move-Item -LiteralPath $partial -Destination $archive
        } finally {
            Remove-Item -LiteralPath $checkout -Recurse -Force -ErrorAction SilentlyContinue
            Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue
        }
    }
    $actualHash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualHash -ne $Dependency.hash) { throw "Source archive checksum mismatch: $($Dependency.file); expected $($Dependency.hash), actual $actualHash. Remove the cached file and retry." }
    return $archive
}

function Get-SigningTool {
    $command = Get-Command signtool.exe -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    if ($IsWindows) {
        $kits = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits/10/bin'
        $candidate = Get-ChildItem -LiteralPath $kits -Directory -ErrorAction SilentlyContinue |
            Sort-Object Name -Descending | ForEach-Object { Join-Path $_.FullName 'x64/signtool.exe' } |
            Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
        if ($candidate) { return $candidate }
    }
    throw 'Release signing requires Windows SDK signtool.exe on Windows.'
}

function Assert-WindowsSigningConfiguration {
    if (-not $IsWindows) { throw 'Trusted Windows build verification requires Windows.' }
    $null = Get-SigningTool
    if ($env:CAGE_WINDOWS_SIGNING_PROVIDER -eq 'esigner') {
        if ($env:CAGE_ESIGNER_CREDENTIAL_ID -notmatch '^[0-9A-Fa-f-]{36}$' -or
            $env:CAGE_ESIGNER_CERTIFICATE_SERIAL -notmatch '^[0-9A-Fa-f]{32,40}$') {
            throw 'eSigner requires CAGE_ESIGNER_CREDENTIAL_ID and the expected CAGE_ESIGNER_CERTIFICATE_SERIAL.'
        }
    } elseif ($env:CAGE_WINDOWS_SIGNING_PROVIDER -and $env:CAGE_WINDOWS_SIGNING_PROVIDER -ne 'certificate-store') {
        throw 'Unknown CAGE_WINDOWS_SIGNING_PROVIDER; use esigner or certificate-store.'
    } elseif ($env:CAGE_WINDOWS_CERT_SHA1 -notmatch '^[0-9A-Fa-f]{40}$') {
        throw 'Certificate-store signing requires CAGE_WINDOWS_CERT_SHA1.'
    }
}

function Sign-CageFile {
    param([string]$Path, [string]$SignTool)
    Assert-WindowsSigningConfiguration
    if ($env:CAGE_WINDOWS_SIGNING_PROVIDER -eq 'esigner') {
        $Path = [IO.Path]::GetFullPath($Path)
        $stage = Join-Path ([IO.Path]::GetTempPath()) ('cage-signed-' + [guid]::NewGuid().ToString('N'))
        try {
            Invoke-BuildTool (Get-BuildCommand @('node.exe', 'node')) @((Join-Path $PSScriptRoot 'esigner.mjs'), 'sign', $Path, '--output-dir', $stage)
            $signed = Join-Path $stage ([IO.Path]::GetFileName($Path))
            Invoke-BuildTool $SignTool @('verify', '/pa', '/all', '/tw', $signed)
            $signature = Get-AuthenticodeSignature -LiteralPath $signed
            if ($signature.Status -ne 'Valid' -or -not $signature.TimeStamperCertificate -or
                $signature.SignerCertificate.SerialNumber -ne $env:CAGE_ESIGNER_CERTIFICATE_SERIAL -or
                ($env:CAGE_WINDOWS_CERT_SHA1 -and $signature.SignerCertificate.Thumbprint -ne $env:CAGE_WINDOWS_CERT_SHA1)) {
                throw 'eSigner output lacks the expected trusted publisher certificate or timestamp. Original file retained.'
            }
            $env:CAGE_WINDOWS_CERT_SHA1 = $signature.SignerCertificate.Thumbprint
            Copy-Item -LiteralPath $signed -Destination $Path -Force
        } finally { if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force } }
    } else {
        Invoke-BuildTool $SignTool @('sign', '/sha1', $env:CAGE_WINDOWS_CERT_SHA1, '/fd', 'SHA256',
            '/tr', 'http://timestamp.digicert.com', '/td', 'SHA256', '/d', 'Cage', $Path)
        Invoke-BuildTool $SignTool @('verify', '/pa', '/all', $Path)
    }
}
