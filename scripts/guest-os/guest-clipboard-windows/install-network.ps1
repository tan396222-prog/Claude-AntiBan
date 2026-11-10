#requires -Version 5.1
#requires -RunAsAdministrator
[CmdletBinding(SupportsShouldProcess)]
param(
    [Parameter(Mandatory)][string]$ProfilePath,
    [Parameter(Mandatory)][pscredential]$GuestAdministrator,
    [Parameter(Mandatory)][string]$AgentPath,
    [Parameter(Mandatory)][string]$WintunPath,
    [Parameter(Mandatory)][string]$WintunLicense,
    [object]$HostEnrollment
)
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
$binding=Get-Content -LiteralPath $ProfilePath -Raw|ConvertFrom-Json
if($HostEnrollment -and [guid]$HostEnrollment.VMID -ne [guid]$binding.vm_id){throw 'Original host enrollment does not match this packet bridge.'}
$vm=Get-VM -Id ([guid]$binding.vm_id)
if($binding.version -ne 1 -or $vm.State -ne 'Running' -or $binding.gui_user_sid -notmatch '^S-1-5-21-' -or
    @(Get-VMNetworkAdapter -VM $vm).Count -ne 0 -or $binding.network_pairing_file){throw 'Use the existing running zero-NIC guest binding without a prior packet pairing.'}
