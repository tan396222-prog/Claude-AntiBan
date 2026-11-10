#requires -Version 5.1
<#
.SYNOPSIS
Create isolated QA accounts after an authorized guest administrator completes Windows OOBE.
.DESCRIPTION
Supply a guest administrator credential with Get-Credential. Both new accounts
get independent random passwords. On Windows, Export-Clixml protects the saved
credentials with DPAPI for this host user. Use the returned AdminCredentialFile
with Import-Clixml and New-PSSession -VMId for later PowerShell Direct sessions.
The standard user is intended for the interactive Claude Desktop session.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][guid]$VMId,
    [Parameter(Mandatory)][string]$InstanceDirectory,
    [Parameter(Mandatory)][pscredential]$GuestAdministrator
)
$ErrorActionPreference = 'Stop'
if ([Environment]::OSVersion.Platform -ne 'Win32NT') { throw 'Run on the Hyper-V host in Windows PowerShell.' }
$root = Get-Item -LiteralPath $InstanceDirectory
$qa = Join-Path $root.FullName 'evaluation-setup'
$qaDir = Get-Item -LiteralPath $qa
if (($root.Attributes -band [IO.FileAttributes]::ReparsePoint) -or ($qaDir.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
    throw 'The private QA directories cannot be redirected.'
}
$receipt = Get-Content -LiteralPath (Join-Path $qa 'receipt.json') -Raw | ConvertFrom-Json
$vm = Get-VM -Id $VMId
$disks = @(Get-VMHardDiskDrive -VM $vm)
if ([guid]$receipt.VMId -ne $VMId -or $receipt.InstanceDirectory -ne $root.FullName -or $vm.State -ne 'Running' -or
    $disks.Count -ne 1 -or $disks[0].Path -ne $receipt.Disk -or @(Get-VMNetworkAdapter -VM $vm).Count) {
    throw 'Select the running offline evaluation VM that owns this setup receipt.'
}
$session = New-PSSession -VMId $VMId -Credential $GuestAdministrator
try {
    $guestComputer = Invoke-Command -Session $session -ArgumentList $VMId -ScriptBlock {
        param($expected)
        $ErrorActionPreference = 'Stop'
        $identity = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters' -Name VirtualMachineId
        $setup = Get-ItemProperty 'HKLM:\SYSTEM\Setup'
        $imageState = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Setup\State' -Name ImageState
        $os = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion' -Name EditionID
        $account = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
        if ([guid]$identity.VirtualMachineId -ne $expected -or $os.EditionID -ne 'EnterpriseEval' -or
            $setup.SystemSetupInProgress -ne 0 -or $setup.OOBEInProgress -eq 1 -or $imageState.ImageState -ne 'IMAGE_STATE_COMPLETE' -or
            !$account.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
            throw 'PowerShell Direct must reach the matching Enterprise Evaluation guest after OOBE with guest administrator rights.'
        }
        $env:COMPUTERNAME
    }

    $credentials = @()
    $secrets = Join-Path $qa 'credentials'
    $owner = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $permitted = @($owner.Value, 'S-1-5-18', 'S-1-5-32-544')
    if (!(Test-Path -LiteralPath $secrets)) {
        $acl = [Security.AccessControl.DirectorySecurity]::new()
        $acl.SetAccessRuleProtection($true, $false)
        $acl.SetOwner($owner)
        foreach ($value in $permitted) {
            $sid = [Security.Principal.SecurityIdentifier]::new($value)
            $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($sid, 'FullControl', 'ContainerInherit, ObjectInherit', 'None', 'Allow'))
        }
        if ($PSVersionTable.PSEdition -eq 'Desktop') {
            $null = [IO.Directory]::CreateDirectory($secrets, $acl)
        } else { [IO.FileSystemAclExtensions]::Create([IO.DirectoryInfo]::new($secrets), $acl) }
    }
    $secretsDir = Get-Item -LiteralPath $secrets
    $acl = Get-Acl -LiteralPath $secrets
    $unexpected = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]) |
        Where-Object { $_.AccessControlType -eq 'Allow' -and $_.IdentityReference.Value -notin $permitted })
    if (($secretsDir.Attributes -band [IO.FileAttributes]::ReparsePoint) -or !$acl.AreAccessRulesProtected -or $unexpected.Count) {
        throw 'The test credential directory must have a private, protected Windows ACL.'
    }
    foreach ($name in @('CageQaAdmin', 'CageQaUser')) {
        $path = Join-Path $secrets "$name.credential.xml"
        $qualified = "$guestComputer\$name"
        if (Test-Path -LiteralPath $path) {
            $credential = Import-Clixml -LiteralPath $path
            if ($credential -isnot [pscredential] -or $credential.UserName -ne $qualified) { throw 'Unexpected saved QA credential identity.' }
        } else {
            $random = [byte[]]::new(32)
            $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
            try { $generator.GetBytes($random) } finally { $generator.Dispose() }
            $password = ConvertTo-SecureString ('Qa9!' + [Convert]::ToBase64String($random)) -AsPlainText -Force
            $credential = [pscredential]::new($qualified, $password)
            $credential | Export-Clixml -LiteralPath $path -NoClobber
        }
        $credentials += $credential
    }
    $provision = Invoke-Command -Session $session -ArgumentList $VMId,$credentials[0],$credentials[1] -ScriptBlock {
        param($expected, [pscredential]$admin, [pscredential]$standard)
        $ErrorActionPreference = 'Stop'
        $tag = "Cage isolated test VM $expected"
        $admins = Get-LocalGroup -SID ([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
        $users = Get-LocalGroup -SID ([Security.Principal.SecurityIdentifier]::new('S-1-5-32-545'))
        foreach ($credential in @($admin,$standard)) {
            $name = $credential.UserName.Split('\')[-1]
            $u = Get-LocalUser -Name $name -ErrorAction SilentlyContinue
            if ($u -and $u.Description -ne $tag) { throw "An unrelated guest account already uses the name $name." }
            if (!$u) { $u = New-LocalUser -Name $name -Password $credential.Password -Description $tag }
            $group = if ($name -eq 'CageQaAdmin') { $admins } else { $users }
            if (!(Get-LocalGroupMember -Group $group | Where-Object { $_.SID -eq $u.SID })) {
                Add-LocalGroupMember -Group $group -Member $u
            }
            if ($name -eq 'CageQaUser' -and (Get-LocalGroupMember -Group $admins | Where-Object { $_.SID -eq $u.SID })) {
                throw 'The test desktop user is unexpectedly an administrator.'
            }
        }
        [pscustomobject]@{ Computer = $env:COMPUTERNAME; Admin = 'CageQaAdmin'; DesktopUser = 'CageQaUser' }
    }
    # Authenticate a fresh VMBus session using the new administrator.
    $verified = Invoke-Command -VMId $VMId -Credential $credentials[0] -ScriptBlock { [Security.Principal.WindowsIdentity]::GetCurrent().Name }
    if ($verified -ne $credentials[0].UserName) { throw 'The independent PowerShell Direct login did not match the QA administrator.' }
    [pscustomobject]@{
        VMId = $VMId; Computer = $provision.Computer; Admin = $provision.Admin; StandardDesktopUser = $provision.DesktopUser
        PowerShellDirectVerified = $true
        AdminCredentialFile = Join-Path $secrets 'CageQaAdmin.credential.xml'
        DesktopCredentialFile = Join-Path $secrets 'CageQaUser.credential.xml'
    }
} finally { Remove-PSSession -Session $session }
