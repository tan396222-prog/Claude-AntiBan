#requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('prepare', 'move', 'restore', 'cleanup', 'complete', 'uninstall')][string]$Operation,
    [Parameter(Mandatory)][string]$Target,
    [string]$Source,
    [string]$Receipt,
    [string]$Manifest,
    [string]$Backup,
    [string]$Diagnostics,
    [switch]$LegacyTarget
)
$ErrorActionPreference = 'Stop'
$diagnosticStep = 'initializing'
trap {
    $failure = $_
    if ($Diagnostics) {
        try {
          if (!(Test-Path -LiteralPath $Diagnostics)) {
            $chain = @()
            $exception = $failure.Exception
            for ($index = 0; $exception -and $index -lt 4; $index++) {
                $chain += @{type = $exception.GetType().FullName; hresult = $exception.HResult}
                $exception = $exception.InnerException
            }
            $lines = @([regex]::Matches($failure.ScriptStackTrace, ': line (\d+)') | ForEach-Object { [int]$_.Groups[1].Value })
            @{operation = $Operation; phase = $diagnosticStep; pointerSize = [IntPtr]::Size; errors = $chain; lines = $lines; category = [string]$failure.CategoryInfo.Category} | ConvertTo-Json -Depth 4 |
                Set-Content -LiteralPath $Diagnostics -Encoding UTF8
          }
        } catch { }
    }
    throw $failure
}
$manifestName = 'Cage.install-files.json'
$identityName = 'Cage.install-id'

