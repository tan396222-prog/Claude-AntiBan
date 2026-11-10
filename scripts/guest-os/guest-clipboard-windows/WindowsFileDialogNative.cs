using System.Diagnostics;
using System.Collections.Concurrent;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;

namespace Cage.GuestClipboard;

// A private capability created from ClaudeWindowRpc's verified registration.
// No process, handle, path, or authorization predicate comes from JSON.
internal static class WindowsFileDialogNative
{
    internal sealed record Owner(int PID,long Started,int Session,IntPtr Window,string Executable,Func<Process,bool> Authorized)
    {
        internal void Assert()
        {
            using var process=Process.GetProcessById(PID);
            if(process.HasExited||process.StartTime.ToUniversalTime().Ticks!=Started||process.SessionId!=Session||
                !string.Equals(process.MainModule?.FileName,Executable,StringComparison.OrdinalIgnoreCase)||!Authorized(process)||
                !IsWindowVisible(Window)||GetWindowThreadProcessId(Window,out var pid)==0||pid!=PID)
                throw new InvalidDataException("file_dialog_owner_changed");
        }
    }
    private static readonly string Property="Cage.FileDialog."+Guid.NewGuid().ToString("N");
    private static readonly ConcurrentDictionary<IntPtr,Observer> Observers=new();
    [UnmanagedFunctionPointer(CallingConvention.Winapi)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private delegate bool WindowCallback(IntPtr window, IntPtr parameter);
    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool EnumWindows(WindowCallback callback,IntPtr parameter);
    [DllImport("user32.dll")] private static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr window,out uint pid);
    [DllImport("user32.dll")] private static extern IntPtr GetWindow(IntPtr window,uint kind);
    [DllImport("user32.dll")] private static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll",CharSet=CharSet.Unicode,ExactSpelling=true)] private static extern int GetClassNameW(IntPtr window,StringBuilder value,int length);
    [DllImport("user32.dll",CharSet=CharSet.Unicode,ExactSpelling=true,SetLastError=true)] private static extern bool SetPropW(IntPtr window,string name,IntPtr value);
    [DllImport("user32.dll",CharSet=CharSet.Unicode,ExactSpelling=true)] private static extern IntPtr GetPropW(IntPtr window,string name);
    [DllImport("user32.dll",CharSet=CharSet.Unicode,ExactSpelling=true)] private static extern IntPtr RemovePropW(IntPtr window,string name);
    [DllImport("user32.dll")] internal static extern int GetDlgCtrlID(IntPtr window);
    [DllImport("user32.dll")] internal static extern bool IsChild(IntPtr parent,IntPtr child);
    [DllImport("user32.dll")] private static extern IntPtr GetDlgItem(IntPtr dialog,int id);
    [DllImport("user32.dll")] private static extern bool IsWindowEnabled(IntPtr window);
    [DllImport("user32.dll",ExactSpelling=true)] private static extern bool PostMessageW(IntPtr window,uint message,IntPtr parameter,IntPtr data);
    [DllImport("user32.dll",CharSet=CharSet.Unicode,ExactSpelling=true)] private static extern IntPtr SendMessageTimeoutW(IntPtr window,uint message,IntPtr parameter,string data,uint flags,uint timeout,out IntPtr result);
    [DllImport("user32.dll",CharSet=CharSet.Unicode,ExactSpelling=true)] private static extern IntPtr SendMessageTimeoutW(IntPtr window,uint message,IntPtr parameter,StringBuilder data,uint flags,uint timeout,out IntPtr result);
    [DllImport("user32.dll",CharSet=CharSet.Unicode,ExactSpelling=true)] private static extern IntPtr SendMessageTimeoutW(IntPtr window,uint message,IntPtr parameter,IntPtr data,uint flags,uint timeout,out IntPtr result);
    private delegate void EventCallback(IntPtr hook,uint kind,IntPtr window,int objectID,int childID,uint thread,uint time);
    [DllImport("user32.dll")] private static extern IntPtr SetWinEventHook(uint first,uint last,IntPtr module,EventCallback callback,uint process,uint thread,uint flags);
    [DllImport("user32.dll")] private static extern bool UnhookWinEvent(IntPtr hook);
    // Install on the GUI message thread. Keep the delegate rooted until the
    // hook is removed; the callback only retires broker state, never clicks UI.
    internal sealed class Observer : IDisposable
    {
        private IntPtr hook;
        private readonly EventCallback callback;
        private readonly int thread=Environment.CurrentManagedThreadId;
        internal Observer(Owner owner,Action<IntPtr> destroyed)
        {
            owner.Assert();
            callback=(_,kind,window,objectID,childID,_,_)=>{if(kind==0x8001&&objectID==0&&childID==0)destroyed(window);};
            hook=SetWinEventHook(0x8001,0x8001,IntPtr.Zero,callback,(uint)owner.PID,0,0);
            if(hook==IntPtr.Zero)throw new InvalidDataException("file_dialog_observer_unavailable");
            Observers[hook]=this; // A failed unhook cannot release the native delegate.
        }
        public void Dispose()
        {
            if(hook==IntPtr.Zero)return;
            if(Environment.CurrentManagedThreadId!=thread||!UnhookWinEvent(hook))throw new InvalidDataException("file_dialog_observer_cleanup_failed");
            Observers.TryRemove(hook,out _);hook=IntPtr.Zero;GC.KeepAlive(callback);
        }
    }

