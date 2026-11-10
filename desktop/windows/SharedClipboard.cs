using System.Collections.Specialized;
using System.Runtime.InteropServices;
using System.Text.Json;

namespace Cage.Windows;

internal static class SharedClipboard
{
    private static int generation;
    private static readonly Dictionary<string, string[]> copies = new();
    private static readonly List<string[]> readers = new();
    private static readonly HashSet<string> pending = new();
    private static readonly System.Windows.Forms.Timer timer = new() { Interval = 1000 };
    private static bool watching, loaded, collecting;

    public static void Cancel() => generation++;
    [DllImport("user32.dll")]
    private static extern uint GetClipboardSequenceNumber();
    public static uint Revision
    {
        get
        {
            if (!watching)
            {
                watching = true;
                timer.Tick += async (_, _) => await Collect();
                timer.Start();
            }
            return GetClipboardSequenceNumber();
        }
    }

    private static async Task<JsonElement> Cache(string action, string id = "")
    {
        var request = JsonSerializer.Serialize(new { action, cache_id = id });
        var result = await Backend.Json(["clipboard-files", "--json", "cache", request], CancellationToken.None, TimeSpan.FromSeconds(10), outputLimit: 30 << 20);
        if (result.Exit != 0) throw new IOException("clipboard_cache_operation_failed");
        return result.Data;
    }

    private static bool References(string[] selected, string[] owned)
    {
        var root = Path.GetDirectoryName(owned[0])!;
        return selected.Any(path => Path.GetFullPath(path).Equals(root, StringComparison.OrdinalIgnoreCase)
            || Path.GetFullPath(path).StartsWith(root + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase)
            || root.StartsWith(Path.GetFullPath(path).TrimEnd(Path.DirectorySeparatorChar) + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase));
    }

    private static async Task ReleaseLocal(string id)
    {
        try { await Cache("release", id); copies.Remove(id); }
        catch { /* The receipt remains available for the next ownership check. */ }
    }

    private static async Task Collect()
    {
        if (collecting) return;
        collecting = true;
        try
        {
            if (!loaded)
            {
                var result = await Cache("list");
                foreach (var item in result.GetProperty("selections").EnumerateArray())
                    copies.TryAdd(item.Text("cache_id"), item.GetProperty("paths").EnumerateArray().Select(path => path.GetString()!).ToArray());
                loaded = true;
            }
            foreach (var copy in copies.ToArray())
            {
                var selected = Clipboard.ContainsFileDropList() ? Clipboard.GetFileDropList().Cast<string>().ToArray() : [];
                if (pending.Contains(copy.Key) || References(selected, copy.Value) || readers.Any(paths => References(paths, copy.Value))) continue;
                await ReleaseLocal(copy.Key);
            }
        }
        catch { /* An unreadable OS clipboard retains its files. */ }
        finally { collecting = false; }
    }

    private static async Task ReleaseRemote(string connection, string id, CancellationToken token)
    {
        var response = await Backend.Json(["clipboard-files", "--json", "--connection", connection, "release", id], token, TimeSpan.FromSeconds(10));
        if (response.Exit != 0) throw new IOException("clipboard_selection_release_failed");
    }

    public static async Task<object> Release(JsonElement request, CancellationToken token)
    {
        var id = request.Text("files");
        if (!Guid.TryParse(id, out _)) throw new InvalidDataException("剪贴板文件标识无效");
        await ReleaseRemote(request.Text("connection_id"), id, token);
        return new { ok = true };
    }

    public static async Task<object> Read(JsonElement request, CancellationToken token)
    {
        var revision = Revision;
        var capturedGeneration = generation;
        if (Clipboard.ContainsFileDropList())
        {
            var selected = Clipboard.GetFileDropList().Cast<string>().ToArray();
            readers.Add(selected);
            try
            {
                var arguments = new List<string> { "clipboard-files", "--json", "--connection", request.Text("connection_id"), "send" };
                arguments.AddRange(selected);
                var response = await Backend.Json(arguments.ToArray(), token, TimeSpan.FromMinutes(30));
                if (response.Exit != 0) throw new IOException(response.Data.Text("message"));
                var files = response.Data.Text("files");
                if (capturedGeneration != generation || revision != Revision)
                {
                    try { await ReleaseRemote(request.Text("connection_id"), files, CancellationToken.None); } catch { }
                }
                return new { revision, files };
            }
            finally { readers.Remove(selected); await Collect(); }
        }
        if (Clipboard.ContainsImage())
        {
            using var image = Clipboard.GetImage() ?? throw new IOException("无法读取剪贴板图片");
            if ((long)image.Width * image.Height > 40_000_000) throw new IOException("图片不能超过 4000 万像素");
            using var output = new MemoryStream();
            image.Save(output, System.Drawing.Imaging.ImageFormat.Png);
            if (output.Length > 20 * 1024 * 1024) throw new IOException("图片不能超过 20 MB");
            return new { revision, png = Convert.ToBase64String(output.ToArray()) };
        }
        return new { revision, text = Clipboard.ContainsText() ? Clipboard.GetText() : "" };
    }

    public static async Task<object> Write(JsonElement request, CancellationToken token)
    {
        _ = Revision;
        var currentGeneration = generation;
        string[]? paths = null;
        var cache = "";
        if (request.Text("files") is { Length: > 0 } id)
        {
            if (!Guid.TryParse(id, out _)) throw new InvalidDataException("剪贴板文件标识无效");
            try
            {
                var response = await Backend.Json(["clipboard-files", "--json", "--connection", request.Text("connection_id"), "receive", id], token, TimeSpan.FromMinutes(30));
                if (response.Exit != 0) throw new IOException(response.Data.Text("message"));
                paths = response.Data.GetProperty("paths").EnumerateArray().Select(value => value.GetString()!).ToArray();
                cache = response.Data.Text("cache_id");
                if (cache.Length != 0) { copies[cache] = paths; pending.Add(cache); }
            }
            catch { loaded = false; throw; }
        }
        try
        {
            if (currentGeneration != generation || request.TryGetProperty("revision", out var expected) && expected.GetUInt32() != Revision)
            {
                if (cache.Length != 0) await ReleaseLocal(cache);
                return new { revision = Revision, superseded = true };
            }
            if (paths != null)
            {
                var files = new StringCollection(); files.AddRange(paths); Clipboard.SetFileDropList(files);
            }
            else if (request.Text("png") is { Length: > 0 } png)
            {
                var bytes = Convert.FromBase64String(png);
                if (bytes.Length > 20 * 1024 * 1024) throw new InvalidDataException("图片不能超过 20 MB");
                using var input = new MemoryStream(bytes);
                using var image = Image.FromStream(input);
                if ((long)image.Width * image.Height > 40_000_000) throw new InvalidDataException("图片不能超过 4000 万像素");
                Clipboard.SetImage(image);
            }
            else if (request.TryGetProperty("text", out var text))
            {
                var value = text.GetString() ?? "";
                if (System.Text.Encoding.UTF8.GetByteCount(value) > 2 * 1024 * 1024) throw new InvalidDataException("文本不能超过 2 MB");
                if (value.Length == 0) Clipboard.Clear(); else Clipboard.SetText(value);
            }
            else throw new InvalidDataException("剪贴板格式无效");
            var writtenRevision = Revision;
            if (cache.Length != 0) { try { await Cache("publish", cache); } catch { } }
            return new { revision = writtenRevision };
        }
        finally { pending.Remove(cache); await Collect(); }
    }
}
