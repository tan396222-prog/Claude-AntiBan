param([Parameter(Mandatory)][string]$Query)
$ErrorActionPreference='Stop'
$targetId=[guid]'9e97dd9a-eb36-489a-bb1d-8d28a0d78682'
$module=New-Module -Name Hyper-V -ScriptBlock {
 $script:id=[guid]'9e97dd9a-eb36-489a-bb1d-8d28a0d78682';$script:state='Off';$script:cpus=2;$script:start=5GB;$script:min=512MB;$script:max=1TB;$script:dynamic=$false;$script:mutations=0;$script:failMemory=$false
 function Get-VM {param([Guid]$Id) if($Id -ne $script:id){throw 'Unscoped VM'};[pscustomobject]@{Id=$Id;State=$script:state;MemoryAssigned=0}}
 function Get-VMProcessor {param($VM) if($VM.Id -ne $script:id){throw 'Wrong VM'};[pscustomobject]@{Count=$script:cpus}}
 function Get-VMMemory {param($VM) if($VM.Id -ne $script:id){throw 'Wrong VM'};[pscustomobject]@{Startup=$script:start;Minimum=$script:min;Maximum=$script:max;DynamicMemoryEnabled=$script:dynamic}}
 function Set-VMProcessor {param($VM,[int]$Count) if($VM.Id -ne $script:id -or $script:state -ne 'Off'){throw 'Unsafe apply'};$script:cpus=$Count;$script:mutations++}
 function Set-VMMemory {param($VM,[bool]$DynamicMemoryEnabled,[long]$MinimumBytes,[long]$StartupBytes,[long]$MaximumBytes) if($VM.Id -ne $script:id -or $script:state -ne 'Off'){throw 'Unsafe memory apply'};if($script:failMemory -and $MaximumBytes -eq 16GB){throw 'Synthetic memory failure'};$script:min=$MinimumBytes;$script:start=$StartupBytes;$script:max=$MaximumBytes;$script:dynamic=$DynamicMemoryEnabled;$script:mutations++}
 Export-ModuleMember -Function *
};$module|Import-Module -Force
$memoryGiB=0;$cpus=0;$read=(& $Query)|ConvertFrom-Json
if($read.cpus -ne 2 -or $read.memory_startup_bytes -ne 5GB -or (& $module {$script:mutations}) -ne 0){throw 'Read mutated resource settings'}
$memoryGiB=8;$cpus=4;$applied=(& $Query)|ConvertFrom-Json
if($applied.cpus -ne 4 -or $applied.memory_maximum_bytes -ne 8GB -or !$applied.dynamic_memory){throw 'Apply mismatch'}
& $module {$script:state='Running'}
$blocked=$false;try{& $Query|Out-Null}catch{$blocked=$true}
if(!$blocked -or (& $module {$script:mutations}) -ne 2){throw 'Running VM changed'}
& $module {$script:state='Off'};$memoryGiB=2
$blocked=$false;try{& $Query|Out-Null}catch{$blocked=$true}
if(!$blocked -or (& $module {$script:mutations}) -ne 2){throw 'Invalid memory changed VM'}
& $module {$script:failMemory=$true};$memoryGiB=16;$cpus=6
$blocked=$false;try{& $Query|Out-Null}catch{$blocked=$true}
if(!$blocked -or (& $module {$script:cpus}) -ne 4 -or (& $module {$script:max}) -ne 8GB){throw 'Failed apply did not restore prior resource settings'}
'Windows resource exact VM, readback, stopped apply and rollback passed.'
