#requires -Version 5.1
param(
    [Parameter(Mandatory)][string]$ProfilePath,
    [Parameter(Mandatory)][string]$CredentialFile,
    [Parameter(Mandatory)][string]$AssetDirectory,
    [Parameter(Mandatory)][string]$EvidenceDirectory,
    [switch]$EditorOnly
)
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
$utf8=[Text.UTF8Encoding]::new($false);[Console]::OutputEncoding=$utf8
$binding=Get-Content -LiteralPath $ProfilePath -Raw|ConvertFrom-Json;$vm=Get-VM -Id ([guid]$binding.vm_id)
if($vm.State -ne 'Running' -or @(Get-VMNetworkAdapter -VM $vm).Count -ne 0){throw 'The bound local-only guest must have zero normal NICs.'}
$credential=Import-Clixml -LiteralPath $CredentialFile
if(($credential.UserName -split '\\')[-1] -cne 'CageLabSetup'){throw 'The retained QA administrator identity does not match.'}
$credential=[pscredential]::new('CageLabSetup',$credential.Password)
$session=New-PSSession -VMId $vm.Id -Credential $credential
$owned=[Collections.Generic.List[Diagnostics.Process]]::new();$changed=$false;$restored=$false
function Bridge([string]$Operation){$s=[Diagnostics.ProcessStartInfo]::new();$s.FileName=$binding.relay_executable;$s.Arguments='--relay-profile "'+$ProfilePath+'" '+$Operation;$s.UseShellExecute=$false;$s.CreateNoWindow=$true;$s.RedirectStandardInput=$true;$s.RedirectStandardOutput=$true;$s.RedirectStandardError=$true;$s.StandardOutputEncoding=$utf8;$p=[Diagnostics.Process]::Start($s);$owned.Add($p);$null=$p.StandardError.ReadToEndAsync();return $p}
function Send($p,$v){$bytes=$utf8.GetBytes((ConvertTo-Json -InputObject $v -Depth 12 -Compress)+"`n");$p.StandardInput.BaseStream.Write($bytes,0,$bytes.Length);$p.StandardInput.BaseStream.Flush()}
function Read($p){$pending=$p.StandardOutput.ReadLineAsync();if(!$pending.Wait(45000) -or !$pending.Result){throw 'The owned local browser QA channel did not complete.'};return $pending.Result|ConvertFrom-Json}
function Call([string]$Path,$Payload=@{},[string]$Method='POST'){Send $browser @{method=$Method;path=$Path;input=$Payload};$r=Read $browser;if($r.error){throw $r.error};return $r}
try {
    $null=New-Item -ItemType Directory -Path $EvidenceDirectory
    $setup=Invoke-Command -Session $session -ArgumentList $vm.Id,$binding.gui_user_sid -ScriptBlock {
        param($expected,$sid)
        if([guid](Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters').VirtualMachineId -ne $expected){throw 'guest_identity_mismatch'}
        $guiProfile=(Get-ItemProperty ('HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\'+$sid)).ProfileImagePath
        $directory=Join-Path $guiProfile ('AppData\Local\Temp\CageLocalBrowserQA-'+[guid]::NewGuid().ToString('N'));$null=New-Item -ItemType Directory -Path $directory
        $program=Join-Path $env:ProgramFiles ('Cage Guest Bridge\'+$expected);$config=Join-Path $program 'guest-config.json';$backup=Join-Path $program ('guest-config.browser-qa-'+[guid]::NewGuid().ToString('N')+'.backup')
        if((Get-Item $config).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'config_link_rejected'}
        Copy-Item -LiteralPath $config -Destination $backup
        [pscustomobject]@{Directory=$directory;Program=$program;Config=$config;Backup=$backup;Hash=(Get-FileHash $config -Algorithm SHA256).Hash}
    }
    Copy-Item -LiteralPath (Join-Path $AssetDirectory 'windows-browser-deny-proxy.mjs') -Destination (Join-Path $setup.Directory 'deny.mjs') -ToSession $session
    $terminal=Bridge 'terminal';Send $terminal @{action='open';workspace='home';shell='powershell';cols=160;rows=30};if(!(Read $terminal).ready){throw 'GUI ConPTY unavailable'}
    $privateHost=@(Get-NetIPAddress -AddressFamily IPv4|Where-Object {$_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' -and $_.AddressState -eq 'Preferred'})[0].IPAddress
    if(!$privateHost){throw 'An actual private host address is required for the denied request probe.'}
    $launch='& '''+(Join-Path $setup.Program 'node.exe')+''' '''+(Join-Path $setup.Directory 'deny.mjs')+''' '''+$setup.Directory+''' '''+$privateHost+''''
    Send $terminal @{action='input';data=[Convert]::ToBase64String($utf8.GetBytes('powershell.exe -NoProfile -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($launch))+"`r"))}
    $output='';while(!$output.Contains('CAGE_QA_DENY_PROXY_READY')){$r=Read $terminal;if($null -ne $r.exit -or $r.error){throw 'The local deny proxy exited.'};if($r.data){$output+=$utf8.GetString([Convert]::FromBase64String($r.data))}}
    $port=Invoke-Command -Session $session -ArgumentList $setup.Directory -ScriptBlock {param($dir);[int](Get-Content (Join-Path $dir 'deny-proxy-port') -Raw)}
    $changed=$true;$windowStartUtc=[DateTime]::UtcNow.ToString('o')
    Invoke-Command -Session $session -ArgumentList $setup.Config,$port -ScriptBlock {
        param($file,$port);$config=Get-Content -LiteralPath $file -Raw|ConvertFrom-Json
        $config|Add-Member -NotePropertyName network_mode -NotePropertyValue 'proxy' -Force
        $config|Add-Member -NotePropertyName browser_proxy -NotePropertyValue ('http://127.0.0.1:'+$port) -Force
        [IO.File]::WriteAllText($file,($config|ConvertTo-Json -Depth 12),[Text.UTF8Encoding]::new($false))
    }
    [Console]::WriteLine('LOCAL_ONLY_QA_CONFIG_STARTED '+$windowStartUtc)
    $browser=Bridge 'browser';$health=Call '/health' @{} 'GET';$version=Call '/info'
    $audited=Call '/audit' @{network=$true;timezone='UTC'}
    if($audited.network.status -ne 503){throw 'The actual browser request did not reach the rejecting local proxy.'}
    $known=@((Call '/tabs' @{} 'GET').tabs|ForEach-Object {$_.id})
    $null=Call '/new' @{url='http://example.com/'};$null=Call '/new' @{url=('http://'+$privateHost+'/')}
    $connectDenied=$false;try{$null=Call '/new' @{url='https://example.com/'}}catch{$connectDenied=$true}
    $counts=Invoke-Command -Session $session -ArgumentList $setup.Directory -ScriptBlock {param($dir);Get-Content (Join-Path $dir 'deny-proxy-counts.json') -Raw|ConvertFrom-Json}
    if($counts.public_probe -lt 1 -or $counts.private_probe -lt 1 -or $counts.connect_probe -lt 1 -or !$connectDenied){throw 'The actual Chrome public/private host HTTP and HTTPS CONNECT probes were not all denied.'}
    foreach($tab in (Call '/tabs' @{} 'GET').tabs){if($tab.id -notin $known){$null=Call '/close' @{id=$tab.id}}}
    $tests=[ordered]@{local_only=$true;normal_nics=0;browser_version=$version.product;browser_audit_status=$audited.network.status;actual_public_http_denied=$true;actual_private_host_http_denied=$true;actual_https_connect_denied=$true;packet_network_verified=$false}
    [IO.File]::WriteAllText((Join-Path $EvidenceDirectory 'local-only-network.json'),($tests|ConvertTo-Json),$utf8)
    if(!$EditorOnly){$perf=Join-Path $EvidenceDirectory 'performance'; & (Join-Path $AssetDirectory 'qa-browser.ps1') -ProfilePath $ProfilePath -CredentialFile $CredentialFile -FixturePath (Join-Path $AssetDirectory 'windows-native-browser-fixture.mjs') -EvidenceDirectory $perf -PerfOnly}
    $env:CAGE_TEST_WINDOWS_WORKBENCH_PROFILE=Split-Path -Parent $ProfilePath;$env:CAGE_TEST_WINDOWS_BROWSER_ARTIFACTS=Join-Path $EvidenceDirectory 'editor';$env:CAGE_TEST_WINDOWS_BROWSER_NATIVE='1'
    & (Join-Path $AssetDirectory 'browser-editor-tests.exe') '-test.run=^TestWindowsBrowserEditorNativeE2E$' '-test.v' '-test.count=1'
    if($LASTEXITCODE -ne 0){throw 'The real local-only native editor test failed.'}
    if(!$EditorOnly){& (Join-Path $AssetDirectory 'qa-browser.ps1') -ProfilePath $ProfilePath -CredentialFile $CredentialFile -FixturePath (Join-Path $AssetDirectory 'windows-native-browser-fixture.mjs') -EvidenceDirectory (Join-Path $EvidenceDirectory 'native-core')}
} finally {
  try {
    if($setup){
        $restored=Invoke-Command -Session $session -ArgumentList $setup.Config,$setup.Backup,$setup.Hash,[bool]$changed,$vm.Id,$binding.gui_user_sid -ScriptBlock {
            param($file,$backup,$hash,$changed,$vm,$sid)
            if($changed){[IO.File]::WriteAllBytes($file,[IO.File]::ReadAllBytes($backup))}
            if((Get-FileHash $file -Algorithm SHA256).Hash -cne $hash){throw 'The original guest configuration was not restored.'}
            Remove-Item -LiteralPath $backup
            if($changed){
                $name='Cage-GUI-Bridge-'+$vm+'-'+$sid;$task=Get-ScheduledTask -TaskName $name
                if($task.Description -cne ('Cage protected GUI bridge '+$vm+' '+$sid)){throw 'Owned GUI task mismatch'}
                Stop-ScheduledTask -InputObject $task
                $until=[DateTime]::UtcNow.AddSeconds(10)
                while((Get-ScheduledTask -TaskName $name).State -eq 'Running' -and [DateTime]::UtcNow -lt $until){Start-Sleep -Milliseconds 100}
                if((Get-ScheduledTask -TaskName $name).State -eq 'Running'){throw 'The owned GUI task did not stop.'}
                Start-ScheduledTask -InputObject $task
                $until=[DateTime]::UtcNow.AddSeconds(10)
                while((Get-ScheduledTask -TaskName $name).State -ne 'Running' -and [DateTime]::UtcNow -lt $until){Start-Sleep -Milliseconds 100}
                if((Get-ScheduledTask -TaskName $name).State -ne 'Running'){throw 'The owned GUI task did not restart.'}
            }
            return $true
        }
    }
    if($restored -and $changed){
        $until=[DateTime]::UtcNow.AddSeconds(10);$confirmed=$false
        do{try{$status=Bridge 'status';$pending=$status.StandardOutput.ReadLineAsync();if($pending.Wait(2000) -and $pending.Result){$reply=$pending.Result|ConvertFrom-Json;$confirmed=$reply.available -eq $true -and $reply.vm_id -eq $binding.vm_id -and $reply.gui_user_sid -ceq $binding.gui_user_sid}}catch{};if(!$confirmed){Start-Sleep -Milliseconds 200}}while(!$confirmed -and [DateTime]::UtcNow -lt $until)
        if(!$confirmed){throw 'Configuration restored, but the actual paired GUI status ACK is unavailable.'}
        [Console]::WriteLine('LOCAL_ONLY_QA_GUI_ACK_CONFIRMED')
    }
    if($restored){$endUtc=[DateTime]::UtcNow.ToString('o');[IO.File]::WriteAllText((Join-Path $EvidenceDirectory 'configuration-window.json'),(@{start_utc=$windowStartUtc;end_utc=$endUtc;original_config_restored=$true;gui_ack_confirmed=$confirmed;network_mode='local-only-proxy';normal_nics=0}|ConvertTo-Json),$utf8);[Console]::WriteLine('LOCAL_ONLY_QA_CONFIG_RESTORED '+$endUtc)}
  } finally {
    foreach($p in $owned){try{$p.StandardInput.Close();if(!$p.WaitForExit(1000)){$p.Kill()};$p.Dispose()}catch{}}
    if($session){Remove-PSSession $session}
  }
}
