# Shared verified package preparation for initial installation and maintenance.
function Get-PinnedPackage($Spec, [string]$Cache) {
    $file = Join-Path $Cache $Spec.filename
    $cached=Test-Path -LiteralPath $file
    if ($cached) {
        $item=Get-Item -LiteralPath $file -Force
        if ($item -isnot [IO.FileInfo] -or $item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'The download cache contains an invalid file.' }
        if ((Get-FileHash -LiteralPath $file).Hash -eq $Spec.sha256) { return $file }
        # Keep the old cache entry until a complete replacement has passed verification.
    }
    $tmp = $file+'.'+[guid]::NewGuid()+'.download'
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $Spec.url -OutFile $tmp
        if ((Get-FileHash -LiteralPath $tmp).Hash -ne $Spec.sha256) { throw ('Package verification failed: '+$Spec.filename) }
        if ($cached) { [IO.File]::Replace($tmp,$file,[Management.Automation.Language.NullString]::Value) }
        else { [IO.File]::Move($tmp,$file) }
    } finally { if ([IO.File]::Exists($tmp)) { [IO.File]::Delete($tmp) } }
    return $file
}

function Expand-PinnedTar([string]$File,[string]$Destination) {
    $null=New-Item -ItemType Directory -Path $Destination -Force
    & (Join-Path $env:SystemRoot 'System32/tar.exe') -xf $File -C $Destination --strip-components 1
    if ($LASTEXITCODE) { throw ('Package extraction failed: '+[IO.Path]::GetFileName($File)) }
}
