#requires -Version 5.1
param([Parameter(Mandatory)][string]$ProfilePath,[Parameter(Mandatory)][string]$EvidenceDirectory)
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
$profile=Get-Content -LiteralPath $ProfilePath -Raw|ConvertFrom-Json;$utf8=[Text.UTF8Encoding]::new($false)
$owned=[Collections.Generic.List[Diagnostics.Process]]::new()
function Start-Transfer([string]$Operation,[bool]$Helper=$false){
    $s=[Diagnostics.ProcessStartInfo]::new();$s.FileName=if($Helper){Join-Path ([IO.Path]::GetDirectoryName($profile.relay_executable)) 'cage-guest-files.exe'}else{$profile.relay_executable}
    $s.Arguments=if($Helper){$Operation}else{'--relay-profile "'+$ProfilePath+'" '+$Operation};if($Operation -eq 'export'){$s.Arguments+=' --paths-stdin'}
    $s.UseShellExecute=$false;$s.CreateNoWindow=$true;$s.RedirectStandardInput=$true;$s.RedirectStandardOutput=$true;$s.RedirectStandardError=$true;$s.StandardOutputEncoding=$utf8
    $p=[Diagnostics.Process]::Start($s);$owned.Add($p);$null=$p.StandardError.ReadToEndAsync();$p
}
function Paths($P,$Selection){$bytes=$utf8.GetBytes((ConvertTo-Json -InputObject @($Selection) -Compress)+"`n");$P.StandardInput.BaseStream.Write($bytes,0,$bytes.Length);$P.StandardInput.Close()}
function Result($P){$read=$P.StandardOutput.ReadLineAsync();if(!$read.Wait(120000) -or !$read.Result -or !$P.WaitForExit(120000) -or $P.ExitCode -ne 0){throw 'The bounded archive stream did not complete.'};$read.Result|ConvertFrom-Json}
function Hash([string]$File){(Get-FileHash -LiteralPath $File -Algorithm SHA256).Hash}
try {
    $directory=Join-Path $EvidenceDirectory ([guid]::NewGuid().ToString('N'));$null=[IO.Directory]::CreateDirectory($directory)
    $source=Join-Path $directory 'stream-64MiB.bin';$file=[IO.File]::Open($source,'CreateNew','Write','None');$buffer=New-Object byte[] (64KB);for($i=0;$i -lt $buffer.Length;$i++){$buffer[$i]=[byte]($i%251)}
    try{for($i=0;$i -lt 1024;$i++){$file.Write($buffer,0,$buffer.Length)}}finally{$file.Dispose()}
    $write=Start-Transfer 'export' $true;Paths $write @($source);$import=Start-Transfer 'import';$write.StandardOutput.BaseStream.CopyTo($import.StandardInput.BaseStream);$import.StandardInput.Close();$guest=Result $import
    if(!$write.WaitForExit(120000) -or $write.ExitCode -ne 0 -or @($guest.paths).Count -ne 1){throw 'The host archive source failed.'}
    $export=Start-Transfer 'export';Paths $export @($guest.paths);$read=Start-Transfer 'import' $true;$export.StandardOutput.BaseStream.CopyTo($read.StandardInput.BaseStream);$read.StandardInput.Close();$returnedHost=Result $read
    if(!$export.WaitForExit(120000) -or $export.ExitCode -ne 0 -or @($returnedHost.paths).Count -ne 1 -or (Get-Item -LiteralPath $returnedHost.paths[0]).Length -ne 64MB -or (Hash $source) -ne (Hash $returnedHost.paths[0])){throw 'The 64 MiB stream hash or completion status differs.'}
    $result=[ordered]@{bytes=64MB;host_to_guest_complete=$true;guest_to_host_complete=$true;sha256_equal=$true;clipboard_unchanged=$true}
    [IO.File]::WriteAllText((Join-Path $EvidenceDirectory 'large-file-result.json'),($result|ConvertTo-Json),$utf8);$result|ConvertTo-Json -Compress
}finally{foreach($p in $owned){try{$p.StandardInput.Close();if(!$p.WaitForExit(1000)){$p.Kill()};$p.Dispose()}catch{}}}
