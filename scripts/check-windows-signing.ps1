#requires -Version 7.0
# Explicit signing or read-only credential verification. No package, version or release writes.
[CmdletBinding()]
param([ValidateSet('sign', 'credential-info')][string]$Mode = 'sign')
. (Join-Path $PSScriptRoot 'windows-common.ps1')
if (-not $IsWindows) { throw 'This verification requires Windows trust APIs.' }
if ($env:GITHUB_ACTIONS -ne 'true' -or $env:GITHUB_EVENT_NAME -ne 'workflow_dispatch' -or
    $env:GITHUB_REPOSITORY -ne 'Lee-xeo/Claude-Cage' -or
    $env:GITHUB_REF -ne 'refs/heads/main') {
    throw 'Run the explicit main-branch signing verification workflow.'
}
if ($env:CAGE_WINDOWS_SIGNING_PROVIDER -ne 'esigner') { throw 'Select esigner for this verification.' }
if ($Mode -eq 'credential-info') {
    try { Invoke-BuildTool 'node' @((Join-Path $PSScriptRoot 'esigner.mjs'), 'diagnose') }
    finally { Remove-Item Env:CAGE_ESIGNER_PASSWORD, Env:CAGE_ESIGNER_TOTP_SECRET -ErrorAction SilentlyContinue }
    return
}
foreach ($name in @('CAGE_ESIGNER_USERNAME', 'CAGE_ESIGNER_PASSWORD', 'CAGE_ESIGNER_CREDENTIAL_ID', 'CAGE_ESIGNER_CERTIFICATE_SERIAL', 'CAGE_ESIGNER_TOTP_SECRET')) {
    if (-not [Environment]::GetEnvironmentVariable($name)) { throw "Missing cage-release setting: $name" }
}
Assert-WindowsSigningConfiguration
$repository = Split-Path $PSScriptRoot -Parent
$reportDirectory = Join-Path $repository '_output/signing-check'
$stage = Join-Path ([IO.Path]::GetTempPath()) ('cage-signing-check-' + [guid]::NewGuid().ToString('N'))
$report = [ordered]@{ status = 'failed'; commit = $env:GITHUB_SHA; windowsTrustTested = $false; installationTested = $false }
$savedCertificate = $env:CAGE_WINDOWS_CERT_SHA1
try {
    New-Item -ItemType Directory -Path $stage | Out-Null
    $source = Join-Path $stage 'SigningProbe.cs'
    @'
using System;
public static class SigningProbe {
    public static void Main() { Console.WriteLine("Cage code signing verification fixture."); }
}
'@ | Set-Content -LiteralPath $source -Encoding utf8
    $compiler = Join-Path $env:WINDIR 'Microsoft.NET/Framework64/v4.0.30319/csc.exe'
    $executable = Join-Path $stage 'probe.exe'
    Invoke-BuildTool $compiler @('/nologo', '/target:exe', "/out:$executable", $source)
    # NSIS hands its signing hook a PE file without an extension. Exercise that path.
    $probe = Join-Path $stage 'uninstaller-probe'
    Move-Item -LiteralPath $executable -Destination $probe
    $report.unsignedSHA256 = (Get-FileHash -LiteralPath $probe -Algorithm SHA256).Hash.ToLowerInvariant()
    $env:CAGE_WINDOWS_CERT_SHA1 = $null
    Sign-CageFile $probe (Get-SigningTool)
    $signature = Get-AuthenticodeSignature -LiteralPath $probe
    $report.windowsTrustTested = $true
    $report.publisher = $signature.SignerCertificate.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::SimpleName, $false)
    $report.certificateSerial = $signature.SignerCertificate.SerialNumber
    $report.certificateThumbprint = $signature.SignerCertificate.Thumbprint
    $report.timestampAuthority = $signature.TimeStamperCertificate.Subject
    $report.signedSHA256 = (Get-FileHash -LiteralPath $probe -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($report.unsignedSHA256 -eq $report.signedSHA256) { throw 'Signing did not change the executable.' }
    $report.status = 'passed'
    Write-Host 'Passed: cloud signing, configured publisher certificate, timestamp and Windows trust.'
} finally {
    $env:CAGE_WINDOWS_CERT_SHA1 = $savedCertificate
    Remove-Item Env:CAGE_ESIGNER_PASSWORD, Env:CAGE_ESIGNER_TOTP_SECRET -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
    New-Item -ItemType Directory -Path $reportDirectory -Force | Out-Null
    $report | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $reportDirectory 'result.json') -Encoding utf8
}
