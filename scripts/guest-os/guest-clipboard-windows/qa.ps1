#requires -Version 5.1
param([Parameter(Mandatory)][string]$ProfilePath,[Parameter(Mandatory)][string]$EvidenceDirectory)
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
$profile=Get-Content -LiteralPath $ProfilePath -Raw|ConvertFrom-Json
$executable=$profile.relay_executable;$helper=Join-Path ([IO.Path]::GetDirectoryName($executable)) 'cage-guest-files.exe'
$utf8=[Text.UTF8Encoding]::new($false)
$owned=[Collections.Generic.List[Diagnostics.Process]]::new()
$script:phase='starting'
function Start-Bridge([string]$Operation,[bool]$Files=$false){
    $s=[Diagnostics.ProcessStartInfo]::new();$s.FileName=if($Files){$helper}else{$executable}
    $s.Arguments=if($Files){$Operation}else{'--relay-profile "'+$ProfilePath+'" '+$Operation}
    if($Operation -eq 'export'){$s.Arguments+=' --paths-stdin'}
    $s.UseShellExecute=$false;$s.CreateNoWindow=$true;$s.RedirectStandardInput=$true;$s.RedirectStandardOutput=$true;$s.RedirectStandardError=$true;$s.StandardOutputEncoding=$utf8
    $p=[Diagnostics.Process]::Start($s);$owned.Add($p);$null=$p.StandardError.ReadToEndAsync();return $p
}
function Send-JSON($Process,$Object){$b=$utf8.GetBytes((ConvertTo-Json -InputObject $Object -Compress -Depth 8)+"`n");$Process.StandardInput.BaseStream.Write($b,0,$b.Length);$Process.StandardInput.BaseStream.Flush()}
function Read-JSON($Process){$r=$Process.StandardOutput.ReadLineAsync();if(!$r.Wait(30000) -or !$r.Result){throw ('The GUI bridge response did not complete at '+$script:phase+'.')};return ($r.Result|ConvertFrom-Json)}
function Check-Exit($Process){if(!$Process.WaitForExit(30000) -or $Process.ExitCode -ne 0){throw 'The GUI bridge child failed.'}}
function SHA([byte[]]$Bytes){return [BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash($Bytes)).Replace('-','').ToLowerInvariant()}
$echoes=0
function Read-Ack([long]$Serial){
    while($true){$value=Read-JSON $clipboard;if($value.error){throw 'Clipboard write failed.'};if($value.ack -eq $Serial){return};if($value.source -eq 'guest' -and $value.text -eq $seed){$script:echoes++}}
}
function Read-Guest([string]$Format){while($true){$v=Read-JSON $clipboard;if($v.error){throw 'Clipboard selection failed.'};if($v.source -eq 'guest' -and $null -ne $v.$Format){return $v}}}
function Invoke-Native([string]$Body){
    $marker='__CQ_'+[guid]::NewGuid().ToString('N').Substring(0,8)+'='
    $script='Add-Type -AssemblyName System.Windows.Forms;'+$Body+';[Console]::WriteLine("'+$marker+'"+$result)'
    $encoded=[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($script))
    Send-JSON $terminal @{action='input';data=[Convert]::ToBase64String($utf8.GetBytes('powershell.exe -Sta -NoProfile -EncodedCommand '+$encoded+"`r"))}
    $output=''
    while($true){$m=Read-JSON $terminal;if($m.error -or $null -ne $m.exit){throw 'The ordinary GUI ConPTY failed.'};if($m.data){$output+=$utf8.GetString([Convert]::FromBase64String($m.data));$match=[regex]::Match($output,[regex]::Escape($marker)+'([a-zA-Z0-9_-]+)[\r\n]');if($match.Success){return $match.Groups[1].Value}}}
}
try {
    $script:phase='clipboard_public_hello';$clipboard=Start-Bridge 'stream';$hello=Read-JSON $clipboard
    if($hello.hello.os -ne 'windows' -or !$hello.hello.available -or $hello.version){throw 'The public clipboard hello shape is invalid.'}
    $script:phase='terminal_ready';$terminal=Start-Bridge 'terminal';Send-JSON $terminal @{action='open';cols=160;rows=35;workspace='home';shell='powershell'}
    if(!(Read-JSON $terminal).ready){throw 'The ordinary GUI ConPTY did not become ready.'}
    Send-JSON $terminal @{action='resize';cols=160;rows=40}
    $script:phase='host_text_ack';$seed="Windows host 中文 😀`nsecond line";Send-JSON $clipboard @{serial=1;text=$seed};Read-Ack 1
    $expected=SHA $utf8.GetBytes($seed)
    $script:phase='host_text_native_read';$read=Invoke-Native '$b=[Text.Encoding]::UTF8.GetBytes([Windows.Forms.Clipboard]::GetText());$result=[BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash($b)).Replace("-","").ToLowerInvariant()'
    if($read -ne $expected){throw 'Native GUI clipboard text does not match the host write.'}
    $guest='Windows guest 中文 😀';$guest64=[Convert]::ToBase64String($utf8.GetBytes($guest))
    $script:phase='guest_text_write';$null=Invoke-Native ('$text=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String("'+$guest64+'"));[Windows.Forms.Clipboard]::SetText($text);$result="written"')
    if((Read-Guest 'text').text -ne $guest){throw 'Native guest text did not reach the host.'}
    # A small valid PNG fixture has stable bytes, dimensions and transparency.
    Add-Type -AssemblyName System.Drawing
    $image=[Drawing.Bitmap]::new(13,7);$g=[Drawing.Graphics]::FromImage($image);$g.Clear([Drawing.Color]::FromArgb(177,21,80,140));$bytes=[IO.MemoryStream]::new();$image.Save($bytes,[Drawing.Imaging.ImageFormat]::Png);$g.Dispose();$image.Dispose();$png=$bytes.ToArray();$bytes.Dispose()
    $script:phase='host_png_ack';Send-JSON $clipboard @{serial=2;png=[Convert]::ToBase64String($png)};Read-Ack 2
    $script:phase='host_png_native_read';$read=Invoke-Native '$b=([IO.MemoryStream][Windows.Forms.Clipboard]::GetData("PNG")).ToArray();$result=[BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash($b)).Replace("-","").ToLowerInvariant()'
    if($read -ne (SHA $png)){throw 'Native PNG bytes do not match the host write.'}
    $script:phase='guest_png_write';$null=Invoke-Native 'Add-Type -AssemblyName System.Drawing;$im=[Drawing.Bitmap]::new(9,5);$g=[Drawing.Graphics]::FromImage($im);$g.Clear([Drawing.Color]::Coral);$s=[IO.MemoryStream]::new();$im.Save($s,[Drawing.Imaging.ImageFormat]::Png);$d=[Windows.Forms.DataObject]::new();$d.SetData("PNG",$false,$s);$d.SetImage($im);[Windows.Forms.Clipboard]::SetDataObject($d,$true);$g.Dispose();$im.Dispose();$result="written"'
    $returned=[Convert]::FromBase64String((Read-Guest 'png').png);$pngImage=[Drawing.Image]::FromStream([IO.MemoryStream]::new($returned));if($pngImage.Width -ne 9 -or $pngImage.Height -ne 5){throw 'Native guest PNG did not reach the host.'};$pngImage.Dispose()
    $fixture=Join-Path $EvidenceDirectory ([guid]::NewGuid().ToString('N'));$null=[IO.Directory]::CreateDirectory($fixture)
    $file=Join-Path $fixture '中文😀.txt';$content=$utf8.GetBytes('Host to guest file 中文😀');[IO.File]::WriteAllBytes($file,$content)
    $script:phase='host_file_import';$writer=Start-Bridge 'export' $true;Send-JSON $writer @($file);$writer.StandardInput.Close()
    $importer=Start-Bridge 'import';$writer.StandardOutput.BaseStream.CopyTo($importer.StandardInput.BaseStream);$importer.StandardInput.Close();$imported=Read-JSON $importer;Check-Exit $writer;Check-Exit $importer
    if(@($imported.paths).Count -ne 1){throw 'The imported file selection is invalid.'}
    $script:phase='host_file_ack';Send-JSON $clipboard @{serial=3;files=@($imported.paths)};Read-Ack 3
    $script:phase='host_file_native_read';$read=Invoke-Native '$files=[Windows.Forms.Clipboard]::GetFileDropList();if($files.Count -ne 1){throw "selection_invalid"};$b=[IO.File]::ReadAllBytes($files[0]);$result=[BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash($b)).Replace("-","").ToLowerInvariant()'
    if($read -ne (SHA $content)){throw 'The actual native GUI file-drop bytes do not match.'}
    $guestContent=$utf8.GetBytes('Guest to host file 中文😀');$guest64=[Convert]::ToBase64String($guestContent)
    $script:phase='guest_file_native_write';$null=Invoke-Native ('$dir=Join-Path ([IO.Path]::GetTempPath()) ("cage-clipboard-qa-"+[guid]::NewGuid().ToString("N"));$null=[IO.Directory]::CreateDirectory($dir);$f=Join-Path $dir "guest.txt";[IO.File]::WriteAllBytes($f,[Convert]::FromBase64String("'+$guest64+'"));$list=[Collections.Specialized.StringCollection]::new();$null=$list.Add($f);[Windows.Forms.Clipboard]::SetFileDropList($list);$result="written"')
    $selected=Read-Guest 'files';$exporter=Start-Bridge 'export';Send-JSON $exporter @($selected.files);$exporter.StandardInput.Close()
    $reader=Start-Bridge 'import' $true;$exporter.StandardOutput.BaseStream.CopyTo($reader.StandardInput.BaseStream);$reader.StandardInput.Close();$received=Read-JSON $reader;Check-Exit $exporter;Check-Exit $reader
    if(@($received.paths).Count -ne 1 -or (SHA ([IO.File]::ReadAllBytes($received.paths[0]))) -ne (SHA $guestContent)){throw 'The native guest selection did not export completely.'}
    # Host paths cannot bypass the GUI agent's recorded selection grants.
    $rejected=Start-Bridge 'export';Send-JSON $rejected @('C:\Windows\win.ini');$rejected.StandardInput.Close();$rejected.StandardOutput.BaseStream.CopyTo([IO.Stream]::Null);if(!$rejected.WaitForExit(30000) -or $rejected.ExitCode -eq 0){throw 'An unselected guest path was exported.'}
    if($echoes -ne 0){throw 'A host clipboard write was echoed as a guest selection.'}
    $result=[ordered]@{public_hello=$true;ordinary_gui_conpty=$true;terminal_resize=$true;text_host_to_guest=$true;text_guest_to_host=$true;png_host_to_guest_bytes=$true;png_guest_to_host_dimensions=$true;files_host_to_guest_bytes=$true;files_guest_to_host_bytes=$true;unselected_export_denied=$true;host_echoes=0}
    $null=[IO.Directory]::CreateDirectory($EvidenceDirectory);[IO.File]::WriteAllText((Join-Path $EvidenceDirectory 'clipboard-result.json'),($result|ConvertTo-Json -Depth 3),$utf8)
    $result|ConvertTo-Json -Compress
} finally {foreach($p in $owned){try{$p.StandardInput.Close();if(!$p.WaitForExit(1000)){$p.Kill()};$p.Dispose()}catch{}}}
