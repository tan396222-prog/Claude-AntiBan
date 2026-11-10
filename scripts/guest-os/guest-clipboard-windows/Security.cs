using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Security.AccessControl;
using System.Security.Cryptography;
using System.Security.Principal;
using Microsoft.Win32;

namespace Cage.GuestClipboard;

internal static class SetupAccount
{
    [StructLayout(LayoutKind.Sequential)]
    private struct UserInfo20
    {
        public IntPtr Name, FullName, Comment;
        public uint Flags, UserID;
    }

    [DllImport("netapi32.dll", CharSet = CharSet.Unicode)]
    private static extern uint NetUserGetInfo(string? server, string user, uint level, out IntPtr buffer);
    [DllImport("netapi32.dll")]
    private static extern uint NetApiBufferFree(IntPtr buffer);

    // The authenticated guest status reports only the fixed setup account's
    // disable flag. A failed query stays unknown and cannot complete cleanup.
    internal static bool? Disabled()
    {
        uint result = NetUserGetInfo(null, "cagesetup", 20, out var buffer);
        try
        {
            if (result != 0 || buffer == IntPtr.Zero) return null;
            return (Marshal.PtrToStructure<UserInfo20>(buffer).Flags & 0x2) != 0;
        }
        finally { if (buffer != IntPtr.Zero) NetApiBufferFree(buffer); }
    }
}

internal static class SessionGuard
{
    private static readonly (int Session, SecurityIdentifier? User, bool Unprivileged) ProcessIdentity = ReadProcessIdentity();

