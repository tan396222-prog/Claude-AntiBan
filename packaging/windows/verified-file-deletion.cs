using System;
using System.ComponentModel;
using System.IO;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;

public static class CageVerifiedFile
{
    [StructLayout(LayoutKind.Sequential)] private struct UnicodeString { public ushort Length, MaximumLength; public IntPtr Buffer; }
    [StructLayout(LayoutKind.Sequential)] private struct ObjectAttributes
    {
        public int Length;
        public IntPtr RootDirectory, ObjectName;
        public uint Attributes;
        public IntPtr SecurityDescriptor, SecurityQualityOfService;
    }
    [StructLayout(LayoutKind.Sequential)] private struct IOStatus { public IntPtr Status, Information; }
    [StructLayout(LayoutKind.Sequential)] private struct AttributeTag { public uint Attributes, Tag; }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern SafeFileHandle CreateFile(string path, uint access, uint share, IntPtr security, uint disposition, uint flags, IntPtr template);
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool GetFileInformationByHandleEx(SafeFileHandle file, int kind, out AttributeTag info, uint size);
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool SetFileInformationByHandle(SafeFileHandle file, int kind, ref byte delete, uint size);
    [DllImport("ntdll.dll")]
    private static extern int NtCreateFile(out SafeFileHandle file, uint access, ref ObjectAttributes attributes,
        out IOStatus status, IntPtr allocationSize, uint fileAttributes, uint share, uint disposition, uint options, IntPtr ea, uint eaLength);
    [DllImport("ntdll.dll")] private static extern uint RtlNtStatusToDosError(int status);

    public static FileStream Open(string root, string relative)
    {
        if (String.IsNullOrEmpty(relative) || Path.IsPathRooted(relative) || relative.IndexOf(':') >= 0)
            throw new IOException("Invalid relative installation path");
        foreach (string part in relative.Replace('/', '\\').Split('\\'))
            if (part == ".." || part == "." || part.Length == 0) throw new IOException("Invalid relative installation path");

        // The directory handle anchors the lookup. OBJ_DONT_REPARSE also
        // refuses a junction swapped into any ancestor of the relative path.
        using (SafeFileHandle directory = CreateFile(root, 0x80000000, 7, IntPtr.Zero, 3, 0x02200000, IntPtr.Zero))
        {
            if (directory.IsInvalid) throw new Win32Exception(Marshal.GetLastWin32Error());
            AttributeTag tag;
            if (!GetFileInformationByHandleEx(directory, 9, out tag, 8)) throw new Win32Exception(Marshal.GetLastWin32Error());
            if ((tag.Attributes & 0x400) != 0) throw new IOException("Installation directory is a reparse point");
            IntPtr text = Marshal.StringToHGlobalUni(relative.Replace('/', '\\'));
            IntPtr name = Marshal.AllocHGlobal(Marshal.SizeOf(typeof(UnicodeString)));
            try
            {
                UnicodeString value = new UnicodeString { Length = checked((ushort)(relative.Length * 2)), MaximumLength = checked((ushort)(relative.Length * 2)), Buffer = text };
                Marshal.StructureToPtr(value, name, false);
                ObjectAttributes attributes = new ObjectAttributes {
                    Length = Marshal.SizeOf(typeof(ObjectAttributes)), RootDirectory = directory.DangerousGetHandle(), ObjectName = name, Attributes = 0x1040,
                };
                IOStatus status;
                SafeFileHandle file;
                // GENERIC_READ | DELETE | SYNCHRONIZE, sharing read/delete
                // but never writes while the verified handle is held.
                int result = NtCreateFile(out file, 0x80110000, ref attributes, out status, IntPtr.Zero, 0, 5, 1, 0x00200060, IntPtr.Zero, 0);
                if (result < 0) { if (file != null) file.Dispose(); throw new Win32Exception((int)RtlNtStatusToDosError(result)); }
                try
                {
                    if (!GetFileInformationByHandleEx(file, 9, out tag, 8)) throw new Win32Exception(Marshal.GetLastWin32Error());
                    if ((tag.Attributes & 0x410) != 0) throw new IOException("Installation file is a directory or reparse point");
                    return new FileStream(file, FileAccess.Read);
                }
                catch { file.Dispose(); throw; }
            }
            finally { Marshal.FreeHGlobal(name); Marshal.FreeHGlobal(text); }
        }
    }

    public static void Delete(FileStream file)
    {
        byte delete = 1;
        if (!SetFileInformationByHandle(file.SafeFileHandle, 4, ref delete, 1)) throw new Win32Exception(Marshal.GetLastWin32Error());
    }
}
