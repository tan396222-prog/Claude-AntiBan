$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$id = [guid]$inputData.vm_id
$vm = Get-VM -Id $id
$recoveringDisk = $inputData.action -eq 'apply' -and $inputData.disk -ieq $inputData.source_disk
$stopped = $vm.State -eq 'Off' -or ($recoveringDisk -and $vm.State -eq 'OffCritical')
if (!$stopped -or $vm.Generation -ne 2 -or
    @(Get-VMNetworkAdapter -VM $vm).Count -ne 0 -or
    (Get-VMFirmware -VM $vm).SecureBoot -ne 'On' -or !(Get-VMSecurity -VM $vm).TpmEnabled) {
    throw 'vm_changed'
}
$disks = @(Get-VMHardDiskDrive -VM $vm)
if ($disks.Count -ne 1) { throw 'vm_changed' }
function Read-Storage {
    $current = Get-VM -Id $id
    [pscustomobject]@{
        configuration = [string]$current.ConfigurationLocation
        snapshots = [string]$current.SnapshotFileLocation
        paging = [string]$current.SmartPagingFilePath
    }
}
if ($inputData.action -eq 'inspect') {
    Read-Storage | ConvertTo-Json -Compress
    exit 0
}
if ($inputData.action -eq 'export') {
    $namespace = 'root\virtualization\v2'
    $system = Get-WmiObject -Namespace $namespace -Class Msvm_ComputerSystem -Filter ("Name='" + $id.ToString('D') + "'")
    $service = Get-WmiObject -Namespace $namespace -Class Msvm_VirtualSystemManagementService
    $settings = ([wmiclass]'\\.\root\virtualization\v2:Msvm_VirtualSystemExportSettingData').CreateInstance()
    $settings.CopySnapshotConfiguration = 0
    $settings.CopyVmRuntimeInformation = $true
    $settings.CopyVmStorage = $false
    $settings.CreateVmExportSubdirectory = $false
    $result = $service.ExportSystemDefinition($system.__PATH, [string]$inputData.backup, $settings.GetText(1))
    if ($result.ReturnValue -eq 4096) {
        $job = [wmi]$result.Job
        while ($job.JobState -in @(2, 3, 4, 6)) { Start-Sleep -Milliseconds 200; $job.Get() }
        if ($job.JobState -ne 7 -or $job.ErrorCode -ne 0) { throw 'storage_export_failed' }
    } elseif ($result.ReturnValue -ne 0) {
        throw 'storage_export_failed'
    }
    [pscustomobject]@{ exported = $true } | ConvertTo-Json -Compress
    exit 0
}
if ($inputData.action -ne 'apply') { throw 'invalid_operation' }
$old = $inputData.old
$next = $inputData.next
$current = Read-Storage
foreach ($field in @('configuration', 'snapshots', 'paging')) {
    if ($current.$field -ine $old.$field -and $current.$field -ine $next.$field) { throw 'vm_changed' }
}
if ($disks[0].Path -ine $inputData.old_disk -and $disks[0].Path -ine $inputData.source_disk -and $disks[0].Path -ine $inputData.new_disk) { throw 'vm_changed' }
$target = $inputData.target
$move = @{}
if ($current.configuration -ine $target.configuration) { $move.VirtualMachinePath = [string]$target.configuration }
if ($current.snapshots -ine $target.snapshots) { $move.SnapshotFilePath = [string]$target.snapshots }
if ($current.paging -ine $target.paging) { $move.SmartPagingFilePath = [string]$target.paging }
if ($move.Count) {
    # Hyper-V owns configuration, vTPM state and snapshot metadata. Its native
    # storage operation preserves the registered VM identity and permissions.
    # The verified VHDX copy is attached separately so its original is retained.
    Move-VMStorage -VM $vm @move -Confirm:$false
}
$vm = Get-VM -Id $id
$disks = @(Get-VMHardDiskDrive -VM $vm)
if (($vm.State -ne 'Off' -and !($recoveringDisk -and $vm.State -eq 'OffCritical')) -or $disks.Count -ne 1) { throw 'vm_changed' }
if ($disks[0].Path -ine $inputData.disk) {
    Set-VMHardDiskDrive -VMHardDiskDrive $disks[0] -Path ([string]$inputData.disk)
}
$readback = Read-Storage
foreach ($field in @('configuration', 'snapshots', 'paging')) {
    if ($readback.$field -ine $target.$field) { throw 'storage_readback_failed' }
}
$disk = @(Get-VMHardDiskDrive -VM (Get-VM -Id $id))
if ($disk.Count -ne 1 -or $disk[0].Path -ine $inputData.disk) { throw 'storage_readback_failed' }
[pscustomobject]@{ applied = $true } | ConvertTo-Json -Compress