    private static (int Session, SecurityIdentifier? User, bool Unprivileged) ReadProcessIdentity()
    {
        using var identity = WindowsIdentity.GetCurrent();
        using var process = Process.GetCurrentProcess();
        return (process.SessionId, identity.User,
            !identity.IsSystem && !new WindowsPrincipal(identity).IsInRole(WindowsBuiltInRole.Administrator));
    }
    [DllImport("user32.dll")] static extern IntPtr OpenInputDesktop(uint flags, bool inherit, uint access);
    [DllImport("user32.dll")] static extern bool CloseDesktop(IntPtr desktop);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern bool GetUserObjectInformation(IntPtr handle, int index, char[] value, int bytes, out int needed);
    [DllImport("wtsapi32.dll", EntryPoint="WTSQuerySessionInformationW", ExactSpelling=true)] static extern bool WTSQuerySessionInformation(IntPtr server, int session, int field, out IntPtr buffer, out int bytes);
    [DllImport("wtsapi32.dll")] static extern void WTSFreeMemory(IntPtr buffer);
    [DllImport("advapi32.dll",SetLastError=true)] static extern bool OpenProcessToken(IntPtr process,uint access,out IntPtr token);
    [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
    [DllImport("kernel32.dll", SetLastError=true)] static extern IntPtr OpenProcess(uint access, bool inherit, int pid);
    [DllImport("kernel32.dll", SetLastError=true)] static extern bool GetProcessTimes(IntPtr process, out long creation, out long exit, out long kernel, out long user);
    [DllImport("advapi32.dll", SetLastError=true)] static extern bool GetTokenInformation(IntPtr token, int field, IntPtr buffer, int size, out int needed);
    [DllImport("advapi32.dll")] static extern bool IsWellKnownSid(IntPtr sid, int kind);
    [StructLayout(LayoutKind.Sequential)] private struct Group { public IntPtr Sid; public uint Flags; }

    // Query only. Constructing WindowsIdentity from a TOKEN_QUERY-only foreign
    // handle requests a duplicate internally and can reject a standard user's
    // own window. Never duplicate or impersonate a process token here.
    private static bool InspectToken(IntPtr token, int field, Func<IntPtr, int, bool> inspect)
    {
        GetTokenInformation(token, field, IntPtr.Zero, 0, out int size);
        if (size <= 0 || size > 65536) return false;
        var buffer = Marshal.AllocHGlobal(size);
        try { return GetTokenInformation(token, field, buffer, size, out var used) && used > 0 && used <= size && inspect(buffer, used); }
        finally { Marshal.FreeHGlobal(buffer); }
    }

    internal static bool OwnsProcess(Process process)
    {
        try
        {
            if (process.HasExited || process.SessionId != ProcessIdentity.Session) return false;
            var handle = OpenProcess(0x1000, false, process.Id); // PROCESS_QUERY_LIMITED_INFORMATION
            if (handle == IntPtr.Zero) return false;
            try
            {
                if (!GetProcessTimes(handle, out var created, out _, out _, out _) ||
                    created != process.StartTime.ToUniversalTime().ToFileTimeUtc() || !OpenProcessToken(handle, 8, out var token)) return false;
                try
                {
                    var user = ProcessIdentity.User;
                    if (user == null || !InspectToken(token, 1, (p,n) => n >= IntPtr.Size &&
                        user.Equals(new SecurityIdentifier(Marshal.ReadIntPtr(p)))) ||
                        !InspectToken(token, 20, (p,n) => n == sizeof(int) && Marshal.ReadInt32(p) == 0)) return false;
                    return InspectToken(token, 2, (p,n) => {
                        var count = Marshal.ReadInt32(p);
                        var offset = IntPtr.Size;
                        var stride = Marshal.SizeOf<Group>();
                        if (count < 0 || count > (n-offset)/stride) return false;
                        for (int i=0; i<count; i++) {
                            var group=Marshal.PtrToStructure<Group>(p+offset+i*stride);
                            if ((group.Flags & 4) != 0 && (group.Flags & 16) == 0 && IsWellKnownSid(group.Sid,26)) return false;
                        }
                        return true;
                    });
                }
                finally { CloseHandle(token); }
            }
            finally { CloseHandle(handle); }
        }
        catch { return false; }
    }
    internal static bool Available()
    {
        // Process identity is fixed at startup. Session and input-desktop
        // availability are still queried for each operation below.
        if (!Environment.UserInteractive || ProcessIdentity.Session == 0 || !ProcessIdentity.Unprivileged) return false;
        int session = ProcessIdentity.Session;
        // A locked Windows 11 console can remain WTSActive and its user
        // process can still see Default. Require explicit OS unlock as well.
        if (!WTSQuerySessionInformation(IntPtr.Zero, session, 25, out var state, out int bytes)) return false; // WTSSessionInfoEx
        try
        {
            // WTSINFOEXW.Data is 8-byte aligned on the win-x64 guest. Read
            // only the level-1 SessionId, SessionState and SessionFlags prefix.
            if (state == IntPtr.Zero || bytes < 20 || Marshal.ReadInt32(state) != 1 ||
                Marshal.ReadInt32(state, 8) != session || Marshal.ReadInt32(state, 12) != 0 ||
                Marshal.ReadInt32(state, 16) != 1) return false; // WTS_SESSIONSTATE_UNLOCK
        }
        finally { WTSFreeMemory(state); }
        var desktop = OpenInputDesktop(0, false, 1);
        if (desktop == IntPtr.Zero) return false;
        try { var name = new char[256]; return GetUserObjectInformation(desktop, 2, name, name.Length * 2, out _) && new string(name).TrimEnd('\0') == "Default"; }
        finally { CloseDesktop(desktop); }
    }
    internal static void Assert() { if (!Available()) throw new InvalidOperationException("requires_gui_login"); }
    internal static void Target(Guid expected, string sid)
    {
        using var identity = WindowsIdentity.GetCurrent();
        if (identity.User?.Value != sid || !sid.StartsWith("S-1-5-21-", StringComparison.Ordinal)) throw new InvalidOperationException("gui_identity_mismatch");
        using var key = Registry.LocalMachine.OpenSubKey(@"SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters");
        if (!Guid.TryParse(key?.GetValue("VirtualMachineId") as string, out var actual) || actual != expected || actual == Guid.Empty)
            throw new InvalidOperationException("guest_identity_mismatch");
        Assert();
    }
}

internal static class Pairing
{
    [StructLayout(LayoutKind.Sequential)] struct Blob { internal int Size; internal IntPtr Data; }
    [DllImport("crypt32.dll", SetLastError = true, CharSet = CharSet.Unicode)] static extern bool CryptProtectData(ref Blob input, string? description, ref Blob entropy, IntPtr reserved, IntPtr prompt, uint flags, out Blob output);
    [DllImport("crypt32.dll", SetLastError = true, CharSet = CharSet.Unicode)] static extern bool CryptUnprotectData(ref Blob input, IntPtr description, ref Blob entropy, IntPtr reserved, IntPtr prompt, uint flags, out Blob output);
    [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr memory);

