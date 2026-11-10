# Windows filesystem regression. Hyper-V, task and service calls are substitutes.
param([Parameter(Mandatory)][string]$Updater,[Parameter(Mandatory)][string]$ProbeExecutable)
$ErrorActionPreference='Stop'
$root=Join-Path ([IO.Path]::GetTempPath()) ('cage-maintenance-test-'+[guid]::NewGuid().ToString('N'))
$null=New-Item -ItemType Directory -Path $root
[IO.File]::WriteAllText((Join-Path $root 'owner'),'Cage isolated maintenance regression')
$previousProgramFiles=$env:ProgramFiles
$env:ProgramFiles=$root
$vm=[guid]::NewGuid()
$sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$bridge=Join-Path $root ('Cage Guest Bridge\'+$vm)
$network=Join-Path $root ('Cage Guest Network\'+$vm)
$null=New-Item -ItemType Directory -Path $bridge,$network
$old=Join-Path $bridge 'helper.txt'
[IO.File]::WriteAllText($old,'old helper')
$account=Join-Path $root 'account-marker'
[IO.File]::WriteAllText($account,'account and project data')
$gui="Cage-GUI-Bridge-$vm-$sid"
$packet="Cage-Packet-Bridge-$vm"
$global:CageTestTasks=@{}
$global:CageTestTasks[$gui]=[pscustomobject]@{Description="Cage protected GUI bridge $vm $sid";State='Running';Actions=@([pscustomobject]@{Execute=(Join-Path $bridge 'Cage.GuestClipboard.exe')})}
$global:CageTestTasks[$packet]=[pscustomobject]@{Description="Cage protected packet bridge $vm $sid";State='Running';Actions=@([pscustomobject]@{Execute=(Join-Path $network 'Cage.GuestNetwork.exe')})}
function Get-ItemProperty { param($Path) [pscustomobject]@{VirtualMachineId=$vm} }
function Get-ScheduledTask { param($TaskName) $global:CageTestTasks[$TaskName] }
function Stop-ScheduledTask { param($TaskName) $global:CageTestTasks[$TaskName].State='Ready' }
function Start-ScheduledTask { param($TaskName) $global:CageTestTasks[$TaskName].State='Running' }
function Get-CimInstance { param($ClassName,$Filter) $null }
function Assert-Value($Condition,[string]$Message) { if (!$Condition) { throw $Message } }
function New-TestPackage([switch]$WithTools) {
    $stage=Join-Path $root ('Cage Component Update\'+[guid]::NewGuid().ToString('N'))
    $source=Join-Path $root ([guid]::NewGuid().ToString('N'))
    $null=New-Item -ItemType Directory -Path $stage,(Join-Path $source 'files/bridge'),(Join-Path $source 'files/network')
    [IO.File]::WriteAllText((Join-Path $source 'files/bridge/helper.txt'),'new helper')
    Copy-Item -LiteralPath $ProbeExecutable -Destination (Join-Path $source 'files/bridge/Cage.GuestClipboard.exe')
    Copy-Item -LiteralPath $ProbeExecutable -Destination (Join-Path $source 'files/network/Cage.GuestNetwork.exe')
    $entries=@()
    foreach ($name in @('bridge/helper.txt','bridge/Cage.GuestClipboard.exe','network/Cage.GuestNetwork.exe')) {
        $digest=(Get-FileHash -LiteralPath (Join-Path $source ('files/'+$name))).Hash.ToLowerInvariant()
        $entries+=@{source=('payload/'+[IO.Path]::GetFileName($name));target=$name;sha256=$digest;installed_sha256=$digest}
    }
    @{protocol=1;system='windows-11';revision=('a'*64);files=$entries}|ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $source 'native-components.json') -Encoding UTF8
    if ($WithTools) {
        $runtime=Join-Path $root ([guid]::NewGuid().ToString('N'))
        $toolStage=Join-Path $source 'tools'
        $null=New-Item -ItemType Directory -Path $runtime,$toolStage
        foreach ($name in @('playwright','vscode-cli','vscode-server')) {
            $null=New-Item -ItemType Directory -Path (Join-Path $runtime $name)
            [IO.File]::WriteAllText((Join-Path $runtime ($name+'/fixture.txt')),'new runtime')
        }
        foreach ($name in @('node.exe','editor-runtime.json')) { [IO.File]::WriteAllText((Join-Path $runtime $name),'new runtime') }
        & (Join-Path $env:SystemRoot 'System32/tar.exe') -a -cf (Join-Path $toolStage 'extras.zip') -C $runtime .
        if ($LASTEXITCODE) { throw 'fixture runtime archive failed' }
        $installer=@'
param($VMId,$GUIUserSID,$Bridge,$Stage,$PackagesJSON,$CLIJSON,$ExtrasSHA,$Phase,[switch]$RuntimeInstalled)
if ($Phase -eq 'Prepare' -and !$RuntimeInstalled) { throw 'fixture received unpacked runtime twice' }
if ($Phase -eq 'UserTools' -and $env:CAGE_TEST_TOOL_FAILURE) { throw 'simulated package installation failure' }
'@
        [IO.File]::WriteAllText((Join-Path $stage 'install-windows-workbench-components.ps1'),$installer)
    }
    $archive=Join-Path $stage 'update.zip'
    & (Join-Path $env:SystemRoot 'System32/tar.exe') -a -cf $archive -C $source .
    if ($LASTEXITCODE) { throw 'fixture archive failed' }
    return @{Stage=$stage;Hash=(Get-FileHash -LiteralPath $archive).Hash}
}
try {
    $package=New-TestPackage
    $env:CAGE_TEST_FAIL_SELF='1'
    $failed=$false
    try { & $Updater $vm $sid $package.Stage $package.Hash $false '{}' '{}' } catch { $failed=$true }
    Assert-Value $failed 'failed update unexpectedly succeeded'
    Assert-Value ([IO.File]::ReadAllText($old) -ceq 'old helper') 'rollback lost old component'
    Assert-Value (!(Test-Path -LiteralPath (Join-Path $bridge 'native-components.json'))) 'failed update published receipt'
    Assert-Value (!(Test-Path -LiteralPath (Join-Path $network 'Cage.GuestNetwork.exe'))) 'rollback left new network agent'
    Assert-Value ($global:CageTestTasks[$gui].State -eq 'Running') 'rollback did not restore task state'
    Remove-Item Env:CAGE_TEST_FAIL_SELF
    for ($attempt=0;$attempt -lt 2;$attempt++) {
        $package=New-TestPackage
        & $Updater $vm $sid $package.Stage $package.Hash $false '{}' '{}'
        Assert-Value ([IO.File]::ReadAllText($old) -ceq 'new helper') 'retry did not install helper'
        $receipt=Get-Content -LiteralPath (Join-Path $bridge 'native-components.json') -Raw | ConvertFrom-Json
        Assert-Value ($receipt.revision -ceq ('a'*64)) 'receipt did not follow installed files'
        Assert-Value ([IO.File]::ReadAllText($account) -ceq 'account and project data') 'personal data changed'
        $acl=Get-Acl -LiteralPath (Join-Path $bridge 'native-components.json')
        Assert-Value $acl.AreAccessRulesProtected 'receipt permissions inherited'
    }
    $package=New-TestPackage
    $failed=$false
    try { & $Updater $vm $sid $package.Stage ('0'*64) $false '{}' '{}' } catch { $failed=$true }
    Assert-Value $failed 'damaged transfer accepted'
    Assert-Value ([IO.File]::ReadAllText($old) -ceq 'new helper') 'rejected transfer changed installed files'
    $editor=Join-Path $bridge 'vscode-server'
    $null=New-Item -ItemType Directory -Path $editor
    [IO.File]::WriteAllText((Join-Path $editor 'fixture.txt'),'original runtime')
    $env:CAGE_TEST_TOOL_FAILURE='1'
    $package=New-TestPackage -WithTools
    $failed=$false
    try { & $Updater $vm $sid $package.Stage $package.Hash $true '{}' '{}' } catch { $failed=$_.Exception.Message -ceq 'simulated package installation failure' }
    Assert-Value $failed 'fixture did not reach package installation failure'
    Assert-Value ([IO.File]::ReadAllText((Join-Path $editor 'fixture.txt')) -ceq 'original runtime') 'old editor was not restored'
    Assert-Value (!(Test-Path -LiteralPath (Join-Path $bridge 'node.exe'))) 'failure published an entirely new runtime'
    Remove-Item Env:CAGE_TEST_TOOL_FAILURE
    $package=New-TestPackage -WithTools
    & $Updater $vm $sid $package.Stage $package.Hash $true '{}' '{}'
    Assert-Value ([IO.File]::ReadAllText((Join-Path $editor 'fixture.txt')) -ceq 'new runtime') 'tool retry did not replace the editor'
    [pscustomobject]@{Rollback=$true;Retry=$true;RepeatedUpdate=$true;Manifest=$true;Permissions=$true;PersonalDataPreserved=$true;DamagedTransferRejected=$true;ToolRuntimeRollbackAndRetry=$true;RealWindowsFilesystem=$true;HyperVAndServicesMocked=$true} | ConvertTo-Json -Compress
} finally {
    $env:ProgramFiles=$previousProgramFiles
    Remove-Item Env:CAGE_TEST_FAIL_SELF -ErrorAction SilentlyContinue
    Remove-Item Env:CAGE_TEST_TOOL_FAILURE -ErrorAction SilentlyContinue
    Remove-Variable CageTestTasks -Scope Global -ErrorAction SilentlyContinue
    if ([IO.File]::ReadAllText((Join-Path $root 'owner')) -ceq 'Cage isolated maintenance regression') { Remove-Item -LiteralPath $root -Recurse -Force }
}
