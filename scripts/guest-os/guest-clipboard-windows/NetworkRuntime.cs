using System.Buffers.Binary;
using System.ComponentModel;
using System.Diagnostics;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text.Json.Nodes;

namespace Cage.GuestClipboard;

// This SYSTEM process owns only the Cage virtual packet interface. The GUI
// clipboard, browser, files and input endpoints run in the ordinary user agent.
internal static class NetworkRuntime
{
    internal static readonly Guid Service = new("f4a4c1ce-3c9f-49f1-b7bf-82df6592c0fb");
    internal const string IP = "192.168.127.2", Gateway = "192.168.127.1";
    private static readonly SemaphoreSlim PacketSlot = new(1,1);
    private static readonly byte[] GuestMac = [0x5a,0x94,0xef,0xe4,0x0c,0xee], GatewayMac = [0x5a,0x94,0xef,0xe4,0x0c,0xdd];
    internal static async Task Run(Guid vm, string sid, string keyPath, CancellationToken token)
    {
        if (WindowsIdentity.GetCurrent().User?.Value != "S-1-5-18") throw new InvalidDataException("network_requires_system");
        var parameters = Microsoft.Win32.Registry.LocalMachine.OpenSubKey(@"SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters");
        if (!Guid.TryParse(parameters?.GetValue("VirtualMachineId")?.ToString(), out var actual) || actual != vm || !sid.StartsWith("S-1-5-21-",StringComparison.Ordinal)) throw new InvalidDataException("network_vm_invalid");
        var secret = Pairing.Load(keyPath,vm,sid,false);
        using var listener = HyperVEndPoint.Socket();
        using var interrupt = token.Register(listener.Dispose);
        listener.Bind(new HyperVEndPoint(Protocol.Parent,Service)); listener.Listen(1);
        using var slots = new SemaphoreSlim(4,4);
        var active = new HashSet<Task>();
        try
        {
            while (!token.IsCancellationRequested)
            {
                await slots.WaitAsync(token);
                Socket socket;
                try { socket = await Task.Run(listener.Accept,CancellationToken.None).WaitAsync(token); }
                catch { slots.Release();throw; }
                lock(active)
                {
                    Task? task = null;
                    task = Task.Run(async()=>{
                        try { await Connection(socket,vm,sid,secret,token); }
                        catch(Exception error) {
                            // Stage-free native code only: never serialize request,
                            // packet bytes, paths, identity secrets or exception text.
                            try { File.WriteAllText(Path.Combine(AppContext.BaseDirectory,"network-error.json"),new JsonObject { ["error_type"]=error.GetType().Name,["hresult"]=error.HResult.ToString("X8"),["stage"]=(error as NetworkFailure)?.Stage ?? "handshake-or-stream",["native_code"]=(error.InnerException as Win32Exception)?.NativeErrorCode ?? (error as Win32Exception)?.NativeErrorCode ?? 0,["updated_at"]=DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() }.ToJsonString()); } catch { }
                        }
                        finally { socket.Dispose();slots.Release();lock(active)active.Remove(task!); }
                    },CancellationToken.None);
                    active.Add(task);
                }
            }
        }
        catch(OperationCanceledException)when(token.IsCancellationRequested){ }
        catch(ObjectDisposedException)when(token.IsCancellationRequested){ }
        finally { Task[] pending;lock(active)pending=active.ToArray();await Task.WhenAll(pending);CryptographicOperations.ZeroMemory(secret); }
    }

