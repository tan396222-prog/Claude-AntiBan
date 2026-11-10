#requires -Version 5.1
# Exercise the packaged media builder on a disposable Windows development runner.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$GuestToolsDirectory)
$ErrorActionPreference = 'Stop'
if ([Environment]::OSVersion.Platform -ne 'Win32NT' -or ![Environment]::Is64BitProcess) {
    throw 'The PowerShell guest media smoke requires a 64-bit Windows runner.'
}
. (Join-Path $GuestToolsDirectory 'windows-guest-media.ps1')
$scratch = [IO.Directory]::CreateDirectory((Join-Path ([IO.Path]::GetTempPath()) ('Cage guest media ' + [guid]::NewGuid().ToString('N'))))
try {
    $iso = Join-Path $scratch.FullName 'guest-tools.iso'
    New-WindowsGuestAnswerISO $iso ([xml]'<unattend xmlns="urn:schemas-microsoft-com:unattend"/>')
    $null = Mount-DiskImage -ImagePath $iso -StorageType ISO -Access ReadOnly
    try {
        $timer = [Diagnostics.Stopwatch]::StartNew()
        do {
            $volume = @(Get-DiskImage -ImagePath $iso | Get-Volume -ErrorAction SilentlyContinue)
            if ($volume.Count -eq 1 -and $volume[0].DriveLetter) { break }
            Start-Sleep -Milliseconds 250
        } while ($timer.Elapsed.TotalSeconds -lt 15)
        if ($volume.Count -ne 1 -or !$volume[0].DriveLetter) { throw 'Cage guest tools DVD did not acquire a drive letter.' }
        $disc = [IO.DriveInfo]::new(('{0}:\' -f $volume[0].DriveLetter))
        if ($disc.DriveFormat -ne 'UDF' -or $disc.VolumeLabel -ne 'CAGE_ANSWER') { throw 'Expected the Cage UDF answer DVD.' }
        $root = $disc.RootDirectory.FullName
        $answer = [xml][IO.File]::ReadAllText((Join-Path $root 'Autounattend.xml'))
        $ns = [Xml.XmlNamespaceManager]::new($answer.NameTable)
        $ns.AddNamespace('u', 'urn:schemas-microsoft-com:unattend')
        $steps = $answer.SelectNodes('/u:unattend/u:settings[@pass="specialize"]/u:component[@name="Microsoft-Windows-Deployment"]/u:RunSynchronous/u:RunSynchronousCommand/u:Path', $ns)
        $installerHash = (Get-FileHash -LiteralPath (Join-Path $root 'install.cmd') -Algorithm SHA256).Hash
        if ($steps.Count -ne 1 -or !$steps[0].InnerText.Contains($installerHash)) { throw 'The answer file does not pin the guest installer on the DVD.' }
        $zip = Join-Path $root 'PowerShell.zip'
        if ((Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash -ne $GuestPowerShellSHA256) { throw 'PowerShell archive on the DVD has an unexpected digest.' }
        $runtime = Join-Path $scratch.FullName 'runtime'
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [IO.Compression.ZipFile]::ExtractToDirectory($zip, $runtime)
    } finally { $null = Dismount-DiskImage -ImagePath $iso }

    $version = @(& (Join-Path $runtime 'pwsh.exe') -NoLogo -NoProfile -NonInteractive -Command '$PSVersionTable.PSVersion.ToString(); [Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString()')
    if ($LASTEXITCODE -ne 0 -or $version.Count -ne 2 -or $version[0] -ne $GuestPowerShellVersion -or $version[1] -ne 'X64') {
        throw "Unexpected guest PowerShell runtime response: $version"
    }
    Write-Output "Verified packaged UDF answer DVD and native Windows x64 PowerShell $GuestPowerShellVersion."
} finally { $scratch.Delete($true) }
