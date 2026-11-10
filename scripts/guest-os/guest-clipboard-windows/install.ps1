#requires -Version 5.1
#requires -RunAsAdministrator
<#
.SYNOPSIS
Bind one existing offline Hyper-V guest and its existing interactive user.
.DESCRIPTION
PowerShell Direct is only the authenticated management transport. The agent is
registered as an InteractiveToken, Limited task for the supplied SID; it cannot
use a management/Session0 clipboard. This script does not log a user in, create
accounts, change VM networking, accept licenses or replace a running agent.
Supply a new protected host profile directory. The returned binding contains
only paths and identities; its separate pairing is DPAPI CurrentUser protected.
#>
[CmdletBinding(SupportsShouldProcess)]
param(
    [Parameter(Mandatory)][guid]$VMId,
    [Parameter(Mandatory)][string]$InstanceDirectory,
    [Parameter(Mandatory)][ValidatePattern('^S-1-5-21-[0-9]+-[0-9]+-[0-9]+-[0-9]+$')][string]$GUIUserSID,
    [Parameter(Mandatory)][pscredential]$GuestAdministrator,
    [Parameter(Mandatory)][string]$PayloadDirectory,
    [Parameter(Mandatory)][string]$HostProfileDirectory,
    [string]$HostUserSID = ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value),
    [object]$HostEnrollment
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
if($HostUserSID -notmatch '^S-1-(5-21|12-1)-(\d+-){3}\d+$' -or
    ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value -cne $HostUserSID -and !$HostEnrollment)){throw 'Another host user requires the original enrollment process.'}
