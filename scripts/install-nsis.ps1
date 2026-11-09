#requires -Version 7.0
[CmdletBinding()]
param([string]$Destination = $env:RUNNER_TEMP)

$ErrorActionPreference = 'Stop'
if (-not $Destination) { throw 'Set RUNNER_TEMP or pass -Destination for the NSIS tool directory.' }
$version = '3.13'
$expectedSHA256 = 'ba63dffc4410ee89193e1cb5a41989991bd77c61068da17e3156d136b7b0b3d8'
$url = "https://downloads.sourceforge.net/project/nsis/NSIS%203/$version/nsis-$version.zip"
New-Item -ItemType Directory -Path $Destination -Force | Out-Null
$zip = Join-Path $Destination "nsis-$version.zip"

# SourceForge returns a browser download page (HTTP 200 HTML) for PowerShell's
# default user agent. Use the command-line downloader it redirects to the ZIP.
$curl = if ($IsWindows) { 'curl.exe' } else { 'curl' }
& $curl --fail --location --silent --show-error --retry 3 --connect-timeout 15 --max-time 120 --output $zip --write-out "NSIS download: HTTP %{http_code}, %{content_type}, %{size_download} bytes\n" $url
if ($LASTEXITCODE -ne 0) { throw "NSIS download failed (curl exit $LASTEXITCODE)." }

$stream = [IO.File]::OpenRead($zip)
try {
    $magic = [byte[]]::new(4)
    $read = $stream.Read($magic, 0, 4)
} finally { $stream.Dispose() }
if ($read -ne 4 -or [Convert]::ToHexString($magic) -ne '504B0304') {
    throw 'NSIS download is not a ZIP. SourceForge may have returned an HTML page; the downloaded content will not be installed.'
}
$actualSHA256 = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash
if ($actualSHA256 -ne $expectedSHA256) {
    throw "NSIS checksum mismatch. Expected $expectedSHA256; received $actualSHA256."
}
Expand-Archive -LiteralPath $zip -DestinationPath $Destination -Force
$toolDirectory = Join-Path $Destination "nsis-$version"
if (-not (Test-Path -LiteralPath (Join-Path $toolDirectory 'makensis.exe') -PathType Leaf)) {
    throw 'Verified NSIS ZIP did not contain the expected compiler.'
}
if ($env:GITHUB_PATH) { Add-Content -LiteralPath $env:GITHUB_PATH -Value $toolDirectory }
Write-Host "Verified NSIS $version ZIP and extracted the compiler."