    internal static bool Owned(Owner owner,IntPtr dialog)
    {
        if(!IsWindowVisible(dialog)||GetWindowThreadProcessId(dialog,out var pid)==0||pid!=owner.PID)return false;
        var name=new StringBuilder(128);GetClassNameW(dialog,name,name.Capacity);if(name.ToString()!="#32770")return false;
        var seen=new HashSet<IntPtr>();var parent=GetWindow(dialog,4);
        while(parent!=IntPtr.Zero&&seen.Add(parent)&&seen.Count<=8){if(parent==owner.Window)return true;parent=GetWindow(parent,4);}
        return false;
    }
    internal static IntPtr[] Candidates(Owner owner)
    {
        owner.Assert();var result=new List<IntPtr>();
        EnumWindows((window,_)=>{if(Owned(owner,window))result.Add(window);return true;},IntPtr.Zero);
        owner.Assert();return result.ToArray();
    }
    internal static IntPtr Stamp(Owner owner,IntPtr dialog)
    {
        owner.Assert();if(!Owned(owner,dialog))throw new InvalidDataException("file_dialog_owner_changed");
        var bytes=RandomNumberGenerator.GetBytes(8);
        var stamp=new IntPtr((long)(BitConverter.ToUInt64(bytes)&0x7fffffffffffffffUL));if(stamp==IntPtr.Zero)stamp=new IntPtr(1);
        if(!SetPropW(dialog,Property,stamp))throw new InvalidDataException("file_dialog_binding_unavailable");
        try{owner.Assert();if(!Owned(owner,dialog)||GetPropW(dialog,Property)!=stamp)throw new InvalidDataException("file_dialog_owner_changed");return stamp;}
        catch{Remove(dialog,stamp);throw;}
    }
    internal static bool Alive(Owner owner,IntPtr dialog,IntPtr stamp)
    {
        try{owner.Assert();return Owned(owner,dialog)&&GetPropW(dialog,Property)==stamp;}
        catch{return false;}
    }
    internal static void Assert(Owner owner,IntPtr dialog,IntPtr stamp,bool foreground)
    {
        if(!Alive(owner,dialog,stamp)||foreground&&GetForegroundWindow()!=dialog)throw new InvalidDataException("file_dialog_changed");
    }
    internal static void Remove(IntPtr dialog,IntPtr stamp)
    {if(GetPropW(dialog,Property)==stamp)RemovePropW(dialog,Property);}

    internal static void Accept(Owner owner,IntPtr dialog,IntPtr stamp)
    {
        Assert(owner,dialog,stamp,true);
        var button=GetDlgItem(dialog,1);
        var name=new StringBuilder(128);GetClassNameW(button,name,name.Capacity);
        if(button==IntPtr.Zero||!IsChild(dialog,button)||!IsWindowEnabled(button)||name.ToString()!="Button"||
            GetWindowThreadProcessId(button,out var pid)==0||pid!=owner.PID||
            !PostMessageW(button,0x00F5,IntPtr.Zero,IntPtr.Zero))throw new InvalidDataException("file_dialog_selection_invalid");
    }

    internal static void SetFilename(Owner owner,IntPtr dialog,IntPtr stamp,IntPtr field,string text)
    {
        Assert(owner,dialog,stamp,true);
        var name=new StringBuilder(128);GetClassNameW(field,name,name.Capacity);
        if(text.Length>1<<20||field==IntPtr.Zero||!IsChild(dialog,field)||name.ToString()!="Edit"||
            GetWindowThreadProcessId(field,out var pid)==0||pid!=owner.PID)
            throw new InvalidDataException("file_dialog_selection_invalid");
        // UIA ValuePattern rejects a multi-file value beyond the Edit control's
        // typing limit. WM_SETTEXT supports the native selection buffer.
        if(SendMessageTimeoutW(field,0x000C,IntPtr.Zero,text,0x22,2000,out var written)==IntPtr.Zero||written==IntPtr.Zero)
            throw new InvalidDataException("file_dialog_input_failed");
        Assert(owner,dialog,stamp,true);
        if(SendMessageTimeoutW(field,0x000E,IntPtr.Zero,IntPtr.Zero,0x22,2000,out var textLength)==IntPtr.Zero||textLength.ToInt64()!=text.Length)
            throw new InvalidDataException("file_dialog_selection_invalid");
        var actual=new StringBuilder(text.Length+1);
        if(SendMessageTimeoutW(field,0x000D,new IntPtr(actual.Capacity),actual,0x22,2000,out var length)==IntPtr.Zero||
            length.ToInt64()!=text.Length||actual.ToString()!=text)
            throw new InvalidDataException("file_dialog_selection_invalid");
        Assert(owner,dialog,stamp,true);
    }
}
