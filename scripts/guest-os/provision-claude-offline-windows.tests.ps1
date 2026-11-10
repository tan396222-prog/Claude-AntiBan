#requires -Version 5.1
$ErrorActionPreference = 'Stop'
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'provision-claude-offline-windows.ps1'), [ref]$null, [ref]$errors)
if ($errors.Count) { throw $errors[0] }
foreach ($f in $ast.FindAll({param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst]},$false)) {
    . ([scriptblock]::Create($f.Extent.Text))
}
function Reject([scriptblock]$operation, [string]$reason) {
    try { $null = & $operation } catch { if ($_.Exception.Message.Contains($reason)) { return }; throw }
    throw "Expected rejection: $reason"
}
$root = [IO.Path]::GetTempPath().TrimEnd([IO.Path]::DirectorySeparatorChar)
$diskFile = Join-Path $root 'system.vhdx'
$id = [guid]::NewGuid()
$vm = [pscustomobject]@{Id=$id; State='Off'; Generation=2; ConfigurationLocation=$root}
$receipt = [pscustomobject]@{VMId=$id; Edition='EnterpriseEval'; Disk=$diskFile; InstanceDirectory=$root}
$drive = @([pscustomobject]@{Path=$diskFile})
$vhd = [pscustomobject]@{Path=$diskFile; VhdFormat='VHDX'; VhdType='Dynamic'; ParentPath=$null; Attached=$false; DiskNumber=17}
Assert-CageOfflineTarget $vm $drive @() $vhd $receipt $root $id
Reject { Assert-CageOfflineTarget $vm $drive @() $vhd $receipt $root ([guid]::NewGuid()) } 'do not match'
Reject { Assert-CageOfflineTarget $vm $drive @('checkpoint') $vhd $receipt $root $id } 'standalone'
$vm.State='Running'; Reject { Assert-CageOfflineTarget $vm $drive @() $vhd $receipt $root $id } 'stopped'
$vm.State='Off'; $vhd.ParentPath='parent'; Reject { Assert-CageOfflineTarget $vm $drive @() $vhd $receipt $root $id } 'standalone'
$vhd.ParentPath=$null; $vhd.Attached=$true; Reject { Assert-CageOfflineTarget $vm $drive @() $vhd $receipt $root $id } 'unattached'
$disk = [pscustomobject]@{Number=17; IsBoot=$false; IsSystem=$false; IsOffline=$false; IsReadOnly=$false; PartitionStyle='GPT'}
Assert-MountedGuestDisk $disk $vhd $diskFile
foreach ($property in @('IsBoot','IsSystem','IsOffline','IsReadOnly')) {
    $disk.$property=$true; Reject { Assert-MountedGuestDisk $disk $vhd $diskFile } 'guest GPT disk'; $disk.$property=$false
}
$vhd.DiskNumber=99; Reject { Assert-MountedGuestDisk $disk $vhd $diskFile } 'guest GPT disk'
$vhd.DiskNumber=17; Reject { Assert-MountedGuestDisk $disk $vhd 'unrelated.vhdx' } 'guest GPT disk'
$part = [pscustomobject]@{GptType='{EBD0A0A2-B9E5-4433-87C0-68B6B72699C7}';DiskNumber=17;PartitionNumber=3;IsBoot=$false;IsSystem=$false}
$recovery = [pscustomobject]@{GptType='{DE94BBA4-06D1-4D40-A16A-BFD50179D6AC}';DiskNumber=17;PartitionNumber=4;IsBoot=$false;IsSystem=$false}
if ((Select-GuestWindowsPartition @($part,$recovery) 17).PartitionNumber -ne 3) { throw 'Wrong guest partition' }
Reject { Select-GuestWindowsPartition @($part,$part) 17 } 'exactly one'
Reject { Select-GuestWindowsPartition @($part) 5 } 'exactly one'

$script:expectedImage = Join-Path $root 'isolated-guest-mount'
$script:fullName = 'Claude_4.23118.0.0_x64__pzs8sxrjxfjjc'
$script:provisioned = @()
$script:writes=0
$image = @{Path=$script:expectedImage; ScratchDirectory=(Join-Path $root 'qa-scratch'); LogPath=(Join-Path $root 'qa.log'); ErrorAction='Stop'}
function Get-AppxProvisionedPackage {
    param($Path,$ScratchDirectory,$LogPath,$ErrorAction,[switch]$Online)
    if ($Path -ne $script:expectedImage -or $Online -or !$ScratchDirectory -or !$LogPath) { throw 'Incorrect DISM target' }
    $script:provisioned
}
function Add-AppxProvisionedPackage {
    param($Path,$PackagePath,[switch]$SkipLicense,$Regions,$ScratchDirectory,$LogPath,$ErrorAction,[switch]$Online)
    if ($Path -ne $script:expectedImage -or $Online -or $Regions -ne 'all' -or !$ScratchDirectory -or !$LogPath) { throw 'Incorrect DISM write target' }
    $script:writes++
    if ($PackagePath -eq 'fixture.msix') { $script:provisioned=@([pscustomobject]@{DisplayName='Claude';PackageName=$script:fullName}) }
}
Reject { Install-OfflineClaude $image 'missing-readback.msix' $script:fullName } 'did not list'
$first=Install-OfflineClaude $image 'fixture.msix' $script:fullName
$second=Install-OfflineClaude $image 'fixture.msix' $script:fullName
if ($first.AlreadyProvisioned -or !$second.AlreadyProvisioned -or $script:writes -ne 2) { throw 'Unexpected repeat provision' }
$script:provisioned[0].PackageName='Claude_wrong_publisher'
Reject { Install-OfflineClaude $image 'fixture.msix' $script:fullName } 'preserved'
if ($script:writes -ne 2) { throw 'Different package was overwritten' }
Write-Output 'Passed offline VM, disk, partition and DISM target guards, exact readback, idempotence and foreign package preservation (simulated).'