function Read-FileManifest([string]$Path) {
    $files = [Collections.Generic.Dictionary[string,string]]::new([StringComparer]::OrdinalIgnoreCase)
    if (!(Test-Path -LiteralPath $Path -PathType Leaf)) { return ,$files }
    if ((Get-Item -LiteralPath $Path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { return ,$files }
    try {
        $value = Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json
        if ($value.version -ne 1) { throw 'Unknown installation manifest version.' }
        foreach ($entry in $value.files) {
            if (!$entry.path -or $entry.path -match '(^/|[\\:]|(^|/)\.\.?(/|$))' -or $entry.sha256 -notmatch '^[a-fA-F0-9]{64}$' -or $files.ContainsKey($entry.path)) {
                throw 'Invalid installation manifest entry.'
            }
            $files.Add($entry.path, $entry.sha256.ToLowerInvariant())
        }
    } catch {
        # An unreadable legacy manifest grants no permission to delete files.
        $files.Clear()
    }
    return ,$files
}

function Directory-Paths([string[]]$Paths) {
    $directories = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    $null = $directories.Add('')
    foreach ($path in $Paths) {
        $parent = Split-Path $path -Parent
        while ($parent) { $null = $directories.Add($parent.Replace('\', '/')); $parent = Split-Path $parent -Parent }
    }
    return ,$directories
}

function Read-Tree([string]$Root, [string[]]$KnownPaths) {
    $entries = [Collections.Generic.List[object]]::new()
    $directories = [Collections.Generic.Queue[string]]::new()
    $neededDirectories = Directory-Paths $KnownPaths
    foreach ($path in $KnownPaths) { $null = $neededDirectories.Add($path) }
    $directories.Enqueue($Root)
    while ($directories.Count) {
        foreach ($entry in Get-ChildItem -LiteralPath $directories.Dequeue() -Force) {
            $entries.Add($entry)
            if ($entry.PSIsContainer -and !($entry.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
                if ($null -eq $KnownPaths -or $neededDirectories.Contains((Relative-Path $Root $entry.FullName))) { $directories.Enqueue($entry.FullName) }
            }
        }
    }
    return $entries.ToArray()
}

function Relative-Path([string]$Root, [string]$Path) {
    return $Path.Substring($Root.TrimEnd([IO.Path]::DirectorySeparatorChar).Length + 1).Replace('\', '/')
}

function Open-ReadLocked([string]$Path) {
    return [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read -bor [IO.FileShare]::Delete)
}

function Stream-Hash([IO.Stream]$Stream) {
    $algorithm = [Security.Cryptography.SHA256]::Create()
    try { return ([BitConverter]::ToString($algorithm.ComputeHash($Stream))).Replace('-', '').ToLowerInvariant() }
    finally { $algorithm.Dispose(); $Stream.Position = 0 }
}

function Initialize-VerifiedDeletion {
    if (!('CageVerifiedFile' -as [type])) { Add-Type -Path (Join-Path $PSScriptRoot 'verified-file-deletion.cs') }
}

function Remove-MatchingFile([string]$Root, [string]$Relative, [string]$Hash) {
    $Path = Join-Path $Root $Relative
    if (!(Test-Path -LiteralPath $Path -PathType Leaf)) { return }
    if ((Get-Item -LiteralPath $Path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { return }
    $windows = $env:OS -eq 'Windows_NT'
    if ($windows) { Initialize-VerifiedDeletion; $stream = [CageVerifiedFile]::Open($Root, $Relative) }
    else { $stream = Open-ReadLocked $Path }
    try {
        if ((Stream-Hash $stream) -eq $Hash) {
            if ($windows) { [CageVerifiedFile]::Delete($stream) } else { [IO.File]::Delete($Path) }
        }
    }
    finally { $stream.Dispose() }
}

function Remove-EmptyDirectories([string]$Root, [string[]]$KnownDirectories) {
    foreach ($directory in @(Read-Tree $Root $KnownDirectories | Where-Object { $_.PSIsContainer -and !($_.Attributes -band [IO.FileAttributes]::ReparsePoint) } | Sort-Object { $_.FullName.Length } -Descending)) {
        if ((Relative-Path $Root $directory.FullName) -in $KnownDirectories -and @(Get-ChildItem -LiteralPath $directory.FullName -Force).Count -eq 0) {
            [IO.Directory]::Delete($directory.FullName)
        }
    }
}

function Preserved-Destination([string]$Relative, [bool]$Quarantine) {
    $path = Join-Path $Target $Relative
    $parent = Split-Path $path -Parent
    $conflict = $Quarantine -or (Test-Path -LiteralPath $path)
    while ($parent -ne $Target) {
        if (Test-Path -LiteralPath $parent -PathType Leaf) { $conflict = $true; break }
        $parent = Split-Path $parent -Parent
    }
    if ($conflict) { return $recovery + '/' + $Relative }
    return $Relative
}

function Save-Transfer($Transfer) {
    $temporary = $Receipt + '.' + [guid]::NewGuid().ToString('N') + '.tmp'
    try {
        $Transfer | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $temporary -Encoding UTF8
        [IO.File]::Replace($temporary, $Receipt, [NullString]::Value)
    } finally { if (Test-Path -LiteralPath $temporary) { [IO.File]::Delete($temporary) } }
}

function Complete-Transfer {
    $transfer = Get-Content -LiteralPath $Receipt -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($transfer.kind -ne 'cage-installation' -or !(Test-Path -LiteralPath (Join-Path $Target $identityName))) { throw '安装目录尚未恢复，事务回执已保留。' }
    if ($transfer.backupPath -and (Test-Path -LiteralPath $transfer.backupPath)) { throw ('旧目录仍需恢复，事务回执已保留：' + $transfer.backupPath) }
    foreach ($entry in $transfer.moves) {
        if ($entry.state -eq 'absent') { continue }
        $relative = if ($entry.state -eq 'moved') { $entry.destination } else { $entry.path }
        if (!(Get-Item -LiteralPath (Join-Path $Target $relative) -Force -ErrorAction SilentlyContinue)) { throw ('个人文件尚未恢复，事务回执已保留：' + $relative) }
    }
    $receiptStream = Open-ReadLocked $Receipt
    try { $hash = Stream-Hash $receiptStream } finally { $receiptStream.Dispose() }
    Remove-MatchingFile (Split-Path $Receipt -Parent) (Split-Path $Receipt -Leaf) $hash
    if (Test-Path -LiteralPath $Receipt) { throw '安装回执在完成期间发生变化，已保留待核对。' }
}

$Target = [IO.Path]::GetFullPath($Target).TrimEnd([IO.Path]::DirectorySeparatorChar)
foreach ($rootPath in @($Target, $Source)) {
    if (!$rootPath) { continue }
    $rootEntry = Get-Item -LiteralPath $rootPath -Force -ErrorAction SilentlyContinue
    if ($rootEntry -and $rootEntry.Attributes -band [IO.FileAttributes]::ReparsePoint) {
        throw ('安装目录本身是文件系统链接，原文件已保留。请选择实际程序目录：' + $rootPath)
    }
}
if ($Operation -eq 'prepare') {
    $diagnosticStep = 'reading_new_manifest'
    if (Test-Path -LiteralPath $Receipt) { throw ('仍有未完成的安装事务，请保留并检查回执：' + $Receipt) }
    $current = Read-FileManifest (Join-Path $Target $manifestName)
    $legacySnapshot = !$current.Count -and $LegacyTarget
    if ($legacySnapshot) {
        $identity = Open-ReadLocked (Join-Path $Target $identityName)
        try { $current[$identityName] = Stream-Hash $identity } finally { $identity.Dispose() }
    }
    if (!$current.Count) { throw '新程序缺少有效文件清单，安装已停止。' }
    $diagnosticStep = 'reading_new_uninstaller'
    $uninstaller = Open-ReadLocked (Join-Path $Target 'Uninstall.exe')
    try { $current['Uninstall.exe'] = Stream-Hash $uninstaller } finally { $uninstaller.Dispose() }
    $transfers = [Collections.Generic.List[object]]::new()
    $moves = [Collections.Generic.List[object]]::new()
    $directories = @()
    $recovery = 'Cage-preserved-files/' + [guid]::NewGuid().ToString('N')
    $programDirectories = Directory-Paths @($current.Keys)
    if ($Source -and (Test-Path -LiteralPath $Source -PathType Container)) {
        $diagnosticStep = 'scanning_previous_installation'
        $Source = [IO.Path]::GetFullPath($Source).TrimEnd([IO.Path]::DirectorySeparatorChar)
        $previous = Read-FileManifest (Join-Path $Source $manifestName)
        $previousDirectories = Directory-Paths @($previous.Keys)
        $pending = [Collections.Generic.Queue[string]]::new()
        $pending.Enqueue($Source)
        while ($pending.Count) {
          foreach ($entry in Get-ChildItem -LiteralPath $pending.Dequeue() -Force) {
            $relative = Relative-Path $Source $entry.FullName
            $isLink = [bool]($entry.Attributes -band [IO.FileAttributes]::ReparsePoint)
            $programContent = $previous.ContainsKey($relative) -or (
                $programDirectories.Contains((Split-Path $relative -Parent).Replace('\', '/')) -and
                ($entry.Extension -in @('.dll','.exe','.ocx','.sys','.pyd','.so','.dylib') -or $entry.Name -eq 'node_modules'))
            $hasOwnedChildren = $previousDirectories.Contains($relative)
            # A legacy rollback snapshot also contains old personal files.
            # The failed candidate's manifest identifies its program paths, so
            # current personal content can return to its original location.
            if ($legacySnapshot -and $previous.Count -and !$programContent -and !$hasOwnedChildren -and
                $relative -notin @($manifestName, 'Cage-preserved-files')) {
                $oldCopy = Get-Item -LiteralPath (Join-Path $Target $relative) -Force -ErrorAction SilentlyContinue
                if ($oldCopy) {
                    $oldDestination = Join-Path $Target ($recovery + '/previous/' + $relative)
                    [IO.Directory]::CreateDirectory((Split-Path $oldDestination -Parent)) | Out-Null
                    if ($oldCopy.Attributes -band [IO.FileAttributes]::Directory) { [IO.Directory]::Move($oldCopy.FullName, $oldDestination) }
                    else { [IO.File]::Move($oldCopy.FullName, $oldDestination) }
                }
            }
            if ($isLink -or ($entry.PSIsContainer -and !$hasOwnedChildren -and !(Test-Path -LiteralPath (Join-Path $Target $relative) -PathType Container))) {
                $moves.Add(@{path = $relative; destination = (Preserved-Destination $relative $programContent); directory = ($entry.PSIsContainer -or [bool]($entry.Attributes -band [IO.FileAttributes]::Directory)); state = 'prepared'})
                continue
            }
            if ($entry.PSIsContainer) {
                $directories += $relative
                $pending.Enqueue($entry.FullName)
                continue
            }
            if (!$previous.ContainsKey($relative) -and !(Test-Path -LiteralPath (Join-Path $Target $relative)) -and $relative -ne $manifestName) {
                $moves.Add(@{path = $relative; destination = (Preserved-Destination $relative $programContent); directory = $false; state = 'prepared'})
                continue
            }
            $sourceStream = Open-ReadLocked $entry.FullName
            try {
                $hash = Stream-Hash $sourceStream
                if (($previous.ContainsKey($relative) -and $previous[$relative] -eq $hash) -or ($relative -eq $manifestName -and $previous.Count)) {
                    $transfers.Add(@{ path = $relative; sha256 = $hash; destination = $null })
                    continue
                }
                $path = Join-Path $Target $relative
                if (Test-Path -LiteralPath $path -PathType Leaf) {
                    $candidate = Open-ReadLocked $path
                    try { $same = (Stream-Hash $candidate) -eq $hash } finally { $candidate.Dispose() }
                    if ($same) {
                        $transfers.Add(@{ path = $relative; sha256 = $hash; destination = $relative })
                        continue
                    }
                }
                $moves.Add(@{path = $relative; destination = (Preserved-Destination $relative $programContent); directory = $false; state = 'prepared'})
            } finally { $sourceStream.Dispose() }
          }
        }
    }
    $diagnosticStep = 'writing_new_manifest'
    $files = @($current.Keys | Sort-Object | ForEach-Object { @{ path = $_; sha256 = $current[$_] } })
    @{ version = 1; files = $files } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $Target $manifestName) -Encoding UTF8
    $diagnosticStep = 'writing_transaction_receipt'
    $record = @{kind = 'cage-installation'; version = 1; installPath = $Source; stagePath = $Target; backupPath = $Backup; files = @($transfers); directories = $directories; moves = @($moves)}
    $encoding = [Text.UTF8Encoding]::new($true)
    [byte[]]$bytes = $encoding.GetPreamble() + $encoding.GetBytes(($record | ConvertTo-Json -Depth 6))
    $temporary = $Receipt + '.' + [guid]::NewGuid().ToString('N') + '.tmp'
    try {
        $output = [IO.File]::Open($temporary, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        try { $output.Write($bytes, 0, $bytes.Length); $output.Flush($true) } finally { $output.Dispose() }
        [IO.File]::Move($temporary, $Receipt)
    } finally { if (Test-Path -LiteralPath $temporary) { [IO.File]::Delete($temporary) } }
} elseif ($Operation -in @('move','restore')) {
    $diagnosticStep = 'moving_preserved_files'
    $transfer = Get-Content -LiteralPath $Receipt -Raw -Encoding UTF8 | ConvertFrom-Json
    $moves = @($transfer.moves)
    if ($Operation -eq 'restore') { [Array]::Reverse($moves) }
    foreach ($entry in $moves) {
        if ($Operation -eq 'restore' -and $entry.state -notin @('moving','moved')) { continue }
        $from = if ($Operation -eq 'move') { $entry.path } else { $entry.destination }
        $to = if ($Operation -eq 'move') { $entry.destination } else { $entry.path }
        $from = Join-Path $Source $from
        $to = Join-Path $Target $to
        if (!(Get-Item -LiteralPath $from -Force -ErrorAction SilentlyContinue)) {
            if ($Operation -eq 'restore' -and !(Get-Item -LiteralPath $to -Force -ErrorAction SilentlyContinue)) { throw ('待恢复的个人文件无法定位，回执已保留：' + $entry.path) }
            if ($Operation -eq 'move' -and $entry.state -eq 'prepared') { $entry.state = 'absent'; Save-Transfer $transfer }
            continue
        }
        [IO.Directory]::CreateDirectory((Split-Path $to -Parent)) | Out-Null
        if ($Operation -eq 'move') { $entry.state = 'moving'; Save-Transfer $transfer }
        $moved = $false
        try {
            if ($entry.directory) { [IO.Directory]::Move($from, $to) } else { [IO.File]::Move($from, $to) }
            $moved = $true
            $entry.state = if ($Operation -eq 'move') { 'moved' } else { 'restored' }
            Save-Transfer $transfer
        } catch {
            if ($Operation -eq 'move' -and !$moved) { $entry.state = 'failed'; Save-Transfer $transfer }
            throw
        }
    }
    if ($Operation -eq 'move' -and @($moves | Where-Object { $_.destination.StartsWith('Cage-preserved-files/') }).Count) {
        Write-Output ('同名修改或旧程序文件已保留在 ' + (Join-Path $Target 'Cage-preserved-files'))
    }
} elseif ($Operation -eq 'cleanup') {
    $diagnosticStep = 'verifying_previous_file_ownership'
    $transfer = Get-Content -LiteralPath $Receipt -Raw -Encoding UTF8 | ConvertFrom-Json
    $actual = @(Read-Tree $Source @($transfer.files.path) | Where-Object { !($_.Attributes -band [IO.FileAttributes]::ReparsePoint) } | ForEach-Object { Relative-Path $Source $_.FullName })
    foreach ($entry in $transfer.files) {
        if ($entry.path -notin $actual) { continue }
        try {
            if ($entry.destination) {
                $copy = Open-ReadLocked (Join-Path $Target $entry.destination)
                try { if ((Stream-Hash $copy) -ne $entry.sha256) { continue } } finally { $copy.Dispose() }
            }
            Remove-MatchingFile $Source $entry.path $entry.sha256
        } catch { Write-Output '安装期间变化或被占用的文件已保留在旧目录。' }
    }
    Remove-EmptyDirectories $Source $transfer.directories
    if (@(Get-ChildItem -LiteralPath $Source -Force).Count) {
        $recovery = Join-Path $Target ('Cage-preserved-files/late-' + [guid]::NewGuid().ToString('N'))
        [IO.Directory]::CreateDirectory((Split-Path $recovery -Parent)) | Out-Null
        [IO.Directory]::Move($Source, $recovery)
        Write-Output ('安装期间变化的文件已保留在 ' + $recovery)
    } else { [IO.Directory]::Delete($Source) }
    Complete-Transfer
} elseif ($Operation -eq 'complete') {
    $diagnosticStep = 'completing_transaction_receipt'
    Complete-Transfer
} else {
    # The payload list comes from this uninstaller's embedded build manifest.
    $diagnosticStep = 'reading_uninstall_manifest'
    $owned = Read-FileManifest $Manifest
    if (!$owned.Count) { throw '卸载文件清单无效，原文件已保留。' }
    $installed = Read-FileManifest (Join-Path $Target $manifestName)
    if ($installed.ContainsKey('Uninstall.exe')) { $owned['Uninstall.exe'] = $installed['Uninstall.exe'] }
    $actual = @(Read-Tree $Target @($owned.Keys) | Where-Object { !($_.Attributes -band [IO.FileAttributes]::ReparsePoint) } | ForEach-Object { Relative-Path $Target $_.FullName })
    $diagnosticStep = 'deleting_verified_program_files'
    foreach ($relative in $owned.Keys) {
        if ($relative -ne $identityName -and $relative -in $actual) { Remove-MatchingFile $Target $relative $owned[$relative] }
    }
    if ($installed.Count) {
        $manifestStream = Open-ReadLocked (Join-Path $Target $manifestName)
        try { $manifestHash = Stream-Hash $manifestStream } finally { $manifestStream.Dispose() }
        Remove-MatchingFile $Target $manifestName $manifestHash
    }
    $directories = @()
    foreach ($relative in $owned.Keys) {
        $directory = Split-Path $relative -Parent
        while ($directory) { $directories += $directory.Replace('\', '/'); $directory = Split-Path $directory -Parent }
    }
    $diagnosticStep = 'removing_program_directories'
    Remove-EmptyDirectories $Target $directories
    # A retained identity lets a later installation reuse this data-bearing folder.
    $remaining = @(Get-ChildItem -LiteralPath $Target -Force | Where-Object Name -ne $identityName)
    if (!$remaining.Count) {
        if ($owned.ContainsKey($identityName)) { Remove-MatchingFile $Target $identityName $owned[$identityName] }
        if (@(Get-ChildItem -LiteralPath $Target -Force).Count -eq 0) { [IO.Directory]::Delete($Target) }
    } else { Write-Output ('用户新增或修改的文件已保留在 ' + $Target) }
}
