# Exercise the bundled read-only query. No Hyper-V service or real VM is used.
param([Parameter(Mandatory)][string]$Query)
$ErrorActionPreference = 'Stop'
$targetId = [Guid]::NewGuid()
$global:hypervTestId = $targetId
$global:hypervCase = 'single'
function Get-CimInstance {
    param($Namespace,$ClassName,$Filter)
    if ($Namespace -ne 'root/virtualization/v2' -or $ClassName -ne 'Msvm_ComputerSystem' -or $Filter -ne "Name='$global:hypervTestId'") { throw 'Unscoped VM lookup.' }
    if ($global:hypervCase -eq 'denied') { throw 'test management access denied' }
    if ($global:hypervCase -ne 'absent') { [pscustomobject]@{Name=$global:hypervTestId} }
}
$fake = New-Module -Name Hyper-V -ScriptBlock {
    function Get-VM {
        param($Id)
        if ($id -ne $global:hypervTestId) { throw 'Unscoped management lookup' }
        $returned = if ($global:hypervCase -eq 'swapped') { [Guid]::NewGuid() } else { $id }
        $generation = if ($global:hypervCase -eq 'legacy') { 1 } else { 2 }
        $state = if ($global:hypervCase -eq 'connected') { 'Running' } else { 'Off' }
        [pscustomobject]@{Id=$returned; Generation=$generation; State=$state}
    }
    function Get-VMHardDiskDrive {
        param($VM)
        [pscustomobject]@{Path='C:\private-qa\system.vhdx'}
        if ($global:hypervCase -eq 'connected') { [pscustomobject]@{Path='C:\private-qa\second.vhdx'} }
    }
    function Get-VMNetworkAdapter { param($VM) if ($global:hypervCase -eq 'connected') { [pscustomobject]@{Name='adapter'} } }
    function Get-VHD {
        param($Path)
        if ($global:hypervCase -eq 'disk-denied') { throw 'test disk metadata unavailable' }
        if ($Path -cne 'C:\private-qa\system.vhdx') { throw 'Unexpected disk lookup' }
        [pscustomobject]@{VhdFormat='VHDX'; VhdType='Dynamic'}
    }
    function Get-VMFirmware {param($VM) if ($VM.Generation -ne 2) { throw 'Legacy VM firmware query' }; [pscustomobject]@{SecureBoot='On'; SecureBootTemplate='MicrosoftWindows'} }
    function Get-VMSecurity {param($VM) if ($VM.Generation -ne 2) { throw 'Legacy VM security query' }; [pscustomobject]@{TpmEnabled=$true} }
    Export-ModuleMember -Function 'Get-*'
}
Import-Module $fake -Force
function Result { (& $Query) | ConvertFrom-Json }
$found = Result
if ($found.state -ne 'Off' -or $found.generation -ne 2 -or $found.diskCount -ne 1 -or !$found.singleLocalVhdx -or $found.diskType -ne 'Dynamic' -or !$found.secureBoot -or !$found.virtualTpm -or $found.networkAdapterCount -ne 0) { throw 'Invalid offline VM observation' }
if (($found | ConvertTo-Json -Compress).Contains('private-qa')) { throw 'Host disk path exposed' }
$global:hypervCase='connected'; $running=Result
if ($running.state -ne 'Running' -or $running.networkAdapterCount -ne 1 -or $running.diskCount -ne 2 -or $running.singleLocalVhdx) { throw 'Connected or multiple-disk VM misreported' }
$global:hypervCase='disk-denied'
if ($null -ne (Result).singleLocalVhdx) { throw 'Denied disk metadata reported as verified' }
$global:hypervCase='legacy'; $legacy = Result
if ($legacy.secureBoot -or $legacy.virtualTpm) { throw 'Legacy VM reported as secure boot/vTPM capable' }
$global:hypervCase='absent'; if ($null -ne (Result)) { throw 'Unexpected VM for missing ID' }
foreach ($testCase in @('denied', 'swapped')) {
    $global:hypervCase = $testCase
    $rejected = $false
    try { $null = Result } catch { $rejected = $true }
    if (!$rejected) { throw 'Access failure or changed VM identity treated as a valid lookup' }
}
