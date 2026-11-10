using System.Collections.Specialized;
using System.Diagnostics;
using System.Drawing.Imaging;
using System.Runtime.InteropServices;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Threading.Channels;

namespace Cage.GuestClipboard;

internal sealed class ClipboardAgent : Control
{
    internal static ClipboardAgent? Current { get; private set; }
    [DllImport("user32.dll")] static extern bool AddClipboardFormatListener(IntPtr window);
    [DllImport("user32.dll")] static extern bool RemoveClipboardFormatListener(IntPtr window);
    [DllImport("user32.dll")] static extern uint GetClipboardSequenceNumber();
    [DllImport("wtsapi32.dll", SetLastError = true)] private static extern bool WTSRegisterSessionNotification(IntPtr window, uint flags);
    [DllImport("wtsapi32.dll")] private static extern bool WTSUnRegisterSessionNotification(IntPtr window);
    internal readonly string Session = Guid.NewGuid().ToString("N");
    private long sessionEpoch = 1;
    internal long SessionEpoch => Interlocked.Read(ref sessionEpoch);
    internal bool SessionEventsRegistered { get; private set; }
    readonly string helper;
    readonly HashSet<string> approved = new(StringComparer.OrdinalIgnoreCase);
    readonly Queue<string[]> selections = new();
    sealed class CachedCopy(string id, string[] paths, bool pinned)
    {
        internal readonly string ID = id;
        internal readonly string[] Paths = paths;
        internal bool Pinned = pinned;
    }
    readonly Dictionary<string, CachedCopy> copies = new();
    readonly Dictionary<Guid, string[]> fileReaders = new();
    bool copiesLoaded, collectingCopies;
    DateTime nextCacheCheck;
    readonly System.Windows.Forms.Timer timer = new() { Interval = 250 };
    Channel<JsonObject>? subscriber;
    uint observed;
    bool seeded, reading, lastAvailable;
    CancellationTokenSource? selectionCopy;
    internal ClipboardAgent(string filesHelper)
    {
        Current = this;
        helper = filesHelper;
        CreateControl(); _ = Handle;
        if (!File.Exists(helper) || !AddClipboardFormatListener(Handle)) throw new InvalidOperationException("clipboard_agent_unavailable");
        SessionEventsRegistered = WTSRegisterSessionNotification(Handle, 0);
        lastAvailable = SessionGuard.Available(); observed = GetClipboardSequenceNumber();
        timer.Tick += (_, _) => Tick(); timer.Start();
    }
    protected override void WndProc(ref Message m)
    {
        if (m.Msg == 0x031D) Tick(); // WM_CLIPBOARDUPDATE
        if (m.Msg == 0x02B1) { Interlocked.Increment(ref sessionEpoch); Tick(); }
        base.WndProc(ref m);
    }
    protected override void Dispose(bool disposing)
    {
        if (disposing) { timer.Stop(); timer.Dispose(); RemoveClipboardFormatListener(Handle); if (SessionEventsRegistered) WTSUnRegisterSessionNotification(Handle); subscriber?.Writer.TryComplete(); }
        base.Dispose(disposing);
    }
    internal Task<T> GUI<T>(Func<T> action)
    {
        var result = new TaskCompletionSource<T>(TaskCreationOptions.RunContinuationsAsynchronously);
        BeginInvoke(() => { try { result.SetResult(action()); } catch (Exception ex) { result.SetException(ex); } });
        return result.Task;
    }
    internal Task<uint> Revision() => GUI(() => { SessionGuard.Assert(); return GetClipboardSequenceNumber(); });
    internal Task<bool> PasteImage(byte[] bytes, Action paste) => GUI(() =>
    {
        SessionGuard.Assert(); Protocol.Png(bytes);
        using var input = new MemoryStream(bytes); using var image = Image.FromStream(input, false, true);
        using var bitmap = new Bitmap(image);
        var data = new DataObject(); data.SetData("PNG", false, new MemoryStream(bytes)); data.SetImage(bitmap);
        Clipboard.SetDataObject(data, true); paste(); return true;
    });
    internal Task<bool> PasteFiles(string[] paths, Action paste) => GUI(() =>
    {
        SessionGuard.Assert(); Protocol.Paths(JsonSerializer.SerializeToNode(paths));
        Approve(paths); var list=new StringCollection();list.AddRange(paths);
        Clipboard.SetFileDropList(list);paste();return true;
    });
    // Private browser receipts confirm native Windows clipboard ownership.
    // Content still travels through the single observer, preserving PNG and
    // file selections instead of rewriting a browser's text-only result.
    internal Task<JsonObject> Receipt(uint previous, bool requireChange) => GUI(() =>
    {
        SessionGuard.Assert(); var revision = GetClipboardSequenceNumber();
        if (requireChange && revision == previous) throw new InvalidDataException("clipboard_copy_not_observed");
        return new JsonObject { ["revision"] = revision, ["changed"] = revision != previous, ["available"] = true };
    });
    internal Task<JsonObject> PreparePaste() => GUI(() =>
    {
        SessionGuard.Assert(); var revision = GetClipboardSequenceNumber();
        if (Clipboard.ContainsFileDropList()) { var paths = Protocol.Paths(JsonSerializer.SerializeToNode(Clipboard.GetFileDropList().Cast<string>().ToArray())); AssertApproved(paths); }
        else if (Clipboard.TryGetData<MemoryStream>("PNG", out var png) && png != null) Protocol.Png(png.ToArray());
        else if (Clipboard.ContainsImage())
        {
            using var image = Clipboard.GetImage() ?? throw new InvalidDataException("clipboard_image_unavailable");
            if ((long)image.Width * image.Height > 40_000_000) throw new InvalidDataException("image_limit");
            using var bytes = new MemoryStream(); image.Save(bytes, ImageFormat.Png); Protocol.Png(bytes.ToArray());
        }
        else if (Clipboard.ContainsText() && Protocol.Utf8.GetByteCount(Clipboard.GetText(TextDataFormat.UnicodeText)) > Protocol.TextLimit) throw new InvalidDataException("text_limit");
        if (GetClipboardSequenceNumber() != revision) throw new InvalidDataException("clipboard_selection_changed");
        return new JsonObject { ["revision"] = revision, ["available"] = true };
    });
    internal Task<JsonObject> WriteBrowserPng(uint expectedRevision, byte[] png) => GUI(() =>
    {
        SessionGuard.Assert(); Protocol.Png(png);
        if (GetClipboardSequenceNumber() != expectedRevision) throw new InvalidDataException("clipboard_selection_changed");
        using var input = new MemoryStream(png); using var image = Image.FromStream(input, false, true); using var bitmap = new Bitmap(image);
        if (GetClipboardSequenceNumber() != expectedRevision) throw new InvalidDataException("clipboard_selection_changed");
        var data = new DataObject(); data.SetData("PNG", false, new MemoryStream(png)); data.SetImage(bitmap); Clipboard.SetDataObject(data, true);
        observed = GetClipboardSequenceNumber(); seeded = true;
        Publish(new JsonObject { ["png"] = Convert.ToBase64String(png), ["source"] = "guest" });
        return new JsonObject { ["revision"] = observed, ["available"] = true };
    });
    internal Task<JsonObject> WriteBrowserText(uint expectedRevision, string text) => GUI(() =>
    {
        SessionGuard.Assert();
        if (Protocol.Utf8.GetByteCount(text) > Protocol.TextLimit) throw new InvalidDataException("text_limit");
        if (GetClipboardSequenceNumber() != expectedRevision) throw new InvalidDataException("clipboard_selection_changed");
        if (text.Length == 0) Clipboard.Clear(); else Clipboard.SetText(text, TextDataFormat.UnicodeText);
        observed = GetClipboardSequenceNumber(); seeded = true;
        Publish(new JsonObject { ["text"] = text, ["source"] = "guest" });
        return new JsonObject { ["revision"] = observed, ["available"] = true };
    });
    internal JsonObject Hello() => new() { ["version"] = 1, ["os"] = "windows", ["session"] = Session, ["formats"] = new JsonArray("text", "png", "files"), ["available"] = SessionGuard.Available(), ["file_release"] = true };
    internal Task<ChannelReader<JsonObject>> Subscribe() => GUI(() =>
    {
        if (subscriber != null) throw new InvalidOperationException("clipboard_busy");
        subscriber = Channel.CreateBounded<JsonObject>(new BoundedChannelOptions(16) { SingleReader = true, FullMode = BoundedChannelFullMode.Wait });
        seeded = false; observed = GetClipboardSequenceNumber();
        return subscriber.Reader;
    });
    internal Task<bool> Unsubscribe() => GUI(() => {
        subscriber?.Writer.TryComplete(); subscriber = null; seeded = false;
        foreach (var copy in copies.Values) copy.Pinned = false;
        CollectCopies(); return true;
    });
    void Publish(JsonObject message)
    {
        if (subscriber != null && subscriber.Writer.TryWrite(message)) return;
        if (message["files"] is not null) RetireCopies(Protocol.Paths(message["files"]));
        subscriber?.Writer.TryComplete(new IOException("clipboard_consumer_slow"));
    }
    internal void ReportError() => Publish(new() { ["error"] = SessionGuard.Available() ? "clipboard_selection_unavailable" : "requires_gui_login" });
    void Tick()
    {
        if (DateTime.UtcNow >= nextCacheCheck) { nextCacheCheck = DateTime.UtcNow.AddSeconds(1); CollectCopies(); }
        var available = SessionGuard.Available();
        if (available != lastAvailable) { Interlocked.Increment(ref sessionEpoch); lastAvailable = available; Publish(Protocol.PublicHello(Hello())); }
        if (!available) selectionCopy?.Cancel();
        if (!available || !seeded || subscriber == null || reading) return;
        var sequence = GetClipboardSequenceNumber();
        if (sequence == observed) return;
        ReadSelection(sequence);
    }
    async void ReadSelection(uint sequence)
    {
        reading = true;
        var capturedSubscriber = subscriber;
        var capturedSession = SessionEpoch;
        try
        {
            SessionGuard.Assert();
            JsonObject payload;
            if (Clipboard.ContainsFileDropList())
            {
                var paths = Clipboard.GetFileDropList().Cast<string>().ToArray();
                Protocol.Paths(JsonSerializer.SerializeToNode(paths));
                Approve(paths); // The native GUI selection itself is an explicit source grant.
                // This child inherits the actual GUI token, never Session0 or a
                // management administrator's access to the selected files.
                using var copying = new CancellationTokenSource(TimeSpan.FromMinutes(30)); selectionCopy = copying;
                string[] copied;
                try { copied = await FilePaths("copy", paths, copying.Token); }
                finally { if (selectionCopy == copying) selectionCopy = null; }
                if (!SessionGuard.Available() || GetClipboardSequenceNumber() != sequence || subscriber != capturedSubscriber || SessionEpoch != capturedSession || !seeded) { RetireCopies(copied); return; }
                Approve(copied);
                payload = new() { ["files"] = JsonSerializer.SerializeToNode(copied) };
            }
            else if (Clipboard.ContainsData("PNG") || Clipboard.ContainsImage())
            {
                byte[] bytes;
                if (Clipboard.TryGetData<MemoryStream>("PNG", out var png) && png != null) bytes = png.ToArray();
                else
                {
                    using var image = Clipboard.GetImage() ?? throw new InvalidDataException("clipboard_image_unavailable");
                    if ((long)image.Width * image.Height > 40_000_000) throw new InvalidDataException("image_limit");
                    using var output = new MemoryStream(); image.Save(output, ImageFormat.Png); bytes = output.ToArray();
                }
                Protocol.Png(bytes); payload = new() { ["png"] = Convert.ToBase64String(bytes) };
            }
            else
            {
                var text = Clipboard.ContainsText() ? Clipboard.GetText(TextDataFormat.UnicodeText) : "";
                if (Protocol.Utf8.GetByteCount(text) > Protocol.TextLimit) throw new InvalidDataException("text_limit");
                payload = new() { ["text"] = text };
            }
            if (!SessionGuard.Available() || GetClipboardSequenceNumber() != sequence) return;
            observed = sequence; payload["source"] = "guest"; Publish(payload);
        }
        catch { if (SessionGuard.Available()) { observed = sequence; Publish(new() { ["error"] = "clipboard_selection_unavailable" }); } }
        finally { reading = false; }
    }
    internal Task<bool> Apply(JsonObject message) => GUI(() =>
    {
        if (message["release"] is not null) { RetireCopies(Protocol.Paths(message["release"])); return true; }
        SessionGuard.Assert();
        selectionCopy?.Cancel();
        var serial = message["serial"]?.GetValue<long>() ?? 0;
        if (serial < 1 || new[] { "text", "png", "files" }.Count(message.ContainsKey) != 1) throw new InvalidDataException("clipboard_message_invalid");
        if (message["files"] != null)
        {
            var paths = Protocol.Paths(message["files"]); AssertApproved(paths);
            var list = new StringCollection(); list.AddRange(paths); Clipboard.SetFileDropList(list);
        }
        else if (message["png"] != null)
        {
            var bytes = Convert.FromBase64String(message["png"]!.GetValue<string>()); Protocol.Png(bytes);
            using var input = new MemoryStream(bytes); using var image = Image.FromStream(input, false, true);
            using var bitmap = new Bitmap(image);
            var data = new DataObject(); data.SetData("PNG", false, new MemoryStream(bytes)); data.SetImage(bitmap);
            Clipboard.SetDataObject(data, true);
        }
        else
        {
            var text = message["text"]!.GetValue<string>();
            if (Protocol.Utf8.GetByteCount(text) > Protocol.TextLimit) throw new InvalidDataException("text_limit");
            if (text.Length == 0) Clipboard.Clear(); else Clipboard.SetText(text, TextDataFormat.UnicodeText);
        }
        // The revision is saved on the STA before queued clipboard notifications
        // run. Only a completed host write receives an acknowledgement.
        observed = GetClipboardSequenceNumber(); seeded = true; Publish(new() { ["ack"] = serial }); return true;
    });
    void RememberCopy(JsonObject result)
    {
        var id = result["cache_id"]?.GetValue<string>();
        if (string.IsNullOrEmpty(id)) return;
        var paths = Protocol.Paths(result["paths"]);
        copies[id] = new CachedCopy(id, paths, true);
        _ = PublishCopy(id);
    }
    async Task PublishCopy(string id)
    {
        try { await Cache("publish", id); } catch { }
    }
    void RetireCopies(string[] paths)
    {
        foreach (var copy in copies.Values)
            if (copy.Paths.Any(path => paths.Contains(path, StringComparer.OrdinalIgnoreCase))) copy.Pinned = false;
        CollectCopies();
    }
    static bool References(string[] paths, string[] owned)
    {
        var root = Path.GetDirectoryName(owned[0])!;
        return paths.Any(path => Path.GetFullPath(path).Equals(root, StringComparison.OrdinalIgnoreCase)
            || Path.GetFullPath(path).StartsWith(root + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase)
            || root.StartsWith(Path.GetFullPath(path).TrimEnd(Path.DirectorySeparatorChar) + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase));
    }
    async Task<JsonObject> Cache(string action, string id = "")
    {
        var start = new ProcessStartInfo(helper) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardInput = true, RedirectStandardOutput = true, RedirectStandardError = true };
        start.ArgumentList.Add("cache"); start.ArgumentList.Add(JsonSerializer.Serialize(new { action, cache_id = id }));
        using var child = Process.Start(start) ?? throw new IOException("clipboard_cache_unavailable");
        child.StandardInput.Close();
        _ = child.StandardError.BaseStream.CopyToAsync(Stream.Null);
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(10));
        using var cancellation = timeout.Token.Register(() => { try { child.Kill(true); } catch { } });
        var result = await Protocol.Read(child.StandardOutput.BaseStream, Protocol.WireLimit, timeout.Token) ?? throw new IOException("clipboard_cache_failed");
        await child.WaitForExitAsync(timeout.Token);
        if (child.ExitCode != 0) throw new IOException("clipboard_cache_failed");
        return result;
    }
    async void CollectCopies()
    {
        if (collectingCopies || IsDisposed) return;
        collectingCopies = true;
        try
        {
            if (!copiesLoaded)
            {
                var previous = await Cache("list");
                foreach (var item in previous["selections"]!.AsArray())
                {
                    var id = item!["cache_id"]!.GetValue<string>();
                    copies.TryAdd(id, new CachedCopy(id, Protocol.Paths(item["paths"]), false));
                }
                copiesLoaded = true;
            }
            foreach (var copy in copies.Values.ToArray())
            {
                var selected = Clipboard.ContainsFileDropList() ? Clipboard.GetFileDropList().Cast<string>().ToArray() : [];
                if (copy.Pinned || References(selected, copy.Paths) || fileReaders.Values.Any(paths => References(paths, copy.Paths))) continue;
                try
                {
                    await Cache("release", copy.ID);
                    copies.Remove(copy.ID);
                    foreach (var path in copy.Paths) approved.Remove(path);
                }
                catch { /* Keep its creation receipt for an ownership-change retry. */ }
            }
        }
        catch { /* An unreadable clipboard retains its currently referenced files. */ }
        finally { collectingCopies = false; }
    }
    void Approve(string[] paths)
    {
        selections.Enqueue(paths);
        foreach (var path in paths) approved.Add(Path.GetFullPath(path));
        while (selections.Count > 100)
        {
            var old = selections.Dequeue();
            foreach (var path in old) if (!selections.Any(s => s.Contains(path, StringComparer.OrdinalIgnoreCase))) approved.Remove(Path.GetFullPath(path));
        }
    }
    void AssertApproved(string[] paths)
    {
        if (paths.Any(path => !approved.Contains(Path.GetFullPath(path)))) throw new InvalidDataException("selection_not_authorized");
    }
    Process StartHelper(string operation)
    {
        SessionGuard.Assert();
        var start = new ProcessStartInfo(helper) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardInput = true, RedirectStandardOutput = true, RedirectStandardError = true };
        start.ArgumentList.Add(operation); if (operation != "import") start.ArgumentList.Add("--paths-stdin");
        var process = Process.Start(start) ?? throw new IOException("file_helper_unavailable");
        _ = process.StandardError.BaseStream.CopyToAsync(Stream.Null); return process;
    }
    internal async Task<string[]> FilePaths(string operation, string[]? paths, CancellationToken token)
    {
        var reader = Guid.NewGuid();
        if (paths != null) await GUI(() => { fileReaders[reader] = paths; return true; });
        string[]? created = null;
        Process? child = null;
        try
        {
            child = StartHelper(operation);
            using var cancellation = token.Register(() => { try { child.Kill(true); } catch { } });
            if (paths != null) await JsonSerializer.SerializeAsync(child.StandardInput.BaseStream, paths, cancellationToken: token);
            child.StandardInput.Close();
            var result = await Protocol.Read(child.StandardOutput.BaseStream, Protocol.PathLimit, token) ?? throw new IOException("file_helper_failed");
            created = Protocol.Paths(result["paths"]);
            await GUI(() => { RememberCopy(result); return true; });
            await child.WaitForExitAsync(token);
            if (child.ExitCode != 0) throw new IOException("file_helper_failed");
            return created;
        }
        catch
        {
            if (child != null && !child.HasExited) { child.Kill(true); await child.WaitForExitAsync(); }
            await GUI(() => { if (created != null) RetireCopies(created); else copiesLoaded = false; return true; });
            throw;
        }
        finally { child?.Dispose(); await GUI(() => { fileReaders.Remove(reader); CollectCopies(); return true; }); }
    }
    internal async Task Import(Stream stream, CancellationToken token)
    {
        var capturedSubscriber = await GUI(() => subscriber);
        var capturedSession = SessionEpoch;
        using var child = StartHelper("import");
        using var cancellation = token.Register(() => { try { child.Kill(true); } catch { } });
        string[]? paths = null;
        try
        {
            await Protocol.ReceiveFrames(stream, child.StandardInput.BaseStream, token); child.StandardInput.Close();
            var result = await Protocol.Read(child.StandardOutput.BaseStream, Protocol.PathLimit, token) ?? throw new IOException("file_helper_failed");
            paths = Protocol.Paths(result["paths"]);
            await GUI(() => { RememberCopy(result); return true; });
            await child.WaitForExitAsync(token);
            if (child.ExitCode != 0) throw new IOException("file_helper_failed");
            await GUI(() => {
                SessionGuard.Assert();
                if (SessionEpoch != capturedSession || capturedSubscriber != null && subscriber != capturedSubscriber) throw new IOException("clipboard_session_changed");
                Approve(paths); return true;
            });
            await Protocol.Write(stream, new() { ["paths"] = JsonSerializer.SerializeToNode(paths) }, token);
        }
        catch
        {
            if (!child.HasExited) { child.Kill(true); await child.WaitForExitAsync(); }
            await GUI(() => { if (paths != null) RetireCopies(paths); else copiesLoaded = false; CollectCopies(); return true; });
            throw;
        }
    }
    internal async Task Export(Stream stream, JsonNode? selection, CancellationToken token)
    {
        var paths = Protocol.Paths(selection);
        var reader = Guid.NewGuid();
        await GUI(() => { SessionGuard.Assert(); AssertApproved(paths); fileReaders[reader] = paths; return true; });
        try
        {
            using var child = StartHelper("export");
            using var cancellation = token.Register(() => { try { child.Kill(true); } catch { } });
            await JsonSerializer.SerializeAsync(child.StandardInput.BaseStream, paths, cancellationToken: token); child.StandardInput.Close();
            await Protocol.SendFrames(child.StandardOutput.BaseStream, stream, token);
            await child.WaitForExitAsync(token); SessionGuard.Assert();
            await Protocol.Write(stream, new() { ["complete"] = child.ExitCode == 0 }, token);
        }
        finally { await GUI(() => { fileReaders.Remove(reader); CollectCopies(); return true; }); }
    }
}
