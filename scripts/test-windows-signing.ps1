#requires -Version 7.0
# Offline orchestration checks; Windows trust is exercised separately on Windows.
. (Join-Path $PSScriptRoot 'windows-common.ps1')
$stage = Join-Path ([IO.Path]::GetTempPath()) ('cage-signing-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage | Out-Null
$names = @('CAGE_WINDOWS_SIGNING_PROVIDER', 'CAGE_ESIGNER_CERTIFICATE_SERIAL', 'CAGE_WINDOWS_CERT_SHA1')
$saved = @{}
foreach ($name in $names) { $saved[$name] = [Environment]::GetEnvironmentVariable($name) }
try {
    $env:CAGE_WINDOWS_SIGNING_PROVIDER = 'esigner'
    $env:CAGE_ESIGNER_CERTIFICATE_SERIAL = '7' * 32
    function Assert-WindowsSigningConfiguration {}
    function Get-BuildCommand { return 'fixture-node' }
    $script:mode = 'valid'
    function Invoke-BuildTool {
        param([string]$Tool, [string[]]$Arguments)
        if ($Tool -eq 'fixture-node') {
            New-Item -ItemType Directory -Path $Arguments[4] -Force | Out-Null
            [IO.File]::WriteAllText((Join-Path $Arguments[4] ([IO.Path]::GetFileName($Arguments[2]))), 'signed fixture')
        } elseif ($script:mode -eq 'untrusted') { throw 'Fixture trust failure' }
    }
    function Get-AuthenticodeSignature {
        return [pscustomobject]@{
            Status = 'Valid'
            TimeStamperCertificate = if ($script:mode -eq 'no-timestamp') { $null } else { 'fixture' }
            SignerCertificate = [pscustomobject]@{
                SerialNumber = if ($script:mode -eq 'wrong-serial') { '8' * 32 } else { '7' * 32 }
                Thumbprint = if ($script:mode -eq 'wrong-key') { 'b' * 40 } else { 'a' * 40 }
            }
        }
    }
    $inputFile = Join-Path $stage 'Cage 空格.exe'
    foreach ($script:mode in @('valid', 'untrusted', 'no-timestamp', 'wrong-serial', 'wrong-key')) {
        [IO.File]::WriteAllText($inputFile, 'original fixture')
        $env:CAGE_WINDOWS_CERT_SHA1 = 'a' * 40
        $rejected = $false
        try { Sign-CageFile $inputFile 'fixture-signtool' } catch { $rejected = $true }
        if ($script:mode -eq 'valid') {
            if ($rejected -or [IO.File]::ReadAllText($inputFile) -ne 'signed fixture') { throw 'Valid signed copy was not promoted.' }
        } else {
            if (-not $rejected -or [IO.File]::ReadAllText($inputFile) -ne 'original fixture') { throw "Original file was replaced on $script:mode." }
        }
    }
    Write-Host 'Passed: valid output promoted; untrusted, untimestamped, wrong-serial and wrong-key outputs rejected without replacing originals.'
} finally {
    foreach ($name in $names) { [Environment]::SetEnvironmentVariable($name, $saved[$name]) }
    Remove-Item -LiteralPath $stage -Recurse -Force
}
