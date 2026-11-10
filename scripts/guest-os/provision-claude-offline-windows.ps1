#requires -Version 5.1
<#
.SYNOPSIS
Stage the official Claude x64 MSIX in a stopped Cage Windows evaluation VM.
.DESCRIPTION
Run from elevated 64-bit Windows PowerShell on the Hyper-V host. Provide the VM
GUID and private directory from new-windows-test-vm.ps1. An evaluation setup
receipt and a readable EnterpriseEval OS partition are required. The report
and DISM scratch/log are saved beside the test disk. This does not boot the VM
or register the application for a user.
.LINK
https://support.claude.com/en/articles/12622703-deploy-claude-desktop-for-windows
.LINK
https://learn.microsoft.com/en-us/powershell/module/dism/add-appxprovisionedpackage
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][Guid]$VMId,
    [Parameter(Mandatory)][string]$InstanceDirectory,
    [Parameter(Mandatory)][string]$MsixPath
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'claude-desktop-windows.ps1') -MsixPath $MsixPath

function Assert-CageOfflineTarget($Machine, $Drive, $Snapshots, $Vhd, $Receipt, [string]$Root, [guid]$Id) {
    $expected = Join-Path $Root 'system.vhdx'
    $location = [IO.Path]::GetFullPath($Machine.ConfigurationLocation).TrimEnd('\')
    if ($Id -eq [guid]::Empty -or $Machine.Id -ne $Id -or [guid]$Receipt.VMId -ne $Id -or
        $Receipt.Edition -ne 'EnterpriseEval' -or $Receipt.InstanceDirectory -ne $Root -or $Receipt.Disk -ne $expected -or
        ($location -ne $Root -and !$location.StartsWith($Root + '\', [StringComparison]::OrdinalIgnoreCase))) {
        throw 'The VM, setup receipt and private instance directory do not match.'
    }
    if ($Machine.State -ne 'Off' -or $Machine.Generation -ne 2 -or @($Snapshots).Count -ne 0 -or
        @($Drive).Count -ne 1 -or $Drive[0].Path -ne $expected -or $Vhd.Path -ne $expected -or
        $Vhd.VhdFormat -ne 'VHDX' -or $Vhd.VhdType -ne 'Dynamic' -or $Vhd.ParentPath -or $Vhd.Attached -ne $false) {
        throw 'Requires a stopped Gen 2 VM with one unattached, standalone dynamic VHDX and no snapshots.'
    }
}

function Assert-MountedGuestDisk($Disk, $BackingVHD, [string]$ExpectedPath) {
    if (@($Disk).Count -ne 1 -or $null -eq $Disk.Number -or $BackingVHD.DiskNumber -ne $Disk.Number -or
        $BackingVHD.Path -ne $ExpectedPath -or $BackingVHD.Attached -ne $true -or
        $Disk.IsBoot -ne $false -or $Disk.IsSystem -ne $false -or $Disk.IsOffline -ne $false -or
        $Disk.IsReadOnly -ne $false -or $Disk.PartitionStyle -ne 'GPT') {
        throw 'The attached disk is not the selected writable guest GPT disk, or is a host boot/system disk.'
    }
}

function Select-GuestWindowsPartition($Partitions, [int]$DiskNumber) {
    $basic = @($Partitions | Where-Object { ([string]$_.GptType).Trim('{}') -eq 'EBD0A0A2-B9E5-4433-87C0-68B6B72699C7' })
    if ($basic.Count -ne 1 -or $basic[0].DiskNumber -ne $DiskNumber -or
        $basic[0].IsBoot -ne $false -or $basic[0].IsSystem -ne $false) {
        throw 'Expected exactly one Windows basic-data partition on the selected guest disk.'
    }
    return $basic[0]
}

function Get-OfflineClaude([hashtable]$Image, [string]$Package) {
    $matchesName = @(Get-AppxProvisionedPackage @Image | Where-Object DisplayName -ceq 'Claude')
    if ($matchesName.Count -gt 1 -or ($matchesName.Count -eq 1 -and $matchesName[0].PackageName -cne $Package)) {
        throw 'The offline image already contains a different Claude package. It was preserved.'
    }
    if ($matchesName.Count -eq 1) { return $matchesName[0] }
}

function Install-OfflineClaude([hashtable]$Image, [string]$PackageFile, [string]$PackageName) {
    $existing = Get-OfflineClaude $Image $PackageName
    if (!$existing) {
        $null = Add-AppxProvisionedPackage @Image -PackagePath $PackageFile -SkipLicense -Regions 'all'
    }
    $installed = Get-OfflineClaude $Image $PackageName
    if (!$installed) { throw 'DISM did not list the expected Claude provisioning entry in the guest.' }
    return [pscustomobject]@{ PackageName=$installed.PackageName; AlreadyProvisioned=[bool]$existing }
}

if ([Environment]::OSVersion.Platform -ne 'Win32NT' -or ![Environment]::Is64BitProcess) { throw 'Run on the 64-bit Windows Hyper-V host.' }
$root = Get-Item -LiteralPath $InstanceDirectory
if ($root -isnot [IO.DirectoryInfo] -or $root.FullName.StartsWith('\\') -or [IO.DriveInfo]::new($root.Root.FullName).DriveType -ne 'Fixed') {
    throw 'The VM directory must be on a local fixed drive.'
}
for ($ancestor=$root; $ancestor; $ancestor=$ancestor.Parent) {
    if ($ancestor.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'A redirected VM directory is not supported.' }
}
$qa = Get-Item -LiteralPath (Join-Path $root.FullName 'evaluation-setup')
$receiptFile = Get-Item -LiteralPath (Join-Path $qa.FullName 'receipt.json')
$diskFile = Get-Item -LiteralPath (Join-Path $root.FullName 'system.vhdx')
foreach ($entry in @($qa,$receiptFile,$diskFile)) {
    if ($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Unexpected reparse point in the instance or receipt.' }
}
$receipt = Get-Content -LiteralPath $receiptFile.FullName -Raw | ConvertFrom-Json
$packageFile = Get-Item -LiteralPath $MsixPath
if ($packageFile -isnot [IO.FileInfo] -or $packageFile.Extension -ne '.msix' -or
    ($packageFile.Attributes -band [IO.FileAttributes]::ReparsePoint) -or $packageFile.FullName.StartsWith('\\') -or
    [IO.DriveInfo]::new($packageFile.Directory.Root.FullName).DriveType -eq 'Network') {
    throw 'Supply a regular local x64 Claude MSIX.'
}
$lock = [IO.File]::Open((Join-Path $qa.FullName 'setup.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
$mounted = $false
$addedAccess = $false
$packageHandle = $null
$run = $null
$report = [ordered]@{ VMId=$VMId; Disk=$diskFile.FullName; Provisioned=$null; UserRegistration='NotTested'; Window='NotTested' }
try {
    $vm = Get-VM -Id $VMId
    $drives = @(Get-VMHardDiskDrive -VM $vm)
    $snapshots = @(Get-VMSnapshot -VM $vm)
    $vhd = Get-VHD -Path $diskFile.FullName
    Assert-CageOfflineTarget $vm $drives $snapshots $vhd $receipt $root.FullName $VMId
    $owners = @(Get-VM | Get-VMHardDiskDrive | Where-Object Path -eq $diskFile.FullName)
    if ($owners.Count -ne 1 -or $owners[0].VMId -ne $VMId) { throw 'The guest disk is not owned exclusively by this VM.' }
    $hostVolume = Get-Volume -FilePath $diskFile.FullName
    if ($hostVolume.FileSystemType -ne 'NTFS') { throw 'The private host directory must support NTFS volume mount points.' }
    $run = New-Item -ItemType Directory -Path (Join-Path $qa.FullName ('offline-claude-' + [guid]::NewGuid().ToString('N')))
    $mount = New-Item -ItemType Directory -Path (Join-Path $run.FullName 'os')
    $scratch = New-Item -ItemType Directory -Path (Join-Path $run.FullName 'scratch')
    $log = Join-Path $run.FullName 'dism.log'
    $report['DismLog'] = $log
    $report['HostBuild'] = (Get-CimInstance Win32_OperatingSystem).BuildNumber
    $packageHandle = [IO.File]::Open($packageFile.FullName,'Open','Read','Read')
    $version = Get-VerifiedClaudeMsixVersion $packageFile $packageHandle
    $expectedPackage = 'Claude_{0}_x64__pzs8sxrjxfjjc' -f $version
    $report['VerifiedInputPackage'] = $expectedPackage

    $attached = Mount-VHD -Path $diskFile.FullName -NoDriveLetter -Passthru
    $mounted = $true
    $report['VhdDetached'] = $false
    $disk = @($attached | Get-Disk)
    if ($disk.Count -ne 1) { throw 'The VHDX attachment did not resolve to one disk.' }
    $disk = $disk[0]
    $backing = Get-VHD -DiskNumber $disk.Number
    Assert-MountedGuestDisk $disk $backing $diskFile.FullName
    $partition = Select-GuestWindowsPartition @(Get-Partition -DiskNumber $disk.Number) $disk.Number
    $volumes = @(Get-Volume -Partition $partition)
    if ($volumes.Count -ne 1 -or !$volumes[0].UniqueId -or $volumes[0].UniqueId -eq $hostVolume.UniqueId) {
        throw 'Could not identify a separate guest OS volume.'
    }
    $volume = $volumes[0]
    # Query only this VHD volume. An unencrypted VHD need not have a BitLocker provider entry.
    $volPath = [string]$volume.Path
    $report['BitLocker'] = 'No provider entry'
    if ($volPath) {
        $escaped = $volPath.Replace('\','\\').Replace("'","\'")
        try {
            $encrypted = @(Get-CimInstance -Namespace 'root/CIMV2/Security/MicrosoftVolumeEncryption' -ClassName Win32_EncryptableVolume -Filter "DeviceID='$escaped'")
        } catch { $encrypted = @(); $report['BitLocker'] = 'Provider unavailable' }
        if ($encrypted.Count -gt 1) { throw 'Ambiguous guest BitLocker volume.' }
        if ($encrypted.Count -eq 1) {
            $state = Invoke-CimMethod -InputObject $encrypted[0] -MethodName GetLockStatus
            if ($state.ReturnValue -ne 0 -or $state.LockStatus -ne 0) { throw 'Guest BitLocker volume is locked or its status is unavailable.' }
            $report['BitLocker'] = 'Unlocked'
        }
    }
    if ($volume.FileSystemType -ne 'NTFS') { throw 'Guest partition is locked or is not a readable NTFS volume.' }
    $accessPath = $mount.FullName + '\'
    Add-PartitionAccessPath -DiskNumber $disk.Number -PartitionNumber $partition.PartitionNumber -AccessPath $accessPath
    $addedAccess = $true
    $report['AccessPathRemoved'] = $false
    foreach ($relative in @('Windows','Windows\System32','Windows\System32\config','Windows\System32\config\SOFTWARE')) {
        $item = Get-Item -LiteralPath (Join-Path $mount.FullName $relative)
        if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Unexpected reparse point in guest OS identification path.' }
    }
    $osVolume = Get-Volume -FilePath $item.FullName
    if ($osVolume.UniqueId -ne $volume.UniqueId) { throw 'The mounted Windows image resolves to another volume.' }
    $dism = Join-Path ([Environment]::SystemDirectory) 'dism.exe'
    $info = @(& $dism /English ("/Image:$($mount.FullName)") /Get-CurrentEdition ("/ScratchDir:$($scratch.FullName)") ("/LogPath:$log") 2>&1)
    if ($LASTEXITCODE -ne 0) { throw "DISM could not service the guest edition (exit $LASTEXITCODE). See $log" }
    $lines = $info -join "`n"
    if ($lines -notmatch '(?im)^\s*Current Edition\s*:\s*EnterpriseEval\s*$') { throw 'Offline OS is not Windows Enterprise Evaluation.' }
    $report['GuestEdition'] = 'EnterpriseEval'
    $report['GuestImageVersion'] = [regex]::Match($lines,'(?im)^\s*Image Version\s*:\s*([\d.]+)').Groups[1].Value
    $report['DismVersion'] = [regex]::Match($lines,'(?im)^\s*Version\s*:\s*([\d.]+)').Groups[1].Value
    if (!$report.GuestImageVersion -or !$report.DismVersion) { throw 'Could not read offline image and DISM servicing versions.' }
    $image = @{Path=$mount.FullName; ScratchDirectory=$scratch.FullName; LogPath=$log; ErrorAction='Stop'}
    $installed = Install-OfflineClaude $image $packageFile.FullName $expectedPackage
    $report['Provisioned'] = $true
    $report['PackageName'] = $installed.PackageName
    $report['AlreadyProvisioned'] = $installed.AlreadyProvisioned
} catch {
    $report['Error'] = $_.Exception.Message
    throw
} finally {
    try {
        if ($addedAccess) {
            Remove-PartitionAccessPath -DiskNumber $disk.Number -PartitionNumber $partition.PartitionNumber -AccessPath $accessPath -Confirm:$false
            $paths = (Get-Partition -DiskNumber $disk.Number -PartitionNumber $partition.PartitionNumber).AccessPaths
            if (@($paths | Where-Object { $_ -and $_.TrimEnd('\') -eq $mount.FullName }).Count) { throw 'The guest access path did not detach.' }
            $report['AccessPathRemoved'] = $true
        }
    } catch {
        $report['AccessPathCleanupError'] = $_.Exception.Message
        throw
    } finally {
        try {
            if ($mounted) { Dismount-VHD -Path $diskFile.FullName -ErrorAction Stop }
            if ($mounted) {
                $report['VhdDetached'] = ((Get-VHD -Path $diskFile.FullName).Attached -eq $false)
                if (!$report.VhdDetached) { throw 'The test VHDX did not detach.' }
            }
        } catch {
            $report['VhdCleanupError'] = $_.Exception.Message
            throw
        } finally {
            if ($packageHandle) { $packageHandle.Dispose() }
            $lock.Dispose()
            if ($run) {
                $reportFile = Join-Path $run.FullName 'result.json'
                [IO.File]::WriteAllText($reportFile, ($report | ConvertTo-Json))
            }
        }
    }
}
[pscustomobject]$report
