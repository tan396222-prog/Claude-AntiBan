using System.Diagnostics;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;
using InputLease = Cage.GuestClipboard.NativeInputCoordinator.Lease;

namespace Cage.GuestClipboard;

// This subtree is separate from desktop Chrome. One enrollment owns the active
// phone ID; a disconnected connection never closes another ID or desktop tab.
internal static class WindowsPhoneBrowser
{
    private static readonly SemaphoreSlim Gate = new(1, 1);
    private static readonly Dictionary<InputLease, string> Owners = new();
    private static readonly Regex ID = new("^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$", RegexOptions.CultureInvariant);
    private static Process? process;
    private static GuestProcessJob? job;
    private static Stream? replies;
    private static string current = "";
    internal static bool Installed() => BrowserRpc.BrowserExecutableInstalled() && new[] { "node.exe", "windows-browser.mjs", "windows-browser-extensions.mjs", "windows-phone-browser.mjs", "playwright/index.mjs", "modules/browser-phone.mjs", "modules/browser-phone-network.mjs" }
        .All(file => File.Exists(Path.Combine(AppContext.BaseDirectory, file)));

    private static JsonObject Error(int status) => new() { ["error"] = "phone_browser_unavailable", ["code"] = "phone_browser_error", ["status"] = status };
    internal static async Task<JsonObject> Exchange(JsonObject request, CancellationToken connection, InputLease owner)
    {
        await Gate.WaitAsync(connection);
        try
        {
            var route = request["path"]?.GetValue<string>() ?? "";
            var input = request["input"] as JsonObject ?? new JsonObject();
            var id = input["id"]?.GetValue<string>() ?? "";
            if (!ID.IsMatch(id)) return Error(400);
            if (route != "/phone-browser/open" && current != id) return Error(410);
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(35));
            if (route == "/phone-browser/open")
            {
                if (!Installed()) return Error(501);
                var settings = await BrowserRpc.ReadConfiguration(timeout.Token, packetOnly: true);
                await Start(settings, timeout.Token);
            }
            if (process is not { HasExited: false }) return Error(410);
            var result = await Control(request, timeout.Token);
            if (result["error"] is null && route == "/phone-browser/open") { current = id; Owners[owner] = id; }
            if (result["error"] is null && route == "/phone-browser/close") { current = ""; foreach (var key in Owners.Where(p => p.Value == id).Select(p => p.Key).ToArray()) Owners.Remove(key); }
            return result;
        }
        catch (InvalidDataException) { return Error(503); }
        catch (Exception error) when (error is OperationCanceledException or EndOfStreamException or IOException)
        { Close(); return Error(503); }
        finally { Gate.Release(); }
    }
    internal static async Task Release(InputLease owner)
    {
        await Gate.WaitAsync();
        try
        {
            if (!Owners.Remove(owner, out var id) || Owners.Values.Contains(id) || current != id) return;
            try
            {
                using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
                await Control(new JsonObject { ["method"] = "POST", ["path"] = "/phone-browser/close", ["input"] = new JsonObject { ["id"] = id } }, timeout.Token);
                current = "";
            }
            catch { Close(); }
        }
        finally { Gate.Release(); }
    }
    private static async Task Start(JsonObject settings, CancellationToken token)
    {
        if (process is { HasExited: false }) return;
        Close();
        var start = new ProcessStartInfo(Path.Combine(AppContext.BaseDirectory, "node.exe"))
        {
            UseShellExecute = false, CreateNoWindow = true, WorkingDirectory = AppContext.BaseDirectory,
            RedirectStandardInput = true, RedirectStandardOutput = true, RedirectStandardError = true,
            StandardInputEncoding = Protocol.Utf8, StandardOutputEncoding = Protocol.Utf8, StandardErrorEncoding = Protocol.Utf8,
        };
        start.ArgumentList.Add(Path.Combine(AppContext.BaseDirectory, "windows-phone-browser.mjs"));
        process = Process.Start(start) ?? throw new InvalidDataException("browser_unavailable");
        try
        {
            job = new GuestProcessJob(process);
            replies = new ReadAheadStream(process.StandardOutput.BaseStream);
            var source = process.StandardError;
            _ = Task.Run(async () => { try { var buffer = new char[4096]; while (await source.ReadAsync(buffer) != 0) { } } catch (ObjectDisposedException) { } });
            var result = await Control(new JsonObject { ["operation"] = "configure", ["input"] = settings }, token);
            if (result["ok"]?.GetValue<bool>() != true) throw new InvalidDataException("browser_unavailable");
        }
        catch { Close(); throw; }
    }
    private static async Task<JsonObject> Control(JsonObject input, CancellationToken token)
    {
        await Protocol.Write(process!.StandardInput.BaseStream, input, token);
        return await Protocol.Read(replies!, 3 << 20, token) ?? throw new EndOfStreamException();
    }
    internal static void Close()
    {
        var owned = Interlocked.Exchange(ref process, null); current = ""; Owners.Clear();
        try { if (owned is not null) { owned.StandardInput.Close(); if (!owned.WaitForExit(3000)) owned.Kill(true); owned.WaitForExit(2000); } }
        catch (InvalidOperationException) { }
        finally { job?.Dispose(); job = null; replies?.Dispose(); replies = null; owned?.Dispose(); }
        // Enrollment profile directories remain intact.
    }
}