    internal static byte[] Crypt(byte[] data, Guid vm, string sid, bool protect)
    {
        var entropyBytes = SHA256.HashData(Protocol.Utf8.GetBytes("Cage.HyperV.v1:" + vm.ToString("D") + ":" + sid));
        var input = new Blob { Size = data.Length, Data = Marshal.AllocHGlobal(data.Length) };
        var entropy = new Blob { Size = entropyBytes.Length, Data = Marshal.AllocHGlobal(entropyBytes.Length) };
        Blob output = default;
        try
        {
            Marshal.Copy(data, 0, input.Data, data.Length); Marshal.Copy(entropyBytes, 0, entropy.Data, entropyBytes.Length);
            var ok = protect ? CryptProtectData(ref input, "Cage guest pairing", ref entropy, IntPtr.Zero, IntPtr.Zero, 1, out output)
                : CryptUnprotectData(ref input, IntPtr.Zero, ref entropy, IntPtr.Zero, IntPtr.Zero, 1, out output);
            if (!ok) throw new Win32Exception(Marshal.GetLastWin32Error(), "pairing_unavailable");
            var result = new byte[output.Size]; Marshal.Copy(output.Data, result, 0, result.Length); return result;
        }
        finally { if (output.Data != IntPtr.Zero) LocalFree(output.Data); Marshal.FreeHGlobal(input.Data); Marshal.FreeHGlobal(entropy.Data); }
    }

    internal static void PrivateFile(string path)
    {
        var file = new FileInfo(path);
        if (!file.Exists || file.Length is < 1 or > 16384 || file.Attributes.HasFlag(FileAttributes.ReparsePoint)) throw new InvalidDataException("pairing_file_invalid");
        using var identity = WindowsIdentity.GetCurrent();
        var allowed = new HashSet<string> { identity.User!.Value, "S-1-5-18", "S-1-5-32-544" };
        var acl = file.GetAccessControl();
        if (!acl.AreAccessRulesProtected) throw new InvalidDataException("pairing_permissions_invalid");
        foreach (FileSystemAccessRule rule in acl.GetAccessRules(true, true, typeof(SecurityIdentifier)))
            if (rule.AccessControlType == AccessControlType.Allow && !allowed.Contains(rule.IdentityReference.Value)) throw new InvalidDataException("pairing_permissions_invalid");
    }

    internal static byte[] Load(string path, Guid vm, string sid, bool encrypted)
    {
        PrivateFile(path);
        var bytes = File.ReadAllBytes(path);
        var secret = encrypted ? Crypt(bytes, vm, sid, false) : bytes;
        if (secret.Length != 32) throw new InvalidDataException("pairing_invalid");
        return secret;
    }

    internal static void Protect(string source, string destination, Guid vm, string sid)
    {
        PrivateFile(source);
        var secret = File.ReadAllBytes(source); if (secret.Length != 32) throw new InvalidDataException("pairing_invalid");
        try
        {
            var encrypted = Crypt(secret, vm, sid, true);
            using var file = new FileStream(destination, FileMode.CreateNew, FileAccess.Write, FileShare.None);
            var acl = new FileSecurity(); acl.SetAccessRuleProtection(true, false);
            using var identity = WindowsIdentity.GetCurrent();
            acl.SetOwner(identity.User!);
            foreach (var owner in new[] { identity.User!.Value, "S-1-5-18", "S-1-5-32-544" })
                acl.AddAccessRule(new FileSystemAccessRule(new SecurityIdentifier(owner), FileSystemRights.FullControl, AccessControlType.Allow));
            new FileInfo(destination).SetAccessControl(acl);
            file.Write(encrypted); file.Flush(true);
        }
        finally { CryptographicOperations.ZeroMemory(secret); }
    }
}
