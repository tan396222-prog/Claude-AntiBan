using System.Net.Sockets;
using System.ComponentModel;
using System.Security.Cryptography;
using System.Runtime.InteropServices;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Cage.GuestClipboard;

internal static class GuestOperations
{
    internal static readonly HashSet<string> Names = ["stream", "status", "import", "export", "terminal", "cli", "browser", "desktop", "attachments", "audio", "resources", "diagnostics", "versions", "safety", "project-mount", "network-stream", "network-settings"];
    static readonly Dictionary<string, Func<Stream, CancellationToken, Func<bool>, Task>> handlers = new(StringComparer.Ordinal);
    internal static void Register(string name, Func<Stream, CancellationToken, Func<bool>, Task> handler)
    {
        if (!Names.Contains(name) || name is "stream" or "status" or "import" or "export" || !handlers.TryAdd(name, handler))
            throw new InvalidOperationException("guest_operation_registration_invalid");
    }
    internal static string[] Capabilities() => ["clipboard", .. handlers.Keys.Order()];
    internal static Task Run(string name, Stream stream, CancellationToken token) => handlers.TryGetValue(name, out var handler)
        ? handler(stream, token, SessionGuard.Available) : throw new InvalidOperationException("guest_operation_unavailable");
}

internal static class Program
{
    [DllImport("kernel32.dll")] static extern bool FreeConsole();
    [STAThread]
    static int Main(string[] args)
    {
        try
        {
            if (args is ["self-test"]) { SelfTest.Run(); Console.WriteLine("Windows guest protocol tests passed."); return 0; }
            if (args.Length > 1 && args[0] == "--relay-profile") { Relay.Run(args).GetAwaiter().GetResult(); return 0; }
            if (args is ["pair", "--vm-id", var pairVM, "--sid", var pairSID, "--token-file", var source, "--output", var destination])
            { Pairing.Protect(source, destination, Guid.Parse(pairVM), pairSID); return 0; }
            if (args is ["serve-network", "--vm-id", var networkVM, "--sid", var networkSID, "--token-file", var networkKey])
            {
                using var networkLifetime = new CancellationTokenSource();
                Console.CancelKeyPress += (_, e) => { e.Cancel = true; networkLifetime.Cancel(); };
                NetworkRuntime.Run(Guid.Parse(networkVM), networkSID, networkKey, networkLifetime.Token).GetAwaiter().GetResult(); return 0;
            }
            if (args is not ["serve", "--vm-id", var vmText, "--sid", var sid, "--token-file", var tokenFile]) throw new InvalidDataException("command_invalid");
            var vm = Guid.Parse(vmText); SessionGuard.Target(vm, sid); GuestContext.VM = vm; GuestContext.SID = sid;
            FreeConsole();
            var secret = Pairing.Load(tokenFile, vm, sid, false);
            Application.SetHighDpiMode(HighDpiMode.SystemAware);
            using var lifetime = new CancellationTokenSource();
            using var agent = new ClipboardAgent(Path.Combine(AppContext.BaseDirectory, "cage-guest-files.exe"));
            using var listener = HyperVEndPoint.Socket();
            listener.Bind(new HyperVEndPoint(Protocol.Parent, Protocol.Service)); listener.Listen(64);
            GuestDiagnostics.Record("lifecycle", "started");
            Application.ApplicationExit += (_, _) => { lifetime.Cancel(); listener.Dispose(); CryptographicOperations.ZeroMemory(secret); };
            _ = Accept(listener, agent, vm, sid, secret, lifetime.Token);
            Application.Run(); return 0;
        }
        catch (Exception ex)
        {
            if (args.Length > 0 && args[0] is "serve" or "serve-network") GuestDiagnostics.Failure("startup", ex);
            var reason = ex switch
            {
                SocketException socketError => "socket_" + socketError.NativeErrorCode,
                Win32Exception nativeError => "win32_" + nativeError.NativeErrorCode,
                _ => ex.GetType().Name
            };
            Console.Error.WriteLine("Windows guest channel unavailable (" + Relay.Phase + ":" + reason + "); check the bound VM, GUI login, pairing and deployed tools."); return 1;
        }
    }
    static async Task Accept(Socket listener, ClipboardAgent agent, Guid vm, string sid, byte[] secret, CancellationToken token)
    {
        using var slots = new SemaphoreSlim(64);
        try
        {
            while (!token.IsCancellationRequested)
            {
                await slots.WaitAsync(token);
                var socket = await Task.Run(listener.Accept, CancellationToken.None).WaitAsync(token);
                _ = Task.Run(async () => { try { await Connection(socket, agent, vm, sid, secret, token); } finally { socket.Dispose(); slots.Release(); } }, CancellationToken.None);
            }
        }
        catch (OperationCanceledException) { }
        catch (ObjectDisposedException) { }
        catch { Application.Exit(); }
    }
    static JsonObject Status(ClipboardAgent agent, Guid vm, string sid)
    {
        var status = agent.Hello(); status["vm_id"] = vm.ToString("D"); status["gui_user_sid"] = sid;
        status["reason"] = SessionGuard.Available() ? "" : "requires_gui_login";
        status["capabilities"] = JsonSerializer.SerializeToNode(GuestOperations.Capabilities());
        status["setup_account_disabled"] = SetupAccount.Disabled();
        var browser=BrowserRpc.Status();status["browser"]=browser;
        status["components"]=WindowsComponentStatus.Snapshot(browser);return status;
    }
    static async Task Connection(Socket socket, ClipboardAgent agent, Guid vm, string sid, byte[] secret, CancellationToken lifetime)
    {
        using var network = new NetworkStream(socket, false);
        using var stream = new ReadAheadStream(network);
        using var handshake = CancellationTokenSource.CreateLinkedTokenSource(lifetime); handshake.CancelAfter(TimeSpan.FromSeconds(10));
        var authenticated = false; var operation = "";
        try
        {
            var nonce = Convert.ToBase64String(RandomNumberGenerator.GetBytes(32));
            await Protocol.Write(stream, new() { ["nonce"] = nonce, ["vm_id"] = vm.ToString("D"), ["sid"] = sid, ["session"] = agent.Session,
                ["proof"] = Protocol.Proof(secret, "server", nonce, vm, sid, agent.Session, "", null) }, handshake.Token);
            var request = await Protocol.Read(stream, Protocol.PathLimit + 4096, handshake.Token) ?? throw new EndOfStreamException();
            operation = request["operation"]?.GetValue<string>() ?? "";
            if (!GuestOperations.Names.Contains(operation) || request["vm_id"]?.GetValue<string>() != vm.ToString("D") || request["sid"]?.GetValue<string>() != sid ||
                !Protocol.EqualProof(Protocol.Proof(secret, "client", nonce, vm, sid, agent.Session, operation, request["paths"]), request["proof"]?.GetValue<string>()))
                throw new InvalidDataException("pairing_invalid");
            authenticated = true;
            if (operation == "status") { await Protocol.Write(stream, Status(agent, vm, sid), lifetime); return; }
            await Protocol.Write(stream, agent.Hello(), lifetime);
            if (operation == "diagnostics") { await GuestOperations.Run(operation, stream, lifetime); return; }
            if (operation == "stream") { await ClipboardStream(stream, agent, lifetime); return; }
            SessionGuard.Assert();
            using var activity = CancellationTokenSource.CreateLinkedTokenSource(lifetime);
            var guard = Task.Run(async () => { try { while (true) { await Task.Delay(100, activity.Token); if (!SessionGuard.Available()) { activity.Cancel(); return; } } } catch (OperationCanceledException) { } });
            try
            {
                if (operation == "import") await agent.Import(stream, activity.Token);
                else if (operation == "export") await agent.Export(stream, request["paths"], activity.Token);
                else await GuestOperations.Run(operation, stream, activity.Token);
            }
            finally { activity.Cancel(); await guard; }
        }
        catch (Exception error)
        {
            if (authenticated && operation != "diagnostics") GuestDiagnostics.Failure(operation, error);
            // Export is framed: an early EOF is a failure. Never write an error
            // JSON object into the middle of its binary frame stream.
            if (authenticated && operation != "export")
                try { using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(1)); await Protocol.Write(stream, new() { ["error"] = SessionGuard.Available() ? "guest_operation_failed" : "requires_gui_login" }, timeout.Token); } catch { }
        }
    }
    static async Task ClipboardStream(Stream stream, ClipboardAgent agent, CancellationToken lifetime)
    {
        using var channel = CancellationTokenSource.CreateLinkedTokenSource(lifetime);
        var events = await agent.Subscribe();
        var writer = Task.Run(async () =>
        {
            try { await foreach (var message in events.ReadAllAsync(channel.Token)) await Protocol.Write(stream, message, channel.Token); }
            finally { channel.Cancel(); }
        });
        try
        {
            while (await Protocol.Read(stream, Protocol.WireLimit, channel.Token) is { } request)
                try { await agent.Apply(request); }
                catch { await agent.GUI(() => { agent.ReportError(); return true; }); }
        }
        finally { channel.Cancel(); await agent.Unsubscribe(); try { await writer; } catch (OperationCanceledException) { } }
    }
}

