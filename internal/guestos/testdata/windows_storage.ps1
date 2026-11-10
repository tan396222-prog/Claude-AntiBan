param([Parameter(Mandatory)][string]$Implementation)
$ErrorActionPreference='Stop'
$global:cageStorageVMID=[guid]'2a6f8208-3906-4e16-8233-2b9d28ecf7be'
$old=[pscustomobject]@{configuration="C:\Cage original\config";snapshots="C:\Cage original\snapshots";paging="C:\Cage original\paging"}
$next=[pscustomobject]@{configuration="C:\ProgramData\Cage\测试's\config";snapshots="C:\ProgramData\Cage\测试's\snapshots";paging="C:\ProgramData\Cage\测试's\paging"}
$source="C:\Cage original\system.vhdx"
$destination="D:\Cage 新位置's\system.vhdx"
function Reset-VM {
    $global:cageStorageVM=[pscustomobject]@{Id=$global:cageStorageVMID;State='Off';Generation=2;ConfigurationLocation=$old.configuration;SnapshotFileLocation=$old.snapshots;SmartPagingFilePath=$old.paging}
    $global:cageStorageDisk=[pscustomobject]@{Path=$source}
    $global:cageStorageMoves=0
    $global:cageStorageAttachments=0
    $global:cageStorageFailMove=$false
}
function Get-VM { param([guid]$Id) if($Id -ne $global:cageStorageVMID){throw 'wrong_vm'};return $global:cageStorageVM }
function Get-VMNetworkAdapter { param($VM) if($VM.Id -ne $global:cageStorageVMID){throw 'wrong_vm'} }
function Get-VMFirmware { param($VM) [pscustomobject]@{SecureBoot='On'} }
function Get-VMSecurity { param($VM) [pscustomobject]@{TpmEnabled=$true} }
function Get-VMHardDiskDrive { param($VM) if($VM.Id -ne $global:cageStorageVMID){throw 'wrong_vm'};return $global:cageStorageDisk }
function Move-VMStorage {
    param($VM,[string]$VirtualMachinePath,[string]$SnapshotFilePath,[string]$SmartPagingFilePath,[switch]$Confirm)
    if($VM.Id -ne $global:cageStorageVMID -or $VM.State -ne 'Off'){throw 'unsafe_move'}
    $global:cageStorageMoves++
    if($VirtualMachinePath){$global:cageStorageVM.ConfigurationLocation=$VirtualMachinePath}
    if($global:cageStorageFailMove){throw 'injected_metadata_failure'}
    if($SnapshotFilePath){$global:cageStorageVM.SnapshotFileLocation=$SnapshotFilePath}
    if($SmartPagingFilePath){$global:cageStorageVM.SmartPagingFilePath=$SmartPagingFilePath}
}
function Set-VMHardDiskDrive {
    param($VMHardDiskDrive,[string]$Path)
    if($global:cageStorageVM.State -notin @('Off','OffCritical') -or $VMHardDiskDrive -ne $global:cageStorageDisk){throw 'unsafe_attachment'}
    $global:cageStorageAttachments++
    $global:cageStorageDisk.Path=$Path
    $global:cageStorageVM.State='Off'
}
function Invoke-Storage($Target,[string]$Disk) {
    $inputData=[pscustomobject]@{vm_id=$global:cageStorageVMID.ToString('D');action='apply';old=$old;next=$next;target=$Target;old_disk=$source;source_disk=$source;new_disk=$destination;disk=$Disk}
    & $Implementation | Out-Null
}
Reset-VM
Invoke-Storage $next $source
if($global:cageStorageMoves -ne 1 -or $global:cageStorageAttachments -ne 0){throw 'registration_not_prepared_before_copy'}
Invoke-Storage $next $destination
if($global:cageStorageVM.ConfigurationLocation -cne $next.configuration -or $global:cageStorageVM.SnapshotFileLocation -cne $next.snapshots -or $global:cageStorageVM.SmartPagingFilePath -cne $next.paging -or $global:cageStorageDisk.Path -cne $destination){throw 'incomplete_migration'}
if($global:cageStorageMoves -ne 1 -or $global:cageStorageAttachments -ne 1){throw 'unexpected_mutation_count'}
Invoke-Storage $next $destination
if($global:cageStorageMoves -ne 1 -or $global:cageStorageAttachments -ne 1){throw 'retry_repeated_mutations'}
$global:cageStorageVM.State='OffCritical'
Invoke-Storage $next $source
if($global:cageStorageVM.ConfigurationLocation -cne $next.configuration -or $global:cageStorageVM.SnapshotFileLocation -cne $next.snapshots -or $global:cageStorageDisk.Path -cne $source){throw 'offline_target_rollback_failed'}

Reset-VM
$global:cageStorageFailMove=$true
$failed=$false
try{Invoke-Storage $next $destination}catch{$failed=$true}
if(!$failed -or $global:cageStorageVM.ConfigurationLocation -cne $next.configuration -or $global:cageStorageAttachments -ne 0){throw 'partial_failure_fixture_invalid'}
$global:cageStorageFailMove=$false
Invoke-Storage $next $source
if($global:cageStorageVM.ConfigurationLocation -cne $next.configuration -or $global:cageStorageDisk.Path -cne $source){throw 'partial_metadata_rollback_failed'}

foreach($failure in @('running','foreign_disk','foreign_configuration')){
    Reset-VM
    if($failure -eq 'running'){$global:cageStorageVM.State='Running'}
    if($failure -eq 'foreign_disk'){$global:cageStorageDisk.Path='C:\Another VM\private.vhdx'}
    if($failure -eq 'foreign_configuration'){$global:cageStorageVM.ConfigurationLocation='C:\Another VM\config'}
    $failed=$false
    try{Invoke-Storage $next $destination}catch{$failed=$true}
    if(!$failed -or $global:cageStorageMoves -ne 0 -or $global:cageStorageAttachments -ne 0){throw ('unsafe_state_accepted_'+$failure)}
}
Write-Output 'PASS fixed system registration, disk move, idempotence, offline target rollback, partial metadata recovery, running and unrelated VM state rejection'
