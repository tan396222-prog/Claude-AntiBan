#requires -Version 5.1
<#
.SYNOPSIS
Prepare a one-use answer DVD for the empty offline VM created by new-windows-test-vm.ps1.
.DESCRIPTION
Targets an x64 en-US Windows 11 Enterprise Evaluation image. Windows Setup inside
the VM partitions its empty VHDX. The Microsoft OOBE, account and activation
requirements are retained. Pass -AcceptEvaluationTerms only after reviewing the
terms for this evaluation. Otherwise Setup asks inside the VM. -Start boots
the VM now. The answer file has no credentials.
.LINK
https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/unattend/microsoft-windows-setup-userdata-accepteula
.LINK
https://www.microsoft.com/en-us/evalcenter/evaluate-windows-11-enterprise
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][guid]$VMId,
    [Parameter(Mandatory)][string]$InstanceDirectory,
    [Parameter(Mandatory)][ValidatePattern('^[a-fA-F0-9]{64}$')][string]$ExpectedIsoSha256,
    [string]$PowerShellZip,
    [switch]$AcceptEvaluationTerms,
    [switch]$Start
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'windows-guest-media.ps1')

function New-EvaluationAnswer([string]$Template, [int]$ImageIndex, [long]$DiskBytes, [bool]$TermsAccepted) {
    if ($ImageIndex -lt 1 -or $DiskBytes -lt 64GB) { throw 'Invalid evaluation image index or disk capacity.' }
    # Preserve 1.5 GiB for WinRE, plus GPT alignment, the ESP and MSR.
    $osMiB = [long][Math]::Floor($DiskBytes / 1MB) - 1536 - 2 - 260 - 16
    return $Template.Replace('OS_SIZE_MIB', "$osMiB").Replace('EVAL_IMAGE_INDEX', "$ImageIndex").Replace('ACCEPT_EULA', $TermsAccepted.ToString().ToLowerInvariant())
}

function Get-EvaluationImage([string]$Path) {
    $alreadyMounted = (Get-DiskImage -ImagePath $Path).Attached
    if (!$alreadyMounted) { $null = Mount-DiskImage -ImagePath $Path -Access ReadOnly -StorageType ISO }
    try {
        $volume = @(Get-DiskImage -ImagePath $Path | Get-Volume)
        if ($volume.Count -ne 1 -or !$volume[0].DriveLetter) { throw 'Cannot inspect the setup ISO.' }
        $sources = '{0}:\sources' -f $volume[0].DriveLetter
        $images = @(@('install.wim','install.esd') | ForEach-Object { Join-Path $sources $_ } | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf })
        if ($images.Count -ne 1) { throw 'Expected one official Windows installation image.' }
        $matchingImages = @(foreach ($entry in (Get-WindowsImage -ImagePath $images[0])) {
            $detail = Get-WindowsImage -ImagePath $images[0] -Index $entry.ImageIndex
            if ($detail.EditionId -eq 'EnterpriseEval' -and [string]$detail.Architecture -in @('9','amd64','x64') -and $detail.Languages -contains 'en-US') {
                $detail
            }
        })
        if ($matchingImages.Count -ne 1) { throw 'The ISO must contain exactly one x64 en-US EnterpriseEval image.' }
        return $matchingImages[0]
    } finally {
        if (!$alreadyMounted) { $null = Dismount-DiskImage -ImagePath $Path }
    }
}

