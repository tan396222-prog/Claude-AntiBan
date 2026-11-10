$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
$vm=Hyper-V\Get-VM -Id $targetId
if(!$vm -or $vm.Id -ne $targetId){throw 'Bound VM unavailable.'}
$memory=Hyper-V\Get-VMMemory -VM $vm
$processor=Hyper-V\Get-VMProcessor -VM $vm
if($memoryGiB -ne 0 -or $cpus -ne 0){
 if($memoryGiB -lt 4 -or $memoryGiB -gt 1024 -or $cpus -lt 1 -or $cpus -gt 1024 -or [string]$vm.State -ne 'Off'){throw 'Stop the guest before applying Windows 11 resources.'}
 $maximum=[int64]$memoryGiB*1GB
 try{
  Hyper-V\Set-VMProcessor -VM $vm -Count $cpus
  Hyper-V\Set-VMMemory -VM $vm -DynamicMemoryEnabled $true -MinimumBytes 4GB -StartupBytes 4GB -MaximumBytes $maximum
 }catch{
  # Restore only the same VM's prior resource settings; keep its disk untouched.
  try{Hyper-V\Set-VMProcessor -VM $vm -Count $processor.Count}catch{}
  try{Hyper-V\Set-VMMemory -VM $vm -DynamicMemoryEnabled $memory.DynamicMemoryEnabled -MinimumBytes $memory.Minimum -StartupBytes $memory.Startup -MaximumBytes $memory.Maximum}catch{}
  throw
 }
 $vm=Hyper-V\Get-VM -Id $targetId
 if(!$vm -or $vm.Id -ne $targetId){throw 'Bound VM changed.'}
 $memory=Hyper-V\Get-VMMemory -VM $vm
 $processor=Hyper-V\Get-VMProcessor -VM $vm
}
[ordered]@{state=[string]$vm.State;cpus=[int]$processor.Count;memory_startup_bytes=[uint64]$memory.Startup;memory_minimum_bytes=[uint64]$memory.Minimum;memory_maximum_bytes=[uint64]$memory.Maximum;memory_assigned_bytes=[uint64]$vm.MemoryAssigned;dynamic_memory=[bool]$memory.DynamicMemoryEnabled}|ConvertTo-Json -Compress
