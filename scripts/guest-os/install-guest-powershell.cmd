@echo off
setlocal EnableExtensions DisableDelayedExpansion
rem Windows Setup runs this command from Cage's read-only answer DVD as SYSTEM.
set "CAGE_PWSH_MEDIA=%~d0\"
set "CAGE_PWSH_HASH=__CAGE_PWSH_SHA256__"
set "CAGE_PWSH_VERSION=__CAGE_PWSH_VERSION__"
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -NonInteractive -Command ^
  "$ErrorActionPreference = 'Stop';" ^
  "$medium = [IO.DriveInfo]::new($env:CAGE_PWSH_MEDIA);" ^
  "if ($medium.DriveType -ne 'CDRom' -or $medium.VolumeLabel -ne 'CAGE_ANSWER' -or ![Environment]::Is64BitProcess -or ![Security.Principal.WindowsIdentity]::GetCurrent().IsSystem) { throw 'PowerShell guest installation requires Windows Setup and the Cage answer DVD.' }" ^
  "$system = Get-CimInstance Win32_ComputerSystem;" ^
  "if ($system.Manufacturer -ne 'Microsoft Corporation' -or $system.Model -ne 'Virtual Machine') { throw 'Expected the Hyper-V guest.' }" ^
  "$zip = Join-Path $medium.RootDirectory.FullName 'PowerShell.zip';" ^
  "$base = Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'PowerShell';" ^
  "$target = Join-Path $base '7';" ^
  "function Test-Runtime([string]$directory, [string]$expectedVersion) {" ^
  "  $exe = Join-Path $directory 'pwsh.exe';" ^
  "  $probe = @(& $exe -NoLogo -NoProfile -NonInteractive -Command '$PSVersionTable.PSVersion.ToString(); [Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString()');" ^
  "  if ($LASTEXITCODE -ne 0 -or $probe.Count -ne 2 -or $probe[0] -notmatch '^7\.[0-9]+\.[0-9]+$' -or $probe[1] -ne 'X64' -or ($expectedVersion -and $probe[0] -ne $expectedVersion)) { throw 'The installed shell did not report the expected x64 PowerShell 7 version.' }" ^
  "}" ^
  "if ((Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash -ne $env:CAGE_PWSH_HASH) { throw 'The offline PowerShell package does not match its pinned SHA-256.' }" ^
  "$null = [IO.Directory]::CreateDirectory($base);" ^
  "if ((Get-Item -LiteralPath $base).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'The PowerShell installation folder is redirected.' }" ^
  "if (!(Test-Path -LiteralPath $target)) {" ^
  "  $stage = Join-Path $base ([guid]::NewGuid().ToString('N'));" ^
  "  try {" ^
  "    Add-Type -AssemblyName System.IO.Compression.FileSystem;" ^
  "    [IO.Compression.ZipFile]::ExtractToDirectory($zip, $stage);" ^
  "    Test-Runtime $stage $env:CAGE_PWSH_VERSION;" ^
  "    try { [IO.Directory]::Move($stage, $target) } catch [IO.IOException] { if (![IO.Directory]::Exists($target)) { throw } }" ^
  "  } finally { if ([IO.Directory]::Exists($stage)) { [IO.Directory]::Delete($stage, $true) } }" ^
  "}" ^
  "if ((Get-Item -LiteralPath $target).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'The installed PowerShell directory is redirected.' }" ^
  "Test-Runtime $target '';" ^
  "$key = [Microsoft.Win32.Registry]::LocalMachine.OpenSubKey('SYSTEM\CurrentControlSet\Control\Session Manager\Environment', $true);" ^
  "try {" ^
  "  $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames);" ^
  "  $entries = @($raw.Split(';') | ForEach-Object { [Environment]::ExpandEnvironmentVariables($_.Trim().Trim([char]34)).TrimEnd('\') });" ^
  "  if ($target -notin $entries) {" ^
  "    $newPath = if ($raw) { $target + ';' + $raw } else { $target };" ^
  "    $kind = if ('Path' -in $key.GetValueNames()) { $key.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString };" ^
  "    $key.SetValue('Path', $newPath, $kind);" ^
  "  }" ^
  "} finally { $key.Close() }"
exit /b %errorlevel%
