#requires -Version 5.1
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'windows-guest-media.ps1')
$setupPath = Join-Path $PSScriptRoot 'start-windows-evaluation-setup.ps1'
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($setupPath, [ref]$null, [ref]$errors)
if ($errors.Count) { throw $errors[0] }
$null = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'initialize-windows-test-accounts.ps1'), [ref]$null, [ref]$errors)
if ($errors.Count) { throw $errors[0] }
foreach ($name in @('New-EvaluationAnswer', 'Get-EvaluationImage')) {
    $fn = $ast.Find({ param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq $name }, $true)
    . ([scriptblock]::Create($fn.Extent.Text))
}
function Assert-Check([bool]$condition, [string]$message) { if (!$condition) { throw $message } }
function Expect-Error([scriptblock]$run, [string]$contains) {
    try { $null = & $run } catch {
        if ($_.Exception.Message.Contains($contains)) { return }
        throw
    }
    throw "Expected failure: $contains"
}
$template = [IO.File]::ReadAllText((Join-Path $PSScriptRoot 'windows-evaluation-autounattend.xml'))
$interactive = [xml](New-EvaluationAnswer $template 3 64GB $false)
Assert-Check ($interactive.SelectSingleNode("//*[local-name()='AcceptEula']").InnerText -eq 'false') 'Native license acceptance page suppressed'
Expect-Error { New-EvaluationAnswer $template 0 64GB $true } 'Invalid evaluation image'
Expect-Error { New-EvaluationAnswer $template 1 32GB $true } 'Invalid evaluation image'
$doc = [xml](New-EvaluationAnswer $template 3 64GB $true)
$ns = [Xml.XmlNamespaceManager]::new($doc.NameTable)
$ns.AddNamespace('u','urn:schemas-microsoft-com:unattend')
function Value([string]$xpath) { return $doc.SelectSingleNode($xpath, $ns).InnerText }
Assert-Check ((Value '//u:WillWipeDisk') -eq 'false') 'Disk wipe enabled'
Assert-Check ((Value '//u:AcceptEula') -eq 'true') 'Missing recorded consent'
Assert-Check ((Value '//u:InstallTo/u:DiskID') -eq '0' -and (Value '//u:InstallTo/u:PartitionID') -eq '3') 'Wrong Windows partition'
Assert-Check ((Value '//u:InstallFrom/u:MetaData/u:Value') -eq '3') 'Selected image index lost'
Assert-Check ((Value '//u:CreatePartition[u:Order="3"]/u:Size') -eq '63722') 'Wrong partition geometry'
Assert-Check ((Value '//u:CreatePartition[u:Order="4"]/u:Extend') -eq 'true') 'Missing recovery partition'
Assert-Check ($doc.SelectNodes('//u:AutoLogon | //u:UserAccounts | //u:RunSynchronous | //u:HideOnlineAccountScreens | //u:SkipMachineOOBE | //u:ProductKey', $ns).Count -eq 0) 'Unexpected setup bypass or embedded credential'

# Exercise multi-index WIM detection while preserving a pre-existing ISO mount.
$temp = New-Item -ItemType Directory -Path (Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString('N')))
try {
    $null = New-PSDrive -Name CAGEQA -PSProvider FileSystem -Root $temp.FullName
    $null = New-Item -Path 'CAGEQA:\sources' -ItemType Directory
    $null = New-Item -Path 'CAGEQA:\sources\install.wim' -ItemType File
    $script:ownMount = $false
    $script:dismounted = 0
    $script:ambiguous = $false
    function Get-DiskImage { param($ImagePath) [pscustomobject]@{Attached = !$script:ownMount} }
    function Get-Volume { process { [pscustomobject]@{DriveLetter='CAGEQA'} } }
    function Mount-DiskImage { param($ImagePath,$Access,$StorageType) Assert-Check ($Access -eq 'ReadOnly') 'ISO was mounted writable' }
    function Dismount-DiskImage { param($ImagePath) $script:dismounted++ }
    function Get-WindowsImage {
        param($ImagePath,$Index)
        if (!$Index) { return @([pscustomobject]@{ImageIndex=1}, [pscustomobject]@{ImageIndex=2}) }
        $edition = if ($Index -eq 2 -or $script:ambiguous) { 'EnterpriseEval' } else { 'Professional' }
        return [pscustomobject]@{ImageIndex=$Index; EditionId=$edition; Architecture=9; Languages=@('en-US')}
    }
    Assert-Check ((Get-EvaluationImage 'test.iso').ImageIndex -eq 2) 'Incorrect edition selected'
    Assert-Check ($script:dismounted -eq 0) 'Existing ISO mount was disconnected'
    $script:ownMount = $true
    $script:ambiguous = $true
    Expect-Error { Get-EvaluationImage 'test.iso' } 'exactly one'
    Assert-Check ($script:dismounted -eq 1) 'Failed to release our ISO mount on error'
} finally {
    Remove-PSDrive -Name CAGEQA -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $temp.FullName -Recurse
}
Write-Output 'Passed evaluation image selection, answer-file consent, GPT layout, reserved recovery capacity, original OOBE and ISO mount cleanup.'

