#requires -Version 7.0
# Runs only on a disposable Windows test user. Does not start a VM.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Installer)
. (Join-Path $PSScriptRoot 'windows-common.ps1')
if (-not $IsWindows) { throw 'Installation smoke tests must run on Windows.' }
$target = Join-Path $env:LOCALAPPDATA ('Programs/Cage-test-' + [guid]::NewGuid().ToString('N'))
$savedKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ClaudeCage'
if (Test-Path $savedKey) { throw 'Use a disposable Windows user without an existing Cage installation.' }
$fixture = Join-Path $PSScriptRoot 'personal-data-fixture.mjs'
$manifest = Join-Path ([IO.Path]::GetTempPath()) ('cage-personal-data-' + [guid]::NewGuid().ToString('N') + '.json')
Invoke-BuildTool 'node' @($fixture, 'seed', $env:USERPROFILE, $manifest)
function Assert-PersonalData {
    Invoke-BuildTool 'node' @($fixture, 'verify', $env:USERPROFILE, $manifest)
}
function Run-Installer {
    param([switch]$Update, [int]$ExpectedExitCode = 0, [string[]]$ShortcutOptions = @())
    $installationReport = Join-Path (Get-WindowsWorkbenchTestBase) ('cage-smoke-installation-' + [guid]::NewGuid().ToString('N') + '.json')
    $arguments = @('/S', "/DIAGNOSTICS=$installationReport")
    if ($Update) { $arguments += '/UPDATE' }
    $arguments += $ShortcutOptions
    $arguments += "/D=$target"
    $process = Start-Process -FilePath $Installer -ArgumentList $arguments -PassThru
    if (-not $process.WaitForExit(300000)) { throw 'Installer exceeded five minutes.' }
    if ($process.ExitCode -ne $ExpectedExitCode) {
        foreach ($path in @($installationReport, "$installationReport.helper.json")) {
            try {
                if ((Test-Path -LiteralPath $path -PathType Leaf) -and (Get-Item -LiteralPath $path).Length -le 8192) {
                    $value = Get-Content -LiteralPath $path -Raw | ConvertFrom-Json -AsHashtable
                    $safe = @{phase = $value['phase']; status = $value['status']; helperExit = $value['helperExit']; operation = $value['operation']; pointerSize = $value['pointerSize']}
                    if ($value['errors']) { $safe.errors = @($value['errors'] | Select-Object -First 4 | ForEach-Object { @{type = $_['type']; hresult = $_['hresult']} }) }
                    Write-Host ('Cage installer diagnostic: ' + ($safe | ConvertTo-Json -Depth 4 -Compress))
                }
            } catch { Write-Warning 'The bounded installer diagnostic could not be read.' }
        }
        throw "Installer returned $($process.ExitCode), expected $ExpectedExitCode"
    }
}
$desktopShortcut = Join-Path ([Environment]::GetFolderPath('DesktopDirectory')) 'Cage.lnk'
$startMenuDirectory = Join-Path ([Environment]::GetFolderPath('Programs')) 'Cage'
function Assert-Shortcuts {
    param([bool]$Desktop, [bool]$StartMenu)
    if ((Test-Path -LiteralPath $desktopShortcut) -ne $Desktop) { throw 'Desktop shortcut differs from the installer selection.' }
    foreach ($name in @('Cage.lnk', 'Uninstall Cage.lnk')) {
        if ((Test-Path -LiteralPath (Join-Path $startMenuDirectory $name)) -ne $StartMenu) { throw "Start menu shortcut differs from the installer selection: $name" }
    }
    if ($Desktop) {
        $shell = New-Object -ComObject WScript.Shell
        if ($shell.CreateShortcut($desktopShortcut).TargetPath -ne (Join-Path $target 'Cage.exe')) { throw 'Desktop shortcut targets a different installation.' }
    }
}
# Cover the directory-picker path where the user creates an empty folder first.
New-Item -ItemType Directory -Path $target | Out-Null
Run-Installer
Assert-PersonalData
Assert-Shortcuts $true $true
if (-not (Test-Path -LiteralPath (Join-Path $target 'Cage.exe'))) { throw 'Installer did not create Cage.exe.' }
foreach ($file in @('new-windows-guest.ps1', 'windows-guest-media.ps1', 'install-guest-powershell.cmd', 'publish-windows-component-manifest.ps1')) {
    if (!(Test-Path -LiteralPath (Join-Path $target "resources/guest-os/$file") -PathType Leaf)) {
        throw "Installed app is missing a Windows guest setup file: $file"
    }
}
$phoneImports = @'
import { pathToFileURL } from 'node:url';
import { join } from 'node:path';
for (const file of ['windows-phone-browser.mjs', 'modules/browser-phone.mjs'])
  await import(pathToFileURL(join(process.argv[1], file)));
