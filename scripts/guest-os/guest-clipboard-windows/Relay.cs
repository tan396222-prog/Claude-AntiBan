using System.Net.Sockets;
using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.Json.Serialization;

namespace Cage.GuestClipboard;

internal sealed class WindowsProfile
{
    [JsonPropertyName("version")] public int Version { get; set; }
    [JsonPropertyName("vm_id")] public Guid VM { get; set; }
    [JsonPropertyName("gui_user_sid")] public string SID { get; set; } = "";
    [JsonPropertyName("gui_user")] public string? User { get; set; }
    [JsonPropertyName("instance_directory")] public string Directory { get; set; } = "";
    [JsonPropertyName("relay_executable")] public string Executable { get; set; } = "";
    [JsonPropertyName("pairing_file")] public string Pairing { get; set; } = "";
    [JsonPropertyName("network_pairing_file")] public string? NetworkPairing { get; set; }
    internal static WindowsProfile Load(string file)
    {
        PairingPrivate(file);
        var p = JsonSerializer.Deserialize<WindowsProfile>(File.ReadAllBytes(file), new JsonSerializerOptions { UnmappedMemberHandling = JsonUnmappedMemberHandling.Disallow }) ?? throw new InvalidDataException();
        if (p.Version != 1 || p.VM == Guid.Empty || !p.SID.StartsWith("S-1-5-21-", StringComparison.Ordinal) ||
            new[] { p.Directory, p.Executable, p.Pairing }.Any(path => !Path.IsPathFullyQualified(path) || path.IndexOfAny(['\0', '\r', '\n']) >= 0)) throw new InvalidDataException("profile_invalid");
        if (p.NetworkPairing != null && (!Path.IsPathFullyQualified(p.NetworkPairing) || p.NetworkPairing.IndexOfAny(['\0', '\r', '\n']) >= 0)) throw new InvalidDataException("profile_invalid");
        return p;
    }
    static void PairingPrivate(string file) => GuestClipboard.Pairing.PrivateFile(file);
}