    private static async Task Connection(Socket socket,Guid vm,string sid,byte[] secret,CancellationToken token)
    {
        using var connection = CancellationTokenSource.CreateLinkedTokenSource(token);
        using var cancel = connection.Token.Register(socket.Dispose);
        using var network = new NetworkStream(socket,false);
        using var stream = new ReadAheadStream(network);
        using var handshake = CancellationTokenSource.CreateLinkedTokenSource(connection.Token); handshake.CancelAfter(TimeSpan.FromSeconds(10));
        var nonce = Convert.ToBase64String(RandomNumberGenerator.GetBytes(32)); var session = Guid.NewGuid().ToString("N");
        await Protocol.Write(stream,new JsonObject { ["nonce"] = nonce,["vm_id"] = vm.ToString("D"),["sid"] = sid,["session"] = session,["proof"] = Protocol.Proof(secret,"server",nonce,vm,sid,session,"",null) },handshake.Token);
        var request = await Protocol.Read(stream,4096,handshake.Token) ?? throw new EndOfStreamException();
        var operation = request["operation"]?.GetValue<string>() ?? "";
        if (operation is not ("network-stream" or "network-settings") || request["paths"] is not null || request["vm_id"]?.GetValue<string>() != vm.ToString("D") || request["sid"]?.GetValue<string>() != sid ||
            !Protocol.EqualProof(Protocol.Proof(secret,"client",nonce,vm,sid,session,operation,null),request["proof"]?.GetValue<string>())) throw new InvalidDataException("pairing_invalid");
        if (operation == "network-settings")
        {
            await Protocol.Write(stream,new JsonObject { ["version"] = 1,["session"] = session,["available"] = true },handshake.Token);
            var settings = await Protocol.Read(stream,4096,handshake.Token) ?? throw new EndOfStreamException();
            await WindowsSystemSettings.Apply(settings,vm,sid,connection.Token);
            await Protocol.Write(stream,new JsonObject { ["ok"] = true },connection.Token); return;
        }
        if(!await PacketSlot.WaitAsync(0,connection.Token))throw new InvalidDataException("network_stream_already_connected");
        try {
        handshake.CancelAfter(TimeSpan.FromSeconds(20));
        using var tun = new Wintun(vm);
        // Adapter configuration is part of the bounded admission handshake.
        // A stalled PowerShell operation must release this packet slot so a
        // later authenticated host connection can retry.
        await tun.Configure(handshake.Token);
        await Protocol.Write(stream,new JsonObject { ["version"] = 1,["os"] = "windows",["vm_id"] = vm.ToString("D"),["gui_user_sid"] = sid,["session"] = session,["available"] = true,["network"] = "packet" },handshake.Token);
        using var writes = new SemaphoreSlim(1,1);
        async Task Send(byte[] frame)
        {
            await writes.WaitAsync(connection.Token);
            try { var length = new byte[4]; BinaryPrimitives.WriteUInt32BigEndian(length,(uint)frame.Length); await stream.WriteAsync(length,connection.Token); await stream.WriteAsync(frame,connection.Token); await stream.FlushAsync(connection.Token); }
            finally { writes.Release(); }
        }
        var readyPath = Path.Combine(AppContext.BaseDirectory,"network-ready.json");
        var receipt = Task.Run(async () =>
        {
            while(!connection.IsCancellationRequested)
            {
                var data = new JsonObject { ["version"] = 1,["vm_id"] = vm.ToString("D"),["gui_user_sid"] = sid,["adapter"] = tun.Name,["interface_index"] = tun.Index,["session"] = session,["updated_at"] = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds(),["network"] = "packet" };
                var stage = readyPath+"."+session;
                await PublishReceipt(stage,readyPath,data.ToJsonString(),connection.Token);
                await Task.Delay(1000,connection.Token);
            }
        },connection.Token);
        var outgoing = Task.Run(async () =>
        {
            while(!connection.IsCancellationRequested)
            {
                var packet = tun.Receive();
                if (packet is null) { await Task.Delay(5,connection.Token); continue; }
                var frame = new byte[14+packet.Length]; GatewayMac.CopyTo(frame,0); GuestMac.CopyTo(frame,6);
                frame[12] = packet[0]>>4 == 6 ? (byte)0x86 : (byte)0x08; frame[13] = packet[0]>>4 == 6 ? (byte)0xdd : (byte)0;
                packet.CopyTo(frame,14); await Send(frame);
            }
        },connection.Token);
        var incoming = Task.Run(async () =>
        {
            var header = new byte[4];
            while(!connection.IsCancellationRequested)
            {
                await stream.ReadExactlyAsync(header,connection.Token); var size = BinaryPrimitives.ReadUInt32BigEndian(header);
                if (size is < 14 or > 65549) throw new InvalidDataException("network_packet_invalid");
                var frame = new byte[size]; await stream.ReadExactlyAsync(frame,connection.Token);
                if (frame[12]==8 && frame[13]==6)
                {
                    var response = Arp(frame); if (response is not null) await Send(response); continue;
                }
                if (!(frame[12]==8 && frame[13]==0 || frame[12]==0x86 && frame[13]==0xdd)) continue;
                if (!frame.AsSpan(0,6).SequenceEqual(GuestMac) && !frame.AsSpan(0,6).SequenceEqual(new byte[]{255,255,255,255,255,255})) continue;
                tun.Send(frame.AsSpan(14));
            }
        },connection.Token);
        Task completed;
        try { completed = await Task.WhenAny(incoming,outgoing,receipt); }
        finally
        {
            connection.Cancel(); socket.Dispose();
            try { await Task.WhenAll(incoming,outgoing,receipt); } catch { }
            try { if (JsonNode.Parse(File.ReadAllText(readyPath))?["session"]?.GetValue<string>()==session) File.Delete(readyPath); } catch { }
            try { File.Delete(readyPath+"."+session); } catch { }
        }
        // Retire all workers before reporting the worker that ended the stream.
        // Awaiting WhenAny alone would discard its exception and leave only an
        // old diagnostic from an unrelated admission failure.
        if (!token.IsCancellationRequested)
        {
            var stage = completed == incoming ? "stream-incoming" : completed == outgoing ? "stream-outgoing" : "stream-receipt";
            try { await completed; }
            catch (NetworkFailure) { throw; }
            catch (Exception error) { throw new NetworkFailure(stage,error); }
            throw new NetworkFailure(stage,new EndOfStreamException());
        }
        } finally { PacketSlot.Release(); }
    }

