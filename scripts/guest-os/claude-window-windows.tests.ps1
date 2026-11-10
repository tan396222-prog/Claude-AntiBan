#requires -Version 5.1
# Test frame rejection and output handling without accessing any desktop.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'claude-window-windows.ps1')

if ($PSVersionTable.PSEdition -eq 'Desktop') {
    Add-Type -TypeDefinition $cageWindowSource -ReferencedAssemblies System.Drawing
} else {
    $references = @((Get-ChildItem "$PSHOME/ref/*.dll").FullName) + @([Drawing.Bitmap].Assembly.Location)
    $references += @((Get-ChildItem "$PSHOME/System.Private.Windows.*.dll").FullName)
    Add-Type -TypeDefinition $cageWindowSource -ReferencedAssemblies $references
}

if ([CageClaudeWindow]::FrameProblem([int[]]@()) -ne 'empty_frame' -or
    [CageClaudeWindow]::FrameProblem([int[]]@(0, 0x010204)) -ne 'black_frame' -or
    [CageClaudeWindow]::FrameProblem([int[]]@(0xFAFAFA, 0xFBFBFB)) -ne 'uniform_frame' -or
    [CageClaudeWindow]::FrameProblem([int[]]@(0xFAFAFA, 0x111111))) {
    throw 'Blank or nonuniform frame classification failed.'
}

$output = Join-Path ([IO.Path]::GetTempPath()) ("cage-window-" + [guid]::NewGuid() + ".png")
if ((Get-ClaudeCapturePath $output) -ne [IO.Path]::GetFullPath($output)) { throw 'Failed to accept a temporary PNG.' }
try {
    [IO.File]::WriteAllText($output, 'preserve')
    foreach ($bad in @($output, "$output.txt", [IO.Path]::Combine([IO.Path]::GetDirectoryName($output), '..', 'outside.png'))) {
        $rejected = $false
        try { $null = Get-ClaudeCapturePath $bad } catch { $rejected = $true }
        if (!$rejected) { throw "Accepted invalid output: $bad" }
    }
    if ([IO.File]::ReadAllText($output) -cne 'preserve') { throw 'Existing output was changed.' }
} finally { [IO.File]::Delete($output) }
Write-Output 'Passed simulated Claude window frame and output-path checks.'
