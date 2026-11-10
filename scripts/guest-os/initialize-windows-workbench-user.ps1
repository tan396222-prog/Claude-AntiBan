# Invoked by the first-run installer inside its own VM through PowerShell Direct.
# No password is serialized, passed on a command line, or returned to the host.
param([Parameter(Mandatory)][guid]$VMId)
$ErrorActionPreference='Stop'
$machine=Get-CimInstance Win32_ComputerSystem
if ($VMId -eq [guid]::Empty -or $machine.Manufacturer -ne 'Microsoft Corporation' -or $machine.Model -ne 'Virtual Machine' -or
    [guid](Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters').VirtualMachineId -ne $VMId) { throw 'This operation requires the exact fresh Cage guest.' }
$userName='Cage-'+$VMId.ToString('N').Substring(0,10)
$description='Cage '+$VMId.ToString('D')
$key='HKLM:\SOFTWARE\Cage\WorkbenchBoot\'+$VMId.ToString('D')
$logon='HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon'
$win=Get-Item -LiteralPath $logon
$user=Get-LocalUser -Name $userName -ErrorAction SilentlyContinue
$record=if(Test-Path -LiteralPath $key){Get-ItemProperty -LiteralPath $key}
if ($record -and ($record.VM -ne $VMId.ToString('D') -or $record.User -ne $userName)) { throw 'An existing boot-account registration was preserved.' }
if ($user -and (!$record -or $user.Description -cne $description -or ($record.SID -and $record.SID -ne $user.SID.Value))) { throw 'An existing Windows account cannot be used for automatic logon.' }
if ($win.GetValueNames() -contains 'DefaultPassword' -or $win.GetValue('ForceAutoLogon','0') -ne '0' -or
    ($win.GetValue('AutoAdminLogon','0') -eq '1' -and (!$record -or $win.GetValue('DefaultUserName','') -ne $userName))) {
    throw 'An existing Windows logon policy was preserved.'
}
if (!$record -or $record.Stage -ne 'ready') {
    if (!$user -and $record -and $record.SID) { throw 'The dedicated account was removed. No replacement was created.' }
    # Microsoft's Winlogon LSA API. Only the fixed DefaultPassword object may
    # be tested for existence or created. Its contents cannot be read here.
    if (!('CageWorkbenchBootSecret' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Security;
using System.ComponentModel;
public static class CageWorkbenchBootSecret {
    [StructLayout(LayoutKind.Sequential)]
    struct Attributes { public uint length; public IntPtr root, name; public uint flags; public IntPtr descriptor, quality; }
    [StructLayout(LayoutKind.Sequential)]
    struct Unicode { public ushort length, maximum; public IntPtr text; }
    [DllImport("advapi32.dll")] static extern int LsaOpenPolicy(IntPtr system,ref Attributes attr,uint access,out IntPtr policy);
    [DllImport("advapi32.dll")] static extern int LsaOpenSecret(IntPtr policy,ref Unicode name,uint access,out IntPtr secret);
    [DllImport("advapi32.dll")] static extern int LsaQuerySecret(IntPtr secret,out IntPtr current,IntPtr setTime,IntPtr previous,IntPtr previousSetTime);
    [DllImport("advapi32.dll")] static extern int LsaFreeMemory(IntPtr memory);
    [DllImport("advapi32.dll")] static extern int LsaStorePrivateData(IntPtr policy,ref Unicode name,ref Unicode data);
    [DllImport("advapi32.dll")] static extern int LsaNtStatusToWinError(int code);
    [DllImport("advapi32.dll")] static extern int LsaClose(IntPtr handle);
    static bool CheckOrStore(SecureString password) {
        var attr=new Attributes(); attr.length=(uint)Marshal.SizeOf(typeof(Attributes));
        IntPtr policy; int code=LsaOpenPolicy(IntPtr.Zero,ref attr,0x24,out policy);
        if(code!=0)throw new Win32Exception(LsaNtStatusToWinError(code));
        const string fixedName="DefaultPassword"; var name=new Unicode();
        name.length=(ushort)(fixedName.Length*2);name.maximum=(ushort)(name.length+2);name.text=Marshal.StringToHGlobalUni(fixedName);
        try {
            IntPtr existing; code=LsaOpenSecret(policy,ref name,2,out existing);
            if(code==0){
                // Fresh Windows can create the LSA object without assigning it
                // a password. Inspect only the length field, never its buffer.
                IntPtr valueHeader=IntPtr.Zero;
                try {
                    code=LsaQuerySecret(existing,out valueHeader,IntPtr.Zero,IntPtr.Zero,IntPtr.Zero);
                    if(code!=0 && unchecked((uint)code)!=0xc0000034U)throw new Win32Exception(LsaNtStatusToWinError(code));
                    if(valueHeader!=IntPtr.Zero && Marshal.ReadInt16(valueHeader)!=0){
                        if(password==null)return true;
                        throw new InvalidOperationException("An existing Winlogon LSA secret was preserved.");
                    }
                } finally {if(valueHeader!=IntPtr.Zero)LsaFreeMemory(valueHeader);LsaClose(existing);}
            } else if(unchecked((uint)code)!=0xc0000034U)throw new Win32Exception(LsaNtStatusToWinError(code));
            if(password==null)return false;
            var value=new Unicode(); value.length=checked((ushort)(password.Length*2));value.maximum=(ushort)(value.length+2);
            value.text=Marshal.SecureStringToGlobalAllocUnicode(password);
            try {code=LsaStorePrivateData(policy,ref name,ref value);if(code!=0)throw new Win32Exception(LsaNtStatusToWinError(code));}
            finally {Marshal.ZeroFreeGlobalAllocUnicode(value.text);}
            return true;
        } finally {Marshal.FreeHGlobal(name.text);LsaClose(policy);}
    }
    public static bool Exists() { return CheckOrStore(null); }
    public static void StoreNew(SecureString password) { if(password==null)throw new ArgumentNullException();CheckOrStore(password); }
}
'@
    }
    $hasSecret=[CageWorkbenchBootSecret]::Exists()
    if ($hasSecret -and (!$record -or $record.Stage -ne 'storing' -or !$user -or $record.SID -ne $user.SID.Value -or $user.LastLogon)) {
        throw 'An existing Winlogon secret was preserved.'
    }
    if (!$record) {
        $null=New-Item -Path $key -Force
        $null=New-ItemProperty -LiteralPath $key -Name VM -Value $VMId.ToString('D') -PropertyType String
        $null=New-ItemProperty -LiteralPath $key -Name User -Value $userName -PropertyType String
    }
    if (!$hasSecret) {
        if ($user -and ($user.Enabled -or $user.LastLogon)) {throw 'The partially prepared user has already been used. Its password was preserved.'}
        $random=[Security.Cryptography.RandomNumberGenerator]::Create()
        $bytes=New-Object byte[] 48
        $password=[Security.SecureString]::new()
        try {
            $random.GetBytes($bytes)
            $alphabet='abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_'
            foreach ($b in $bytes) { $password.AppendChar($alphabet[[int]$b -band 63]) }
            foreach ($c in 'a9Q!'.ToCharArray()) { $password.AppendChar($c) }
            $password.MakeReadOnly()
            if (!$user) {
                $user=New-LocalUser -Name $userName -Password $password -Disabled -Description $description -PasswordNeverExpires -UserMayNotChangePassword
            } else {
                # Only our disabled, never-used account can reach this interrupted
                # initial transaction. An enabled account never has its password reset.
                Set-LocalUser -InputObject $user -Password $password
            }
            $null=New-ItemProperty -LiteralPath $key -Name SID -Value $user.SID.Value -PropertyType String -Force
            $null=New-ItemProperty -LiteralPath $key -Name Stage -Value 'storing' -PropertyType String -Force
            [CageWorkbenchBootSecret]::StoreNew($password)
        } finally { $password.Dispose();$random.Dispose();[Array]::Clear($bytes,0,$bytes.Length) }
    }
    if (!$user.Enabled) { Enable-LocalUser -InputObject $user }
    $user=Get-LocalUser -SID $user.SID
    $null=New-ItemProperty -LiteralPath $key -Name Stage -Value 'ready' -PropertyType String -Force
}
if (!$user) {throw 'The dedicated workbench account was removed. No replacement was created.'}
if (!$user.Enabled -or $user.PrincipalSource -ne 'Local' -or $user.Description -cne $description) { throw 'The dedicated local account is unavailable.' }
$sid=$user.SID.Value
foreach ($group in Get-LocalGroup) {
    $member=@(Get-LocalGroupMember -Group $group -ErrorAction Stop | Where-Object { $_.SID.Value -eq $sid })
    if ($member.Count -and $group.SID.Value -ne 'S-1-5-32-545') { throw 'The workbench account belongs to a privileged or unexpected group.' }
}
$users=Get-LocalGroup -SID 'S-1-5-32-545'
if (!@(Get-LocalGroupMember -Group $users | Where-Object {$_.SID.Value -eq $sid}).Count) { Add-LocalGroupMember -Group $users -Member $user }
$null=New-ItemProperty -LiteralPath $key -Name SID -Value $sid -PropertyType String -Force

# First boot is armed by the separate boot service only during a real shutdown.
# Windows logon UI is never sent a broad keyboard event or a cleartext value.
[pscustomobject]@{ SID=$sid; Name=$userName; BootID=(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToUniversalTime().ToString('o'); Program=(Join-Path $env:ProgramFiles ("Cage Guest Bridge\"+$VMId)); AlreadySignedIn=($machine.UserName -eq ($env:COMPUTERNAME+'\'+$userName)) }
