using System.Security.AccessControl;
using System.Runtime.CompilerServices;
using System.Security.Principal;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace Cage.GuestClipboard;

internal static class GuestDiagnostics
{
    private const long FileBytes = 1 << 20;
    private static readonly object Sync = new();
    private static readonly Regex Code = new("^[a-zA-Z][a-zA-Z0-9_]{0,95}$", RegexOptions.CultureInvariant);
    private static string last = "";
    private static DateTime lastAt;

    private static IEnumerable<string> Files(string directory) => Enumerable.Range(0, 10)
        .Select(index => Path.Combine(directory, index == 0 ? "events.jsonl" : $"events-{index}.jsonl"))
        .Where(File.Exists);

    [ModuleInitializer]
    internal static void Register() => GuestOperations.Register("diagnostics", Read);

    private static string DirectoryPath()
    {
        var directory = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Cage", "Diagnostics");
        foreach (var current in new[] { Path.GetDirectoryName(directory)!, directory })
        {
            Directory.CreateDirectory(current);
            if (new DirectoryInfo(current).Attributes.HasFlag(FileAttributes.ReparsePoint)) throw new IOException("diagnostic_path_invalid");
        }
        using var identity = WindowsIdentity.GetCurrent();
        var acl = new DirectorySecurity();
        acl.SetAccessRuleProtection(true, false);
        foreach (var sid in new[] { identity.User!, new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null) })
            acl.AddAccessRule(new FileSystemAccessRule(sid, FileSystemRights.FullControl,
                InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit, PropagationFlags.None, AccessControlType.Allow));
        new DirectoryInfo(directory).SetAccessControl(acl);
        return directory;
    }

    private static void Clean(string directory)
    {
        foreach (var file in Files(directory))
        {
            var info = new FileInfo(file);
            if (info.Attributes.HasFlag(FileAttributes.ReparsePoint)) throw new IOException("diagnostic_path_invalid");
            if (info.LastWriteTimeUtc < DateTime.UtcNow.AddDays(-14)) File.Delete(file);
        }
    }

    internal static void Record(string source, string reason)
    {
        if (!Code.IsMatch(source) || !Code.IsMatch(reason)) return;
        try
        {
            lock (Sync)
            {
                var now = DateTime.UtcNow;
                var key = source + "/" + reason;
                if (key == last && now - lastAt < TimeSpan.FromSeconds(5)) return;
                var directory = DirectoryPath();
                Clean(directory);
                var file = Path.Combine(directory, "events.jsonl");
                var entry = JsonSerializer.Serialize(new { time = now, source, reason }) + "\n";
                if (File.Exists(file) && (new FileInfo(file).Length + Protocol.Utf8.GetByteCount(entry) > FileBytes || File.GetCreationTimeUtc(file).Date != now.Date))
                {
                    for (var index = 9; index >= 1; index--)
                    {
                        var old = Path.Combine(directory, index == 1 ? "events.jsonl" : $"events-{index - 1}.jsonl");
                        var target = Path.Combine(directory, $"events-{index}.jsonl");
                        if (File.Exists(old)) File.Move(old, target, true);
                    }
                }
                File.AppendAllText(file, entry, Protocol.Utf8);
                last = key;
                lastAt = now;
            }
        }
        catch { /* Diagnostic storage failure must preserve the original operation. */ }
    }

    internal static void Failure(string source, Exception error) => Record(source, error.GetType().Name);

    private static async Task Read(Stream stream, CancellationToken token, Func<bool> available)
    {
        var request = await Protocol.Read(stream, 1024, token);
        if (request?["action"]?.GetValue<string>() != "read") throw new InvalidDataException("diagnostic_request_invalid");
        var lines = new Queue<string>();
        var bytes = 0;
        lock (Sync)
        {
            var directory = DirectoryPath();
            Clean(directory);
            // Rotation numbers preserve the write order even when file timestamps tie.
            foreach (var file in Files(directory).Reverse())
            {
                if (new FileInfo(file).Length > FileBytes + 4096) continue;
                foreach (var line in File.ReadLines(file, Protocol.Utf8))
                {
                    if (line.Length > 4096) continue;
                    try
                    {
                        using var item = JsonDocument.Parse(line);
                        var value = item.RootElement;
                        var source = value.GetProperty("source").GetString() ?? "";
                        var reason = value.GetProperty("reason").GetString() ?? "";
                        var time = value.GetProperty("time").GetDateTime();
                        if (!Code.IsMatch(source) || !Code.IsMatch(reason) || time < DateTime.UtcNow.AddDays(-14)) continue;
                        var safe = JsonSerializer.Serialize(new { time, source, reason }) + "\n";
                        lines.Enqueue(safe);
                        bytes += Protocol.Utf8.GetByteCount(safe);
                        while (lines.Count > 150 || bytes > 48 << 10) bytes -= Protocol.Utf8.GetByteCount(lines.Dequeue());
                    }
                    catch (Exception error) when (error is JsonException or InvalidOperationException or KeyNotFoundException or FormatException) { }
                }
            }
        }
        await Protocol.Write(stream, new() { ["version"] = 1, ["lines"] = string.Concat(lines) }, token);
    }
}
