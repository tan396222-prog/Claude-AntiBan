$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
$vm = Hyper-V\Get-VM -Id $targetId
if (!$vm -or $vm.Id -ne $targetId) { throw 'Binding unavailable.' }
if ($action -eq 'start') {
    if ($vm.Generation -ne 2) { throw 'Guest generation unavailable.' }
    $firmware = Hyper-V\Get-VMFirmware -VM $vm
    $security = Hyper-V\Get-VMSecurity -VM $vm
    if ([string]$firmware.SecureBoot -ne 'On' -or [string]$firmware.SecureBootTemplate -ne 'MicrosoftWindows' -or $security.TpmEnabled -ne $true) { throw 'Guest security unavailable.' }
    # Until Cage's managed exit has a Windows adapter, this guest remains offline.
    if (@(Hyper-V\Get-VMNetworkAdapter -VM $vm).Count -ne 0) { throw 'Controlled exit unavailable.' }
    if ([string]$vm.State -eq 'Off') { Hyper-V\Start-VM -VM $vm | Out-Null }
    elseif ([string]$vm.State -ne 'Running') { throw 'Guest cannot be started from this state.' }
} elseif ($action -eq 'stop') {
    if ([string]$vm.State -eq 'Running') {
        # Stop-VM without TurnOff/Force requests the guest shutdown integration service.
        Hyper-V\Stop-VM -VM $vm -ErrorAction Stop | Out-Null
    } elseif ([string]$vm.State -ne 'Off') { throw 'Guest cannot be stopped safely from this state.' }
} else { throw 'Invalid operation.' }
$vm = Hyper-V\Get-VM -Id $targetId
if (!$vm -or $vm.Id -ne $targetId) { throw 'Binding changed.' }
[ordered]@{state=[string]$vm.State} | ConvertTo-Json -Compress
