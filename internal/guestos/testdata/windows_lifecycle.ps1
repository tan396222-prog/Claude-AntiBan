param([Parameter(Mandatory)][string]$Query)
$ErrorActionPreference='Stop'
$script:targetId=[guid]'9e97dd9a-eb36-489a-bb1d-8d28a0d78682'
$script:state='Off';$script:nics=0;$script:mutations=0;$script:generation=2;$script:secure='On'
New-Module -Name Hyper-V -ScriptBlock {
 function Get-VM { param([Guid]$Id) if ($Id -ne $script:targetId) { throw 'Unscoped VM operation.' }; [pscustomobject]@{Id=$Id;State=$script:state;Generation=$script:generation} }
 function Get-VMFirmware { param($VM) if ($VM.Id -ne $script:targetId) { throw 'Wrong VM.' };[pscustomobject]@{SecureBoot=$script:secure;SecureBootTemplate='MicrosoftWindows'} }
 function Get-VMSecurity { param($VM) [pscustomobject]@{TpmEnabled=$true} }
 function Get-VMNetworkAdapter { param($VM) for($i=0;$i -lt $script:nics;$i++){[pscustomobject]@{Id=$i}} }
 function Start-VM { param($VM) if($VM.Id -ne $script:targetId){throw 'Wrong VM.'};$script:state='Running';$script:mutations++ }
 function Stop-VM { param($VM,[switch]$TurnOff,[switch]$Force) if($TurnOff -or $Force -or $VM.Id -ne $script:targetId){throw 'Unsafe stop.'};$script:state='Off';$script:mutations++ }
 Export-ModuleMember -Function *
} | Import-Module -Force
# Module state is independent: configure it deliberately for each case.
$module=Get-Module Hyper-V
& $module { $script:targetId=[guid]'9e97dd9a-eb36-489a-bb1d-8d28a0d78682';$script:state='Off';$script:nics=0;$script:mutations=0;$script:generation=2;$script:secure='On' }
$action='start';$result=(& $Query)|ConvertFrom-Json
if($result.state -ne 'Running'){throw 'Start failed.'}
$action='stop';$result=(& $Query)|ConvertFrom-Json
if($result.state -ne 'Off'){throw 'Stop failed.'}
$action='stop';$result=(& $Query)|ConvertFrom-Json
if((& $module {$script:mutations}) -ne 2){throw 'Non-idempotent stop.'}
foreach($unsafe in 'nic','generation','secure'){
 & $module { $script:nics=0;$script:generation=2;$script:secure='On' }
 switch($unsafe){'nic'{& $module {$script:nics=1}}'generation'{& $module {$script:generation=1}}'secure'{& $module {$script:secure='Off'}}}
 $action='start';$rejected=$false;try{& $Query | Out-Null}catch{$rejected=$true}
 if(!$rejected -or (& $module {$script:mutations}) -ne 2){throw "Unsafe $unsafe guest started."}
}
'Windows lifecycle exact VM and safe shutdown checks passed.'
