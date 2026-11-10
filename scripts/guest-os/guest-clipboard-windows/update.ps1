#requires -Version 5.1
#requires -RunAsAdministrator
[CmdletBinding(SupportsShouldProcess)]
param(
    [Parameter(Mandatory)][string]$ProfilePath,
    [Parameter(Mandatory)][string]$PayloadDirectory,
    [Parameter(Mandatory)][pscredential]$GuestAdministrator,
    [switch]$RestartActiveBridge,
    [switch]$GuestOnly
)
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
$profile=Get-Item -LiteralPath $ProfilePath
if($profile -isnot [IO.FileInfo] -or $profile.Length -gt 16384 -or $profile.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Invalid protected guest binding.'}
$binding=Get-Content -LiteralPath $profile.FullName -Raw|ConvertFrom-Json
$vm=Get-VM -Id ([guid]$binding.vm_id)
if($binding.version -ne 1 -or $vm.State -ne 'Running' -or @(Get-VMNetworkAdapter -VM $vm).Count -ne 0 -or
    $profile.Directory.FullName -ne [IO.Path]::GetDirectoryName($binding.relay_executable) -or
    !(Test-Path -LiteralPath $binding.pairing_file)){throw 'The existing VM/profile binding is not usable.'}
$payload=Get-Item -LiteralPath $PayloadDirectory
if($payload -isnot [IO.DirectoryInfo] -or $payload.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Select a local payload directory.'}
foreach($entry in (Get-ChildItem -LiteralPath $payload.FullName -Recurse -Force)){if($entry.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Payload links are not allowed.'}}
foreach($name in @('Cage.GuestClipboard.exe','cage-guest-files.exe')){if(!(Test-Path -LiteralPath (Join-Path $payload.FullName $name))){throw 'The fixed bridge payloads are required.'}}
if(!$GuestOnly){
    # A running packet or clipboard relay maps this host executable. Detect
    # that conflict before stopping the independent guest GUI agent.
    $image=[IO.Path]::GetFileNameWithoutExtension($binding.relay_executable)
    foreach($process in [Diagnostics.Process]::GetProcessesByName($image)){
        try { if($process.MainModule.FileName -ieq $binding.relay_executable){throw 'The bound host relay is active. Close its associated workbench before a full update, or use GuestOnly with a compatible host relay.'} }
        finally { $process.Dispose() }
    }
}
if(!$PSCmdlet.ShouldProcess($vm.Id,'Update only the independently bound guest bridge application files')){return}
$session=New-PSSession -VMId $vm.Id -Credential $GuestAdministrator
$deployment=$null
try {
    $deployment=Invoke-Command -Session $session -ArgumentList $vm.Id,$binding.gui_user_sid,[bool]$RestartActiveBridge -ScriptBlock {
        param($expected,$sid,$restart)
        $ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
        if([guid](Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters').VirtualMachineId -ne $expected){throw 'guest_identity_mismatch'}
        $name="Cage-GUI-Bridge-$expected-$sid";$task=Get-ScheduledTask -TaskName $name
        $program=Join-Path $env:ProgramFiles ("Cage Guest Bridge\"+$expected)
        $actualSID=([Security.Principal.NTAccount]::new($task.Principal.UserId)).Translate([Security.Principal.SecurityIdentifier]).Value
        if($task.Description -cne "Cage protected GUI bridge $expected $sid" -or $actualSID -ne $sid -or
            $task.Principal.RunLevel -ne 'Limited' -or $task.Principal.LogonType -ne 'Interactive' -or
            $task.Actions.Count -ne 1 -or $task.Actions[0].Execute -ine (Join-Path $program 'Cage.GuestClipboard.exe')){throw 'The GUI task does not match the owned deployment.'}
        $active=$task.State -eq 'Running'
        if($active -and !$restart){throw 'The bridge is active. Close its workbench before updating, or explicitly request RestartActiveBridge.'}
        if($active){
            Stop-ScheduledTask -InputObject $task
            $until=[DateTime]::UtcNow.AddSeconds(10)
            while((Get-ScheduledTask -TaskName $name).State -eq 'Running' -and [DateTime]::UtcNow -lt $until){Start-Sleep -Milliseconds 100}
            if((Get-ScheduledTask -TaskName $name).State -eq 'Running'){throw 'The owned bridge did not stop; preserved its application files.'}
        }
        [pscustomobject]@{Program=$program;Task=$name;WasActive=$active}
    }
    foreach($entry in (Get-ChildItem -LiteralPath $payload.FullName -Force)){Copy-Item -LiteralPath $entry.FullName -Destination $deployment.Program -Recurse -ToSession $session -Force}
    if(!$GuestOnly){foreach($entry in (Get-ChildItem -LiteralPath $payload.FullName -Force)){Copy-Item -LiteralPath $entry.FullName -Destination $profile.Directory.FullName -Recurse -Force}}
    $started=Invoke-Command -Session $session -ArgumentList $deployment.Task,$binding.gui_user_sid,$deployment.Program -ScriptBlock {
        param($name,$sid,$program)
        $administrators=[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
        foreach($entry in @((Get-Item -LiteralPath $program)) + @(Get-ChildItem -LiteralPath $program -Recurse -Force)){
            if($entry.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Guest application files must not traverse links.'}
            $acl=Get-Acl -LiteralPath $entry.FullName;$acl.SetOwner($administrators);Set-Acl -LiteralPath $entry.FullName -AclObject $acl
        }
        $gui=@(Get-CimInstance Win32_Process -Filter "Name='explorer.exe'"|Where-Object {$_.SessionId -gt 0}|ForEach-Object{Invoke-CimMethod -InputObject $_ -MethodName GetOwnerSid}|Where-Object {$_.Sid -eq $sid}).Count -gt 0
        if($gui){Start-ScheduledTask -TaskName $name};$gui
    }
    if(@(Get-VMNetworkAdapter -VM (Get-VM -Id $vm.Id)).Count -ne 0){throw 'VM networking changed during update.'}
    [pscustomobject]@{Updated=$true;HostRelayUpdated=(!$GuestOnly);RequiresGUILogin=(!$started);PairingPreserved=$true;PersonalDataPreserved=$true}
} catch {
    $failure=$_
    if($deployment -and $deployment.WasActive -and $session.State -eq 'Opened'){
        try {
            Invoke-Command -Session $session -ArgumentList $deployment.Task,$deployment.Program -ScriptBlock {
                param($name,$program)
                $ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
                $exe=Join-Path $program 'Cage.GuestClipboard.exe'
                if((Get-Item -LiteralPath $exe).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Bridge executable changed to a link.'}
                # Do not launch a truncated executable after a failed copy.
                & $exe self-test | Out-Null
                if($LASTEXITCODE -ne 0){throw 'The copied bridge failed its self-test.'}
                Start-ScheduledTask -TaskName $name
            } | Out-Null
        } catch { throw 'The bridge update failed and its GUI task could not be restored; preserve the binding and application files for recovery.' }
    }
    throw $failure
} finally { Remove-PSSession $session }
