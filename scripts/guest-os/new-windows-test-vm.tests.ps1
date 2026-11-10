#requires -Version 5.1
# Simulate a Hyper-V host which defaults to dynamic memory on New-VM.
$ErrorActionPreference = 'Stop'
$path = Join-Path $PSScriptRoot 'new-windows-test-vm.ps1'
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($path, [ref]$null, [ref]$errors)
if ($errors.Count) { throw $errors[0] }
$function = $ast.Find({
    param($node)
    $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Set-TestVMResources'
}, $true)
. ([scriptblock]::Create($function.Extent.Text))

$vm = [pscustomobject]@{ DynamicMemoryEnabled = $true; Startup = 4GB }
function Set-VM {
    param($VM, $ProcessorCount, [switch]$StaticMemory, $MemoryStartupBytes, $AutomaticStartAction, $AutomaticCheckpointsEnabled)
    if (!$script:ignoreStaticMode -and $StaticMemory) { $VM.DynamicMemoryEnabled = $false }
    $VM.Startup = $MemoryStartupBytes
}
function Get-VMMemory { param($VM) $VM }
Set-TestVMResources $vm 4 8GB
if ($vm.DynamicMemoryEnabled -or $vm.Startup -ne 8GB) { throw 'Guest can exceed the requested memory.' }

$vm.DynamicMemoryEnabled = $true
$script:ignoreStaticMode = $true
try {
    Set-TestVMResources $vm 4 8GB
} catch {
    if (!$_.Exception.Message.Contains('requested fixed budget')) { throw }
    Write-Output 'Passed fixed guest memory budgeting and rejection of inconsistent Hyper-V readback.'
    return
}
throw 'Ignored static-memory request was reported as successful.'