if($HostEnrollment -and ($HostEnrollment.HostSID -cne $HostUserSID -or [guid]$HostEnrollment.VMID -ne $VMId)){throw 'Original host enrollment does not match this bridge.'}
$service='f4a4c1ce-3c9f-49f1-b7bf-82df6592c0fa'
function Assert-LocalAncestors([string]$Path){
    if(![IO.Path]::IsPathRooted($Path) -or $Path.StartsWith('\\') -or [IO.DriveInfo]::new([IO.Path]::GetPathRoot($Path)).DriveType -ne 'Fixed'){throw 'A local fixed-disk path is required.'}
    $current=$Path
    while($current){if(Test-Path -LiteralPath $current){if((Get-Item -LiteralPath $current -Force).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Deployment paths must not traverse links.'}};$current=[IO.Path]::GetDirectoryName($current)}
}
$vm=Get-VM -Id $VMId
$instance=Get-Item -LiteralPath $InstanceDirectory
$payload=Get-Item -LiteralPath $PayloadDirectory
if ($VMId -eq [guid]::Empty -or $vm.State -ne 'Running' -or $instance -isnot [IO.DirectoryInfo] -or
    $payload -isnot [IO.DirectoryInfo] -or $instance.Attributes -band [IO.FileAttributes]::ReparsePoint -or
    !($vm.ConfigurationLocation -eq $instance.FullName -or $vm.ConfigurationLocation.StartsWith($instance.FullName.TrimEnd('\')+'\',[StringComparison]::OrdinalIgnoreCase)) -or
    @(Get-VMNetworkAdapter -VM $vm).Count -ne 0) { throw 'Supply the exact running, retained zero-NIC guest and its private instance directory.' }
$profile=$ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($HostProfileDirectory)
foreach($path in @($profile,$instance.FullName,$payload.FullName)){Assert-LocalAncestors $path}
if (!(Test-Path -LiteralPath ([IO.Path]::GetDirectoryName($profile))) -or (Test-Path -LiteralPath $profile)) { throw 'Supply a new host profile directory under an existing local parent.' }
foreach($file in @('Cage.GuestClipboard.exe','cage-guest-files.exe')) {
    $entry=Get-Item -LiteralPath (Join-Path $payload.FullName $file)
    if($entry -isnot [IO.FileInfo] -or $entry.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'The fixed GUI agent and file helper payloads are required.'}
}
if(!$PSCmdlet.ShouldProcess($VMId,'Install a protected GUI clipboard bridge and bind this host user')){return}
$session=New-PSSession -VMId $VMId -Credential $GuestAdministrator
try {
    $checked=Invoke-Command -Session $session -ArgumentList $VMId,$GUIUserSID -ScriptBlock {
        param($expected,$sid)
        $ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
        $kvp=Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters' -Name VirtualMachineId
        if([guid]$kvp.VirtualMachineId -ne $expected){throw 'guest_identity_mismatch'}
        $users=@(Get-LocalUser | Where-Object {$_.SID.Value -eq $sid -and $_.Enabled})
        if($users.Count -ne 1){throw 'The independently selected GUI SID is not an enabled existing local user.'}
        $name="Cage-GUI-Bridge-$expected-$sid"
        if(Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue){throw 'A bridge task already exists; preserve the running user session and use its existing binding.'}
        $program=Join-Path $env:ProgramFiles ("Cage Guest Bridge\"+$expected)
        $data=Join-Path $env:ProgramData ("Cage\GuestBridge\"+$expected)
        foreach($directory in @($program,$data)){$current=$directory;while($current){if(Test-Path -LiteralPath $current){if((Get-Item -LiteralPath $current -Force).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Guest deployment paths must not traverse links.'}};$current=[IO.Path]::GetDirectoryName($current)}}
        if((Test-Path -LiteralPath $program) -or (Test-Path -LiteralPath $data)){throw 'A guest bridge deployment already exists; preserve it.'}
        foreach($directory in @($program,$data)){
            $null=New-Item -ItemType Directory -Path $directory -Force
            $acl=[Security.AccessControl.DirectorySecurity]::new();$acl.SetAccessRuleProtection($true,$false)
            foreach($owner in @('S-1-5-18','S-1-5-32-544')){$acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($owner),'FullControl','ContainerInherit,ObjectInherit','None','Allow'))}
            $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid),'ReadAndExecute','ContainerInherit,ObjectInherit','None','Allow'))
            Set-Acl -LiteralPath $directory -AclObject $acl
        }
        [pscustomobject]@{Program=$program;Data=$data;Task=$name;User=$users[0].Name}
    }
    # Copy only the caller-selected payload directory. Optional Node/browser
    # dependencies remain local to this deployment and never need a guest NIC.
    Get-ChildItem -LiteralPath $payload.FullName -File|ForEach-Object {
        if($_.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Payload links are not allowed.'}
        Copy-Item -LiteralPath $_.FullName -Destination (Join-Path $checked.Program $_.Name) -ToSession $session
    }
    $secret=New-Object byte[] 32;$random=[Security.Cryptography.RandomNumberGenerator]::Create();$random.GetBytes($secret);$random.Dispose()
    try {
        Invoke-Command -Session $session -ArgumentList $checked.Data,$secret,$GUIUserSID -ScriptBlock {
            param($data,[byte[]]$secret,$sid)
            $file=Join-Path $data 'pairing.bin';[IO.File]::WriteAllBytes($file,$secret)
            $acl=[Security.AccessControl.FileSecurity]::new();$acl.SetAccessRuleProtection($true,$false)
            foreach($owner in @('S-1-5-18','S-1-5-32-544')){$acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($owner),'FullControl','Allow'))}
            $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid),'Read','Allow'));Set-Acl -LiteralPath $file -AclObject $acl
        }
        $hostSID=[Security.Principal.SecurityIdentifier]::new($HostUserSID)
        $hostOwner=if($HostEnrollment){[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')}else{$hostSID}
        $acl=[Security.AccessControl.DirectorySecurity]::new();$acl.SetAccessRuleProtection($true,$false);$acl.SetOwner($hostOwner)
        foreach($sid in @($hostSID,[Security.Principal.SecurityIdentifier]::new('S-1-5-18'),[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))){$acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid,'FullControl','ContainerInherit,ObjectInherit','None','Allow'))}
        $null=New-Item -ItemType Directory -Path $profile;Set-Acl -LiteralPath $profile -AclObject $acl
        foreach($entry in (Get-ChildItem -LiteralPath $payload.FullName -File)){Copy-Item -LiteralPath $entry.FullName -Destination (Join-Path $profile $entry.Name)}
        Add-Type -AssemblyName System.Security
        $entropy=[Security.Cryptography.SHA256]::Create().ComputeHash([Text.Encoding]::UTF8.GetBytes("Cage.HyperV.v1:$($VMId.ToString('D')):$GUIUserSID"))
        $pairing=Join-Path $profile 'pairing.dpapi'
        # The relay verifies protected file ACLs independently before decrypting.
        $fileACL=[Security.AccessControl.FileSecurity]::new();$fileACL.SetAccessRuleProtection($true,$false);$fileACL.SetOwner($hostOwner)
        foreach($sid in @($hostSID,[Security.Principal.SecurityIdentifier]::new('S-1-5-18'),[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))){$fileACL.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid,'FullControl','Allow'))}
        if(!$HostEnrollment){[IO.File]::WriteAllBytes($pairing,[Security.Cryptography.ProtectedData]::Protect($secret,$entropy,'CurrentUser'));Set-Acl -LiteralPath $pairing -AclObject $fileACL}
        $key="HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Virtualization\GuestCommunicationServices\$service"
        if(Test-Path -LiteralPath $key){if((Get-ItemProperty -LiteralPath $key).ElementName -cne 'Cage GUI Guest Bridge v1'){throw 'The fixed service GUID is owned by another application.'}}
        else{$null=New-Item -Path $key;New-ItemProperty -LiteralPath $key -Name ElementName -Value 'Cage GUI Guest Bridge v1' -PropertyType String|Out-Null}
        $task=Invoke-Command -Session $session -ArgumentList $checked.Program,$checked.Data,$checked.Task,$GUIUserSID,$VMId -ScriptBlock {
            param($program,$data,$name,$sid,$vm)
            $executable=Join-Path $program 'Cage.GuestClipboard.exe';$token=Join-Path $data 'pairing.bin'
            $action=New-ScheduledTaskAction -Execute $executable -Argument "serve --vm-id $vm --sid $sid --token-file `"$token`""
            $trigger=New-ScheduledTaskTrigger -AtLogOn -User $sid
            $principal=New-ScheduledTaskPrincipal -UserId $sid -LogonType Interactive -RunLevel Limited
            $settings=New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew
            $null=Register-ScheduledTask -TaskName $name -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description "Cage protected GUI bridge $vm $sid"
            $gui=@(Get-CimInstance Win32_Process -Filter "Name='explorer.exe'"|Where-Object {$_.SessionId -gt 0}|ForEach-Object {Invoke-CimMethod -InputObject $_ -MethodName GetOwnerSid}|Where-Object {$_.Sid -eq $sid}).Count -gt 0
            if($gui){Start-ScheduledTask -TaskName $name}
            [pscustomobject]@{Registered=$true;GUIAlreadyLoggedIn=$gui}
        }
        $binding=[ordered]@{version=1;vm_id=$VMId.ToString('D');gui_user_sid=$GUIUserSID;gui_user=$checked.User;instance_directory=$instance.FullName;relay_executable=(Join-Path $profile 'Cage.GuestClipboard.exe');pairing_file=$pairing}
        $path=Join-Path $profile 'windows-guest.json';[IO.File]::WriteAllText($path,($binding|ConvertTo-Json -Compress)+[Environment]::NewLine,[Text.UTF8Encoding]::new($false));Set-Acl -LiteralPath $path -AclObject $fileACL
        if($HostEnrollment){. (Join-Path $PSScriptRoot '../windows-host-enrollment.ps1');Send-CageHostEnrollmentKey $HostEnrollment 'gui' $GUIUserSID $secret}
        if(@(Get-VMNetworkAdapter -VM (Get-VM -Id $VMId)).Count -ne 0){throw 'VM networking changed during deployment.'}
        [pscustomobject]@{Profile=$path;VMId=$VMId;GUIUserSID=$GUIUserSID;TaskRegistered=$task.Registered;RequiresGUILogin=(!$task.GUIAlreadyLoggedIn)}
    } finally { [Array]::Clear($secret,0,$secret.Length) }
} finally { Remove-PSSession $session }