'@
Invoke-BuildTool 'node' @('--input-type=module', '--eval', $phoneImports, (Join-Path $target 'resources/guest-os/payload'))
Invoke-BuildTool (Join-Path $target 'resources/cage.exe') @('--help')
Invoke-BuildTool (Join-Path $target 'resources/cage.exe') @('verify-vm-assets')
Invoke-BuildTool (Join-Path $target 'resources/cage.exe') @('verify-web-assets')
Invoke-BuildTool (Join-Path $target 'resources/mihomo.exe') @('-v')
Invoke-BuildTool (Join-Path $target 'resources/runtime/qemu/qemu-system-x86_64.exe') @('--version')
Invoke-BuildTool (Join-Path $target 'resources/runtime/openssh/ssh.exe') @('-V')
# Use the same isolated profile, process lifetime and retained diagnostics as
# build acceptance. The pre-existing default profile holds preservation markers.
Assert-WindowsWorkbench -AppDirectory $target
Assert-PersonalData
# A locked installed executable must fail before replacing the installation.
$before = (Get-FileHash -LiteralPath (Join-Path $target 'Cage.exe') -Algorithm SHA256).Hash
$locked = [IO.File]::Open((Join-Path $target 'Cage.exe'), [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
try { Run-Installer -Update -ExpectedExitCode 2 } finally { $locked.Dispose() }
Assert-PersonalData
if ((Get-FileHash -LiteralPath (Join-Path $target 'Cage.exe') -Algorithm SHA256).Hash -ne $before) { throw 'Failed update replaced the existing application.' }
# Reinstall through the real atomic replacement path. User data must survive.
Run-Installer -Update
Assert-PersonalData
Assert-Shortcuts $true $true
Run-Installer -Update -ShortcutOptions @('/DESKTOPSHORTCUT=0', '/STARTMENUSHORTCUT=0')
Assert-Shortcuts $false $false
Run-Installer -Update
Assert-Shortcuts $false $false
Run-Installer -Update -ShortcutOptions @('/DESKTOPSHORTCUT=1', '/STARTMENUSHORTCUT=0')
Assert-Shortcuts $true $false
Run-Installer -Update -ShortcutOptions @('/DESKTOPSHORTCUT=0', '/STARTMENUSHORTCUT=1')
Assert-Shortcuts $false $true
Run-Installer -Update -ShortcutOptions @('/DESKTOPSHORTCUT=1', '/STARTMENUSHORTCUT=1')
Assert-Shortcuts $true $true
Assert-PersonalData
$uninstall = Start-Process -FilePath (Join-Path $target 'Uninstall.exe') -ArgumentList @('/S') -PassThru
if (-not $uninstall.WaitForExit(120000) -or $uninstall.ExitCode -ne 0) { throw 'Uninstall failed.' }
# NSIS may relaunch its uninstaller from TEMP so that it can delete the original.
# The launcher can disappear before the worker clears registration and shortcuts.
$deadline = [DateTime]::UtcNow.AddSeconds(120)
while (((Test-Path -LiteralPath $target) -or (Test-Path $savedKey) -or
        (Test-Path -LiteralPath $desktopShortcut) -or (Test-Path -LiteralPath $startMenuDirectory)) -and
        [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 200 }
if (Test-Path -LiteralPath (Join-Path $target 'Cage.exe')) { throw 'Uninstall left Cage.exe behind.' }
if (Test-Path -LiteralPath (Join-Path $target 'Uninstall.exe')) { throw 'Uninstall left its launcher behind.' }
if (Test-Path -LiteralPath $target) { throw 'Uninstall left packaged files or directories that would block installation into the same folder.' }
if (Test-Path $savedKey) { throw 'Uninstall left the Cage registration behind.' }
Assert-PersonalData
Assert-Shortcuts $false $false
Write-Host 'Install, WebView2 workbench, reinstall, uninstall and user-data preservation passed. VM startup was not tested.'
