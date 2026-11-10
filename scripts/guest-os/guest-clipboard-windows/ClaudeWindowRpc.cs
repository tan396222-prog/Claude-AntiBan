using System.Diagnostics;
using System.Drawing.Imaging;
using System.Runtime.CompilerServices;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text.Json.Nodes;
using System.Xml.Linq;
using System.Windows.Automation;
using Windows.Management.Deployment;

namespace Cage.GuestClipboard;

// This endpoint contains one registered Claude app window. It never captures
// the desktop or accepts a path, process ID or window handle from its client.
internal static class ClaudeWindowRpc
{
    private const string Family = "Claude_pzs8sxrjxfjjc";
    private const string Publisher = "CN=\"Anthropic, PBC\", O=\"Anthropic, PBC\", L=San Francisco, S=California, C=US, SERIALNUMBER=4860621, OID.2.5.4.15=Private Organization, OID.1.3.6.1.4.1.311.60.2.1.2=Delaware, OID.1.3.6.1.4.1.311.60.2.1.3=US";
    private static readonly SemaphoreSlim Operations = new(1, 1);
    private static readonly SemaphoreSlim Captures = new(1, 1);
    [ModuleInitializer] internal static void Register() => GuestOperations.Register("desktop", Run);

    private sealed record Registration(string Executable, string Version);
    private sealed record Window(Process Process, IntPtr Handle, Rect Bounds, string Executable);
    [StructLayout(LayoutKind.Sequential)] private struct Rect { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)] private struct Point { public int X, Y; }
    [StructLayout(LayoutKind.Sequential)] private struct Keyboard { public ushort Key, Scan; public uint Flags, Time; public UIntPtr Extra; }
    [StructLayout(LayoutKind.Sequential)] private struct Mouse { public int X, Y; public uint Data, Flags, Time; public UIntPtr Extra; }
    [StructLayout(LayoutKind.Explicit)] private struct InputData { [FieldOffset(0)] public Keyboard Keyboard; [FieldOffset(0)] public Mouse Mouse; }
    [StructLayout(LayoutKind.Sequential)] private struct Input { public uint Type; public InputData Data; }
    [DllImport("user32.dll")] private static extern short VkKeyScanW(char key);
    [DllImport("user32.dll")] private static extern bool PostMessageW(IntPtr window, uint message, IntPtr wparam, IntPtr lparam);
    [DllImport("user32.dll")] private static extern bool GetClientRect(IntPtr window, out Rect rect);
    [DllImport("user32.dll")] private static extern bool ClientToScreen(IntPtr window, ref Point point);
    [DllImport("user32.dll")] private static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] private static extern bool IsIconic(IntPtr window);
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
    [DllImport("user32.dll")] private static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] private static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")] private static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll")] private static extern bool PrintWindow(IntPtr window, IntPtr dc, uint flags);
    [DllImport("user32.dll")] private static extern uint SendInput(uint count, Input[] input, int size);
    [DllImport("user32.dll")] private static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
    [DllImport("user32.dll")] private static extern uint GetDpiForWindow(IntPtr window);
    [DllImport("user32.dll")] private static extern bool GetWindowRect(IntPtr window, out Rect rect);
    [DllImport("user32.dll")] private static extern bool GetCursorPos(out Point point);
    [DllImport("user32.dll")] private static extern IntPtr MonitorFromWindow(IntPtr window, uint flags);
    [StructLayout(LayoutKind.Sequential)] private struct MonitorInfo { public uint Size; public Rect Monitor, Work; public uint Flags; }
    [DllImport("user32.dll", EntryPoint="GetMonitorInfoW", ExactSpelling=true)] private static extern bool GetMonitorInfo(IntPtr monitor, ref MonitorInfo info);
    [DllImport("user32.dll")] private static extern bool SetWindowPos(IntPtr window, IntPtr after, int x, int y, int width, int height, uint flags);
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] private static extern int GetWindowTextW(IntPtr window,System.Text.StringBuilder text,int count);
    [DllImport("user32.dll")] private static extern IntPtr WindowFromPoint(Point point);
    [DllImport("user32.dll")] private static extern IntPtr GetAncestor(IntPtr window,uint flags);
    [ComVisible(true),Guid("00000121-0000-0000-C000-000000000046"),InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IDropSource
    {
        [PreserveSig] int QueryContinueDrag([MarshalAs(UnmanagedType.Bool)] bool escape,uint state);
        [PreserveSig] int GiveFeedback(uint effect);
    }
    [ComVisible(true),ClassInterface(ClassInterfaceType.None)]
    private sealed class DropSource(Action guard) : IDropSource
    {
        public int QueryContinueDrag(bool escape,uint state) { try { guard();return escape?0x40101:0x40100; } catch { return 0x40101; } }
        public int GiveFeedback(uint effect)=>0x40102;
    }
    [DllImport("ole32.dll")] private static extern int DoDragDrop([MarshalAs(UnmanagedType.Interface)] System.Runtime.InteropServices.ComTypes.IDataObject data,IDropSource source,uint effects,out uint effect);
    [DllImport("ole32.dll")] private static extern int OleInitialize(IntPtr reserved);
    [DllImport("ole32.dll")] private static extern void OleUninitialize();

    private static Registration? RegisteredExecutable()
    {
        var packages = new PackageManager().FindPackagesForUser("").Where(p => p.Id.FamilyName == Family).ToArray();
        if (packages.Length == 0) return null;
        if (packages.Length != 1) throw new InvalidDataException("desktop_registration_invalid");
        var p = packages[0];
        if (p.Id.Name != "Claude" || p.Id.Publisher != Publisher || p.IsDevelopmentMode || !p.Status.VerifyIsOK() ||
            p.SignatureKind is not (Windows.ApplicationModel.PackageSignatureKind.Developer or Windows.ApplicationModel.PackageSignatureKind.Store or Windows.ApplicationModel.PackageSignatureKind.Enterprise))
            throw new InvalidDataException("desktop_registration_invalid");
        var manifest = XDocument.Load(Path.Combine(p.InstalledPath, "AppxManifest.xml"));
        var app = manifest.Descendants().SingleOrDefault(e => e.Name.LocalName == "Application" && (string?)e.Attribute("Id") == "Claude");
        if (app is null || (string?)app.Attribute("Executable") != "app\\Claude.exe" || (string?)app.Attribute("EntryPoint") != "Windows.FullTrustApplication")
            throw new InvalidDataException("desktop_registration_invalid");
        var version = p.Id.Version;
        return new Registration(Path.Combine(p.InstalledPath, "app", "Claude.exe"), $"{version.Major}.{version.Minor}.{version.Build}.{version.Revision}");
    }

    private static Window Find(string executable)
    {
        var session = Process.GetCurrentProcess().SessionId;
        foreach (var p in Process.GetProcessesByName("Claude"))
        {
            try
            {
                if (p.SessionId != session || !string.Equals(p.MainModule?.FileName, executable, StringComparison.OrdinalIgnoreCase)) { p.Dispose(); continue; }
                var handle = p.MainWindowHandle;
                var w = new Window(p, handle, default, executable);
                Validate(w, false);
                if (!GetClientRect(handle, out var bounds) || bounds.Right < 1 || bounds.Bottom < 1 || (long)bounds.Right * bounds.Bottom > 40_000_000)
                    throw new InvalidDataException("desktop_size_invalid");
                return w with { Bounds = bounds };
            }
            catch { p.Dispose(); }
        }
        throw new InvalidDataException("desktop_not_running");
    }

    internal static JsonObject ComponentStatus()
    {
        try
        {
            var registration=RegisteredExecutable();
            return new JsonObject{["installed"]=registration!=null,["ready"]=registration!=null,["reason"]=registration==null?"desktop_not_installed":""};
        }
        catch{return new JsonObject{["installed"]=false,["ready"]=false,["reason"]="desktop_registration_unavailable"};}
    }

    private static void Validate(Window w, bool focus)
    {
        if (!SessionGuard.Available() || !SessionGuard.OwnsProcess(w.Process) || !IsWindowVisible(w.Handle) || IsIconic(w.Handle) ||
            GetWindowThreadProcessId(w.Handle, out var pid) == 0 || pid != w.Process.Id ||
            !string.Equals(w.Process.MainModule?.FileName, w.Executable, StringComparison.OrdinalIgnoreCase) ||
            focus && GetForegroundWindow() != w.Handle) throw new InvalidDataException("desktop_target_changed");
    }

    // WGC can capture the part of our own window outside the guest monitor,
    // whereas SendInput clamps the system pointer to its physical screen.
    // Bring only the requested part of this window into the guest work area.
    private static Point PositionPointer(Window w, int x, int y)
    {
        Validate(w,true);
        var point=new Point {X=x, Y=y};
        var monitor=new MonitorInfo {Size=(uint)Marshal.SizeOf<MonitorInfo>()};
        if (!ClientToScreen(w.Handle,ref point) || !GetMonitorInfo(MonitorFromWindow(w.Handle,2),ref monitor) ||
            monitor.Work.Right<=monitor.Work.Left || monitor.Work.Bottom<=monitor.Work.Top)
            throw new InvalidDataException("desktop_pointer_failed");
        var dx=Math.Clamp(point.X,monitor.Work.Left,monitor.Work.Right-1)-point.X;
        var dy=Math.Clamp(point.Y,monitor.Work.Top,monitor.Work.Bottom-1)-point.Y;
        if(dx!=0 || dy!=0)
        {
            if(!GetWindowRect(w.Handle,out var outer) ||
                !SetWindowPos(w.Handle,IntPtr.Zero,outer.Left+dx,outer.Top+dy,0,0,0x0001|0x0004|0x0010))
                throw new InvalidDataException("desktop_pointer_failed");
            point=new Point {X=x,Y=y};
            if(!ClientToScreen(w.Handle,ref point)) throw new InvalidDataException("desktop_pointer_failed");
        }
        Validate(w,true);
        if(!SetCursorPos(point.X,point.Y) || !GetCursorPos(out var actual) || actual.X!=point.X || actual.Y!=point.Y ||
            GetAncestor(WindowFromPoint(actual),2)!=w.Handle) throw new InvalidDataException("desktop_target_changed");
        return actual;
    }

    // A cropped WGC window can display an onboarding button beneath the
    // guest taskbar or another guest app. UI Automation invokes only the
    // authenticated app's visible button without using the global pointer.
    private static AutomationElement? WelcomeButton(Window w, int x, int y)
    {
        Validate(w,false);
        var point=new Point {X=x,Y=y};
        if(!ClientToScreen(w.Handle,ref point)) return null;
        var root=AutomationElement.FromHandle(w.Handle);
        if(root.Current.ProcessId!=w.Process.Id) return null;
        var buttons=root.FindAll(TreeScope.Descendants,new PropertyCondition(AutomationElement.ControlTypeProperty,ControlType.Button));
        if(buttons.Count>128) return null;
        AutomationElement? selected=null;
        foreach(AutomationElement element in buttons)
        {
            var current=element.Current;
            if(current.ProcessId!=w.Process.Id || !current.IsEnabled || current.IsOffscreen ||
                !current.BoundingRectangle.Contains(point.X,point.Y)) continue;
            if(selected!=null || current.Name is not ("Get started" or "开始使用" or "開始使用")) return null;
            if(!element.TryGetCurrentPattern(InvokePattern.Pattern,out _)) return null;
            selected=element;
        }
        return selected;
    }

    internal static async Task Run(Stream stream, CancellationToken token, Func<bool> available)
    {
        int mask = 0;
        AutomationElement? welcomeDown = null;
        var inputLease = NativeGuestInput.OpenLease();
        try
        {
        while (await Protocol.Read(stream, Protocol.WireLimit, token) is { } value)
        {
            await Operations.WaitAsync(token);
            var dpi = SetThreadDpiAwarenessContext(new IntPtr(-4));
            try
            {
                if (!available()) throw new InvalidDataException("requires_gui_login");
                var action = value["action"]?.GetValue<string>() ?? "";
                if (action is "files-download-open" or "files-download-read" or "files-download-close" or "files-download-retry" or "files-download-ack")
                {
                    await Protocol.Write(stream, WindowsDesktopDownloads.Read(value), token); continue;
                }
                if (action is "files-open-claim" or "files-open-complete")
                {
                    await Protocol.Write(stream, WindowsDesktopFiles.OpenAction(value["id"]?.GetValue<string>() ?? "", action == "files-open-complete"), token); continue;
                }
                var registered = RegisteredExecutable();
                var executable = registered?.Executable;
                if (action == "status")
                {
                    bool running = false; double density = 1;
                    if (executable is not null) { try { var window = Find(executable); using var owner = window.Process; density = GetDpiForWindow(window.Handle) / 96.0; running = true; } catch (InvalidDataException) { } }
                    await Protocol.Write(stream, new JsonObject { ["installed"] = executable is not null, ["running"] = running, ["version"] = registered?.Version ?? "", ["native_user_session"] = true, ["display_scale"] = density, ["phone_supported"] = true }, token); continue;
                }
                if(action=="files-cancel")
                {
                    WindowsDesktopFiles.Cancel(value["id"]?.GetValue<string>() ?? "");
                    AttachmentsRpc.CancelBatch(value["id"]?.GetValue<string>() ?? "");
                    await Protocol.Write(stream,new JsonObject{["ok"]=true},token);continue;
                }
                if(action=="files-poll" && executable is null)
                {
                    await Protocol.Write(stream,new JsonObject{["available"]=false,["uploads"]=new JsonArray(),["downloads"]=WindowsDesktopDownloads.Snapshot(),["opens"]=WindowsDesktopFiles.OpenSnapshot(),["downloadAck"]=true},token);continue;
                }
                if (executable is null) throw new InvalidDataException("desktop_not_installed");
                if (action == "start")
                {
                    Process.Start(new ProcessStartInfo(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows), "explorer.exe"), "shell:AppsFolder\\" + Family + "!Claude") { UseShellExecute = false });
                    await Protocol.Write(stream, new JsonObject { ["ok"] = true }, token); continue;
                }
                if(action=="files-poll")
                {
                    Window? found = null;
                    try { found = Find(executable); }
                    catch (InvalidDataException) { }
                    if (found is null)
                        await Protocol.Write(stream,new JsonObject{["available"]=false,["uploads"]=new JsonArray(),["downloads"]=WindowsDesktopDownloads.Snapshot(),["opens"]=WindowsDesktopFiles.OpenSnapshot(),["downloadAck"]=true},token);
                    else
                    {
                        using var owner = found.Process;
                        var binding = new WindowsFileDialogNative.Owner(owner.Id, owner.StartTime.ToUniversalTime().Ticks,
                            owner.SessionId, found.Handle, executable, process => SessionGuard.Available() && SessionGuard.OwnsProcess(process));
                        await Protocol.Write(stream, WindowsDesktopFiles.Poll(binding), token);
                    }
                    continue;
                }
                var w = Find(executable);
                using var process = w.Process;
                if(action=="files-start")
                {
                    Validate(w,true);
                    var kind=value["kind"]?.GetValue<string>() ?? "";
                    var binding=await AttachmentTarget(w,kind,token);
                    var result=AttachmentsRpc.StartBatch(binding,kind,value["x"]?.GetValue<double>() ?? 0,value["y"]?.GetValue<double>() ?? 0);
                    await Protocol.Write(stream,result,token);continue;
                }
                if(action is "files-finish" or "files-finish-image")
                {
                    var id = value["id"]?.GetValue<string>() ?? "";
                    if (WindowsDesktopFiles.Contains(id))
                    {
                        await AttachmentsRpc.FinishBatch(id, (_, kind, paths, _, _) =>
                        {
                            if (kind != "chooser") throw new InvalidDataException("file_dialog_selection_invalid");
                            return WindowsDesktopFiles.Finish(id, paths, token);
                        });
                        await Protocol.Write(stream,new JsonObject{["ok"]=true},token);continue;
                    }
                    await AttachmentsRpc.FinishBatch(value["id"]?.GetValue<string>() ?? "",async(binding,kind,paths,x,y)=>
                    {
                        Validate(w,true);
                        if(binding!=await AttachmentTarget(w,kind,token))throw new InvalidDataException("desktop_target_changed");
                        var agent=ClipboardAgent.Current ?? throw new InvalidDataException("clipboard_agent_unavailable");
                        if(action=="files-finish-image")
                        {
                            if(kind!="image"||paths.Length!=1)throw new InvalidDataException("attachment_request_invalid");
                            await using var file=new FileStream(paths[0],FileMode.Open,FileAccess.Read,FileShare.Read);
                            if(file.Length<33||file.Length>Protocol.PngLimit)throw new InvalidDataException("image_limit");
                            var bytes=new byte[checked((int)file.Length)];await file.ReadExactlyAsync(bytes,token);Protocol.Png(bytes);
                            if(binding!=await AttachmentTarget(w,kind,token))throw new InvalidDataException("desktop_target_changed");
                            await agent.PasteImage(bytes,()=>Emit(w,[KeyInput(0x11,true),KeyInput(0x56,true),KeyInput(0x56,false),KeyInput(0x11,false)]));
                        }
                        else if(kind=="paste")await agent.PasteFiles(paths,()=>Emit(w,[KeyInput(0x11,true),KeyInput(0x56,true),KeyInput(0x56,false),KeyInput(0x11,false)]));
                        else if(kind=="image")throw new InvalidDataException("attachment_request_invalid");
                        else await agent.GUI(()=>{DropFiles(w,paths,x,y);return true;});
                    });
                    await Protocol.Write(stream,new JsonObject{["ok"]=true},token);continue;
                }
                if (action == "files-directory")
                {
                    var id = value["id"]?.GetValue<string>() ?? "";
                    var folder = ProjectRpc.Resolve(value["project_id"]?.GetValue<string>() ?? "", value["grant"]?.GetValue<string>() ?? "");
                    await WindowsDesktopFiles.Finish(id, [folder], token);
                    await Protocol.Write(stream, new JsonObject { ["ok"] = true }, token); continue;
                }
                if (action == "paste-image")
                {
                    Validate(w, true);
                    var bytes = Convert.FromBase64String(value["png"]?.GetValue<string>() ?? ""); Protocol.Png(bytes);
                    var agent = ClipboardAgent.Current ?? throw new InvalidDataException("clipboard_agent_unavailable");
                    await agent.PasteImage(bytes, () => Emit(w, [KeyInput(0x11, true), KeyInput(0x56, true), KeyInput(0x56, false), KeyInput(0x11, false)]));
                    await Protocol.Write(stream, new JsonObject { ["ok"] = true }, token); continue;
                }
                if (action == "ime")
                {
                    Validate(w, true);
                    var origin = new Point();
                    if (!ClientToScreen(w.Handle, ref origin)) throw new InvalidDataException("desktop_target_changed");
                    var result = await WindowsIme.Handle(value, w.Process.Id, Target(w), origin.X, origin.Y,
                        content => Emit(w, content.SelectMany(c => new[] { Unicode(c, false), Unicode(c, true) }).ToArray()), token);
                    await Protocol.Write(stream, result, token); continue;
                }
                if (action == "frame")
                {
                    if (!await Captures.WaitAsync(0, token)) throw new InvalidDataException("desktop_capture_busy");
                    // A driver timeout retains one capture slot until its native
                    // copy finishes. Later requests do not accumulate workers.
                    Process? retainedProcess=null;
                    Window retained;
                    try{
                        retainedProcess=Process.GetProcessById(w.Process.Id);
                        if(retainedProcess.StartTime!=w.Process.StartTime)throw new InvalidDataException("desktop_target_changed");
                        retained=w with { Process=retainedProcess };
                    }catch{retainedProcess?.Dispose();Captures.Release();throw;}
                    var capture = Task.Run(() => {
                        using var owner=retained.Process;
                        try { return Capture(retained); } finally { Captures.Release(); }
                    });
                    var bytes = await capture.WaitAsync(TimeSpan.FromSeconds(10), token);
                    var target = Target(w);
                    var sequence = target + Convert.ToHexString(SHA256.HashData(bytes));
                    if (value["after"]?.GetValue<string>() == sequence)
                        await Protocol.Write(stream, new JsonObject { ["unchanged"] = true, ["sequence"] = sequence }, token);
                    else
                        await Protocol.Write(stream, new JsonObject { ["png"] = Convert.ToBase64String(bytes), ["sequence"] = sequence, ["width"] = w.Bounds.Right, ["height"] = w.Bounds.Bottom, ["target"] = target, ["density"] = GetDpiForWindow(w.Handle) / 96.0 }, token);
                    continue;
                }
                if (action == "resize")
                {
                    int width = value["width"]?.GetValue<int>() ?? 0, height = value["height"]?.GetValue<int>() ?? 0;
                    if (width < 320 || height < 240 || width > 8192 || height > 8192 || (long)width * height > 40_000_000) throw new InvalidDataException("desktop_size_invalid");
                    // Electron's custom title bar occupies its client area. Measure
                    // the actual window decorations instead of assuming stock chrome.
                    if (!GetWindowRect(w.Handle,out var outer) ||
                        !SetWindowPos(w.Handle,IntPtr.Zero,0,0,width+outer.Right-outer.Left-w.Bounds.Right,
                            height+outer.Bottom-outer.Top-w.Bounds.Bottom,0x0002|0x0004|0x0010))
                        throw new InvalidDataException("desktop_resize_failed");
                    await Protocol.Write(stream, new JsonObject { ["ok"] = true }, token); continue;
                }
                if (action == "stop") {
                    Validate(w, false);
                    if (!PostMessageW(w.Handle, 0x0010, IntPtr.Zero, IntPtr.Zero)) throw new InvalidDataException("desktop_close_failed");
                    await Protocol.Write(stream, new JsonObject { ["ok"] = true }, token); continue;
                }
                if (value["target"]?.GetValue<string>() != Target(w)) throw new InvalidDataException("desktop_target_changed");
                await WindowsIme.RequirePhoneFocus(value, w.Process.Id, Target(w), token);
                if (action == "pointer")
                {
                    int x = value["x"]?.GetValue<int>() ?? -1, y = value["y"]?.GetValue<int>() ?? -1, next = value["mask"]?.GetValue<int>() ?? -1;
                    if (x < 0 || y < 0 || x >= w.Bounds.Right || y >= w.Bounds.Bottom || next < 0 || next > 127) throw new InvalidDataException("desktop_pointer_invalid");
                    if (welcomeDown!=null)
                    {
                        var down=welcomeDown;
                        if(next!=1) welcomeDown=null;
                        if(next==0)
                        {
                            var up=WelcomeButton(w,x,y);
                            if(up==null || !Automation.Compare(down,up) || !up.TryGetCurrentPattern(InvokePattern.Pattern,out var command))
                                throw new InvalidDataException("desktop_target_changed");
                            Validate(w,false);
                            ((InvokePattern)command).Invoke();
                            await Protocol.Write(stream,new JsonObject{["ok"]=true,["invoked"]=true},token);continue;
                        }
                        await Protocol.Write(stream,new JsonObject{["ok"]=true},token);continue;
                    }
                    if(next==1 && mask==0)
                    {
                        try { welcomeDown=WelcomeButton(w,x,y); }
                        catch(ElementNotAvailableException) { welcomeDown=null; }
                        if(welcomeDown!=null) {await Protocol.Write(stream,new JsonObject{["ok"]=true},token);continue;}
                    }
                    if (GetForegroundWindow() != w.Handle && next == 0)
                    {
                        Validate(w,false);
                        NativeGuestInput.ResetLease(inputLease);
                        mask=NativeGuestInput.PressedButtons(inputLease);
                        await Protocol.Write(stream,new JsonObject{["ok"]=true},token);continue;
                    }
                    if (GetForegroundWindow() != w.Handle && next != 0) SetForegroundWindow(w.Handle);
                    Validate(w, true);
                    PositionPointer(w, x, y);
                    var inputs = new List<Input>();
                    uint[] downs = [2, 0x20, 8], ups = [4, 0x40, 0x10];
                    for (int i=0; i<3; i++) if (((mask ^ next) & (1 << i)) != 0) inputs.Add(new Input { Data = new InputData { Mouse = new Mouse { Flags = (next & (1 << i)) != 0 ? downs[i] : ups[i] } } });
                    if ((next & 8) != 0) inputs.Add(Wheel(120)); if ((next & 16) != 0) inputs.Add(Wheel(-120));
                    if ((next & 32) != 0) inputs.Add(Wheel(-120, true)); if ((next & 64) != 0) inputs.Add(Wheel(120, true));
                    Emit(w, inputs.ToArray(), inputLease); mask = NativeGuestInput.PressedButtons(inputLease);
                }
                else if (action == "key")
                {
                    var keysym = value["keysym"]?.GetValue<uint>() ?? 0; var down = value["down"]?.GetValue<bool>() ?? false;
                    ushort key = Key(keysym);
                    if (key == 0) throw new InvalidDataException("desktop_key_invalid");
                    var stroke=InputStroke.Key(key,!down);
                    if(down)inputLease.Symbols[keysym]=[stroke];
                    else if(inputLease.Symbols.TryGetValue(keysym,out var original))stroke=original[0] with {Up=true};
                    EmitStrokes(w,[stroke],inputLease);
                    if(!down)inputLease.Symbols.Remove(keysym);
                }
                else if (action == "text")
                {
                    var text = value["text"]?.GetValue<string>() ?? "";
                    if (Protocol.Utf8.GetByteCount(text) > 8192) throw new InvalidDataException("desktop_text_limit");
                    var inputs = text.SelectMany(c => new[] { Unicode(c, false), Unicode(c, true) }).ToArray(); Emit(w, inputs);
                }
                else throw new InvalidDataException("desktop_request_invalid");
                await Protocol.Write(stream, new JsonObject { ["ok"] = true }, token);
            }
            catch (Exception e) when (e is not OperationCanceledException)
            {
                // Only a type and HRESULT cross the trust boundary. Exception
                // messages from Windows can include user-controlled strings.
                await Protocol.Write(stream, new JsonObject {
                    ["error"] = e is InvalidDataException ? e.Message : "desktop_operation_failed",
                    ["exception_type"] = e.GetType().Name, ["native_code"] = e.HResult.ToString("X8"),
                }, token);
            }
            finally { if (dpi != IntPtr.Zero) SetThreadDpiAwarenessContext(dpi); Operations.Release(); }
        }
        }
        finally
        {
            NativeGuestInput.CloseLease(inputLease);
        }
    }
    private static string Target(Window w) {
        Validate(w, false);
        var title=new System.Text.StringBuilder(4096);GetWindowTextW(w.Handle,title,title.Capacity);
        var data = Protocol.Utf8.GetBytes($"{w.Process.Id}:{w.Process.StartTime.ToUniversalTime().Ticks}:{w.Handle.ToInt64()}:{w.Bounds.Right}:{w.Bounds.Bottom}:{GetDpiForWindow(w.Handle)}:{title}");
        return Convert.ToHexString(SHA256.HashData(data).AsSpan(0,16)).ToLowerInvariant();
    }
    private static async Task<string> AttachmentTarget(Window w,string kind,CancellationToken token)
    {
        var target=Target(w);if(kind=="drop")return target;
        if(kind is not ("paste" or "image"))throw new InvalidDataException("attachment_request_invalid");
        var origin=new Point();if(!ClientToScreen(w.Handle,ref origin))throw new InvalidDataException("desktop_target_changed");
        var focus=await WindowsIme.Handle(new JsonObject{["mode"]="status"},w.Process.Id,target,origin.X,origin.Y,_=>throw new InvalidDataException("attachment_request_invalid"),token);
        if(focus["available"]?.GetValue<bool>()!=true)throw new InvalidDataException("desktop_input_not_focused");
        return target+":"+focus["context"]!.GetValue<string>();
    }
    private static void DropFiles(Window w,string[] paths,double x,double y)
    {
        Validate(w,true);
        var point=PositionPointer(w,Math.Clamp((int)(x*w.Bounds.Right),0,w.Bounds.Right-1),Math.Clamp((int)(y*w.Bounds.Bottom),0,w.Bounds.Bottom-1));
        void Guard(){Validate(w,true);if(GetAncestor(WindowFromPoint(point),2)!=w.Handle)throw new InvalidDataException("desktop_target_changed");}
        Guard();
        var initialized=OleInitialize(IntPtr.Zero);Marshal.ThrowExceptionForHR(initialized);
        try
        {
            var data=new DataObject();data.SetData(DataFormats.FileDrop,true,paths);
            using var release=new System.Windows.Forms.Timer{Interval=100};
            release.Tick+=(_,_)=>{try{Emit(w,[KeyInput(17,true),KeyInput(17,false)]);}catch{release.Stop();}};
            release.Start();
            var result=DoDragDrop((System.Runtime.InteropServices.ComTypes.IDataObject)data,new DropSource(Guard),1,out var effect);
            if(result!=0x40100 || (effect&1)==0)throw new InvalidDataException("desktop_drop_not_accepted");
        }
        finally{OleUninitialize();}
    }
    internal static WindowsAudioRpc.Target? AudioTarget()
    {
        if (!SessionGuard.Available()) throw new InvalidDataException("requires_gui_login");
        var registered = RegisteredExecutable(); if (registered is null) return null;
        var session = Process.GetCurrentProcess().SessionId; WindowsAudioRpc.Target? first = null;
        foreach (var process in Process.GetProcessesByName("Claude"))
        {
            var owned = false;
            try
            {
                if (process.SessionId != session || !string.Equals(process.MainModule?.FileName, registered.Executable, StringComparison.OrdinalIgnoreCase)) continue;
                var target = new WindowsAudioRpc.Target(process, registered.Executable); owned = true;
                if (first is null || target.Started < first.Started) { first?.Dispose(); first = target; }
                else target.Dispose();
            }
            catch (Exception e) when (e is InvalidDataException or InvalidOperationException or System.ComponentModel.Win32Exception) { }
            finally { if (!owned) process.Dispose(); }
        }
        return first;
    }
    private static byte[] Capture(Window w) => ClaudeGraphicsCapture.Capture(w.Handle,w.Process.Id,w.Bounds.Right,w.Bounds.Bottom,()=>Validate(w,false)).GetAwaiter().GetResult();
    private static Input Wheel(int amount, bool horizontal = false) => new() { Data = new InputData { Mouse = new Mouse { Flags = horizontal ? 0x1000u : 0x800u, Data = unchecked((uint)amount) } } };
    private static Input Unicode(char c, bool up) => new() { Type = 1, Data = new InputData { Keyboard = new Keyboard { Scan = c, Flags = 4u | (up ? 2u : 0u) } } };
    private static Input KeyInput(ushort key, bool down) => new() { Type = 1, Data = new InputData { Keyboard = new Keyboard { Key = key, Flags = down ? 0u : 2u } } };
    private static void Emit(Window w, Input[] inputs, NativeInputCoordinator.Lease? lease=null)
    {
        var strokes=inputs.Select(input=>input.Type==1
            ? InputStroke.Key((input.Data.Keyboard.Flags&4)!=0?input.Data.Keyboard.Scan:input.Data.Keyboard.Key,(input.Data.Keyboard.Flags&2)!=0,(input.Data.Keyboard.Flags&4)!=0)
            : input.Data.Mouse.Flags switch
            {
                2=>InputStroke.Button(1,false),4=>InputStroke.Button(1,true),32=>InputStroke.Button(2,false),64=>InputStroke.Button(2,true),8=>InputStroke.Button(4,false),16=>InputStroke.Button(4,true),
                0x800=>InputStroke.Wheel(unchecked((int)input.Data.Mouse.Data)),0x1000=>InputStroke.Wheel(unchecked((int)input.Data.Mouse.Data),true),
                _=>throw new InvalidDataException("desktop_input_failed")
            }).ToArray();
        var own=lease is null;lease??=NativeGuestInput.OpenLease();
        try{EmitStrokes(w,strokes,lease);}finally{if(own)NativeGuestInput.CloseLease(lease);}
    }
    private static void EmitStrokes(Window w,InputStroke[] strokes,NativeInputCoordinator.Lease lease)
    {
        var id=w.Process.Id;var started=w.Process.StartTime.ToUniversalTime();var session=w.Process.SessionId;var handle=w.Handle;var executable=w.Executable;
        bool ReleaseAllowed()
        {
            if(!SessionGuard.Available() || Process.GetCurrentProcess().SessionId!=session)return false;
            try
            {
                using var process=Process.GetProcessById(id);
                if(process.HasExited)return true;
                if(process.StartTime.ToUniversalTime()!=started || process.SessionId!=session || !string.Equals(process.MainModule?.FileName,executable,StringComparison.OrdinalIgnoreCase))return false;
                var thread=GetWindowThreadProcessId(handle,out var current);
                return thread==0 || current==id;
            }
            catch(ArgumentException){return true;}
            catch{return false;}
        }
        NativeGuestInput.Emit(lease,strokes,()=>Validate(w,true),ReleaseAllowed);
    }
    private static ushort Key(uint sym) => sym switch
    {
        0xff08 => 8, 0xff09 => 9, 0xff0d => 13, 0xff1b => 27, 0xffff => 46,
        0xff50 => 36, 0xff51 => 37, 0xff52 => 38, 0xff53 => 39, 0xff54 => 40, 0xff55 => 33, 0xff56 => 34, 0xff57 => 35, 0xff63 => 45,
        0xffe1 or 0xffe2 => 16, 0xffe3 or 0xffe4 => 17, 0xffe9 or 0xffea => 18, 0xffe7 or 0xffe8 or 0xffeb or 0xffec => 91,
        >= 0xffbe and <= 0xffc9 => (ushort)(112 + sym - 0xffbe),
        >= 32 and <= 126 => (ushort)(VkKeyScanW((char)sym) & 0xff), _ => 0
    };
}
