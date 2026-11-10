using System.Diagnostics;
using System.Text;
using System.Text.Json;

namespace Cage.Windows;

internal static class Backend
{
    public static bool ClientOnly => File.Exists(Path.Combine(AppContext.BaseDirectory, "resources", "client-only"));
    private static string ConfigPath => Path.GetFullPath(
        Environment.GetEnvironmentVariable("CLAUDE_CAGE_CONFIG") is { Length: > 0 } configured
            ? configured : Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".claude-cage", ClientOnly ? "client" : "", "config.json"));
    public static string Root => Path.GetDirectoryName(ConfigPath)!;
    public static string Executable => Path.Combine(AppContext.BaseDirectory, "resources", "cage.exe");

    public static List<Process> CaptureSmokeControllers()
    {
        var processes = new List<Process>();
        try
        {
            foreach (var profile in new[] { Root, Path.Combine(Root, "desktop-entry") })
            {
                var record = Path.Combine(profile, "control.json");
                if (!File.Exists(record)) continue;
                using var data = JsonDocument.Parse(File.ReadAllText(record));
                Process process;
                try { process = Process.GetProcessById(data.RootElement.GetProperty("pid").GetInt32()); }
                catch (ArgumentException) { continue; } // It already exited.
                try
                {
                    // Pin the actual process handle before requesting shutdown.
                    // A released profile lock alone does not release its image.
                    _ = process.SafeHandle;
                    if (process.HasExited) { process.Dispose(); continue; }
                    if (!string.Equals(Path.GetFullPath(process.MainModule!.FileName), Path.GetFullPath(Executable), StringComparison.OrdinalIgnoreCase))
                        throw new IOException("自检后台身份与当前应用不符，未等待其他程序");
                    processes.Add(process);
                }
                catch { process.Dispose(); throw; }
            }
            return processes;
        }
        catch { foreach (var process in processes) process.Dispose(); throw; }
    }

    public static ProcessStartInfo StartInfo(string executable, params string[] arguments)
    {
        var start = new ProcessStartInfo(executable) {
            UseShellExecute = false, CreateNoWindow = true, RedirectStandardOutput = true,
            RedirectStandardError = true, StandardOutputEncoding = Encoding.UTF8, StandardErrorEncoding = Encoding.UTF8,
            WorkingDirectory = AppContext.BaseDirectory,
        };
        // Helpers execute outside the installed directory. Keep an explicitly
        // selected relative profile bound to the same absolute file.
        start.Environment["CLAUDE_CAGE_CONFIG"] = ConfigPath;
        if (arguments.Length > 0 && arguments[0] == "desktop") start.Environment["CAGE_DESKTOP_OWNER_PID"] = Environment.ProcessId.ToString();
        foreach (var arg in arguments) start.ArgumentList.Add(arg);
        return start;
    }

    public static Task<(int Exit, JsonElement Data)> Json(string[] arguments, CancellationToken cancellation = default, TimeSpan? deadline = null, int outputLimit = 65536) =>
        JsonAt(Executable, arguments, cancellation, deadline, outputLimit);

    public static async Task<(int Exit, JsonElement Data)> JsonAt(string executable, string[] arguments, CancellationToken cancellation = default, TimeSpan? deadline = null, int outputLimit = 65536)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellation);
        timeout.CancelAfter(deadline ?? TimeSpan.FromMinutes(3));
        using var process = new Process { StartInfo = StartInfo(executable, arguments) };
        if (!process.Start()) throw new IOException("无法启动 Cage 后台");
        using var abort = timeout.Token.Register(() => { try { if (!process.HasExited) process.Kill(); } catch (InvalidOperationException) { } });
        try
        {
            var errors = Drain(process.StandardError, timeout.Token);
            var text = await ReadLimited(process.StandardOutput, outputLimit, timeout.Token);
            await process.WaitForExitAsync(timeout.Token); await errors;
            try { return (process.ExitCode, JsonDocument.Parse(text).RootElement.Clone()); }
            catch (JsonException) { throw new IOException("Cage 后台未返回有效状态，请查看工作台日志"); }
        }
        finally
        {
            if (!process.HasExited) { process.Kill(); await process.WaitForExitAsync(); }
        }
    }

    public static async Task<string> ReadLimited(StreamReader reader, int limit, CancellationToken token)
    {
        var text = new StringBuilder(); var buffer = new char[4096]; int count;
        while ((count = await reader.ReadAsync(buffer.AsMemory(), token)) > 0)
        {
            text.Append(buffer, 0, count);
            if (text.Length > limit) throw new IOException("后台响应超过允许大小");
        }
        return text.ToString();
    }

    public static async Task Drain(StreamReader reader, CancellationToken token)
    {
        var buffer = new char[4096];
        while (await reader.ReadAsync(buffer.AsMemory(), token) > 0) { }
    }
    public static async Task Report(string message)
    {
        try
        {
            using var process = new Process { StartInfo = StartInfo(Executable, "diagnostic-event") };
            process.StartInfo.RedirectStandardInput = true;
            if (!process.Start()) return;
            await process.StandardInput.WriteAsync(JsonSerializer.Serialize(new { source = "native", message = message[..Math.Min(message.Length, 4096)] }));
            process.StandardInput.Close();
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
            await process.WaitForExitAsync(timeout.Token);
        }
        catch (Exception) { /* Logging failure must not recurse or replace the original error. */ }
    }
    public static string Text(this JsonElement data, string name) =>
        data.TryGetProperty(name, out var value) && value.ValueKind == JsonValueKind.String ? value.GetString()! : "";
}
