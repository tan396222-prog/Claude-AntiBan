#requires -Version 5.1
# Pinned to the x64 standalone ZIP in Microsoft's PowerShell v7.6.9 release.
$GuestPowerShellVersion = '7.6.9'
$GuestPowerShellSHA256 = 'ce7484804c0f0ad4dda10c958e71acdaf7eebfb22505c1b480f89e0e9f1da94d'

function Add-GuestPowerShellToAnswer([string]$Source, [xml]$Answer, [string]$PowerShellZip) {
    $ErrorActionPreference = 'Stop'
    $ns = [Xml.XmlNamespaceManager]::new($Answer.NameTable)
    $unattend = 'urn:schemas-microsoft-com:unattend'
    $ns.AddNamespace('u', $unattend)
    $root = $Answer.SelectSingleNode('/u:unattend', $ns)
    if (!$root) { throw 'Expected a Windows Setup answer document.' }
    $archive = Join-Path $Source 'PowerShell.zip'
    $script = Join-Path $Source 'install.cmd'
    $xml = Join-Path $Source 'Autounattend.xml'
    foreach ($path in @($archive, $script, $xml)) {
        if (Test-Path -LiteralPath $path) { throw "Answer DVD staging file already exists: $path" }
    }
    if ($PowerShellZip) {
        $local = Get-Item -LiteralPath $PowerShellZip
        [IO.File]::Copy($local.FullName, $archive)
    } else {
        $ProgressPreference = 'SilentlyContinue'
        $url = "https://github.com/PowerShell/PowerShell/releases/download/v$GuestPowerShellVersion/PowerShell-$GuestPowerShellVersion-win-x64.zip"
        Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $archive
    }
    if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash -ne $GuestPowerShellSHA256) {
        throw 'The PowerShell archive does not match the pinned Microsoft release SHA-256.'
    }
    $template = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'install-guest-powershell.cmd'))
    $installer = $template.Replace('__CAGE_PWSH_VERSION__', $GuestPowerShellVersion).Replace('__CAGE_PWSH_SHA256__', $GuestPowerShellSHA256)
    [IO.File]::WriteAllText($script, ($installer -replace '\r?\n', "`r`n"), [Text.Encoding]::ASCII)
    $scriptHash = (Get-FileHash -LiteralPath $script -Algorithm SHA256).Hash
    # Windows Setup limits Path to 259 characters. Select the single labeled
    # DVD even when the installation DVD takes a different drive letter.
    $command = 'powershell -NoProfile -c "$d=@(Get-Volume -FileSystemLabel CAGE_ANSWER);if($d.Count -eq 1){$p=$d[0].DriveLetter+'':\install.cmd'';if((Get-FileHash $p).Hash -eq ''HASH''){&$p;exit $LASTEXITCODE}};exit 1"'.Replace('HASH', $scriptHash)
    if ($command.Length -gt 259) { throw 'Windows Setup command exceeds its maximum length.' }

    $settings = $root.SelectSingleNode('u:settings[@pass="specialize"]', $ns)
    if (!$settings) {
        $settings = $root.AppendChild($Answer.CreateElement('settings', $unattend))
        $settings.SetAttribute('pass', 'specialize')
    }
    $deployment = $settings.SelectSingleNode('u:component[@name="Microsoft-Windows-Deployment" and @processorArchitecture="amd64"]', $ns)
    if (!$deployment) {
        $deployment = $settings.AppendChild($Answer.CreateElement('component', $unattend))
        $deployment.SetAttribute('name', 'Microsoft-Windows-Deployment')
        $deployment.SetAttribute('processorArchitecture', 'amd64')
        $deployment.SetAttribute('publicKeyToken', '31bf3856ad364e35')
        $deployment.SetAttribute('language', 'neutral')
        $deployment.SetAttribute('versionScope', 'nonSxS')
    }
    $run = $deployment.SelectSingleNode('u:RunSynchronous', $ns)
    if (!$run) { $run = $deployment.AppendChild($Answer.CreateElement('RunSynchronous', $unattend)) }
    $order = 1
    foreach ($existing in $run.SelectNodes('u:RunSynchronousCommand/u:Order', $ns)) {
        $order = [Math]::Max($order, 1 + [int]$existing.InnerText)
    }
    $entry = $run.AppendChild($Answer.CreateElement('RunSynchronousCommand', $unattend))
    $action = $Answer.CreateAttribute('wcm', 'action', 'http://schemas.microsoft.com/WMIConfig/2002/State')
    $action.Value = 'add'
    $null = $entry.Attributes.Append($action)
    $entry.AppendChild($Answer.CreateElement('Order', $unattend)).InnerText = [string]$order
    $entry.AppendChild($Answer.CreateElement('Path', $unattend)).InnerText = $command
    $Answer.Save($xml)
}