    internal static async Task PublishReceipt(string stage,string destination,string contents,CancellationToken token)
    {
        try { await File.WriteAllTextAsync(stage,contents,token); }
        catch (Exception error) when (!token.IsCancellationRequested) { throw new NetworkFailure("receipt-write",error); }
        try { await WindowsFilePublication.Replace(stage,destination,token); }
        catch(Exception error) when(!token.IsCancellationRequested) { throw new NetworkFailure("receipt-replace",error); }
    }

    private static byte[]? Arp(byte[] frame)
    {
        if (frame.Length < 42 || frame[14]!=0 || frame[15]!=1 || frame[16]!=8 || frame[17]!=0 || frame[18]!=6 || frame[19]!=4 || frame[20]!=0 || frame[21]!=1 || !frame.AsSpan(38,4).SequenceEqual(new byte[]{192,168,127,2})) return null;
        var result = new byte[42]; frame.AsSpan(6,6).CopyTo(result); GuestMac.CopyTo(result,6); result[12]=8; result[13]=6;
        frame.AsSpan(14,8).CopyTo(result.AsSpan(14)); result[21]=2; GuestMac.CopyTo(result,22); new byte[]{192,168,127,2}.CopyTo(result,28);
        frame.AsSpan(22,10).CopyTo(result.AsSpan(32)); return result;
    }