if((Get-FileHash -LiteralPath $WintunPath -Algorithm SHA256).Hash -cne 'E5DA8447DC2C320EDC0FC52FA01885C103DE8C118481F683643CACC3220DAFCE'){throw 'Unexpected pinned Wintun payload.'}
if((Get-AuthenticodeSignature -LiteralPath $WintunPath).Status -ne 'Valid'){throw 'Windows did not validate the official Wintun signature.'}
if(!$PSCmdlet.ShouldProcess($vm.Id,'Install the separate SYSTEM packet bridge without adding a Hyper-V NIC')){return}
$session=New-PSSession -VMId $vm.Id -Credential $GuestAdministrator
try {
    $target=Invoke-Command -Session $session -ArgumentList $vm.Id,$binding.gui_user_sid -ScriptBlock {
        param($id,$sid)
        $ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
        if([guid](Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters').VirtualMachineId -ne $id){throw 'guest_identity_mismatch'}
        $program=Join-Path $env:ProgramFiles ("Cage Guest Network\"+$id);$data=Join-Path $env:ProgramData ("Cage\PacketBridge\"+$id);$name="Cage-Packet-Bridge-$id"
        if((Test-Path -LiteralPath $program) -or (Test-Path -LiteralPath $data) -or (Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue)){throw 'A packet deployment already exists; preserve its key and interface.'}
        foreach($directory in @($program,$data)){
            $current=$directory;while($current){if(Test-Path -LiteralPath $current){if((Get-Item -LiteralPath $current -Force).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Packet deployment paths must not traverse links.'}};$current=[IO.Path]::GetDirectoryName($current)}
            $null=New-Item -ItemType Directory -Path $directory -Force
            $acl=[Security.AccessControl.DirectorySecurity]::new();$acl.SetAccessRuleProtection($true,$false);$acl.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
            foreach($owner in @('S-1-5-18','S-1-5-32-544')){$acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($owner),'FullControl','ContainerInherit,ObjectInherit','None','Allow'))}
            if($directory -eq $program){$acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid),'ReadAndExecute','ContainerInherit,ObjectInherit','None','Allow'))}
            Set-Acl -LiteralPath $directory -AclObject $acl
        }
        [pscustomobject]@{Program=$program;Data=$data;Task=$name}
    }
    Copy-Item -LiteralPath $AgentPath -Destination (Join-Path $target.Program 'Cage.GuestNetwork.exe') -ToSession $session
    Copy-Item -LiteralPath $WintunPath -Destination (Join-Path $target.Program 'wintun.dll') -ToSession $session
    Copy-Item -LiteralPath $WintunLicense -Destination (Join-Path $target.Program 'WINTUN-LICENSE.txt') -ToSession $session
    $secret=New-Object byte[] 32;$random=[Security.Cryptography.RandomNumberGenerator]::Create();$random.GetBytes($secret);$random.Dispose()
    try {
        Invoke-Command -Session $session -ArgumentList $target.Program,$target.Data,$secret -ScriptBlock {
            param($program,$data,[byte[]]$secret)
            $ErrorActionPreference='Stop';$admin=[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
            foreach($entry in (Get-ChildItem -LiteralPath $program -File)){$acl=Get-Acl -LiteralPath $entry.FullName;$acl.SetOwner($admin);Set-Acl -LiteralPath $entry.FullName -AclObject $acl}
            $acl=[Security.AccessControl.FileSecurity]::new();$acl.SetAccessRuleProtection($true,$false);$acl.SetOwner($admin)
            foreach($owner in @('S-1-5-18','S-1-5-32-544')){$acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($owner),'FullControl','Allow'))}
            $stream=[IO.FileStream]::new((Join-Path $data 'packet.bin'),[IO.FileMode]::CreateNew,[Security.AccessControl.FileSystemRights]::Write,[IO.FileShare]::None,4096,[IO.FileOptions]::None,$acl)
            try{$stream.Write($secret,0,$secret.Length);$stream.Flush($true)}finally{$stream.Dispose()}
            if((Get-FileHash -LiteralPath (Join-Path $program 'wintun.dll') -Algorithm SHA256).Hash -cne 'E5DA8447DC2C320EDC0FC52FA01885C103DE8C118481F683643CACC3220DAFCE'){throw 'Guest Wintun transfer digest differs.'}
        }
        Add-Type -AssemblyName System.Security
        $entropy=[Security.Cryptography.SHA256]::Create().ComputeHash([Text.Encoding]::UTF8.GetBytes("Cage.HyperV.v1:$($vm.Id.ToString('D')):$($binding.gui_user_sid)"))
        $pair=Join-Path ([IO.Path]::GetDirectoryName($ProfilePath)) 'packet.dpapi';if(Test-Path -LiteralPath $pair){throw 'A host packet pairing already exists.'}
        if(!$HostEnrollment){[IO.File]::WriteAllBytes($pair,[Security.Cryptography.ProtectedData]::Protect($secret,$entropy,'CurrentUser'));Set-Acl -LiteralPath $pair -AclObject (Get-Acl -LiteralPath $binding.pairing_file)}
        $service='f4a4c1ce-3c9f-49f1-b7bf-82df6592c0fb';$key="HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Virtualization\GuestCommunicationServices\$service"
        if(Test-Path -LiteralPath $key){if((Get-ItemProperty -LiteralPath $key).ElementName -cne 'Cage Packet Guest Bridge v1'){throw 'The packet service GUID is owned by another application.'}}
        else{$null=New-Item -Path $key;New-ItemProperty -LiteralPath $key -Name ElementName -Value 'Cage Packet Guest Bridge v1' -PropertyType String|Out-Null}
        Invoke-Command -Session $session -ArgumentList $target.Program,$target.Data,$target.Task,$vm.Id,$binding.gui_user_sid -ScriptBlock {
            param($program,$data,$name,$vm,$sid)
            $exe=Join-Path $program 'Cage.GuestNetwork.exe';$key=Join-Path $data 'packet.bin'
            $action=New-ScheduledTaskAction -Execute $exe -Argument "serve-network --vm-id $vm --sid $sid --token-file `"$key`""
            $principal=New-ScheduledTaskPrincipal -UserId 'S-1-5-18' -LogonType ServiceAccount -RunLevel Highest
            $settings=New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew
            $null=Register-ScheduledTask -TaskName $name -Action $action -Principal $principal -Trigger (New-ScheduledTaskTrigger -AtStartup) -Settings $settings -Description "Cage protected packet bridge $vm $sid"
        }
        $binding|Add-Member -NotePropertyName network_pairing_file -NotePropertyValue $pair
        $stage=$ProfilePath+'.packet-stage';[IO.File]::WriteAllText($stage,($binding|ConvertTo-Json -Compress)+[Environment]::NewLine,[Text.UTF8Encoding]::new($false));Set-Acl -LiteralPath $stage -AclObject (Get-Acl -LiteralPath $ProfilePath);[IO.File]::Replace($stage,$ProfilePath,[Management.Automation.Language.NullString]::Value)
        Invoke-Command -Session $session -ArgumentList $target.Task -ScriptBlock {param($name);Start-ScheduledTask -TaskName $name}
        if($HostEnrollment){. (Join-Path $PSScriptRoot '../windows-host-enrollment.ps1');Send-CageHostEnrollmentKey $HostEnrollment 'packet' $binding.gui_user_sid $secret}
        if(@(Get-VMNetworkAdapter -VM (Get-VM -Id $vm.Id)).Count -ne 0){throw 'Hyper-V networking changed during packet setup.'}
        [pscustomobject]@{PacketTaskRegistered=$true;SystemKeyProtected=$true;IndependentHostPairing=$true;HyperVAdapters=0;NetworkConfigured=$false}
    }finally{[Array]::Clear($secret,0,$secret.Length)}
}finally{Remove-PSSession $session}
