@echo off
setlocal EnableExtensions DisableDelayedExpansion
rem Run in a fresh standard-user session after Windows Setup. Set
rem CAGE_QA_EXPECTED_VM_ID to the GUID read independently from Get-VM on the host.
rem In guest PowerShell, set $env:CAGE_QA_EXPECTED_VM_ID before running this file.
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -NonInteractive -Command ^
  "$ErrorActionPreference = 'Stop';" ^
  "$expected = [guid]::Empty; if (![guid]::TryParse($env:CAGE_QA_EXPECTED_VM_ID, [ref]$expected) -or $expected -eq [guid]::Empty) { throw 'Supply a host-verified VM GUID.' }" ^
  "$system = Get-CimInstance Win32_ComputerSystem;" ^
  "if ($system.Manufacturer -ne 'Microsoft Corporation' -or $system.Model -ne 'Virtual Machine') { throw 'Run this check inside the selected Hyper-V guest.' }" ^
  "$actual = Get-ItemPropertyValue 'HKLM:\SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters' -Name VirtualMachineId;" ^
  "if ([guid]$actual -ne $expected) { throw 'The guest differs from the host-verified VM ID.' }" ^
  "$identity = [Security.Principal.WindowsIdentity]::GetCurrent();" ^
  "$groups = & (Join-Path $env:SystemRoot 'System32\whoami.exe') /groups /fo csv /nh;" ^
  "if (!$groups -or $LASTEXITCODE -ne 0 -or $identity.IsSystem -or $groups -match '\bS-1-5-32-544\b') { throw 'Sign in as a standard guest user outside the Administrators group.' }" ^
  "$legacy = $PSVersionTable.PSVersion;" ^
  "if ($PSVersionTable.PSEdition -ne 'Desktop' -or $legacy.Major -ne 5 -or $legacy.Minor -ne 1) { throw 'Expected the system Windows PowerShell 5.1.' }" ^
  "$command = Get-Command pwsh; $expectedExe = Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'PowerShell\7\pwsh.exe';" ^
  "if ($command.CommandType -ne 'Application' -or $command.Path -ne $expectedExe) { throw 'The user PATH does not resolve pwsh to the installed shell.' }" ^
  "$probe = @(pwsh -NoLogo -NoProfile -NonInteractive -Command '$PSVersionTable.PSEdition; $PSVersionTable.PSVersion.ToString(); [Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString()');" ^
  "if ($LASTEXITCODE -ne 0 -or $probe.Count -ne 3 -or $probe[0] -ne 'Core' -or $probe[1] -notmatch '^7\.[0-9]+\.[0-9]+$' -or $probe[2] -ne 'X64') { throw 'Expected a working x64 PowerShell 7 release.' }" ^
  "[pscustomobject]@{ VMId = $expected; User = $identity.Name; WindowsPowerShell = $legacy.ToString(); PowerShell = $probe[1]; Architecture = $probe[2]; Executable = $command.Path } | ConvertTo-Json -Compress"
exit /b %errorlevel%
