using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;

namespace Cage.Windows;

internal static class Authenticode
{
    public static string Publisher(string path)
    {
        UpdatePolicy.PlainPath(path);
        var file = new TrustFile { Size = (uint)Marshal.SizeOf<TrustFile>(), Path = path };
        var memory = Marshal.AllocHGlobal(Marshal.SizeOf<TrustFile>());
        try
        {
            Marshal.StructureToPtr(file, memory, false);
            var data = new TrustData {
                Size = (uint)Marshal.SizeOf<TrustData>(), UiChoice = 2, UnionChoice = 1,
                File = memory, StateAction = 1,
                // Verification must not make a direct network request around the
                // selected Cage proxy. Windows uses its existing trust cache.
                ProviderFlags = 0x1000,
            };
            var action = new Guid("00AAC56B-CD44-11D0-8CC2-00C04FC295EE");
            var result = WinVerifyTrust(new IntPtr(-1), ref action, ref data);
            data.StateAction = 2; _ = WinVerifyTrust(new IntPtr(-1), ref action, ref data);
            if (result != 0) throw new IOException("Windows 无法验证安装包签名；原应用已保留。请确认系统证书信任状态。");
#pragma warning disable SYSLIB0057
            using var signed = X509Certificate.CreateFromSignedFile(path);
            using var certificate = new X509Certificate2(signed);
#pragma warning restore SYSLIB0057
            return Convert.ToHexString(SHA256.HashData(certificate.PublicKey.ExportSubjectPublicKeyInfo()));
        }
        finally { Marshal.DestroyStructure<TrustFile>(memory); Marshal.FreeHGlobal(memory); }
    }

    public static void SamePublisher(string path, string expected)
    {
        if (!string.Equals(Publisher(path), expected, StringComparison.Ordinal))
            throw new IOException("更新包的发布者签名与当前 Cage 不一致");
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct TrustFile { public uint Size; [MarshalAs(UnmanagedType.LPWStr)] public string Path; public IntPtr Handle; public IntPtr KnownSubject; }
    [StructLayout(LayoutKind.Sequential)]
    private struct TrustData {
        public uint Size; public IntPtr PolicyCallback, SipClient;
        public uint UiChoice, RevocationChecks, UnionChoice; public IntPtr File;
        public uint StateAction; public IntPtr StateData, UrlReference;
        public uint ProviderFlags, UiContext; public IntPtr SignatureSettings;
    }
    [DllImport("wintrust.dll", ExactSpelling = true, PreserveSig = true)]
    private static extern int WinVerifyTrust(IntPtr window, ref Guid action, ref TrustData data);
}
