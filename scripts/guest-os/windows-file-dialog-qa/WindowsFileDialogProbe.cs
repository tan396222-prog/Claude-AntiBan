using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Security.Principal;
using System.Text;
using System.Text.Json;
using System.Windows.Automation;
using NativeDialog=Cage.GuestClipboard.WindowsFileDialogNative;

namespace Cage.WindowsFileDialogQA;

internal static class WindowsFileDialogProbe
{
    [UnmanagedFunctionPointer(CallingConvention.Winapi)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private delegate bool WindowCallback(IntPtr window, IntPtr data);
    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool EnumWindows(WindowCallback callback,IntPtr data);
    [DllImport("user32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool EnumChildWindows(IntPtr parent,WindowCallback callback,IntPtr data);
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr window,out uint pid);
    [DllImport("user32.dll")] private static extern IntPtr GetWindow(IntPtr window,uint command);
    [DllImport("user32.dll",CharSet=CharSet.Unicode,ExactSpelling=true)] private static extern int GetClassNameW(IntPtr window,StringBuilder value,int length);
    [DllImport("user32.dll")] private static extern int GetDlgCtrlID(IntPtr window);
    [DllImport("user32.dll",ExactSpelling=true)] private static extern bool PostMessageW(IntPtr window,uint message,IntPtr parameter,IntPtr data);
    [DllImport("user32.dll")] private static extern IntPtr OpenInputDesktop(uint flags,bool inherit,uint access);
    [DllImport("user32.dll")] private static extern bool CloseDesktop(IntPtr desktop);
    [DllImport("user32.dll",CharSet=CharSet.Unicode,ExactSpelling=true)] private static extern bool GetUserObjectInformationW(IntPtr handle,int index,StringBuilder value,int bytes,out int needed);
    private static readonly string[] Modes=["open-single","open-multi","save","folder"];
    private static readonly object ProgressLock=new();
    private static string? ProgressPath;
    private static void Progress(string stage,string mode="",object? detail=null)
    {
        lock(ProgressLock)
        {
            var line=JsonSerializer.Serialize(new {utc=DateTime.UtcNow,stage,mode,detail});
            Console.WriteLine(line);
            if(ProgressPath!=null)File.AppendAllText(ProgressPath,line+Environment.NewLine,new UTF8Encoding(false));
        }
    }
    [STAThread] private static int Main(string[] args)
    {
        Console.WriteLine("Native dialog probe bootstrap.");
        try
        {
            if(args is not ["probe",var output])throw new InvalidDataException("usage_probe_private_output");
            using var identity=WindowsIdentity.GetCurrent();
            if(!Environment.UserInteractive||Process.GetCurrentProcess().SessionId==0||new WindowsPrincipal(identity).IsInRole(WindowsBuiltInRole.Administrator))throw new InvalidDataException("requires_ordinary_gui_user");
            var desktop=OpenInputDesktop(0,false,1);if(desktop==IntPtr.Zero)throw new InvalidDataException("requires_unlocked_gui");
            try{var name=new StringBuilder(256);if(!GetUserObjectInformationW(desktop,2,name,name.Capacity*2,out _)||name.ToString()!="Default")throw new InvalidDataException("requires_unlocked_gui");}finally{CloseDesktop(desktop);}
            var home=Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
            output=Path.GetFullPath(output);
            if(!output.StartsWith(home+Path.DirectorySeparatorChar,StringComparison.OrdinalIgnoreCase))throw new InvalidDataException("output_requires_user_directory");
            Directory.CreateDirectory(Path.GetDirectoryName(output)!);
            ProgressPath=output+".progress.jsonl";
            Progress("start",detail:new {pid=Environment.ProcessId,session=Process.GetCurrentProcess().SessionId});
            var files=Path.Combine(Path.GetDirectoryName(output)!,"input-"+Guid.NewGuid().ToString("N"));Directory.CreateDirectory(files);
            File.WriteAllText(Path.Combine(files,"one 中文.txt"),"own dialog fixture");
            File.WriteAllText(Path.Combine(files,"two.txt"),"own second fixture");
            var results=new List<object>();Exception? failure=null;
            Application.SetHighDpiMode(HighDpiMode.PerMonitorV2);
            using var owner=new Form {Text="Cage file dialog QA",Width=400,Height=240,StartPosition=FormStartPosition.CenterScreen};
            owner.Shown+=async (_,_)=>
            {
                try
                {
                    Progress("owner-shown");
                    using var current=Process.GetCurrentProcess();
                    var nativeOwner=new NativeDialog.Owner(current.Id,current.StartTime.ToUniversalTime().Ticks,current.SessionId,owner.Handle,current.MainModule!.FileName!,
                        process=>process.Id==Environment.ProcessId&&process.SessionId==current.SessionId);
                    foreach(var mode in Modes)
                    {
                        Progress("mode-start",mode);
                        using var watchdog=new System.Threading.Timer(_=>
                        {
                            Progress("mode-timeout",mode);
                            // This is the standalone QA process. Exiting closes only
                            // its own synthetic windows if an automation provider stalls.
                            Environment.Exit(2);
                        },null,TimeSpan.FromSeconds(20),Timeout.InfiniteTimeSpan);
                        var destroyed=new HashSet<IntPtr>();
                        using var observer=new NativeDialog.Observer(nativeOwner,window=>destroyed.Add(window));
                        Progress("observer-ready",mode);
                        var inspection=Inspect(nativeOwner,mode);
                        CommonDialog dialog=mode switch
                        {
                            "folder"=>new FolderBrowserDialog {InitialDirectory=files,AutoUpgradeEnabled=true},
                            "save"=>new SaveFileDialog {InitialDirectory=files,FileName="own-new.txt",AutoUpgradeEnabled=true,RestoreDirectory=true,Title="Cage QA save"},
                            _=>new OpenFileDialog {InitialDirectory=files,Multiselect=mode=="open-multi",AutoUpgradeEnabled=true,RestoreDirectory=true,Title="Cage QA open"},
                        };
                        using(dialog)
                        {
                            Progress("dialog-showing",mode);
                            var result=dialog.ShowDialog(owner);
                            Progress("dialog-returned",mode,new {result=result.ToString()});
                            var observation=await inspection;
                            results.Add(new {mode,result=result.ToString(),controls=observation.Controls,bindingValidated=observation.BindingValidated,foregroundValidated=observation.ForegroundValidated,retiredBindingRejected=observation.RetiredBindingRejected,destroyObserved=destroyed.Contains(observation.Handle)});
                            Progress("mode-complete",mode);
                        }
                    }
                    File.WriteAllText(output,JsonSerializer.Serialize(new {os=Environment.OSVersion.Version.ToString(),culture=System.Globalization.CultureInfo.CurrentUICulture.Name,results},new JsonSerializerOptions {WriteIndented=true}),new UTF8Encoding(false));
                }
                catch(Exception error){Progress("failure",detail:new {type=error.GetType().Name,code=error.Message});failure=error;}
                finally{owner.Close();}
            };
            try { Application.Run(owner); }
            finally { Directory.Delete(files, true); }
            if(failure!=null)throw failure;
            Console.WriteLine("Owned native file dialog probe completed.");return 0;
        }
        catch(Exception error){Console.Error.WriteLine("Native dialog probe failed: "+error);return 1;}
    }
    private sealed record Observation(IntPtr Handle,List<object> Controls,bool BindingValidated,bool ForegroundValidated,bool RetiredBindingRejected);
    private static Task<Observation> Inspect(NativeDialog.Owner owner,string mode)=>Task.Run(async()=>
    {
        Progress("inspection-start",mode);
        try{return await InspectDialog(owner,mode);}
        catch(Exception error)
        {
            Progress("inspection-failed",mode,new {type=error.GetType().Name,code=error.Message});
            // A failure before stamping must also close the owned modal dialog.
            EnumWindows((window,_)=>{if(NativeDialog.Owned(owner,window))PostMessageW(window,0x0111,new IntPtr(2),IntPtr.Zero);return true;},IntPtr.Zero);
            throw;
        }
    });
    private static async Task<Observation> InspectDialog(NativeDialog.Owner owner,string mode)
    {
        IntPtr dialog=IntPtr.Zero;var deadline=DateTime.UtcNow.AddSeconds(8);
        while(dialog==IntPtr.Zero&&DateTime.UtcNow<deadline)
        {
            var candidates=NativeDialog.Candidates(owner);if(candidates.Length>1)throw new InvalidDataException("ambiguous_owned_dialog");
            if(candidates.Length==1)dialog=candidates[0];
            if(dialog==IntPtr.Zero)await Task.Delay(10);
        }
        if(dialog==IntPtr.Zero)throw new InvalidDataException("owned_common_dialog_missing");
        Progress("dialog-found",mode);
        var nativeControls=new List<object>();
        EnumChildWindows(dialog,(window,_)=>
        {
            if(GetWindowThreadProcessId(window,out var pid)!=0&&pid==owner.PID)
            {
                var name=new StringBuilder(128);GetClassNameW(window,name,name.Capacity);
                nativeControls.Add(new {nativeId=GetDlgCtrlID(window),className=name.ToString()});
            }
            return nativeControls.Count<4096;
        },IntPtr.Zero);
        Progress("native-controls",mode,nativeControls);
        var stamp=NativeDialog.Stamp(owner,dialog);var foreground=false;var retired=false;
        try
        {
            await Task.Delay(250);
            NativeDialog.Assert(owner,dialog,stamp,false);
            try{NativeDialog.Assert(owner,dialog,stamp,true);foreground=true;}catch(InvalidDataException){ }
            var root=AutomationElement.FromHandle(dialog);
            var controls=new List<object>();
            Progress("uia-enumerating",mode);
            var elements=root.FindAll(TreeScope.Descendants,Condition.TrueCondition);
            Progress("uia-enumerated",mode,new {count=elements.Count});
            if(elements.Count>4096)throw new InvalidDataException("owned_dialog_control_limit");
            foreach(AutomationElement element in elements)
            {
                var current=element.Current;var type=current.ControlType;
                if(type==ControlType.ListItem||type==ControlType.TreeItem||type==ControlType.DataItem)continue;
                bool value=element.TryGetCurrentPattern(ValuePattern.Pattern,out _),invoke=element.TryGetCurrentPattern(InvokePattern.Pattern,out _);
                bool? multi=null;if(element.TryGetCurrentPattern(SelectionPattern.Pattern,out var pattern))multi=((SelectionPattern)pattern).Current.CanSelectMultiple;
                var nativeId=current.NativeWindowHandle==0?0:GetDlgCtrlID(new IntPtr(current.NativeWindowHandle));
                // Only stock dialog commands and filename controls retain
                // labels. Other buttons can contain breadcrumb paths. This
                // observation filter is not a production mode classifier.
                var name=nativeId is 1 or 2 or 1148 or 1152?current.Name:"";
                controls.Add(new {automationId=current.AutomationId,nativeId,className=current.ClassName,type=type.ProgrammaticName,enabled=current.IsEnabled,offscreen=current.IsOffscreen,name,value,invoke,multi});
            }
            NativeDialog.Assert(owner,dialog,stamp,false);
            Progress("uia-controls",mode,controls);
            NativeDialog.Remove(dialog,stamp);
            try{NativeDialog.Assert(owner,dialog,stamp,false);}catch(InvalidDataException){retired=true;}
            if(!retired)throw new InvalidDataException("retired_dialog_binding_accepted");
            return new Observation(dialog,controls,true,foreground,retired);
        }
        finally{NativeDialog.Remove(dialog,stamp);if(NativeDialog.Owned(owner,dialog))PostMessageW(dialog,0x0111,new IntPtr(2),IntPtr.Zero);}
    }
}