if ([Environment]::OSVersion.Platform -ne 'Win32NT' -or ![Environment]::Is64BitProcess) {
    throw 'Run in 64-bit Windows PowerShell on the Hyper-V host.'
}
$root = Get-Item -LiteralPath $InstanceDirectory
$vm = Get-VM -Id $VMId
$firmware = Get-VMFirmware -VM $vm
$configPath = [IO.Path]::GetFullPath($vm.ConfigurationLocation).TrimEnd('\')
if ($root -isnot [IO.DirectoryInfo] -or ($root.Attributes -band [IO.FileAttributes]::ReparsePoint) -or
    ($configPath -ne $root.FullName -and !$configPath.StartsWith($root.FullName + '\', [StringComparison]::OrdinalIgnoreCase)) -or
    $vm.State -ne 'Off' -or $vm.Generation -ne 2 -or @(Get-VMNetworkAdapter -VM $vm).Count -or
    @(Get-VMSnapshot -VM $vm).Count -or !(Get-VMSecurity -VM $vm).TpmEnabled -or
    $firmware.SecureBoot -ne 'On' -or $firmware.SecureBootTemplate -ne 'MicrosoftWindows') {
    throw 'Expected a stopped, offline, checkpoint-free generation 2 test VM with Secure Boot and vTPM.'
}
$drives = @(Get-VMHardDiskDrive -VM $vm)
$answerDir = Join-Path $root.FullName 'evaluation-setup'
$mode = if ($AcceptEvaluationTerms) { 'accepted' } else { 'native' }
$mediaDir = Join-Path $answerDir ($mode + '-pwsh7')
$answerISO = Join-Path $mediaDir 'answer.iso'
$genericAnswer = Join-Path $root.FullName 'windows-guest-tools.iso'
$knownAnswers = @('accepted','native','accepted-pwsh7','native-pwsh7') | ForEach-Object { Join-Path (Join-Path $answerDir $_) 'answer.iso' }
$knownAnswers = @($knownAnswers) + @($genericAnswer)
$allDVDs = @(Get-VMDvdDrive -VM $vm)
$dvds = @($allDVDs | Where-Object { $_.Path -notin $knownAnswers })
$priorAnswers = @($allDVDs | Where-Object { $_.Path -in $knownAnswers })
$expectedDisk = Join-Path $root.FullName 'system.vhdx'
if ($drives.Count -ne 1 -or $drives[0].Path -ne $expectedDisk -or $dvds.Count -ne 1 -or !$dvds[0].Path -or $priorAnswers.Count -gt 1) {
    throw 'Expected the single system.vhdx and original installation DVD created for this VM.'
}
if (@(Get-VM | Get-VMHardDiskDrive | Where-Object Path -eq $expectedDisk).Count -ne 1) {
    throw 'The empty test disk must belong exclusively to the selected VM.'
}
$diskFile = Get-Item -LiteralPath $expectedDisk
$vhd = Get-VHD -Path $diskFile.FullName
if ($diskFile.Attributes -band [IO.FileAttributes]::ReparsePoint -or $vhd.VhdType -ne 'Dynamic' -or $vhd.ParentPath -or $vhd.Attached) {
    throw 'Expected an unattached standalone dynamic VHDX in the test instance directory.'
}
$iso = Get-Item -LiteralPath $dvds[0].Path
if ($iso.Extension -ne '.iso' -or (Get-FileHash -LiteralPath $iso.FullName -Algorithm SHA256).Hash -ne $ExpectedIsoSha256) {
    throw 'The mounted setup DVD does not match the expected Microsoft ISO digest.'
}
$edition = Get-EvaluationImage $iso.FullName

# Publish a complete media directory before touching any boot device. A retry
# can reuse that media after a DVD attach or firmware error, even on a new shell.
$null = [IO.Directory]::CreateDirectory($answerDir)
if ((Get-Item -LiteralPath $answerDir).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Unexpected redirected answer directory.' }
$lock = [IO.File]::Open((Join-Path $answerDir 'setup.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
try {
    if ((Get-VM -Id $VMId).State -ne 'Off') { throw 'The test VM started during preparation.' }
    $null = Mount-VHD -Path $diskFile.FullName -ReadOnly -NoDriveLetter
    try {
        $virtualDisk = Get-VHD -Path $diskFile.FullName | Get-Disk
        if (!$virtualDisk.IsReadOnly -or $virtualDisk.PartitionStyle -ne 'RAW') { throw 'Refusing to install on a disk that is not empty and RAW.' }
    } finally { Dismount-VHD -Path $diskFile.FullName }
    if (!(Test-Path -LiteralPath $mediaDir)) {
        $stage = Join-Path $answerDir ([guid]::NewGuid().ToString('N'))
        $stageISO = Join-Path $stage 'answer.iso'
        $stageReceipt = Join-Path $stage 'receipt.json'
        $null = [IO.Directory]::CreateDirectory($stage)
        try {
            $answer = New-EvaluationAnswer ([IO.File]::ReadAllText((Join-Path $PSScriptRoot 'windows-evaluation-autounattend.xml'))) $edition.ImageIndex $vhd.Size $AcceptEvaluationTerms.IsPresent
            New-WindowsGuestAnswerISO $stageISO ([xml]$answer) $PowerShellZip $genericAnswer
            Grant-VMAnswerReadAccess $stageISO $VMId
            $metadata = [ordered]@{
                VMId = $vm.Id; InstanceDirectory = $root.FullName; Disk = $diskFile.FullName
                Edition = $edition.EditionId; ImageIndex = $edition.ImageIndex
                WindowsIsoSHA256 = $ExpectedIsoSha256; AnswerISO = $answerISO
                AnswerSHA256 = (Get-FileHash -LiteralPath $stageISO -Algorithm SHA256).Hash
                LicenseUI = $mode; RequiresMicrosoftOOBE = $true
                PowerShellVersion = $GuestPowerShellVersion; PowerShellSHA256 = $GuestPowerShellSHA256
            }
            if ($AcceptEvaluationTerms) { $metadata['EvaluationTermsAcceptedUtc'] = [DateTime]::UtcNow.ToString('o') }
            [IO.File]::WriteAllText($stageReceipt, ($metadata | ConvertTo-Json))
            [IO.Directory]::Move($stage, $mediaDir)
        } finally {
            # Only remove files created in this attempt's unique staging folder.
            if ([IO.Directory]::Exists($stage)) {
                foreach ($f in @($stageISO, $stageReceipt)) { [IO.File]::Delete($f) }
                [IO.Directory]::Delete($stage)
            }
        }
    }
    $mediaReceipt = Join-Path $mediaDir 'receipt.json'
    $receipt = Get-Content -LiteralPath $mediaReceipt -Raw | ConvertFrom-Json
    if ([guid]$receipt.VMId -ne $VMId -or $receipt.Disk -ne $diskFile.FullName -or $receipt.AnswerISO -ne $answerISO -or
        $receipt.WindowsIsoSHA256 -ne $ExpectedIsoSha256 -or $receipt.LicenseUI -ne $mode -or
        $receipt.PowerShellSHA256 -ne $GuestPowerShellSHA256 -or
        $receipt.AnswerSHA256 -ne (Get-FileHash -LiteralPath $answerISO -Algorithm SHA256).Hash) {
        throw 'Existing answer media does not match this VM, installer or license selection.'
    }
    # Refresh the DVD inventory under the lock. Only the two answer files
    # belonging to this private instance are eligible for replacement.
    $priorAnswers = @(Get-VMDvdDrive -VM $vm | Where-Object { $_.Path -in $knownAnswers })
    if ($priorAnswers.Count -gt 1) { throw 'More than one answer DVD is attached.' }
    if ($priorAnswers.Count -eq 0) { $dvd = Add-VMDvdDrive -VM $vm -Path $answerISO -Passthru }
    elseif ($priorAnswers[0].Path -eq $answerISO) { $dvd = $priorAnswers[0] }
    else { $dvd = Set-VMDvdDrive -VMDvdDrive $priorAnswers[0] -Path $answerISO -Passthru }
    # The installed disk wins after the first reboot. The original Microsoft DVD
    # boots when that disk is blank. The answer DVD contains only data.
    Set-VMFirmware -VM $vm -BootOrder @($drives[0], $dvds[0], $dvd)
    $receiptPath = Join-Path $answerDir 'receipt.json'
    $tempReceipt = Join-Path $answerDir ([guid]::NewGuid().ToString('N') + '.tmp')
    try {
        [IO.File]::Copy($mediaReceipt, $tempReceipt)
        if ([IO.File]::Exists($receiptPath)) {
            [IO.File]::Replace($tempReceipt, $receiptPath, [Management.Automation.Language.NullString]::Value)
        } else { [IO.File]::Move($tempReceipt, $receiptPath) }
    } finally { [IO.File]::Delete($tempReceipt) }
    if ($Start) {
        Start-VM -VM $vm | Out-Null
        # The original installer DVD can request one boot key. N cannot confirm
        # a license prompt. Send it only during the first boot of this blank VM.
        $namespace = 'root/virtualization/v2'
        $system = Get-CimInstance -Namespace $namespace -ClassName Msvm_ComputerSystem -Filter "Name='$VMId'"
        $keyboard = @(Get-CimAssociatedInstance -InputObject $system -ResultClassName Msvm_Keyboard)
        if ($keyboard.Count -eq 1) {
            for ($i = 0; $i -lt 5; $i++) {
                Start-Sleep -Seconds 1
                $key = Invoke-CimMethod -InputObject $keyboard[0] -MethodName TypeKey -Arguments @{ keyCode = [uint32]0x4E }
                if ($key.ReturnValue -ne 0) { Write-Warning 'Check the DVD boot prompt in VMConnect.'; break }
            }
        } else { Write-Warning 'Check the DVD boot prompt in VMConnect.' }
    }
} finally { $lock.Dispose() }
$receipt