# Run the production orchestration starting at target validation. Host services
# are simulated here, while media/receipt publishing uses the real filesystem.
$steps = $ast.EndBlock.Statements
$first = @($steps | Where-Object { $_.Extent.Text.StartsWith('$root = Get-Item') })[0].Extent.StartOffset
$testScriptDirectory = $PSScriptRoot
$orchestration = [scriptblock]::Create($ast.Extent.Text.Substring($first).Replace('$PSScriptRoot', '$testScriptDirectory'))
$temp = New-Item -ItemType Directory -Path (Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString('N')))
try {
    $VMId = [guid]::NewGuid()
    $InstanceDirectory = $temp.FullName
    $script:testVM = [pscustomobject]@{Id=$VMId; ConfigurationLocation=$temp.FullName; State='Off'; Generation=2}
    $disk = Join-Path $temp.FullName 'system.vhdx'
    $iso = Join-Path $temp.FullName 'setup.iso'
    $PowerShellZip = Join-Path $temp.FullName 'fixture.zip'
    [IO.File]::WriteAllText($PowerShellZip, 'package fixture')
    $GuestPowerShellSHA256 = (Get-FileHash -LiteralPath $PowerShellZip).Hash
    [IO.File]::WriteAllText($disk,'qa-placeholder')
    [IO.File]::WriteAllText($iso,'simulated official media')
    $ExpectedIsoSha256 = (Get-FileHash -LiteralPath $iso).Hash
    $script:testDrive = [pscustomobject]@{Path=$disk}
    $script:testDVDs = @([pscustomobject]@{Path=$iso})
    $script:failSectorWrite = $true
    $script:failFirmware = $false
    $script:partitionStyle = 'RAW'
    $script:mounts = 0
    $script:answerAttachments = 0
    $Start = $false
    [switch]$AcceptEvaluationTerms = $false
    function Get-VM { param($Id) $script:testVM }
    function Get-VMFirmware { param($VM) [pscustomobject]@{SecureBoot='On'; SecureBootTemplate='MicrosoftWindows'} }
    function Get-VMNetworkAdapter { param($VM) }
    function Get-VMSnapshot { param($VM) }
    function Get-VMSecurity { param($VM) [pscustomobject]@{TpmEnabled=$true} }
    function Get-VMHardDiskDrive { process { $script:testDrive } }
    function Get-VMDvdDrive { param($VM) $script:testDVDs }
    function Get-VHD { param($Path) [pscustomobject]@{VhdType='Dynamic';ParentPath=$null;Attached=$false;Size=64GB} }
    function Get-EvaluationImage { param($Path) [pscustomobject]@{EditionId='EnterpriseEval';ImageIndex=3} }
    function Mount-VHD { param($Path,[switch]$ReadOnly,[switch]$NoDriveLetter) Assert-Check ($ReadOnly -and $NoDriveLetter) 'Writable host mount';$script:mounts++ }
    function Get-Disk { process { [pscustomobject]@{ IsReadOnly=$true; PartitionStyle=$script:partitionStyle } } }
    function Dismount-VHD { param($Path) $script:mounts-- }
    function Write-AnswerISO {
        param($Source,$Output)
        $xml = Join-Path $Source 'Autounattend.xml'
        $answer = [xml][IO.File]::ReadAllText($xml)
        $runs = @($answer.SelectNodes('//*[local-name()="RunSynchronousCommand"]'))
        Assert-Check ($runs.Count -eq 1 -and $runs[0].ParentNode.ParentNode.ParentNode.pass -eq 'specialize') 'Expected one system setup action'
        $path = $runs[0].SelectSingleNode('*[local-name()="Path"]').InnerText
        $cmd = Join-Path $Source 'install.cmd'
        $hash = (Get-FileHash -LiteralPath $cmd).Hash
        Assert-Check ($path.Length -le 259 -and $path -match $hash) 'Boot command did not pin its installer or exceeds the Windows Setup length limit'
        Assert-Check ((Get-FileHash -LiteralPath (Join-Path $Source 'PowerShell.zip')).Hash -eq $GuestPowerShellSHA256) 'Guest package is absent or incorrect'
        Assert-Check ([IO.File]::ReadAllText($cmd) -match $GuestPowerShellSHA256) 'Guest did not receive the trusted package digest'
        [IO.File]::Copy($xml, $Output)
        if ($script:failSectorWrite) { throw 'injected media-write failure' }
    }
    function Grant-VMAnswerReadAccess { param($ISO,$Id) }
    function Add-VMDvdDrive {
        param($VM,$Path,[switch]$Passthru)
        $script:answerAttachments++
        $added = [pscustomobject]@{Path=$Path}
        $script:testDVDs += $added
        $added
    }
    function Set-VMDvdDrive {
        param($VMDvdDrive,$Path,[switch]$Passthru)
        $VMDvdDrive.Path = $Path
        $VMDvdDrive
    }
    function Set-VMFirmware {
        param($VM,$BootOrder)
        if ($script:failFirmware) { throw 'injected firmware failure' }
        Assert-Check ($BootOrder[0].Path -eq $script:testDrive.Path) 'Blank disk must precede setup media in boot order'
    }

    Expect-Error { & $orchestration } 'injected media-write failure'
    $answerRoot = Join-Path $temp.FullName 'evaluation-setup'
    Assert-Check (!(Test-Path (Join-Path $answerRoot 'native-pwsh7'))) 'Incomplete answer was published'
    Assert-Check ($script:testDVDs.Count -eq 1) 'DVD attached before media was complete'
    $script:failSectorWrite = $false
    $script:failFirmware = $true
    Expect-Error { & $orchestration } 'injected firmware failure'
    Assert-Check ($script:testDVDs.Count -eq 2) 'Did not exercise partially attached DVD'
    $script:failFirmware = $false
    $ready = & $orchestration
    Assert-Check ($ready.LicenseUI -eq 'native' -and !$ready.PSObject.Properties['EvaluationTermsAcceptedUtc']) 'Native setup receipt claimed acceptance'
    Assert-Check ($script:answerAttachments -eq 1) 'Reused VM received duplicate DVD'
    $AcceptEvaluationTerms = $true
    $ready = & $orchestration
    Assert-Check ($ready.LicenseUI -eq 'accepted' -and [bool]$ready.EvaluationTermsAcceptedUtc) 'Explicit acceptance not recorded'
    Assert-Check ($script:answerAttachments -eq 1 -and $script:testDVDs.Count -eq 2) 'Changing license mode added another drive'
    # A newly created VM already has a generic answer DVD. Evaluation setup
    # must replace it, otherwise Windows can discover two answer documents.
    $script:testDVDs[1].Path = Join-Path $InstanceDirectory 'windows-guest-tools.iso'
    $ready = & $orchestration
    Assert-Check ($script:testDVDs.Count -eq 2 -and $script:testDVDs[1].Path -eq $ready.AnswerISO -and $script:answerAttachments -eq 1) 'Generic setup DVD was not replaced'
    $script:partitionStyle = 'GPT'
    $AcceptEvaluationTerms = $false
    Expect-Error { & $orchestration } 'not empty and RAW'
    Assert-Check ($script:testDVDs[1].Path -eq $ready.AnswerISO -and $script:mounts -eq 0) 'Retry modified a partitioned VM or leaked a mount'

    # Exercise the generic-media reuse path with no download, then confirm its
    # mount is released even if the payload has been damaged.
    $genericISO = Join-Path $temp.FullName 'generic.iso'
    [IO.File]::WriteAllText($genericISO, 'simulated Cage DVD')
    $null = New-PSDrive -Name CAGEMEDIA -PSProvider FileSystem -Root $temp.FullName
    $onDisc = Join-Path 'CAGEMEDIA:\' 'PowerShell.zip'
    Copy-Item -LiteralPath $PowerShellZip -Destination $onDisc
    $generic = '<unattend xmlns="urn:schemas-microsoft-com:unattend"><settings pass="specialize"/></unattend>'
    function Get-DiskImage { param($ImagePath) [pscustomobject]@{Attached=$false} }
    function Get-Volume { process { [pscustomobject]@{DriveLetter='CAGEMEDIA'; FileSystemLabel='CAGE_ANSWER'} } }
    function Mount-DiskImage { param($ImagePath,$Access,$StorageType) Assert-Check ($Access -eq 'ReadOnly') 'Writable optical source'; $script:mounts++ }
    function Dismount-DiskImage { param($ImagePath) $script:mounts-- }
    function Invoke-WebRequest { throw 'Downloaded an existing offline package' }
    $output = Join-Path $temp.FullName 'readback.iso'
    New-WindowsGuestAnswerISO $output ([xml]$generic) '' $genericISO
    $raw = [xml][IO.File]::ReadAllText($output)
    Assert-Check (!$raw.SelectSingleNode('//*[local-name()="AcceptEula" or local-name()="DiskConfiguration" or local-name()="UserAccounts"]')) 'Generic media changed the interactive Windows setup'
    Remove-Item -LiteralPath $output
    Set-Content -LiteralPath $onDisc -Value 'damaged package'
    Expect-Error { New-WindowsGuestAnswerISO $output ([xml]$generic) '' $genericISO } 'SHA-256'
    Assert-Check (!(Test-Path $output) -and $script:mounts -eq 0) 'Corrupt guest tools were published or the optical image remained mounted'
} finally {
    Remove-PSDrive -Name CAGEMEDIA -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $temp.FullName -Recurse
}
Write-Output 'Passed injected media-write and firmware failures, retry with DVD attached, explicit/native license selection and refusal to touch a partitioned guest disk.'
