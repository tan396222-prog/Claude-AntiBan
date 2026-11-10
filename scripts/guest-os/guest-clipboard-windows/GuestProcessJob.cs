using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;

namespace Cage.GuestClipboard;

// Attach before the controller sends its first request. Chrome and editor
// children then inherit this non-inheritable, kill-on-owner-exit job.
internal sealed class GuestProcessJob : IDisposable
{
    private IntPtr handle;
    internal GuestProcessJob(Process process)
    {
        handle=CreateJobObject(IntPtr.Zero,null);
        if(handle==IntPtr.Zero)throw new Win32Exception();
        try
        {
            var limits=new Limits();limits.Basic.Flags=0x2000;
            if(!SetInformationJobObject(handle,9,ref limits,(uint)Marshal.SizeOf<Limits>())||!AssignProcessToJobObject(handle,process.Handle))throw new Win32Exception();
        }
        catch{Dispose();throw;}
    }
    public void Dispose(){var job=Interlocked.Exchange(ref handle,IntPtr.Zero);if(job!=IntPtr.Zero)CloseHandle(job);}
    [StructLayout(LayoutKind.Sequential)]private struct Basic{internal long ProcessTime,JobTime;internal uint Flags;internal nuint MinWorking,MaxWorking;internal uint Active;internal nuint Affinity;internal uint Priority,Scheduling;}
    [StructLayout(LayoutKind.Sequential)]private struct IO{internal ulong ReadOperations,WriteOperations,OtherOperations,ReadBytes,WriteBytes,OtherBytes;}
    [StructLayout(LayoutKind.Sequential)]private struct Limits{internal Basic Basic;internal IO IO;internal nuint ProcessMemory,JobMemory,PeakProcessMemory,PeakJobMemory;}
    [DllImport("kernel32.dll",CharSet=CharSet.Unicode,EntryPoint="CreateJobObjectW",SetLastError=true)]private static extern IntPtr CreateJobObject(IntPtr attributes,string? name);
    [DllImport("kernel32.dll",SetLastError=true)]private static extern bool SetInformationJobObject(IntPtr job,int type,ref Limits limits,uint size);
    [DllImport("kernel32.dll",SetLastError=true)]private static extern bool AssignProcessToJobObject(IntPtr job,IntPtr process);
    [DllImport("kernel32.dll")]private static extern bool CloseHandle(IntPtr handle);
}