function Write-AnswerISO([string]$Source, [string]$Output) {
    if (!('CageEvaluationIsoStream' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.ComTypes;
public static class CageEvaluationIsoStream {
    public static void Save(object nativeStream, string destination) {
        var source = (IStream)nativeStream;
        var buffer = new byte[32768];
        var count = Marshal.AllocHGlobal(sizeof(int));
        try {
            using (var file = new FileStream(destination, FileMode.CreateNew, FileAccess.Write, FileShare.None)) {
                while (true) {
                    source.Read(buffer, buffer.Length, count);
                    int n = Marshal.ReadInt32(count);
                    if (n == 0) break;
                    file.Write(buffer, 0, n);
                }
            }
        } finally { Marshal.FreeHGlobal(count); }
    }
}
'@
    }
    $filesystem = New-Object -ComObject IMAPI2FS.MsftFileSystemImage
    $root = $result = $stream = $null
    try {
        $filesystem.ChooseImageDefaultsForMediaType(6) # DVD+R
        $filesystem.FileSystemsToCreate = 4 # UDF
        $filesystem.VolumeName = 'CAGE_ANSWER'
        $root = $filesystem.Root
        $root.AddTree($Source, $false)
        $result = $filesystem.CreateResultImage()
        $stream = $result.ImageStream
        [CageEvaluationIsoStream]::Save($stream, $Output)
    } finally {
        # IMAPI can retain handles to the source ZIP until its image is released.
        foreach ($object in @($stream, $result, $root, $filesystem)) {
            if ($null -ne $object) { $null = [Runtime.InteropServices.Marshal]::ReleaseComObject($object) }
        }
    }
}

function Grant-VMAnswerReadAccess([string]$ISO, [guid]$Id) {
    $isoAcl = Get-Acl -LiteralPath $ISO
    $worker = [Security.Principal.NTAccount]::new("NT VIRTUAL MACHINE\$Id")
    $isoAcl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($worker, 'ReadAndExecute', 'Allow'))
    Set-Acl -LiteralPath $ISO -AclObject $isoAcl
}

# Build input in a private, same-instance staging directory. Only the ISO is
# published. Evaluation Setup can reuse the ZIP on the original generic DVD.
function New-WindowsGuestAnswerISO([string]$Output, [xml]$Answer, [string]$PowerShellZip, [string]$GenericAnswer) {
    $parent = [IO.Path]::GetDirectoryName($Output)
    $stage = Join-Path $parent ([guid]::NewGuid().ToString('N'))
    $source = Join-Path $stage 'source'
    $candidate = Join-Path $stage 'answer.iso'
    $null = [IO.Directory]::CreateDirectory($source)
    try {
        $mountOwned = $false
        try {
            if (!$PowerShellZip -and $GenericAnswer -and (Test-Path -LiteralPath $GenericAnswer -PathType Leaf)) {
                if (!(Get-DiskImage -ImagePath $GenericAnswer).Attached) {
                    $null = Mount-DiskImage -ImagePath $GenericAnswer -Access ReadOnly -StorageType ISO
                    $mountOwned = $true
                }
                $timer = [Diagnostics.Stopwatch]::StartNew()
                do {
                    $volumes = @(Get-DiskImage -ImagePath $GenericAnswer | Get-Volume -ErrorAction SilentlyContinue)
                    if ($volumes.Count -eq 1 -and $volumes[0].DriveLetter) { break }
                    Start-Sleep -Milliseconds 250
                } while ($timer.Elapsed.TotalSeconds -lt 15)
                if ($volumes.Count -ne 1 -or !$volumes[0].DriveLetter -or $volumes[0].FileSystemLabel -ne 'CAGE_ANSWER') {
                    throw 'Cannot read the original Cage guest tools DVD.'
                }
                $PowerShellZip = '{0}:\PowerShell.zip' -f $volumes[0].DriveLetter
            }
            Add-GuestPowerShellToAnswer $source $Answer $PowerShellZip
        } finally {
            if ($mountOwned) { $null = Dismount-DiskImage -ImagePath $GenericAnswer }
        }
        Write-AnswerISO $source $candidate
        [IO.File]::Move($candidate, $Output)
    } finally {
        [IO.Directory]::Delete($stage, $true)
    }
}
