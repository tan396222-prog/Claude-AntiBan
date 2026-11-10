#requires -Version 5.1
param(
    [Parameter(Mandatory)][string]$ProfilePath,
    [Parameter(Mandatory)][string]$CredentialFile,
    [Parameter(Mandatory)][string]$FixturePath,
    [Parameter(Mandatory)][string]$EvidenceDirectory,
    [switch]$SplitOnly,
    [switch]$PerfOnly
)
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
$utf8=[Text.UTF8Encoding]::new($false)
$copiedFixtureText=$utf8.GetString([Convert]::FromBase64String('Q2FnZSBuYXRpdmUgY29weSDkuK3mlofwn5mC'))
$binding=Get-Content -LiteralPath $ProfilePath -Raw|ConvertFrom-Json
$vm=Get-VM -Id ([guid]$binding.vm_id)
if($binding.version -ne 1 -or $vm.State -ne 'Running' -or @(Get-VMNetworkAdapter -VM $vm).Count -ne 0){throw 'The independently bound offline guest is not ready.'}
$credential=Import-Clixml -LiteralPath $CredentialFile
if(($credential.UserName -split '\\')[-1] -cne 'CageLabSetup'){throw 'The retained QA administrator identity does not match.'}
$credential=[pscredential]::new('CageLabSetup',$credential.Password)
$session=New-PSSession -VMId $vm.Id -Credential $credential
$owned=[Collections.Generic.List[Diagnostics.Process]]::new();$tabs=[Collections.Generic.List[string]]::new()
$results=[ordered]@{};$script:phase='starting'
function Bridge([string]$Operation){
    $s=[Diagnostics.ProcessStartInfo]::new();$s.FileName=$binding.relay_executable
    $s.Arguments='--relay-profile "'+$ProfilePath+'" '+$Operation
    $s.UseShellExecute=$false;$s.CreateNoWindow=$true;$s.RedirectStandardInput=$true;$s.RedirectStandardOutput=$true;$s.RedirectStandardError=$true;$s.StandardOutputEncoding=$utf8
    $p=[Diagnostics.Process]::Start($s);$owned.Add($p);$null=$p.StandardError.ReadToEndAsync();return $p
}
function Send($p,$value){$bytes=$utf8.GetBytes((ConvertTo-Json -InputObject $value -Compress -Depth 12)+"`n");$p.StandardInput.BaseStream.Write($bytes,0,$bytes.Length);$p.StandardInput.BaseStream.Flush()}
function Read($p){$pending=$p.StandardOutput.ReadLineAsync();if(!$pending.Wait(45000) -or !$pending.Result){throw ('The owned browser response did not complete: '+$script:phase)};return $pending.Result|ConvertFrom-Json}
function Call([string]$Path,$Payload=@{},[string]$Method='POST'){Send $browser @{method=$Method;path=$Path;input=$Payload};$value=Read $browser;if($value.error){throw ($script:phase+': '+$value.error)};return $value}
function State {return Invoke-Command -Session $session -ArgumentList $guestDirectory -ScriptBlock {param($dir);$file=Join-Path $dir 'fixture-state.json';if(Test-Path -LiteralPath $file){foreach($report in (Get-Content -LiteralPath $file -Raw|ConvertFrom-Json)){$report}}}}
function Frame([string]$Name){
    $f=Call '/frame' @{} 'GET';if($f.width -lt 1 -or $f.height -lt 1 -or !$f.target){throw 'The native frame receipt is invalid.'}
    Add-Type -AssemblyName System.Drawing
    $image=[Drawing.Bitmap]::new($f.width,$f.height);$g=[Drawing.Graphics]::FromImage($image)
    try {
        $parts=@();if($f.jpeg -or $f.png){$parts+=@([pscustomobject]@{jpeg=$f.jpeg;png=$f.png;x=0;y=0;width=$f.width;height=$f.height})};$parts+=@($f.panes);$parts+=@($f.overlays)
        foreach($part in $parts){if(!$part){continue};$encoded=if($part.png){$part.png}else{$part.jpeg};$stream=[IO.MemoryStream]::new([Convert]::FromBase64String($encoded));$piece=[Drawing.Image]::FromStream($stream);try{$g.DrawImage($piece,[Drawing.Rectangle]::new($part.x,$part.y,$part.width,$part.height))}finally{$piece.Dispose();$stream.Dispose()}}
        $image.Save((Join-Path $EvidenceDirectory ($Name+'.png')),[Drawing.Imaging.ImageFormat]::Png)
    } finally {$g.Dispose();$image.Dispose()}
    return $f
}
function Pointer($Frame,[int]$X,[int]$Y,[int]$Mask){$null=Call '/input' @{kind='pointer';target=$Frame.target;x=$X;y=$Y;mask=$Mask}}
function Click($Frame,[double]$X,[double]$Y,[int]$Mask=1){Pointer $Frame ([int]($X*$Frame.scale)) ([int]($Y*$Frame.scale)) $Mask;Pointer $Frame ([int]($X*$Frame.scale)) ([int]($Y*$Frame.scale)) 0}
function Key($Frame,[uint32]$Symbol,[bool]$Down){$null=Call '/input' @{kind='key';target=$Frame.target;keysym=$Symbol;down=$Down}}
function Text($Frame,[string]$Value){foreach($char in $Value.ToCharArray()){Key $Frame ([uint32][char]$char) $true;Key $Frame ([uint32][char]$char) $false}}
function ReadAck([long]$Serial){while($true){$r=Read $clipboard;if($r.error){throw 'The GUI clipboard write failed.'};if($r.ack -eq $Serial){return}}}
function NativeKeys {
    $probe=Bridge 'terminal';Send $probe @{action='open';workspace='home';shell='powershell';cols=160;rows=25};if(!(Read $probe).ready){throw 'The GUI native key probe is unavailable.'}
    $marker='__BN_'+[guid]::NewGuid().ToString('N').Substring(0,8)+'='
    $code='Add-Type -TypeDefinition ''using System.Runtime.InteropServices; public static class BrowserKeyProbe { [DllImport("user32.dll")] public static extern short GetAsyncKeyState(int key); }'';$ctrl=if(([BrowserKeyProbe]::GetAsyncKeyState(17)-band 32768)-ne 0){"held"}else{"released"};$mouse=if(([BrowserKeyProbe]::GetAsyncKeyState(1)-band 32768)-ne 0){"held"}else{"released"};[Console]::WriteLine("'+$marker+'"+$ctrl+"_"+$mouse)'
    $command='powershell.exe -NoProfile -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($code))+"`r"
    Send $probe @{action='input';data=[Convert]::ToBase64String($utf8.GetBytes($command))};$output=''
    while($true){$r=Read $probe;if($r.error -or $null -ne $r.exit){throw 'The GUI native key probe exited.'};if($r.data){$output+=$utf8.GetString([Convert]::FromBase64String($r.data));$match=[regex]::Match($output,[regex]::Escape($marker)+'(held|released)_(held|released)');if($match.Success){return @{ctrl=$match.Groups[1].Value;mouse=$match.Groups[2].Value}}}}
}
function NativeGeometry {
    $probe=Bridge 'terminal';Send $probe @{action='open';workspace='home';shell='powershell';cols=160;rows=25};if(!(Read $probe).ready){throw 'The GUI geometry probe is unavailable.'}
    $source=@'
using System;using System.Collections.Generic;using System.Runtime.InteropServices;using System.Text;using System.Diagnostics;
public static class BrowserGeometryProbe {
 [StructLayout(LayoutKind.Sequential)]public struct Rect{public int Left,Top,Right,Bottom;}
 [StructLayout(LayoutKind.Sequential)]public struct Gui{public uint Size,Flags;public IntPtr Active,Focus,Capture,Menu,Move,Caret;public Rect CaretRect;}
 [DllImport("user32.dll")]static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")]static extern uint GetWindowThreadProcessId(IntPtr h,out int p);
 [DllImport("user32.dll")]static extern bool GetWindowRect(IntPtr h,out Rect r);
 [StructLayout(LayoutKind.Sequential)]public struct Point{public int X,Y;}
 [DllImport("user32.dll")]static extern bool GetClientRect(IntPtr h,out Rect r);
 [DllImport("user32.dll")]static extern bool ClientToScreen(IntPtr h,ref Point p);
 [DllImport("user32.dll")]static extern bool ScreenToClient(IntPtr h,ref Point p);
 [DllImport("user32.dll")]static extern IntPtr ChildWindowFromPointEx(IntPtr h,Point p,uint flags);
 [DllImport("user32.dll")]static extern uint GetDpiForWindow(IntPtr h);
 [DllImport("user32.dll")]static extern bool GetGUIThreadInfo(uint thread,ref Gui info);
 [DllImport("user32.dll")]static extern IntPtr SetThreadDpiAwarenessContext(IntPtr c);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)]static extern int GetClassName(IntPtr h,StringBuilder s,int n);
 delegate bool EnumFn(IntPtr h,IntPtr p);[DllImport("user32.dll")]static extern bool EnumChildWindows(IntPtr h,EnumFn f,IntPtr p);
 public static string Read(){var old=SetThreadDpiAwarenessContext(new IntPtr(-4));try{var h=GetForegroundWindow();int p;var thread=GetWindowThreadProcessId(h,out p);using(var process=Process.GetProcessById(p)){var name=process.ProcessName;if(name!="msedge"&&name!="chrome")return "unowned_foreground";}Rect w;GetWindowRect(h,out w);var a=new List<string>();a.Add(GetDpiForWindow(h)+","+w.Left+","+w.Top+","+(w.Right-w.Left)+","+(w.Bottom-w.Top));var info=new Gui{Size=(uint)Marshal.SizeOf(typeof(Gui))};if(GetGUIThreadInfo(thread,ref info)){var cl=new StringBuilder(128);GetClassName(info.Focus,cl,128);Rect fr;GetWindowRect(info.Focus,out fr);a.Add("focus:"+cl+","+fr.Left+","+fr.Top+","+(fr.Right-fr.Left)+","+(fr.Bottom-fr.Top));}Rect cr;GetClientRect(h,out cr);var center=new Point{X=(cr.Right-cr.Left)/2,Y=(cr.Bottom-cr.Top)/2};ClientToScreen(h,ref center);foreach(uint flags in new uint[]{0,1,7}){var parent=h;for(int depth=0;depth<12;depth++){var pt=center;ScreenToClient(parent,ref pt);var child=ChildWindowFromPointEx(parent,pt,flags);if(child==IntPtr.Zero||child==parent){a.Add("hit"+flags+":none");break;}var cl=new StringBuilder(128);GetClassName(child,cl,128);Rect r;GetWindowRect(child,out r);a.Add("hit"+flags+":"+cl+","+r.Left+","+r.Top+","+(r.Right-r.Left)+","+(r.Bottom-r.Top));parent=child;}}EnumChildWindows(h,(c,_)=>{var s=new StringBuilder(128);GetClassName(c,s,128);Rect r;if(s.ToString()=="Chrome_RenderWidgetHostHWND"&&GetWindowRect(c,out r))a.Add(r.Left+","+r.Top+","+(r.Right-r.Left)+","+(r.Bottom-r.Top));return true;},IntPtr.Zero);return string.Join(";",a);}finally{SetThreadDpiAwarenessContext(old);}}
}
'@
    $marker='__BG_'+[guid]::NewGuid().ToString('N').Substring(0,8)+'='
    $geometryFile=Join-Path $guestDirectory ('geometry-'+[guid]::NewGuid().ToString('N')+'.txt')
    $code='Add-Type -TypeDefinition ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String("'+[Convert]::ToBase64String($utf8.GetBytes($source))+'")));[IO.File]::WriteAllText('''+$geometryFile+''',[BrowserGeometryProbe]::Read());[Console]::WriteLine("'+$marker+'done")'
    Send $probe @{action='input';data=[Convert]::ToBase64String($utf8.GetBytes('powershell.exe -NoProfile -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($code))+"`r"))};$output=''
    while($true){$r=Read $probe;if($r.error -or $null -ne $r.exit){throw 'The GUI geometry probe exited.'};if($r.data){$output+=$utf8.GetString([Convert]::FromBase64String($r.data));if($output.Contains($marker+'done')){return Invoke-Command -Session $session -ArgumentList $geometryFile -ScriptBlock {param($path);Get-Content -LiteralPath $path -Raw}}}}
}
try {
    $null=[IO.Directory]::CreateDirectory($EvidenceDirectory)
    $guestDirectory=Invoke-Command -Session $session -ArgumentList $vm.Id,$binding.gui_user_sid -ScriptBlock {
        param($expected,$sid)
        if([guid](Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters').VirtualMachineId -ne $expected){throw 'guest_identity_mismatch'}
        $guiHome=(Get-ItemProperty -LiteralPath ('HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\'+$sid)).ProfileImagePath
        $directory=Join-Path $guiHome ('AppData\Local\Temp\CageBrowserAcceptance-'+[guid]::NewGuid().ToString('N'));$null=[IO.Directory]::CreateDirectory($directory);return $directory
    }
    $copiedFixtureText+=' '+[IO.Path]::GetFileName($guestDirectory)
    $copiedFixtureBytes=[Convert]::ToBase64String($utf8.GetBytes($copiedFixtureText))
    Copy-Item -LiteralPath $FixturePath -Destination (Join-Path $guestDirectory 'fixture.mjs') -ToSession $session
    $script:phase='gui_status';$statusProcess=Bridge 'status';$status=Read $statusProcess
    if(!$status.available -or !$status.browser.installed -or !$status.browser.network_configured){throw 'The actual GUI browser dependencies or trusted network receipt are unavailable.'}
    $script:phase='fixture_terminal';$terminal=Bridge 'terminal';Send $terminal @{action='open';workspace='home';shell='powershell';cols=160;rows=30};if(!(Read $terminal).ready){throw 'The GUI fixture terminal is unavailable.'}
    $guestNode='C:\Program Files\Cage Guest Bridge\'+$vm.Id.ToString('D')+'\node.exe'
    $launch='& '''+$guestNode+''' '''+(Join-Path $guestDirectory 'fixture.mjs')+''' '''+$guestDirectory+''''
    $command='powershell.exe -NoProfile -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($launch))+"`r"
    Send $terminal @{action='input';data=[Convert]::ToBase64String($utf8.GetBytes($command))}
    $output='';while($output -notmatch 'CAGE_NATIVE_FIXTURE_READY'){ $r=Read $terminal;if($r.error -or $null -ne $r.exit){throw 'The GUI fixture server exited.'};if($r.data){$output+=$utf8.GetString([Convert]::FromBase64String($r.data))}}
    $port=Invoke-Command -Session $session -ArgumentList $guestDirectory -ScriptBlock {param($dir);[int](Get-Content -LiteralPath (Join-Path $dir 'fixture-port') -Raw)}
    $browser=Bridge 'browser';$clipboard=Bridge 'stream';$null=Read $clipboard
    # The production clipboard stream starts guest observation after the first
    # host write ACK. Exercise that lifecycle instead of waiting on an unseeded reader.
    Send $clipboard @{serial=1;text=('Cage fixture baseline '+[IO.Path]::GetFileName($guestDirectory))};ReadAck 1
    if($PerfOnly){
        $script:phase='native_capture_performance';$tab=Call '/new' @{url=('http://127.0.0.1:'+$port+'/perf')};$tabs.Add($tab.id);Start-Sleep -Milliseconds 300
        $frame=Frame 'perf-01-page';Key $frame 65307 $true;Key $frame 65307 $false;$frame=Frame 'perf-02-native-ready'
        function Dimensions($f){if($f.unchanged){return};$encoded=if($f.jpeg){$f.jpeg}else{$f.png};$memory=[IO.MemoryStream]::new([Convert]::FromBase64String($encoded));$picture=[Drawing.Image]::FromStream($memory);try{if($picture.Width -ne $f.width -or $picture.Height -ne $f.height){throw 'The actual encoded frame does not match its native HiDPI dimensions.'}}finally{$picture.Dispose();$memory.Dispose()}}
        Dimensions $frame;$before=(Call '/health' @{} 'GET').capture;if(!$before){throw 'The actual capture counters are unavailable.'}
        $static=[Collections.Generic.List[double]]::new();$unchanged=0
        for($i=0;$i -lt 16;$i++){$watch=[Diagnostics.Stopwatch]::StartNew();$next=Call '/frame' @{after=$frame.sequence} 'GET';$watch.Stop();$static.Add($watch.Elapsed.TotalMilliseconds);if($next.unchanged){$unchanged++}else{Dimensions $next;$frame=$next};Start-Sleep -Milliseconds 80}
        $still=(Call '/health' @{} 'GET').capture;$scroll=[Collections.Generic.List[double]]::new()
        for($i=0;$i -lt 20;$i++){Pointer $frame ([int](100*$frame.scale)) ([int](600*$frame.scale)) 16;Pointer $frame ([int](100*$frame.scale)) ([int](600*$frame.scale)) 0;$watch=[Diagnostics.Stopwatch]::StartNew();$next=Call '/frame' @{after=$frame.sequence} 'GET';$watch.Stop();$scroll.Add($watch.Elapsed.TotalMilliseconds);if(!$next.unchanged){Dimensions $next;$frame=$next};Start-Sleep -Milliseconds 80}
        $scrolled=(Call '/health' @{} 'GET').capture;if(!(@(State)|Where-Object {$_.type -eq 'scroll' -and $_.y -gt 0})){throw 'The actual native wheel did not scroll the fixture.'}
        $summary=[ordered]@{static_polls=16;static_unchanged=$unchanged;static_screenshots=$still.screenshots-$before.screenshots;static_damage_events=$still.damage_frames-$before.damage_frames;static_latency_ms=@($static);scroll_polls=20;scroll_screenshots=$scrolled.screenshots-$still.screenshots;scroll_damage_events=$scrolled.damage_frames-$still.damage_frames;scroll_latency_ms=@($scroll);actual_native_pixels=@($frame.width,$frame.height);completed=$true}
        [IO.File]::WriteAllText((Join-Path $EvidenceDirectory 'capture-performance.json'),($summary|ConvertTo-Json -Depth 8),$utf8);[IO.File]::WriteAllText((Join-Path $EvidenceDirectory 'perf-events.json'),(@(State)|ConvertTo-Json -Depth 8),$utf8)
        $summary|ConvertTo-Json -Compress;return
    }
    if(!$SplitOnly){
    $script:phase='native_frame';$tab=Call '/new' @{url=('http://127.0.0.1:'+$port+'/one');incognito=$true};$tabs.Add($tab.id);Start-Sleep -Milliseconds 400
    $frame=Frame '01-page';$results.native_frame=$true
    $script:phase='native_text';Click $frame 100 130;Text $frame 'native-text';Start-Sleep -Milliseconds 200
    if(!(@(State)|Where-Object {$_.type -eq 'text' -and $_.value -eq 'native-text'})){throw 'Native key input did not reach the fixture textarea.'};$results.native_text=$true
    $frame=Frame '02-text';$script:phase='native_find';Key $frame 65507 $true;Key $frame 102 $true;Key $frame 102 $false;Key $frame 65507 $false;Start-Sleep -Milliseconds 200
    $find=Frame '03-native-find';if(@($find.overlays).Count -eq 0){throw 'The native Find UI was not captured.'};Text $find 'cage-native-find-needle';Start-Sleep -Milliseconds 100
    if(@(State|Where-Object {$_.type -eq 'text'})[-1].value -ne 'native-text'){throw 'Find input reached the webpage instead of native Chrome UI.'};$results.native_find=$true
    $find=Frame '04-find-query';Key $find 65307 $true;Key $find 65307 $false
    $frame=Frame '05-find-closed';$script:phase='native_context';Click $frame 100 130 4;Start-Sleep -Milliseconds 150
    $menu=Frame '06-native-context';if(@($menu.overlays).Count -eq 0){throw 'The native context menu was not captured.'};$results.native_context_menu=$true;Key $menu 65307 $true;Key $menu 65307 $false
    $frame=Frame '07-context-closed';$script:phase='page_copy';Click $frame 100 215
    do {$r=Read $clipboard}while($r.source -ne 'guest' -or $r.text -cne $copiedFixtureText)
    if([Convert]::ToBase64String($utf8.GetBytes($r.text)) -cne $copiedFixtureBytes){throw 'The actual clipboard text bytes differ from the fixture copy.'}
    if(!(@(State)|Where-Object type -eq copied)){throw 'The actual fixture copy button did not complete.'};$results.page_copy_reaches_os_clipboard=$true
    $script:phase='image_paste';$frame=Frame '08-before-png';Click $frame 100 295
    $bitmap=[Drawing.Bitmap]::new(17,9);$graphics=[Drawing.Graphics]::FromImage($bitmap);$graphics.Clear([Drawing.Color]::Coral);$bytes=[IO.MemoryStream]::new();$bitmap.Save($bytes,[Drawing.Imaging.ImageFormat]::Png);$png=$bytes.ToArray();$bytes.Dispose();$graphics.Dispose();$bitmap.Dispose()
    $started=Call '/image-start' @{id=$tab.id;length=$png.Length};$null=Call '/image-chunk' @{transfer=$started.transfer;sequence=0;data=[Convert]::ToBase64String($png)};$null=Call '/image-finish' @{transfer=$started.transfer;sequence=1};Start-Sleep -Milliseconds 250
    if(!(@(State)|Where-Object {$_.type -eq 'png' -and $_.width -eq 17 -and $_.height -eq 9})){throw 'The native OS PNG did not reach the fixture paste event.'};$results.native_png_paste=$true;$null=Frame '09-png-pasted'
    $script:phase='native_permission';$frame=Frame '10-before-permission';Click $frame 330 215;Start-Sleep -Milliseconds 250
    $permission=Frame '11-native-permission';if(@($permission.overlays).Count -eq 0){throw 'The native permission prompt was not captured.'};$results.native_permission_prompt=$true;Key $permission 65307 $true;Key $permission 65307 $false
    $script:phase='native_private_tabs';$frame=Frame '11a-private-one';Click $frame ($frame.width/$frame.scale-120) 36
    $privateSecond=$null;for($i=0;$i-lt 40;$i++){$snapshot=Call '/tabs' @{} 'GET';$privateSecond=@($snapshot.tabs|Where-Object {$_.incognito -and $_.url -like '*/private-two'})[0];if($privateSecond){break};Start-Sleep -Milliseconds 50}
    if(!$privateSecond){throw 'The native private popup was not registered.'};$tabs.Add($privateSecond.id)
    $privateFrame=Frame '11b-private-two';if($privateFrame.id -ne $privateSecond.id){throw 'The private popup did not become the actual active page.'}
    Key $privateFrame 65507 $true;Key $privateFrame 65289 $true;Key $privateFrame 65289 $false;Key $privateFrame 65507 $false;Start-Sleep -Milliseconds 200
    $cycled=Frame '11c-native-ctrl-tab';if($cycled.id -ne $tab.id){throw 'Native Ctrl+Tab did not synchronize the known private document.'};$results.native_ctrl_tab=$true
    $null=Call '/close' @{id=$tab.id};$snapshot=Call '/tabs' @{} 'GET';if(!(@($snapshot.tabs)|Where-Object id -eq $privateSecond.id)){throw 'Closing a private tab also closed its sibling.'};$results.private_sibling_retained=$true
    }
    # Split follows the common browser contract: two ordinary tabs in one
    # context. Only these newly created fixture tabs are used and closed.
    $script:phase='split';$left=Call '/new' @{url=('http://127.0.0.1:'+$port+'/split-one')};$tabs.Add($left.id)
    $second=Call '/new' @{url=('http://127.0.0.1:'+$port+'/split-two')};$tabs.Add($second.id);$null=Call '/split' @{id=$left.id;left=$left.id;right=$second.id};Start-Sleep -Milliseconds 250
    $split=Frame '12-split';if(@($split.panes).Count -ne 2){throw 'The real split frame does not contain both panes.'}
    Pointer $split ([int]($split.panes[1].x+100*$split.scale)) ([int](130*$split.scale)) 1;Pointer $split ([int]($split.panes[1].x+100*$split.scale)) ([int](130*$split.scale)) 0;Text $split 'split-right';Start-Sleep -Milliseconds 250
    if(!(@(State)|Where-Object {$_.page -eq '/split-two' -and $_.type -eq 'text' -and $_.value -eq 'split-right'})){$null=Frame '13-split-failed';[IO.File]::WriteAllText((Join-Path $EvidenceDirectory 'split-events.json'),(@(State)|ConvertTo-Json -Depth 8),$utf8);[IO.File]::WriteAllText((Join-Path $EvidenceDirectory 'split-native-geometry.txt'),(NativeGeometry),$utf8);throw 'The native split input did not reach the right pane.'};$results.native_split_input=$true;$null=Frame '13-split-input'
    $null=Call '/split' @{}
    $script:phase='native_disconnect';$baseline=NativeKeys;if($baseline.ctrl -ne 'released' -or $baseline.mouse -ne 'released'){throw 'The GUI already contains held input before this isolated disconnect test.'}
    $primary=$browser;$viewerA=Bridge 'browser';$viewerB=Bridge 'browser'
    $browser=$viewerA;$leaseFrame=Frame '14-before-disconnect';Key $leaseFrame 65507 $true;Pointer $leaseFrame ([int](100*$leaseFrame.scale)) ([int](130*$leaseFrame.scale)) 1
    $browser=$viewerB;Key $leaseFrame 65507 $true
    $viewerA.StandardInput.Close();if(!$viewerA.WaitForExit(3000)){$viewerA.Kill()};Start-Sleep -Milliseconds 250
    $native=NativeKeys;if($native.ctrl -ne 'held' -or $native.mouse -ne 'released'){throw 'Disconnect did not release its own mouse while preserving the other viewer Ctrl.'};$results.disconnect_preserves_other_viewer=$true
    $viewerB.StandardInput.Close();if(!$viewerB.WaitForExit(3000)){$viewerB.Kill()};Start-Sleep -Milliseconds 250
    $native=NativeKeys;if($native.ctrl -ne 'released' -or $native.mouse -ne 'released'){throw 'The last viewer disconnect left native input held.'};$results.disconnect_native_keys_released=$true
    $browser=$primary;$results.completed=$true
    [IO.File]::WriteAllText((Join-Path $EvidenceDirectory 'browser-result.json'),($results|ConvertTo-Json),$utf8)
    $results|ConvertTo-Json -Compress
} catch {
    try{[IO.File]::WriteAllText((Join-Path $EvidenceDirectory 'failure-native-geometry.txt'),(NativeGeometry),$utf8)}catch{}
    $results.completed=$false;$results.failed_phase=$script:phase;$results.reason=$_.Exception.Message
    if(Test-Path -LiteralPath $EvidenceDirectory){[IO.File]::WriteAllText((Join-Path $EvidenceDirectory 'browser-result.json'),($results|ConvertTo-Json),$utf8)}
    $results|ConvertTo-Json -Compress
    throw
} finally {
    if($primary){$browser=$primary}
    if($browser -and $frame){try{$cleanup=Call '/frame' @{} 'GET';foreach($symbol in @(65507,65505,65513)){Key $cleanup $symbol $false};Pointer $cleanup 1 1 0}catch{}}
    if($browser){foreach($id in $tabs){try{$null=Call '/close' @{id=$id}}catch{}}}
    foreach($p in $owned){try{$p.StandardInput.Close();if(!$p.WaitForExit(1000)){$p.Kill()};$p.Dispose()}catch{}}
    if($session){Remove-PSSession $session}
}
