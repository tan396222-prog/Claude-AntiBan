$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$WarningPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
# An exact CIM query can return zero matches without conflating a failed or
# unauthorized management request with a nonexistent VM.
$systems = @(Get-CimInstance -Namespace root/virtualization/v2 -ClassName Msvm_ComputerSystem -Filter "Name='$targetId'")
if ($systems.Count -eq 0) { Write-Output 'null'; return }
if ($systems.Count -ne 1) { throw 'Unexpected Hyper-V management result.' }
$vm = Hyper-V\Get-VM -Id $targetId
if ($vm.Id -ne $targetId -or [string]::IsNullOrWhiteSpace([string]$vm.State) -or $vm.Generation -notin 1,2) {
    throw 'Hyper-V instance changed.'
}
$disks = @(Hyper-V\Get-VMHardDiskDrive -VM $vm)
$adapters = @(Hyper-V\Get-VMNetworkAdapter -VM $vm)
$singleLocalVHDX = $false
$diskType = ''
if ($disks.Count -eq 1 -and $disks[0].Path) {
    $path = [string]$disks[0].Path
    if ($path -match '^[A-Za-z]:[\\/]') {
        try {
            if ([IO.DriveInfo]::new($path.Substring(0,3)).DriveType -ne 'Network') {
                $disk = Hyper-V\Get-VHD -Path $path
                if (!$disk -or !$disk.VhdFormat) { throw 'No local VHD metadata.' }
                $singleLocalVHDX = [string]$disk.VhdFormat -eq 'VHDX'
                $diskType = [string]$disk.VhdType
            }
        } catch {
            $singleLocalVHDX = $null
        }
    }
}
$secureBoot = $false
$template = ''
$virtualTPM = $false
if ($vm.Generation -eq 2) {
    $firmware = Hyper-V\Get-VMFirmware -VM $vm
    $security = Hyper-V\Get-VMSecurity -VM $vm
    if ($null -eq $security.TpmEnabled -or [string]$firmware.SecureBoot -notin 'On','Off') { throw 'Incomplete security configuration.' }
    $secureBoot = [string]$firmware.SecureBoot -eq 'On'
    $template = [string]$firmware.SecureBootTemplate
    $virtualTPM = [bool]$security.TpmEnabled
}
[ordered]@{
    state=[string]$vm.State; generation=[int]$vm.Generation
    diskCount=$disks.Count; singleLocalVhdx=$singleLocalVHDX; diskType=$diskType
    secureBoot=$secureBoot; secureBootTemplate=$template; virtualTpm=$virtualTPM
    networkAdapterCount=$adapters.Count
} | ConvertTo-Json -Compress