    private sealed class Wintun : IDisposable
    {
        [UnmanagedFunctionPointer(CallingConvention.Winapi,CharSet=CharSet.Unicode,SetLastError=true)] private delegate IntPtr Create(string name,string type,ref Guid id);
        [UnmanagedFunctionPointer(CallingConvention.Winapi)] private delegate void Close(IntPtr handle);
        [UnmanagedFunctionPointer(CallingConvention.Winapi,SetLastError=true)] private delegate IntPtr Start(IntPtr adapter,uint capacity);
        [UnmanagedFunctionPointer(CallingConvention.Winapi,SetLastError=true)] private delegate IntPtr ReceivePacket(IntPtr session,out uint size);
        [UnmanagedFunctionPointer(CallingConvention.Winapi)] private delegate void ReleasePacket(IntPtr session,IntPtr packet);
        [UnmanagedFunctionPointer(CallingConvention.Winapi,SetLastError=true)] private delegate IntPtr AllocatePacket(IntPtr session,uint size);
        [UnmanagedFunctionPointer(CallingConvention.Winapi)] private delegate void Luid(IntPtr adapter,out ulong luid);
        [DllImport("iphlpapi.dll")] private static extern uint ConvertInterfaceLuidToIndex(ref ulong luid,out uint index);
        private readonly IntPtr library,adapter,session;
        private readonly Close close,end; private readonly ReceivePacket receive; private readonly ReleasePacket release,send; private readonly AllocatePacket allocate;
        internal string Name { get; }
        internal uint Index { get; }
        private T Load<T>(string name) where T:Delegate => Marshal.GetDelegateForFunctionPointer<T>(NativeLibrary.GetExport(library,name));
        internal Wintun(Guid vm)
        {
            Name = "Cage-"+vm.ToString("N")[..8];
            library = NativeLibrary.Load(Path.Combine(AppContext.BaseDirectory,"wintun.dll"));
            close = Load<Close>("WintunCloseAdapter"); end = Load<Close>("WintunEndSession"); receive = Load<ReceivePacket>("WintunReceivePacket"); release = Load<ReleasePacket>("WintunReleaseReceivePacket"); send = Load<ReleasePacket>("WintunSendPacket"); allocate = Load<AllocatePacket>("WintunAllocateSendPacket");
            try
            {
                adapter = Load<Create>("WintunCreateAdapter")(Name,"Cage",ref vm);
                if (adapter==IntPtr.Zero) throw new Win32Exception(Marshal.GetLastPInvokeError());
                Load<Luid>("WintunGetAdapterLUID")(adapter,out var luid);
                if (ConvertInterfaceLuidToIndex(ref luid,out var index)!=0 || index==0) throw new InvalidDataException("network_adapter_invalid"); Index=index;
                session = Load<Start>("WintunStartSession")(adapter,4<<20);
                if (session==IntPtr.Zero) throw new Win32Exception(Marshal.GetLastPInvokeError());
            }
            catch(Exception error) { Dispose(); throw new NetworkFailure("driver",error); }
        }
        internal async Task Configure(CancellationToken token)
        {
            var script = $"$ErrorActionPreference='Stop';$stage='adapter';try{{$i={Index};$a=Get-NetAdapter -Name '{Name}';if($a.ifIndex -ne $i -or $a.Name -cne '{Name}'){{throw 'network_adapter_invalid'}};$stage='ip';New-NetIPAddress -InterfaceIndex $i -IPAddress '{IP}' -PrefixLength 24 -DefaultGateway '{Gateway}' -AddressFamily IPv4 | Out-Null;$stage='dns';Set-DnsClientServerAddress -InterfaceIndex $i -ServerAddresses '{Gateway}';$stage='interface';Set-NetIPInterface -InterfaceIndex $i -AddressFamily IPv4 -InterfaceMetric 1 -NlMtuBytes 1500";
            // New-NetIPAddress returns before Windows completes duplicate
            // address detection. A tentative address cannot send the probes.
            // Admit this exact adapter only when its real address and route
            // are usable; a timer by itself is never readiness evidence.
            script += $";$stage='preferred';$until=[DateTime]::UtcNow.AddSeconds(10);$ready=$false;while([DateTime]::UtcNow -lt $until){{$ip=Get-NetIPAddress -InterfaceIndex $i -AddressFamily IPv4 -IPAddress '{IP}' -ErrorAction SilentlyContinue;if($ip.AddressState -eq 'Duplicate'){{throw 'network_address_duplicate'}};$route=Get-NetRoute -InterfaceIndex $i -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue;$dns=Get-DnsClientServerAddress -InterfaceIndex $i -AddressFamily IPv4;$ready=($ip.AddressState -eq 'Preferred' -and (Get-NetAdapter -Name '{Name}').Status -eq 'Up' -and @($route|Where-Object {{$_.NextHop -eq '{Gateway}'}}).Count -gt 0 -and $dns.ServerAddresses -contains '{Gateway}');if($ready){{break}};Start-Sleep -Milliseconds 100}};if(!$ready){{throw 'network_address_not_ready'}}}}catch{{[Console]::Out.WriteLine((@{{stage=$stage;hresult=[int]$_.Exception.HResult;category=[string]$_.CategoryInfo.Category}}|ConvertTo-Json -Compress));exit 1}}";
            var info = new ProcessStartInfo(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows),@"System32\WindowsPowerShell\v1.0\powershell.exe")) { UseShellExecute=false,RedirectStandardInput=true,RedirectStandardOutput=true,RedirectStandardError=true,CreateNoWindow=true };
            foreach(var argument in new[]{"-NoLogo","-NoProfile","-NonInteractive","-Command","-"}) info.ArgumentList.Add(argument);
            using var process = Process.Start(info) ?? throw new InvalidDataException("network_configuration_failed");
            using var cancel = token.Register(()=>{try{process.Kill(true);}catch{}});
            var output = process.StandardOutput.ReadToEndAsync(token); var errors = process.StandardError.BaseStream.CopyToAsync(Stream.Null,token);
            await process.StandardInput.WriteLineAsync(script); process.StandardInput.Close(); await process.WaitForExitAsync(token); await Task.WhenAll(output,errors);
            if (process.ExitCode!=0)
            {
                var diagnostic=await output;
                try
                {
                    if(diagnostic.Length>4096)throw new InvalidDataException("network_diagnostic_invalid");
                    var detail=JsonNode.Parse(diagnostic) as JsonObject;
                    var stage=detail?["stage"]?.GetValue<string>();
                    if(stage is "adapter" or "ip" or "dns" or "interface" or "preferred")
                    {
                        var failure=new NetworkConfigurationFailure(detail?["hresult"]?.GetValue<int>() ?? 0);
                        throw new NetworkFailure("configure-"+stage,failure);
                    }
                }
                catch(NetworkFailure){throw;}
                catch(Exception error) when(error is System.Text.Json.JsonException or InvalidDataException or InvalidOperationException){ }
                throw new NetworkFailure("configure",new Win32Exception(process.ExitCode));
            }
        }
        internal byte[]? Receive()
        {
            var pointer = receive(session,out var size);
            if(pointer==IntPtr.Zero) return null;
            try { if(size is <20 or >65535) throw new InvalidDataException("network_packet_invalid"); var data=new byte[size];Marshal.Copy(pointer,data,0,(int)size);return data; }
            finally { release(session,pointer); }
        }
        internal void Send(ReadOnlySpan<byte> data)
        {
            if(data.Length is <20 or >65535) throw new InvalidDataException("network_packet_invalid");
            var pointer=allocate(session,(uint)data.Length);if(pointer==IntPtr.Zero)return;
            Marshal.Copy(data.ToArray(),0,pointer,data.Length);send(session,pointer);
        }
        public void Dispose() { if(session!=IntPtr.Zero)end(session);if(adapter!=IntPtr.Zero)close(adapter);NativeLibrary.Free(library); }
    }
    private sealed class NetworkConfigurationFailure:Exception{internal NetworkConfigurationFailure(int hresult):base("network_configuration_failed"){HResult=hresult;}}
    private sealed class NetworkFailure:Exception{internal NetworkFailure(string stage,Exception error):base("network_stage_failed",error){Stage=stage;HResult=error.HResult;}internal string Stage{get;}}
}