internal static class Relay
{
    internal static string Phase { get; private set; } = "startup";
    internal static async Task Run(string[] args)
    {
        if (args.Length is < 3 or > 4 || !GuestOperations.Names.Contains(args[2]) || args.Length == 4 && !(args[2] == "export" && args[3] == "--paths-stdin")) throw new InvalidDataException("command_invalid");
        Phase = "profile"; var profile = WindowsProfile.Load(args[1]); var operation = args[2];
        Phase = "pairing";
        var privilegedNetwork = operation is "network-stream" or "network-settings";
        var pairing = privilegedNetwork ? profile.NetworkPairing ?? throw new InvalidDataException("network_pairing_unavailable") : profile.Pairing;
        var service = privilegedNetwork ? NetworkRuntime.Service : Protocol.Service;
        var secret = Pairing.Load(pairing, profile.VM, profile.SID, true);
        using var socket = HyperVEndPoint.Socket();
        using var lifetime = new CancellationTokenSource();
        Console.CancelKeyPress += (_, e) => { e.Cancel = true; lifetime.Cancel(); socket.Dispose(); };
        using var connect = CancellationTokenSource.CreateLinkedTokenSource(lifetime.Token); connect.CancelAfter(TimeSpan.FromSeconds(10));
        try
        {
            Phase = "connect";
            // ConnectEx used by Socket.ConnectAsync is not supported by the
            // Hyper-V provider. Basic Winsock Connect supports AF_HYPERV; close
            // the socket to interrupt the bounded connect when canceled.
            using (connect.Token.Register(socket.Dispose))
                await Task.Run(() => socket.Connect(new HyperVEndPoint(profile.VM, service)), CancellationToken.None).WaitAsync(connect.Token);
            using var network = new NetworkStream(socket, false); using var stream = new ReadAheadStream(network);
            Phase = "server_proof"; var challenge = await Protocol.Read(stream, 4096, connect.Token) ?? throw new EndOfStreamException();
            var nonce = challenge["nonce"]?.GetValue<string>() ?? ""; var session = challenge["session"]?.GetValue<string>() ?? "";
            if (challenge["vm_id"]?.GetValue<string>() != profile.VM.ToString("D") || challenge["sid"]?.GetValue<string>() != profile.SID ||
                !Protocol.EqualProof(Protocol.Proof(secret, "server", nonce, profile.VM, profile.SID, session, "", null), challenge["proof"]?.GetValue<string>())) throw new InvalidDataException("pairing_invalid");
            JsonNode? paths = null;
            if (operation == "export")
            {
                using var input = new MemoryStream(); var buffer = new byte[64 << 10]; int n;
                var source = Console.OpenStandardInput();
                while ((n = await source.ReadAsync(buffer, lifetime.Token)) > 0)
                { if (input.Length + n > Protocol.PathLimit) throw new InvalidDataException("selection_limit"); input.Write(buffer, 0, n); }
                paths = JsonNode.Parse(input.ToArray()); Protocol.Paths(paths);
            }
            Phase = "client_proof"; await Protocol.Write(stream, new() { ["vm_id"] = profile.VM.ToString("D"), ["sid"] = profile.SID, ["operation"] = operation,
                ["paths"] = paths, ["proof"] = Protocol.Proof(secret, "client", nonce, profile.VM, profile.SID, session, operation, paths) }, connect.Token);
            if (operation == "network-stream") connect.CancelAfter(TimeSpan.FromSeconds(20));
            Phase = "hello"; var hello = await Protocol.Read(stream, 16384, connect.Token) ?? throw new EndOfStreamException();
            if (hello["error"] != null || hello["version"]?.GetValue<int>() != 1 || hello["session"]?.GetValue<string>() != session) throw new IOException("guest_unavailable");
            Phase = "transfer"; if (operation == "status") { await Protocol.Write(Console.OpenStandardOutput(), hello, lifetime.Token); return; }
            if (operation != "stream" && operation != "diagnostics" && hello["available"]?.GetValue<bool>() != true) throw new IOException("requires_gui_login");
            if (operation == "import")
            {
                await Protocol.SendFrames(Console.OpenStandardInput(), stream, lifetime.Token);
                var result = await Protocol.Read(stream, Protocol.PathLimit, lifetime.Token) ?? throw new EndOfStreamException();
                Protocol.Paths(result["paths"]); await Protocol.Write(Console.OpenStandardOutput(), result, lifetime.Token);
            }
            else if (operation == "export")
            {
                await Protocol.ReceiveFrames(stream, Console.OpenStandardOutput(), lifetime.Token);
                var result = await Protocol.Read(stream, 4096, lifetime.Token) ?? throw new EndOfStreamException();
                if (result["complete"]?.GetValue<bool>() != true) throw new IOException("archive_incomplete");
            }
            else
            {
                if (operation == "stream") await Protocol.Write(Console.OpenStandardOutput(), Protocol.PublicHello(hello), lifetime.Token);
                // Packet stdout stays exclusively binary. The host consumes
                // this bounded receipt before it publishes network readiness.
                // It follows the authenticated service's completed TUN setup.
                if (operation == "network-stream")
                {
                    if (hello["os"]?.GetValue<string>() != "windows" || hello["network"]?.GetValue<string>() != "packet" ||
                        hello["vm_id"]?.GetValue<string>() != profile.VM.ToString("D") || hello["gui_user_sid"]?.GetValue<string>() != profile.SID)
                        throw new IOException("network_identity_invalid");
                    await Protocol.Write(Console.OpenStandardError(), new() { ["network_ready"] = true, ["version"] = 1,
                        ["vm_id"] = profile.VM.ToString("D"), ["gui_user_sid"] = profile.SID,
                        ["session"] = session, ["network"] = "packet", ["available"] = true }, lifetime.Token);
                }
                var input = Task.Run(async () => { await Console.OpenStandardInput().CopyToAsync(stream, lifetime.Token); socket.Shutdown(SocketShutdown.Send); });
                var output = stream.CopyToAsync(Console.OpenStandardOutput(), lifetime.Token);
                await output; lifetime.Cancel(); try { await input; } catch (OperationCanceledException) { }
            }
        }
        finally { CryptographicOperations.ZeroMemory(secret); }
    }
}
