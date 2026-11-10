using System.Buffers.Binary;
using System.Diagnostics;
using System.Runtime.CompilerServices;
using System.Runtime.InteropServices;
using System.Text.Json.Nodes;

namespace Cage.GuestClipboard;

// Output-only PCM for the paired GUI agent's tree and the registered Claude
// app. It never activates a microphone or system/endpoint-wide loopback.
internal static class WindowsAudioRpc
{
    private static readonly SemaphoreSlim Slots = new(4, 4);
    private const int SampleRate = 48000, Channels = 2, Samples = 960 * Channels;
    [ModuleInitializer] internal static void Register()
    {
        if (GuestOperations.Names.Contains("audio") && Environment.OSVersion.Version.Build >= 20348)
            GuestOperations.Register("audio", Run);
    }
    internal sealed class Target : IDisposable
    {
        internal Process Process { get; }
        internal long Started { get; }
        private readonly string executable;
        private readonly int session;
        internal Target(Process process, string path)
        {
            Process = process; Started = process.StartTime.ToUniversalTime().Ticks; executable = path;
            using var current = System.Diagnostics.Process.GetCurrentProcess(); session = current.SessionId;
            try { Validate(); } catch { Process.Dispose(); throw; }
        }
        internal void Validate()
        {
            if (!SessionGuard.Available() || !SessionGuard.OwnsProcess(Process) || Process.HasExited || Process.StartTime.ToUniversalTime().Ticks != Started ||
                Process.SessionId != session ||
                !string.Equals(Process.MainModule?.FileName, executable, StringComparison.OrdinalIgnoreCase))
                throw new InvalidDataException("audio_target_changed");
        }
        public void Dispose() => Process.Dispose();
    }
    internal static async Task Run(Stream stream, CancellationToken token, Func<bool> available)
    {
        var request = await Protocol.Read(stream, 4096, token) ?? throw new EndOfStreamException();
        if (request.Count != 1 || request["action"]?.GetValue<string>() is not ("status" or "capture")) throw new InvalidDataException("audio_request_invalid");
        if (!available()) throw new InvalidDataException("requires_gui_login");
        if (!await Slots.WaitAsync(0, token)) throw new InvalidDataException("audio_capture_limit");
        var began = false;
        try
        {
            // A native activation can finish late. This bounded worker retains
            // its slot and callback/parameters until Windows actually completes.
            await Task.Factory.StartNew(() => Worker(stream, request["action"]!.GetValue<string>(), token, available, () => began = true),
                CancellationToken.None, TaskCreationOptions.LongRunning, TaskScheduler.Default);
        }
        catch (Exception) when (began) { /* Close PCM without appending a JSON error as sound. */ }
        finally { Slots.Release(); }
    }
    private static void Worker(Stream output, string action, CancellationToken token, Func<bool> available, Action began)
    {
        if(action=="status"){StatusDiagnostic(output,token,available);return;}
        Marshal.ThrowExceptionForHR(CoInitializeEx(0, 0));
        try
        {
            token.ThrowIfCancellationRequested(); if (!available()) throw new InvalidDataException("requires_gui_login");
            var endpoints = RenderEndpoints();
            using var process = Process.GetCurrentProcess();
            using var owner = new Target(Process.GetProcessById(process.Id), process.MainModule!.FileName!);
            var sources = new List<Source>();
            try
            {
                sources.Add(new Source(new Target(Process.GetProcessById(owner.Process.Id), owner.Process.MainModule!.FileName!), token));
                RefreshClaude(sources, owner, token);
                Protocol.Write(output, new JsonObject { ["ready"] = true, ["format"] = "s16le", ["sample_rate"] = SampleRate,
                    ["channels"] = Channels, ["render_endpoints"] = endpoints, ["requires_render_stream"] = true }, token).GetAwaiter().GetResult();
                began();
                var clock = Stopwatch.StartNew(); var next = 20L; var refresh = 1000L;
                var mixed = new int[Samples]; var packet = new byte[Samples * 2];
                while (true)
                {
                    token.ThrowIfCancellationRequested(); if (!available()) throw new InvalidDataException("requires_gui_login"); owner.Validate();
                    for (var i = sources.Count - 1; i >= 0; i--)
                    {
                        try { sources[i].Drain(); }
                        catch (InvalidDataException) when (i != 0) { sources[i].Dispose(); sources.RemoveAt(i); refresh = 0; }
                    }
                    if (clock.ElapsedMilliseconds >= refresh) { RefreshClaude(sources, owner, token); refresh = clock.ElapsedMilliseconds + 1000; }
                    var remaining = next - clock.ElapsedMilliseconds;
                    if (remaining > 0 && token.WaitHandle.WaitOne((int)Math.Min(remaining, 20))) token.ThrowIfCancellationRequested();
                    if (clock.ElapsedMilliseconds < next) continue;
                    Array.Clear(mixed);
                    foreach (var source in sources) { source.Target.Validate(); source.Mix(mixed); }
                    for (var i = 0; i < Samples; i++) BinaryPrimitives.WriteInt16LittleEndian(packet.AsSpan(i * 2), (short)Math.Clamp(mixed[i], short.MinValue, short.MaxValue));
                    owner.Validate(); output.WriteAsync(packet, token).GetAwaiter().GetResult();
                    next += 20; if (next < clock.ElapsedMilliseconds - 100) next = clock.ElapsedMilliseconds + 20;
                }
            }
            finally { foreach (var source in sources) source.Dispose(); }
        }
        finally { CoUninitialize(); }
    }
    private static void StatusDiagnostic(Stream output,CancellationToken token,Func<bool> available)
    {
        var stage="com_initialize";bool initialized=false;
        object? enumerator=null;IMMDeviceCollection? devices=null;
        try
        {
            Marshal.ThrowExceptionForHR(CoInitializeEx(0,0));initialized=true;
            token.ThrowIfCancellationRequested();if(!available())throw new InvalidDataException("requires_gui_login");
            stage="render_activate";enumerator=Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("BCDE0395-E52F-467C-8E3D-C4579291692E"))!);
            stage="render_enumerate";Marshal.ThrowExceptionForHR(((IMMDeviceEnumerator)enumerator!).EnumAudioEndpoints(0,1,out devices));
            stage="render_count";Marshal.ThrowExceptionForHR(devices.GetCount(out var count));
            stage="audio_service";var audio=ServiceState("Audiosrv",out var audioCode);
            stage="endpoint_service";var endpoint=ServiceState("AudioEndpointBuilder",out var endpointCode);
            token.ThrowIfCancellationRequested();if(!available())throw new InvalidDataException("requires_gui_login");
            stage="status_emit";
            Protocol.Write(output,new JsonObject { ["supported"]=Environment.OSVersion.Version.Build>=20348,["format"]="s16le",["sample_rate"]=SampleRate,["channels"]=Channels,["render_endpoints"]=checked((int)count),["audio_service"]=audio,["endpoint_service"]=endpoint,["audio_service_native_code"]=audioCode.ToString("X8"),["endpoint_service_native_code"]=endpointCode.ToString("X8"),["requires_render_stream"]=true },token).GetAwaiter().GetResult();
        }
        catch(Exception error) when(error is not OperationCanceledException)
        {
            Protocol.Write(output,new JsonObject { ["error"]="audio_status_unavailable",["stage"]=stage,["native_code"]=unchecked((uint)error.HResult).ToString("X8"),["exception_type"]=error.GetType().Name },token).GetAwaiter().GetResult();
        }
        finally
        {
            if(devices!=null)Marshal.ReleaseComObject(devices);
            if(enumerator!=null)Marshal.ReleaseComObject(enumerator);
            if(initialized)CoUninitialize();
        }
    }
    internal static JsonObject ComponentStatus()
    {
        var supported=Environment.OSVersion.Version.Build>=20348;
        if(!supported)return new JsonObject{["supported"]=false,["installed"]=false,["ready"]=false,["reason"]="audio_os_unsupported"};
        using var memory=new MemoryStream();StatusDiagnostic(memory,CancellationToken.None,SessionGuard.Available);
        var state=JsonNode.Parse(memory.ToArray())as JsonObject??throw new InvalidDataException("audio_status_unavailable");
        var result=new JsonObject{["supported"]=true,["installed"]=true,["ready"]=false,["reason"]="audio_status_unavailable"};
        foreach(var field in new[]{"stage","native_code","audio_service_native_code","endpoint_service_native_code"})
            if(state[field]!=null)result[field]=state[field]!.DeepClone();
        if(state["supported"]?.GetValue<bool>()==true)
        {
            var services=state["audio_service"]?.GetValue<string>()=="running"&&state["endpoint_service"]?.GetValue<string>()=="running";
            var endpoints=(state["render_endpoints"]?.GetValue<int>()??0)>0;
            result["ready"]=services&&endpoints;result["reason"]=!services?"audio_services_unavailable":!endpoints?"no_active_render_endpoint":"";
        }
        return result;
    }
    private static void RefreshClaude(List<Source> sources, Target owner, CancellationToken token)
    {
        var target = ClaudeWindowRpc.AudioTarget();
        if (target is null) return;
        try
        {
            if (IsDescendant(target.Process, owner.Process) || sources.Any(s => s.Target.Process.Id == target.Process.Id && s.Target.Started == target.Started)) return;
            for (var i = sources.Count - 1; i > 0; i--) { sources[i].Dispose(); sources.RemoveAt(i); }
            sources.Add(new Source(target, token)); target = null;
        }
        finally { target?.Dispose(); }
    }
    private static bool IsDescendant(Process child, Process ancestor)
    {
        var seen = new HashSet<int>(); var pid = child.Id; var started = child.StartTime.ToUniversalTime();
        while (seen.Add(pid))
        {
            if (pid == ancestor.Id) return started == ancestor.StartTime.ToUniversalTime();
            using var current = Process.GetProcessById(pid);
            if (NtQueryInformationProcess(current.Handle, 0, out var basic, (uint)Marshal.SizeOf<ProcessBasic>(), out _) != 0) throw new InvalidDataException("audio_target_ancestry_unavailable");
            pid = checked((int)basic.Parent.ToInt64()); if (pid < 1) return false;
            try { using var parent = Process.GetProcessById(pid); if (parent.StartTime.ToUniversalTime() > started) throw new InvalidDataException("audio_target_ancestry_unavailable"); started = parent.StartTime.ToUniversalTime(); }
            catch (ArgumentException) { throw new InvalidDataException("audio_target_ancestry_unavailable"); }
        }
        return false;
    }
    private sealed class Source : IDisposable
    {
        internal Target Target { get; }
        private IAudioClient? client; private IAudioCaptureClient? capture;
        private readonly EventWaitHandle ready = new(false, EventResetMode.AutoReset);
        private readonly short[] ring = new short[SampleRate * Channels / 5];
        private int read, count; private bool started;
        internal Source(Target target, CancellationToken token)
        {
            Target = target;
            try
            {
                target.Validate(); client = Activate(target.Process.Id);
                token.ThrowIfCancellationRequested(); target.Validate();
                var format = new WaveFormat { Tag = 1, Channels = Channels, Rate = SampleRate, Average = SampleRate * Channels * 2, Align = Channels * 2, Bits = 16, Extra = 0 };
                Check(client.Initialize(0, 0x80000000u | 0x00020000u | 0x00040000u, 0, 0, ref format, 0));
                Check(client.SetEventHandle(ready.SafeWaitHandle.DangerousGetHandle()));
                Check(client.GetBufferSize(out var frames)); if (frames is < 1 or > SampleRate) throw new InvalidDataException("audio_buffer_invalid");
                var id = typeof(IAudioCaptureClient).GUID; Check(client.GetService(ref id, out var service)); capture = (IAudioCaptureClient)service;
                target.Validate(); Check(client.Start()); started = true;
            }
            catch { Dispose(); throw; }
        }
        internal void Drain()
        {
            Target.Validate(); ready.WaitOne(0); var batches = 0;
            while (true)
            {
                Check(capture!.GetNextPacketSize(out var next)); if (next == 0) return;
                if (++batches > 100 || next > SampleRate) throw new InvalidDataException("audio_buffer_invalid");
                Check(capture.GetBuffer(out var data, out var frames, out var flags, out _, out _));
                try
                {
                    if (frames > SampleRate || (data == 0 && (flags & 2) == 0)) throw new InvalidDataException("audio_buffer_invalid");
                    Target.Validate(); if ((flags & 1) != 0) { read = count = 0; }
                    var values = new short[checked((int)frames * Channels)]; if ((flags & 2) == 0) Marshal.Copy(data, values, 0, values.Length);
                    Target.Validate();
                    foreach (var value in values) { if (count == ring.Length) { read = (read + 1) % ring.Length; count--; } ring[(read + count++) % ring.Length] = value; }
                }
                finally { Check(capture.ReleaseBuffer(frames)); }
            }
        }
        internal void Mix(int[] output)
        {
            for (var i = 0; i < output.Length && count != 0; i++) { output[i] += ring[read]; read = (read + 1) % ring.Length; count--; }
        }
        public void Dispose()
        {
            if (started) { client?.Stop(); started = false; }
            if (capture is not null) { Marshal.ReleaseComObject(capture); capture = null; }
            if (client is not null) { Marshal.ReleaseComObject(client); client = null; }
            ready.Dispose(); Target.Dispose();
        }
    }
    private static IAudioClient Activate(int pid)
    {
        var data = Marshal.AllocHGlobal(12); IActivateOperation? operation = null;
        var callback = new Completion();
        try
        {
            Marshal.WriteInt32(data, 1); Marshal.WriteInt32(data, 4, pid); Marshal.WriteInt32(data, 8, 0); // include target tree only
            var value = new Variant { Type = 65, Bytes = 12, Data = data }; var iid = typeof(IAudioClient).GUID;
            Check(ActivateAudioInterfaceAsync("VAD\\Process_Loopback", ref iid, ref value, callback, out operation));
            var result = callback.Done.Task.GetAwaiter().GetResult(); Check(result.Result); return result.Client ?? throw new InvalidDataException("audio_activation_failed");
        }
        finally { GC.KeepAlive(callback); if (operation is not null) Marshal.ReleaseComObject(operation); Marshal.FreeHGlobal(data); }
    }
    [ComVisible(true), ClassInterface(ClassInterfaceType.None)] private sealed class Completion : ICompletion, IAgile
    {
        internal readonly TaskCompletionSource<(int Result, IAudioClient? Client)> Done = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public int ActivateCompleted(IActivateOperation operation)
        {
            try { var result = operation.GetActivateResult(out var activated, out var audio); if (result >= 0) result = activated; Done.TrySetResult((result, result >= 0 ? (IAudioClient)audio : null)); }
            catch (Exception e) { Done.TrySetResult((Marshal.GetHRForException(e), null)); }
            return 0;
        }
    }
    private static int RenderEndpoints()
    {
        object? enumerator = null; IMMDeviceCollection? devices = null;
        try { enumerator = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("BCDE0395-E52F-467C-8E3D-C4579291692E"))!); Check(((IMMDeviceEnumerator)enumerator!).EnumAudioEndpoints(0, 1, out devices)); Check(devices.GetCount(out var count)); return checked((int)count); }
        catch (COMException) { return -1; }
        finally { if (devices is not null) Marshal.ReleaseComObject(devices); if (enumerator is not null) Marshal.ReleaseComObject(enumerator); }
    }
    private static string ServiceState(string name,out uint code)
    {
        code=0;var manager = OpenSCManagerW(null, null, 1); if (manager == 0) { code=Win32Code();return "unknown"; }
        try { var service = OpenServiceW(manager, name, 4); if (service == 0) { code=Win32Code();return "unknown"; } try { if(!QueryServiceStatusEx(service, 0, out var state, (uint)Marshal.SizeOf<ServiceStatus>(), out _)){code=Win32Code();return "unknown";}return state.Current==4?"running":"stopped"; } finally { CloseServiceHandle(service); } }
        finally { CloseServiceHandle(manager); }
    }
    private static uint Win32Code() {var error=unchecked((uint)Marshal.GetLastWin32Error());return error==0?0x80004005:error|0x80070000;}
    private static void Check(int hr) { if (hr < 0) throw new InvalidDataException("audio_native_" + unchecked((uint)hr).ToString("x8")); }
    [StructLayout(LayoutKind.Sequential, Pack = 2)] private struct WaveFormat { public ushort Tag, Channels; public uint Rate, Average; public ushort Align, Bits, Extra; }
    [StructLayout(LayoutKind.Explicit, Size = 24)] private struct Variant { [FieldOffset(0)] public ushort Type; [FieldOffset(8)] public uint Bytes; [FieldOffset(16)] public nint Data; }
    [StructLayout(LayoutKind.Sequential)] private struct ProcessBasic { public nint Exit, Peb, Affinity, Priority, ID, Parent; }
    [StructLayout(LayoutKind.Sequential)] private struct ServiceStatus { public uint Type, Current, Controls, Exit, ServiceExit, Checkpoint, Hint, PID, Flags; }
    [ComImport, Guid("1CB9AD4C-DBFA-4C32-B178-C2F568A703B2"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)] private interface IAudioClient
    {
        [PreserveSig] int Initialize(int mode, uint flags, long duration, long period, ref WaveFormat format, nint session);
        [PreserveSig] int GetBufferSize(out uint frames); [PreserveSig] int GetStreamLatency(out long latency); [PreserveSig] int GetCurrentPadding(out uint padding);
        [PreserveSig] int IsFormatSupported(int mode, nint format, out nint closest); [PreserveSig] int GetMixFormat(out nint format); [PreserveSig] int GetDevicePeriod(out long normal, out long minimum);
        [PreserveSig] int Start(); [PreserveSig] int Stop(); [PreserveSig] int Reset(); [PreserveSig] int SetEventHandle(nint handle);
        [PreserveSig] int GetService(ref Guid iid, [MarshalAs(UnmanagedType.IUnknown)] out object service);
    }
    [ComImport, Guid("C8ADBD64-E71E-48A0-A4DE-185C395CD317"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)] private interface IAudioCaptureClient
    { [PreserveSig] int GetBuffer(out nint data, out uint frames, out uint flags, out ulong device, out ulong clock); [PreserveSig] int ReleaseBuffer(uint frames); [PreserveSig] int GetNextPacketSize(out uint frames); }
    [ComImport, Guid("72A22D78-CDE4-431D-B8CC-843A71199B6D"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)] private interface IActivateOperation
    { [PreserveSig] int GetActivateResult(out int result, [MarshalAs(UnmanagedType.IUnknown)] out object audio); }
    [ComVisible(true), Guid("41D949AB-9862-444A-80F6-C261334DA5EB"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)] private interface ICompletion
    { [PreserveSig] int ActivateCompleted(IActivateOperation operation); }
    [ComVisible(true), Guid("94EA2B94-E9CC-49E0-C0FF-EE64CA8F5B90"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)] private interface IAgile { }
    [ComImport, Guid("A95664D2-9614-4F35-A746-DE8DB63617E6"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)] private interface IMMDeviceEnumerator
    { [PreserveSig] int EnumAudioEndpoints(int flow, uint state, out IMMDeviceCollection devices); }
    [ComImport, Guid("0BD7A1BE-7A1A-44DB-8397-CC5392387B5E"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)] private interface IMMDeviceCollection
    { [PreserveSig] int GetCount(out uint count); }
    [DllImport("mmdevapi.dll", ExactSpelling = true, CharSet = CharSet.Unicode)] private static extern int ActivateAudioInterfaceAsync(string device, ref Guid iid, ref Variant parameters, ICompletion callback, out IActivateOperation operation);
    [DllImport("ole32.dll")] private static extern int CoInitializeEx(nint reserved, uint mode); [DllImport("ole32.dll")] private static extern void CoUninitialize();
    [DllImport("ntdll.dll")] private static extern int NtQueryInformationProcess(nint process, int kind, out ProcessBasic basic, uint bytes, out uint returned);
    [DllImport("advapi32.dll", ExactSpelling=true,CharSet = CharSet.Unicode,SetLastError=true)] private static extern nint OpenSCManagerW(string? machine, string? database, uint rights);
    [DllImport("advapi32.dll", ExactSpelling=true,CharSet = CharSet.Unicode,SetLastError=true)] private static extern nint OpenServiceW(nint manager, string name, uint rights);
    [DllImport("advapi32.dll", ExactSpelling=true,SetLastError=true)] private static extern bool QueryServiceStatusEx(nint service, int level, out ServiceStatus state, uint bytes, out uint needed);
    [DllImport("advapi32.dll")] private static extern bool CloseServiceHandle(nint handle);
}