internal static class GuestContext
{
    internal static Guid VM { get; set; }
    internal static string SID { get; set; } = "";
}

// Read buffering is independent from writes, which is essential for a duplex
// socket carrying JSON lines followed by binary archive frames.
internal sealed class ReadAheadStream(Stream inner) : Stream
{
    readonly byte[] buffer = new byte[64 << 10]; int position, count;
    internal async Task<byte[]?> Line(int limit, CancellationToken token)
    {
        using var line = new MemoryStream();
        while (true)
        {
            if (position == count)
            {
                count = await inner.ReadAsync(buffer, token); position = 0;
                if (count == 0) { if (line.Length == 0) return null; throw new EndOfStreamException(); }
            }
            var newline = buffer.AsSpan(position, count - position).IndexOf((byte)10);
            var n = newline < 0 ? count - position : newline;
            if (line.Length + n > limit) throw new InvalidDataException("message_limit");
            line.Write(buffer, position, n); position += n;
            if (newline >= 0) { position++; return line.ToArray(); }
        }
    }
    public override bool CanRead => true; public override bool CanSeek => false; public override bool CanWrite => true;
    public override long Length => throw new NotSupportedException(); public override long Position { get => throw new NotSupportedException(); set => throw new NotSupportedException(); }
    public override async ValueTask<int> ReadAsync(Memory<byte> destination, CancellationToken token = default)
    {
        if (destination.Length == 0) return 0;
        if (position == count) { count = await inner.ReadAsync(buffer, token); position = 0; if (count == 0) return 0; }
        var n = Math.Min(destination.Length, count - position); buffer.AsMemory(position, n).CopyTo(destination); position += n; return n;
    }
    public override int Read(byte[] bytes, int offset, int size) => ReadAsync(bytes.AsMemory(offset, size)).AsTask().GetAwaiter().GetResult();
    public override ValueTask WriteAsync(ReadOnlyMemory<byte> bytes, CancellationToken token = default) => inner.WriteAsync(bytes, token);
    public override void Write(byte[] bytes, int offset, int size) => inner.Write(bytes, offset, size);
    public override void Flush() => inner.Flush(); public override Task FlushAsync(CancellationToken token) => inner.FlushAsync(token);
    public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException(); public override void SetLength(long length) => throw new NotSupportedException();
}
