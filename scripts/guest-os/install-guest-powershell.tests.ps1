#requires -Version 5.1
# Exercise the actual Setup command with an isolated filesystem and registry stand-in.
$ErrorActionPreference = 'Stop'
$file = Join-Path $PSScriptRoot 'install-guest-powershell.cmd'
$lines = @(Get-Content -LiteralPath $file | Where-Object { $_.StartsWith('  "') })
$parts = for ($i = 0; $i -lt $lines.Count; $i++) {
    $suffix = if ($i -lt $lines.Count - 1) { '" ^' } else { '"' }
    if (!$lines[$i].EndsWith($suffix)) { throw 'Broken CMD line continuation.' }
    $lines[$i].Substring(3, $lines[$i].Length - 3 - $suffix.Length)
}
$command = $parts -join ' '
if ($command.Length -gt 7700 -or $command.Contains('"') -or $command.Contains('%')) {
    throw 'The guest installer command may be truncated or expanded by cmd.exe.'
}
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseInput($command, [ref]$null, [ref]$errors)
if ($errors.Count) { throw $errors[0] }
$steps = $ast.EndBlock.Statements
$installStart = @($steps | Where-Object { $_.Extent.Text.StartsWith('if ((Get-FileHash ') })[0]
$regStart = @($steps | Where-Object { $_.Extent.Text.StartsWith('$key = ') })[0]
$install = [scriptblock]::Create($command.Substring($installStart.Extent.StartOffset, $regStart.Extent.StartOffset - $installStart.Extent.StartOffset))
$updatePath = [scriptblock]::Create($steps[-1].Extent.Text)

function Assert-Failure([scriptblock]$run, [string]$message) {
    try { & $run } catch {
        if ($_.Exception.Message -like "*$message*") { return }
        throw
    }
    throw "Expected failure: $message"
}

# The archive, hash checks, staging, publication, cleanup and retry are real.
# Simulate the version response of a Windows executable on non-Windows hosts.
function Test-Runtime([string]$directory, [string]$expectedVersion) {
    $version = [IO.File]::ReadAllText((Join-Path $directory 'pwsh.exe'))
    if ($version -notmatch '^7\.[0-9]+\.[0-9]+$' -or ($expectedVersion -and $expectedVersion -ne $version)) {
        throw 'Simulated incompatible runtime'
    }
}

$oldHash = $env:CAGE_PWSH_HASH
$oldVersion = $env:CAGE_PWSH_VERSION
$temp = [IO.Directory]::CreateDirectory((Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString('N'))))
try {
    $version = '7.6.0'
    $env:CAGE_PWSH_VERSION = $version
    $files = [IO.Directory]::CreateDirectory((Join-Path $temp 'source'))
    [IO.File]::WriteAllText((Join-Path $files.FullName 'pwsh.exe'), $version)
    $zip = Join-Path $temp.FullName 'PowerShell.zip'
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [IO.Compression.ZipFile]::CreateFromDirectory($files.FullName, $zip)
    $base = Join-Path $temp.FullName 'Program Files'
    $target = Join-Path $base '7'
    $env:CAGE_PWSH_HASH = '00' * 32
    Assert-Failure { & $install } 'pinned SHA-256'
    if ([IO.Directory]::Exists($base)) { throw 'A hash mismatch changed the destination.' }

    $env:CAGE_PWSH_HASH = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash
    $env:CAGE_PWSH_VERSION = '7.5.0'
    Assert-Failure { & $install } 'incompatible runtime'
    if (@([IO.Directory]::EnumerateFileSystemEntries($base)).Count) { throw 'Failed runtime left partial files.' }
    $env:CAGE_PWSH_VERSION = $version
    & $install
    $exe = Join-Path $target 'pwsh.exe'
    if ([IO.File]::ReadAllText($exe) -ne $version) { throw 'Verified shell was not published.' }
    [IO.File]::WriteAllText($exe, '7.8.0')
    & $install
    if ([IO.File]::ReadAllText($exe) -ne '7.8.0') { throw 'A later installed release was overwritten.' }
    [IO.File]::WriteAllText($exe, 'unexpected')
    Assert-Failure { & $install } 'incompatible runtime'
    if ([IO.File]::ReadAllText($exe) -ne 'unexpected') { throw 'Existing installation was modified on failure.' }

    $key = [pscustomobject]@{ Raw = '%SystemRoot%\System32'; Writes = 0 }
    $key | Add-Member ScriptMethod GetValue { param($name,$fallback,$flags) $this.Raw }
    $key | Add-Member ScriptMethod GetValueNames { @('Path') }
    $key | Add-Member ScriptMethod GetValueKind { param($name) [Microsoft.Win32.RegistryValueKind]::ExpandString }
    $key | Add-Member ScriptMethod SetValue {
        param($name,$value,$kind)
        if ($kind -ne [Microsoft.Win32.RegistryValueKind]::ExpandString) { throw 'Changed registry value kind.' }
        $this.Raw = $value; $this.Writes++
    }
    $key | Add-Member ScriptMethod Close {}
    & $updatePath
    & $updatePath
    if ($key.Writes -ne 1 -or $key.Raw -cne ($target + ';%SystemRoot%\System32')) {
        throw 'PATH retry duplicated the entry or rewrote existing variables.'
    }
} finally {
    $env:CAGE_PWSH_HASH = $oldHash
    $env:CAGE_PWSH_VERSION = $oldVersion
    $temp.Delete($true)
}
Write-Output 'Passed offline ZIP verification, publication, retry, existing shell preservation and machine PATH checks (simulated guest).'
