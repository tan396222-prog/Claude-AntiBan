#requires -Version 5.1
$ErrorActionPreference = 'Stop'
$source = Join-Path $PSScriptRoot '../packaging/windows'
$root = Join-Path ([IO.Path]::GetTempPath()) ('delete-race-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $root | Out-Null
try {
    Add-Type -Path (Join-Path $source 'verified-file-deletion.cs')
    $tokens = $null
    $errors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile((Join-Path $source 'installation-files.ps1'), [ref]$tokens, [ref]$errors)
    if ($errors.Count) { throw 'The production installation helper has syntax errors.' }
    $definitions = $ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] }, $true)
    foreach ($definition in $definitions) {
        if ($definition.Name -in @('Open-ReadLocked', 'Stream-Hash', 'Initialize-VerifiedDeletion', 'Remove-MatchingFile')) {
            . ([scriptblock]::Create($definition.Extent.Text))
        }
    }

    $script:originalHash = (Get-Item function:Stream-Hash).ScriptBlock
    $script:replacePath = Join-Path $root 'program.dll'
    $script:renamedPath = Join-Path $root 'original-renamed.dll'
    [IO.File]::WriteAllText($script:replacePath, 'verified old program')
    $expected = (Get-FileHash $script:replacePath).Hash
    function Stream-Hash([IO.Stream]$Stream) {
        $hash = & $script:originalHash $Stream
        [IO.File]::Move($script:replacePath, $script:renamedPath)
        [IO.File]::WriteAllText($script:replacePath, 'new personal data')
        return $hash
    }
    Remove-MatchingFile $root 'program.dll' $expected
    $replacementRetained = [IO.File]::ReadAllText($script:replacePath) -eq 'new personal data'
    $verifiedObjectDeleted = !(Test-Path $script:renamedPath)

    $outside = Join-Path $root 'outside'
    New-Item -ItemType Directory -Path $outside | Out-Null
    [IO.File]::WriteAllText((Join-Path $outside 'owned.dll'), 'external target')
    $link = Join-Path $root 'linked'
    & cmd.exe /c mklink /J $link $outside | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not create the isolated junction fixture.' }
    $reparseDenied = $false
    try {
        $stream = [CageVerifiedFile]::Open($root, 'linked/owned.dll')
        $stream.Dispose()
    } catch { $reparseDenied = $true }
    $externalRetained = [IO.File]::ReadAllText((Join-Path $outside 'owned.dll')) -eq 'external target'
    [pscustomobject]@{
        ReplacementRetained = $replacementRetained
        VerifiedObjectDeleted = $verifiedObjectDeleted
        ReparseTraversalDenied = $reparseDenied
        ExternalTargetRetained = $externalRetained
        PowerShell = $PSVersionTable.PSVersion.ToString()
    } | ConvertTo-Json -Compress
    if (!$replacementRetained -or !$verifiedObjectDeleted -or !$reparseDenied -or !$externalRetained) {
        throw 'Verified-handle deletion postconditions failed.'
    }
} finally {
    if (Test-Path -LiteralPath (Join-Path $root 'linked')) { [IO.Directory]::Delete((Join-Path $root 'linked')) }
    Remove-Item -LiteralPath $root -Recurse -Force
}
