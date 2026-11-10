#requires -Version 5.1
param([string]$HelperShell = (Get-Process -Id $PID).Path, [switch]$InstallerSmoke)
$ErrorActionPreference = 'Stop'
$helper = Join-Path $PSScriptRoot '../packaging/windows/installation-files.ps1'
$root = Join-Path ([IO.Path]::GetTempPath()) ('cage-install-files-' + [guid]::NewGuid().ToString('N'))
$checks = 0
$windows = $env:OS -eq 'Windows_NT'
function Check([bool]$Value, [string]$Message) {
    if (!$Value) { throw $Message }
    $script:checks++
}
function Write-File([string]$Path, [string]$Value) {
    [IO.Directory]::CreateDirectory((Split-Path $Path -Parent)) | Out-Null
    [IO.File]::WriteAllText($Path, $Value)
}
function New-FileLink([string]$Path, [string]$Target) {
    if ($windows) {
        & cmd.exe /c mklink $Path.Replace('/', '\') $Target.Replace('/', '\') | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Could not create the isolated file link fixture.' }
    } else { [IO.File]::CreateSymbolicLink($Path, $Target) | Out-Null }
}
function Read-Link([string]$Path) {
    $entry = Get-Item -LiteralPath $Path -Force
    if ($windows) { return ([string]@($entry.Target)[0]).Replace('\', '/') }
    return $entry.LinkTarget
}
function Remove-TestTree([string]$Path) {
    foreach ($entry in Get-ChildItem -LiteralPath $Path -Force) {
        if (!$windows -and $entry.Attributes -band [IO.FileAttributes]::ReparsePoint) {
            [IO.File]::Delete($entry.FullName)
        } elseif ($entry.Attributes -band [IO.FileAttributes]::Directory) {
            if (!($entry.Attributes -band [IO.FileAttributes]::ReparsePoint)) { Remove-TestTree $entry.FullName }
            [IO.Directory]::Delete($entry.FullName)
        } else { [IO.File]::Delete($entry.FullName) }
    }
}
function App([string]$Path, [string]$Version) {
    Write-File (Join-Path $Path 'Cage.install-id') 'io.github.lee-xeo.claude-cage'
    Write-File (Join-Path $Path 'program.txt') $Version
    Write-File (Join-Path $Path 'resources/customizable.txt') ('default-' + $Version)
    Write-File (Join-Path $Path 'Uninstall.exe') ('uninstaller-' + $Version)
    $files = @(Get-ChildItem -LiteralPath $Path -File -Recurse | ForEach-Object {
        @{path = $_.FullName.Substring($Path.TrimEnd('\','/').Length + 1).Replace('\', '/'); sha256 = (Get-FileHash $_.FullName).Hash.ToLowerInvariant()}
    })
    @{version = 1; files = $files} | ConvertTo-Json -Depth 4 | Set-Content (Join-Path $Path 'Cage.install-files.json')
}
function Invoke-Helper([string[]]$Arguments, [int]$Expected = 0, [switch]$WithoutFileHash) {
    $entry = $helper
    if ($WithoutFileHash) {
        $entry = Join-Path $root 'helper-without-filehash.ps1'
        Write-File $entry @'
$ErrorActionPreference = 'Stop'
function global:Get-FileHash { throw [Management.Automation.CommandNotFoundException]::new('Get-FileHash is intentionally unavailable in this native helper fixture.') }
$remaining = @($args | Select-Object -Skip 1)
& $args[0] @remaining
'@
        $Arguments = @($helper) + $Arguments
    }
    try {
        $ErrorActionPreference = 'Continue'
        $output = & $HelperShell -NoProfile -File $entry @Arguments 2>&1 | Out-String
    } finally { $ErrorActionPreference = 'Stop' }
    if ($LASTEXITCODE -ne $Expected) { throw "Helper returned $LASTEXITCODE, expected $Expected. $output" }
}
function Prepare([string]$Old, [string]$Stage, [string]$Receipt) {
    Invoke-Helper @('-Operation','prepare','-Source',$Old,'-Target',$Stage,'-Receipt',$Receipt,'-Backup',($Old+'-backup'))
}
function Commit([string]$Old, [string]$Stage, [string]$Receipt) {
    $backup = $Old + '-backup'
    [IO.Directory]::Move($Old, $backup)
    Invoke-Helper @('-Operation','move','-Source',$backup,'-Target',$Stage,'-Receipt',$Receipt)
    [IO.Directory]::Move($Stage, $Old)
    Invoke-Helper @('-Operation','cleanup','-Source',$backup,'-Target',$Old,'-Receipt',$Receipt)
    return $backup
}
function Test-InstallerTransaction {
    if (!$windows -or $PSVersionTable.PSVersion.Major -lt 7) { throw 'The optional NSIS fixture requires PowerShell 7 on a disposable Windows user.' }
    $key = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ClaudeCage'
    $desktop = Join-Path ([Environment]::GetFolderPath('DesktopDirectory')) 'Cage.lnk'
    $menu = Join-Path ([Environment]::GetFolderPath('Programs')) 'Cage'
    if ((Test-Path $key) -or (Test-Path -LiteralPath $desktop) -or (Test-Path -LiteralPath $menu)) { throw 'The NSIS fixture refuses an existing Cage registration or shortcuts.' }
    $runtime = $false
    foreach ($hive in @([Microsoft.Win32.RegistryHive]::LocalMachine, [Microsoft.Win32.RegistryHive]::CurrentUser)) {
        $base = [Microsoft.Win32.RegistryKey]::OpenBaseKey($hive, [Microsoft.Win32.RegistryView]::Registry32)
        $client = $base.OpenSubKey('SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}')
        try { if ($client -and [string]$client.GetValue('pv') -notin @('','0.0.0.0')) { $runtime = $true } }
        finally { if ($client) { $client.Dispose() }; $base.Dispose() }
    }
    if (!$runtime) { Write-Output 'SKIP: quick NSIS transaction needs an existing WebView2 runtime; full installer acceptance is still required.'; return }
    $scenario = Join-Path $root 'nsis-transaction'; $app = Join-Path $scenario 'payload'; $target = Join-Path $scenario 'empty target'
    $setup = Join-Path $scenario 'fixture-setup.exe'
    $reportBase = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }
    $report = Join-Path $reportBase ('cage-smoke-installation-fixture-' + [guid]::NewGuid().ToString('N') + '.json')
    Write-File (Join-Path $app 'LICENSE.txt') 'Compile-only text payload; no Cage application or VM.'
    App $app 'fixture'
    $version = (Get-Content (Join-Path $PSScriptRoot '../VERSION') -Raw).Trim()
    & (Get-Command makensis.exe -ErrorAction Stop).Source /V2 /INPUTCHARSET UTF8 "/DAPP_DIR=$app" "/DVERSION=$version" "/DVERSION_NUMERIC=$(($version -split '-')[0]).0" "/DFILE_MANIFEST=$app\Cage.install-files.json" "/DOUTPUT_FILE=$setup" (Join-Path $PSScriptRoot '../packaging/windows/Cage.nsi')
    if ($LASTEXITCODE -ne 0) { throw 'The production NSIS fixture did not compile.' }
    function Run-Fixture([string]$Executable, [string[]]$Arguments) {
        $process = Start-Process -FilePath $Executable -ArgumentList $Arguments -PassThru
        try {
            if (!$process.WaitForExit(30000)) { $process.Kill($true); $process.WaitForExit(); throw 'The owned NSIS fixture exceeded 30 seconds.' }
            if ($process.ExitCode -ne 0) {
                foreach ($path in @($report, "$report.helper.json", "$report.uninstall.json", "$report.uninstall.json.helper.json")) {
                    if ((Test-Path -LiteralPath $path) -and (Get-Item -LiteralPath $path).Length -le 8192) { Write-Output ('Quick NSIS diagnostic: ' + (Get-Content -LiteralPath $path -Raw)) }
                }
                throw "The NSIS fixture returned $($process.ExitCode)."
            }
        } finally { $process.Dispose() }
    }
    try {
        [IO.Directory]::CreateDirectory($target) | Out-Null
        Run-Fixture $setup @('/S','/DESKTOPSHORTCUT=0','/STARTMENUSHORTCUT=0',"/DIAGNOSTICS=`"$report`"","/D=$target")
        Check ((Get-Content (Join-Path $target 'program.txt') -Raw) -eq 'fixture') 'NSIS did not publish its text payload.'
        Check (!(Test-Path -LiteralPath ($target + '.cage-installation.json'))) 'NSIS left the first-install receipt unfinished.'
        Check ((Get-ItemProperty $key).InstallLocation -eq $target) 'NSIS registered a different fixture target.'
    } finally {
        if (Test-Path $key) {
            if ((Get-ItemProperty $key).InstallLocation -ne $target) { throw 'NSIS fixture registration changed owner; retained for inspection.' }
            Run-Fixture (Join-Path $target 'Uninstall.exe') @('/S',('/DIAGNOSTICS="' + $report + '.uninstall.json"'))
            # NSIS can return from its bootstrapper while the TEMP worker is
            # still running. Its helper removes the target before NSIS clears
            # registration and shortcuts, so target disappearance is not done.
            $deadline = [DateTime]::UtcNow.AddSeconds(120)
            while (((Test-Path -LiteralPath $target) -or (Test-Path $key) -or (Test-Path -LiteralPath $desktop) -or (Test-Path -LiteralPath $menu)) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 100 }
        }
    }
    $remaining = @{target = (Test-Path -LiteralPath $target); registration = (Test-Path $key); desktopShortcut = (Test-Path -LiteralPath $desktop); startMenu = (Test-Path -LiteralPath $menu)}
    if ($remaining.Values -contains $true) {
        foreach ($path in @("$report.uninstall.json", "$report.uninstall.json.helper.json")) {
            if ((Test-Path -LiteralPath $path) -and (Get-Item -LiteralPath $path).Length -le 8192) { Write-Output ('Quick NSIS uninstall diagnostic: ' + (Get-Content -LiteralPath $path -Raw)) }
        }
    }
    Check (!($remaining.Values -contains $true)) ('NSIS fixture did not finish uninstalling owned artifacts: ' + ($remaining | ConvertTo-Json -Compress))
    Write-Output 'PASS: production NSIS silently installed/uninstalled its text-only fixture through PowerShell 5.1 transactions; no application or VM was started.'
}
try {
    if ($InstallerSmoke) { Test-InstallerTransaction }
    # A new install has no backup path or existing ownership manifest.
    $scenario = Join-Path $root 'first-install'
    $old = Join-Path $scenario 'empty-destination'; $stage = Join-Path $scenario 'staged'; $receipt = Join-Path $scenario 'receipt.json'
    [IO.Directory]::CreateDirectory($old) | Out-Null
    App $stage 'fresh'
    Invoke-Helper @('-Operation','prepare','-Source',$old,'-Target',$stage,'-Receipt',$receipt)
    $transfer = Get-Content -LiteralPath $receipt -Raw | ConvertFrom-Json
    Check (!$transfer.backupPath -and @($transfer.moves).Count -eq 0) 'First install invented a backup or personal file transfer.'
    [IO.Directory]::Delete($old); [IO.Directory]::Move($stage, $old)
    Invoke-Helper @('-Operation','complete','-Target',$old,'-Receipt',$receipt) -WithoutFileHash
    Check (!(Test-Path -LiteralPath $receipt)) 'First install did not finish its transaction.'

    $diagnostic = Join-Path $scenario 'diagnostic.json'
    Write-File (Join-Path $old 'Cage.install-files.json') 'invalid-private-manifest'
    Invoke-Helper @('-Operation','prepare','-Target',$old,'-Receipt',$receipt,'-Diagnostics',$diagnostic) 1
    $text = Get-Content -LiteralPath $diagnostic -Raw
    $failure = $text | ConvertFrom-Json
    Check ($failure.operation -eq 'prepare' -and $failure.phase -eq 'reading_new_manifest' -and @($failure.errors).Count -gt 0) 'Helper omitted its safe failure classification.'
    Check (!$text.Contains($root) -and !$text.Contains('invalid-private-manifest') -and !$text.Contains('message')) 'Helper diagnostic contains file paths or private error text.'

    foreach ($manifestState in @('current','missing','invalid')) {
        $scenario = Join-Path $root $manifestState
        $old = Join-Path $scenario 'installed'; $stage = Join-Path $scenario 'staged'; $receipt = Join-Path $scenario 'receipt.json'
        App $old 'old'; App $stage 'new'
        $manifest = Join-Path $old 'Cage.install-files.json'
        if ($manifestState -eq 'missing') { Remove-Item $manifest }
        if ($manifestState -eq 'invalid') { Write-File $manifest '{broken' }
        Write-File (Join-Path $old 'my-project/work.txt') 'project data'
        Write-File (Join-Path $old 'resources/customizable.txt') 'user customization'
        Write-File (Join-Path $scenario 'outside/config.txt') 'outside data'
        [IO.Directory]::CreateDirectory((Join-Path $old 'empty-project')) | Out-Null
        foreach ($file in @('state/guest-media/system.img','state/lima/instance/disk','cache/lima/image')) {
            Write-File (Join-Path $old ('CageData/' + $file)) ('persistent-' + $file)
        }
        Prepare $old $stage $receipt
        Check ((Get-Content (Join-Path $old 'program.txt') -Raw) -eq 'old') 'Preparation modified the original installation.'
        $backup = Commit $old $stage $receipt
        Check ((Get-Content (Join-Path $old 'program.txt') -Raw) -eq 'new') 'New program was overwritten by legacy files.'
        Check ((Get-Content (Join-Path $old 'my-project/work.txt') -Raw) -eq 'project data') 'Project data was lost.'
        Check ((Get-Content (Join-Path $scenario 'outside/config.txt') -Raw) -eq 'outside data') 'External profile was modified.'
        Check (Test-Path (Join-Path $old 'empty-project') -PathType Container) 'Empty project directory was lost.'
        $copies = @(Get-ChildItem (Join-Path $old 'Cage-preserved-files') -File -Recurse | Where-Object { (Get-Content $_.FullName -Raw) -eq 'user customization' })
        Check ($copies.Count -eq 1) 'Modified same-name program content was not retained exactly once.'
        Check (!(Test-Path $backup)) 'Completed transfer left a full old application copy.'
        foreach ($file in @('state/guest-media/system.img','state/lima/instance/disk','cache/lima/image')) {
            Check ((Get-Content (Join-Path $old ('CageData/' + $file)) -Raw) -eq ('persistent-' + $file)) 'Update changed CageData.'
        }
        $embedded = Join-Path $scenario 'embedded.json'
        Copy-Item (Join-Path $old 'Cage.install-files.json') $embedded
        Write-File (Join-Path $old 'resources/customizable.txt') 'new customization'
        Invoke-Helper @('-Operation','uninstall','-Target',$old,'-Manifest',$embedded)
        Check (!(Test-Path (Join-Path $old 'program.txt'))) 'Uninstaller retained an unchanged program file.'
        Check ((Get-Content (Join-Path $old 'my-project/work.txt') -Raw) -eq 'project data') 'Uninstall deleted a project.'
        Check ((Get-Content (Join-Path $old 'resources/customizable.txt') -Raw) -eq 'new customization') 'Uninstall deleted a modified program file.'
        Check (Test-Path (Join-Path $old 'empty-project') -PathType Container) 'Uninstall deleted an unknown empty project.'
        Check (Test-Path (Join-Path $old 'Cage.install-id')) 'Retained data directory cannot be recognized on reinstall.'
        foreach ($file in @('state/guest-media/system.img','state/lima/instance/disk','cache/lima/image')) {
            Check ((Get-Content (Join-Path $old ('CageData/' + $file)) -Raw) -eq ('persistent-' + $file)) 'Uninstall changed CageData.'
        }
        App $stage 'reinstalled'
        Prepare $old $stage $receipt
        $null = Commit $old $stage $receipt
        Check ((Get-Content (Join-Path $old 'my-project/work.txt') -Raw) -eq 'project data') 'Reinstall after uninstall lost the project.'
        foreach ($file in @('state/guest-media/system.img','state/lima/instance/disk','cache/lima/image')) {
            Check ((Get-Content (Join-Path $old ('CageData/' + $file)) -Raw) -eq ('persistent-' + $file)) 'Reinstall changed CageData.'
        }
    }
    $scenario = Join-Path $root 'concurrent'
    $old = Join-Path $scenario 'installed'; $stage = Join-Path $scenario 'staged'; $receipt = Join-Path $scenario 'receipt.json'
    App $old 'old'; App $stage 'new'
    Write-File (Join-Path $old 'project.txt') 'first'
    Prepare $old $stage $receipt
    Write-File (Join-Path $old 'project.txt') 'late edit'
    Write-File (Join-Path $old 'new-project.txt') 'created during install'
    [IO.Directory]::CreateDirectory((Join-Path $old 'late-empty-project')) | Out-Null
    $null = Commit $old $stage $receipt
    $late = @(Get-ChildItem (Join-Path $old 'Cage-preserved-files') -File -Recurse)
    Check ((Get-Content (Join-Path $old 'project.txt') -Raw) -eq 'late edit') 'Concurrent edit was deleted.'
    Check (@($late | Where-Object { (Get-Content $_.FullName -Raw) -eq 'created during install' }).Count -eq 1) 'Concurrent new file was deleted.'
    Check (@(Get-ChildItem (Join-Path $old 'Cage-preserved-files') -Directory -Recurse | Where-Object Name -eq 'late-empty-project').Count -eq 1) 'Concurrent empty project was deleted.'
    $scenario = Join-Path $root 'rollback'
    $old = Join-Path $scenario 'installed'; $stage = Join-Path $scenario 'staged'; $receipt = Join-Path $scenario 'receipt.json'
    App $old 'old'; App $stage 'new'
    Write-File (Join-Path $old 'project.txt') 'rollback project'
    Write-File (Join-Path $old 'CageData/state/lima/disk') 'rollback disk'
    Prepare $old $stage $receipt
    $backup = $old + '-backup'
    [IO.Directory]::Move($old, $backup)
    Remove-Item -LiteralPath $stage -Recurse
    try { [IO.Directory]::Move($stage, $old); throw 'Expected the missing stage to fail.' }
    catch [IO.DirectoryNotFoundException] { [IO.Directory]::Move($backup, $old) }
    Invoke-Helper @('-Operation','complete','-Target',$old,'-Receipt',$receipt)
    Check ((Get-Content (Join-Path $old 'project.txt') -Raw) -eq 'rollback project') 'Failed replacement lost the original project.'
    Check ((Get-Content (Join-Path $old 'program.txt') -Raw) -eq 'old') 'Failed replacement changed the original program.'
    Check ((Get-Content (Join-Path $old 'CageData/state/lima/disk') -Raw) -eq 'rollback disk') 'Failed replacement changed CageData.'
    $stage = Join-Path $scenario 'staged-again'; App $stage 'new'
    $locked = [IO.File]::Open((Join-Path $old 'program.txt'), 'Open', 'ReadWrite', 'None')
    try { Invoke-Helper @('-Operation','prepare','-Source',$old,'-Target',$stage,'-Receipt',$receipt) 1 }
    finally { $locked.Dispose() }
    Check ((Get-Content (Join-Path $old 'project.txt') -Raw) -eq 'rollback project') 'Failed preparation changed a locked project.'
    $fresh = Join-Path $root 'fresh'; App $fresh 'fresh'
    $embedded = Join-Path $root 'fresh-embedded.json'; Copy-Item (Join-Path $fresh 'Cage.install-files.json') $embedded
    Invoke-Helper @('-Operation','uninstall','-Target',$fresh,'-Manifest',$embedded)
    Check (!(Test-Path $fresh)) 'An unmodified installation was not fully removed.'
    $scenario = Join-Path $root 'links'
    $old = Join-Path $scenario 'installed'; $stage = Join-Path $scenario 'staged'; $receipt = Join-Path $scenario 'receipt.json'
    App $old 'old'; App $stage 'new'
    Write-File (Join-Path $scenario 'outside/target.txt') 'external link target'
    [IO.Directory]::CreateDirectory((Join-Path $old 'my-project')) | Out-Null
    New-FileLink (Join-Path $old 'my-project/relative.txt') '../../outside/target.txt'
    New-FileLink (Join-Path $old 'my-project/missing.txt') 'missing-target'
    $junction = Join-Path $old 'my-project/junction'
    if ($windows) {
        & cmd.exe /c mklink /J $junction (Join-Path $scenario 'outside') | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Could not create the isolated junction fixture.' }
    } else { [IO.Directory]::CreateSymbolicLink($junction, (Join-Path $scenario 'outside')) | Out-Null }
    Prepare $old $stage $receipt
    $null = Commit $old $stage $receipt
    Check ((Read-Link (Join-Path $old 'my-project/relative.txt')) -eq '../../outside/target.txt') 'Relative symlink text changed during installation.'
    Check ((Get-Content (Join-Path $old 'my-project/relative.txt') -Raw) -eq 'external link target') 'Relative symlink no longer reaches its target.'
    Check ((Read-Link (Join-Path $old 'my-project/missing.txt')) -eq 'missing-target') 'Dangling symlink was lost.'
    Check ((Get-Content (Join-Path $junction 'target.txt') -Raw) -eq 'external link target') 'Directory link was not retained.'
    $embedded = Join-Path $scenario 'embedded.json'; Copy-Item (Join-Path $old 'Cage.install-files.json') $embedded
    Invoke-Helper @('-Operation','uninstall','-Target',$old,'-Manifest',$embedded)
    Check ((Get-Content (Join-Path $scenario 'outside/target.txt') -Raw) -eq 'external link target') 'Uninstall changed an external link target.'
    Check ((Read-Link (Join-Path $old 'my-project/relative.txt')) -eq '../../outside/target.txt') 'Uninstall removed a project link.'
    $scenario = Join-Path $root 'linked-program-directory'
    $old = Join-Path $scenario 'installed'; App $old 'old'
    $embedded = Join-Path $scenario 'embedded.json'; Copy-Item (Join-Path $old 'Cage.install-files.json') $embedded
    Write-File (Join-Path $scenario 'outside/customizable.txt') 'default-old'
    Remove-Item (Join-Path $old 'resources') -Recurse
    if ($windows) {
        & cmd.exe /c mklink /J (Join-Path $old 'resources') (Join-Path $scenario 'outside') | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Could not create the isolated junction fixture.' }
    } else { [IO.Directory]::CreateSymbolicLink((Join-Path $old 'resources'), (Join-Path $scenario 'outside')) | Out-Null }
    Invoke-Helper @('-Operation','uninstall','-Target',$old,'-Manifest',$embedded)
    Check ((Get-Content (Join-Path $scenario 'outside/customizable.txt') -Raw) -eq 'default-old') 'Uninstall followed a replaced program directory link.'
    Check ([bool]((Get-Item (Join-Path $old 'resources') -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) 'Uninstall removed a user directory link.'
    $scenario = Join-Path $root 'linked-installation-root'
    $actual = Join-Path $scenario 'actual'; $old = Join-Path $scenario 'installed'; $stage = Join-Path $scenario 'staged'; $receipt = Join-Path $scenario 'receipt.json'
    App $actual 'old'; App $stage 'new'
    Write-File (Join-Path $actual 'my-project/work.txt') 'external installation project'
    if ($windows) {
        & cmd.exe /c mklink /J $old $actual | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Could not create the isolated installation junction.' }
    } else { [IO.Directory]::CreateSymbolicLink($old, $actual) | Out-Null }
    Invoke-Helper @('-Operation','prepare','-Source',$old,'-Target',$stage,'-Receipt',$receipt) 1
    Check ((Get-Content (Join-Path $actual 'my-project/work.txt') -Raw) -eq 'external installation project') 'Preparation followed the installation root link and changed its target.'
    Check (!(Test-Path $receipt)) 'Preparation published a transaction for a redirected installation root.'
    $scenario = Join-Path $root 'move-rollback'
    $old = Join-Path $scenario 'installed'; $stage = Join-Path $scenario 'staged'; $receipt = Join-Path $scenario 'receipt.json'
    App $old 'old'; App $stage 'new'
    Write-File (Join-Path $old 'first-project/work.txt') 'first user project'
    Write-File (Join-Path $old 'second-project/work.txt') 'second user project'
    New-FileLink (Join-Path $old 'first-project/link.txt') 'work.txt'
    Prepare $old $stage $receipt
    $moves = @((Get-Content $receipt -Raw | ConvertFrom-Json).moves)
    $backup = $old + '-backup'; [IO.Directory]::Move($old, $backup)
    [IO.Directory]::CreateDirectory((Join-Path $stage $moves[-1].destination)) | Out-Null
    Invoke-Helper @('-Operation','move','-Source',$backup,'-Target',$stage,'-Receipt',$receipt) 1
    Invoke-Helper @('-Operation','restore','-Source',$stage,'-Target',$backup,'-Receipt',$receipt)
    [IO.Directory]::Move($backup, $old)
    Check ((Get-Content (Join-Path $old 'first-project/work.txt') -Raw) -eq 'first user project') 'Rollback missed an already moved project.'
    Check ((Get-Content (Join-Path $old 'second-project/work.txt') -Raw) -eq 'second user project') 'Rollback changed the project whose move failed.'
    Check ((Read-Link (Join-Path $old 'first-project/link.txt')) -eq 'work.txt') 'Rollback changed a relative project link.'
    $scenario = Join-Path $root 'type-conflicts'
    $old = Join-Path $scenario 'installed'; $stage = Join-Path $scenario 'staged'; $receipt = Join-Path $scenario 'receipt.json'
    App $old 'old'; App $stage 'new'
    Remove-Item (Join-Path $old 'Cage.install-files.json')
    Write-File (Join-Path $old 'obsolete.dll') 'obsolete library'
    Write-File (Join-Path $old 'resources/new-tool/data.txt') 'user directory'
    Write-File (Join-Path $stage 'resources/new-tool') 'new program file'
    Prepare $old $stage $receipt
    $null = Commit $old $stage $receipt
    Check (!(Test-Path (Join-Path $old 'obsolete.dll'))) 'Legacy unknown DLL remained in the new application loader path.'
    Check ((Get-Content (Join-Path $old 'resources/new-tool') -Raw) -eq 'new program file') 'A user directory replaced a new program file.'
    $retained = @(Get-ChildItem (Join-Path $old 'Cage-preserved-files') -File -Recurse)
    Check (@($retained | Where-Object { (Get-Content $_.FullName -Raw) -eq 'obsolete library' }).Count -eq 1) 'Legacy DLL bytes were lost.'
    Check (@($retained | Where-Object { (Get-Content $_.FullName -Raw) -eq 'user directory' }).Count -eq 1) 'Conflicting user directory was lost.'
    $scenario = Join-Path $root 'legacy-automatic-rollback'
    $old = Join-Path $scenario 'installed'; $stage = Join-Path $scenario 'restored'; $receipt = Join-Path $scenario 'receipt.json'
    App $stage 'old-program'
    Remove-Item (Join-Path $stage 'Cage.install-files.json')
    Write-File (Join-Path $stage 'my-project/work.txt') 'before attempted update'
    Write-File (Join-Path $stage 'personal.txt') 'old personal file'
    App $old 'failed-new-program'
    Write-File (Join-Path $old 'my-project/work.txt') 'saved while the new version was running'
    Write-File (Join-Path $old 'personal.txt') 'latest personal file'
    New-FileLink (Join-Path $old 'my-project/latest-link.txt') 'work.txt'
    Invoke-Helper @('-Operation','prepare','-Source',$old,'-Target',$stage,'-Receipt',$receipt,'-Backup',($old+'-backup'),'-LegacyTarget')
    $null = Commit $old $stage $receipt
    Check ((Get-Content (Join-Path $old 'program.txt') -Raw) -eq 'old-program') 'Legacy recovery did not restore the previous program.'
    Check ((Get-Content (Join-Path $old 'my-project/work.txt') -Raw) -eq 'saved while the new version was running') 'Legacy recovery put an old project at the active path.'
    Check ((Get-Content (Join-Path $old 'personal.txt') -Raw) -eq 'latest personal file') 'Legacy recovery put an old personal file at the active path.'
    Check ((Read-Link (Join-Path $old 'my-project/latest-link.txt')) -eq 'work.txt') 'Legacy recovery lost the current relative link.'
    $previousCopies = @(Get-ChildItem (Join-Path $old 'Cage-preserved-files') -File -Recurse)
    Check (@($previousCopies | Where-Object { (Get-Content $_.FullName -Raw) -eq 'before attempted update' }).Count -eq 1) 'Legacy recovery deleted the previous project version.'
    Check (@($previousCopies | Where-Object { (Get-Content $_.FullName -Raw) -eq 'old personal file' }).Count -eq 1) 'Legacy recovery deleted the previous personal-file version.'
    if ($windows) {
        & $HelperShell -NoProfile -File (Join-Path $PSScriptRoot 'test-windows-verified-file-deletion.ps1')
        if ($LASTEXITCODE -ne 0) { throw 'Verified-handle deletion regression failed.' }
    }
    Write-Output "Installation file ownership: $checks checks passed."
} finally {
    if (Test-Path -LiteralPath $root) { Remove-TestTree $root; [IO.Directory]::Delete($root) }
}
