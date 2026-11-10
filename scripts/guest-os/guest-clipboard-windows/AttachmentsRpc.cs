using System.Runtime.CompilerServices;
using System.Collections.Concurrent;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text.Json.Nodes;

namespace Cage.GuestClipboard;

// Bytes enter a fixed directory owned by the logged-in guest user. Neither a
// guest path nor a command is accepted from the controller.
internal static class AttachmentsRpc
{
    private static readonly SemaphoreSlim Slots = new(12, 12);
    private sealed record Batch(string Directory, string Target, string Kind, double X, double Y)
    {
        internal readonly List<string> Paths = [];
        internal readonly DateTimeOffset Created = DateTimeOffset.UtcNow;
        internal readonly long Epoch = ClipboardAgent.Current?.SessionEpoch ?? 0;
        internal bool Receiving;
        internal Action? AssertChooser;
    }
    private static readonly ConcurrentDictionary<string, Batch> Batches = new();
    internal const long FileLimit = 2L << 30;
    [ModuleInitializer] internal static void Register() => GuestOperations.Register("attachments", Run);
    internal static bool NameValid(string name) => name.Length is > 0 and <= 255 && name is not "." and not ".." &&
        !name.EndsWith(' ') && !name.EndsWith('.') && name.IndexOfAny(Path.GetInvalidFileNameChars()) < 0 &&
        !new[] { "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9" }.Contains(name.Split('.')[0], StringComparer.OrdinalIgnoreCase);
    internal static string NewDirectory()
    {
        var home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        using var identity = WindowsIdentity.GetCurrent();
        var parent = Path.Combine(home, "Cage", "Attachments");
        var directory = Path.Combine(parent, Guid.NewGuid().ToString("N"));
        foreach (var path in new[] { Path.Combine(home, "Cage"), parent, directory })
        {
            Directory.CreateDirectory(path);
            var info = new DirectoryInfo(path);
            if (info.Attributes.HasFlag(FileAttributes.ReparsePoint) || info.GetAccessControl().GetOwner(typeof(SecurityIdentifier))?.Value != identity.User?.Value)
                throw new InvalidDataException("attachment_directory_invalid");
        }
        return directory;
    }
    internal static JsonObject StartBatch(string target, string kind, double x, double y)
    {
        foreach(var pair in Batches.Where(pair=>DateTimeOffset.UtcNow-pair.Value.Created>TimeSpan.FromMinutes(30))) CancelBatch(pair.Key);
        if(Batches.Count>=12 || kind is not ("paste" or "drop" or "image") || kind=="drop" && (!double.IsFinite(x) || !double.IsFinite(y) || x<0 || x>1 || y<0 || y>1))throw new InvalidDataException("attachment_request_invalid");
        var id=Guid.NewGuid().ToString("D"); var batch=new Batch(NewDirectory(),target,kind,x,y);
        if(!Batches.TryAdd(id,batch))throw new InvalidDataException("attachment_request_invalid");
        return new JsonObject { ["ok"]=true,["id"]=id };
    }
    // Only the authenticated dialog broker can supply this capability. The
    // JSON files-start endpoint continues to accept paste/drop alone.
    internal static JsonObject StartChooserBatch(string target,Action assertChooser)
    {
        assertChooser();
        foreach(var pair in Batches.Where(pair=>DateTimeOffset.UtcNow-pair.Value.Created>TimeSpan.FromMinutes(30)))CancelBatch(pair.Key);
        if(Batches.Count>=12)throw new InvalidDataException("attachment_request_limit");
        var id=Guid.NewGuid().ToString("D");var batch=new Batch(NewDirectory(),target,"chooser",0,0){AssertChooser=assertChooser};
        if(!Batches.TryAdd(id,batch))throw new InvalidDataException("attachment_request_invalid");
        try{assertChooser();return new JsonObject{["ok"]=true,["id"]=id};}catch{CancelBatch(id);throw;}
    }
    internal static void CancelBatch(string id)
    {
        if(!Batches.TryRemove(id,out var batch))return;
        lock(batch) { if(!batch.Receiving) try { Directory.Delete(batch.Directory,true); } catch { } }
    }
    internal static async Task FinishBatch(string id, Func<string,string,string[],double,double,Task> deliver)
    {
        if(!Batches.TryGetValue(id,out var batch))throw new InvalidDataException("attachment_request_invalid");
        string[] paths;
        lock(batch) { if(batch.Receiving || batch.Paths.Count==0 || batch.Epoch!=ClipboardAgent.Current?.SessionEpoch)throw new InvalidDataException("attachment_incomplete");batch.AssertChooser?.Invoke(); batch.Receiving=true;paths=batch.Paths.ToArray(); }
        try
        {
            await deliver(batch.Target,batch.Kind,paths,batch.X,batch.Y);
            Batches.TryRemove(id,out _); // Completed attachments remain user data.
            if(batch.Kind=="image")Directory.Delete(batch.Directory,true);
        }
        finally { lock(batch)batch.Receiving=false; }
    }
    private static async Task Run(Stream stream, CancellationToken token, Func<bool> available)
    {
        if (!available()) throw new InvalidDataException("requires_gui_login");
        if (!await Slots.WaitAsync(0, token)) throw new InvalidDataException("attachment_limit");
        string? directory = null; bool completed = false, ownsReceiving=false; Batch? batch=null;
        try
        {
            var request = await Protocol.Read(stream, 4096, token) ?? throw new EndOfStreamException();
            var name = request["name"]?.GetValue<string>() ?? "";
            if (request["action"]?.GetValue<string>() != "save" || !NameValid(name)) throw new InvalidDataException("attachment_request_invalid");
            var id=request["id"]?.GetValue<string>();
            if(id!=null)
            {
                if(!Batches.TryGetValue(id,out batch))throw new InvalidDataException("attachment_request_invalid");
                lock(batch) { if(batch.Receiving || batch.Paths.Count>=100)throw new InvalidDataException("attachment_limit");batch.AssertChooser?.Invoke();batch.Receiving=true;ownsReceiving=true; }
                directory=batch.Directory;
            }
            else directory = NewDirectory();
            var fileDirectory=directory;
            if(batch?.Kind=="chooser")
            {
                // Different selected host folders may contain the same name.
                // Each chooser file retains its original name in its own fixed
                // child directory; no uploaded path controls this location.
                fileDirectory=Path.Combine(directory,Guid.NewGuid().ToString("N"));
                Directory.CreateDirectory(fileDirectory);
                using var identity=WindowsIdentity.GetCurrent();var info=new DirectoryInfo(fileDirectory);
                if(info.Attributes.HasFlag(FileAttributes.ReparsePoint)||info.GetAccessControl().GetOwner(typeof(SecurityIdentifier))?.Value!=identity.User?.Value)
                    throw new InvalidDataException("attachment_directory_invalid");
                batch.AssertChooser?.Invoke();
            }
            var path = Path.Combine(fileDirectory, name);
            await using (var file = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.None, 64 << 10, FileOptions.Asynchronous))
            {
                using var bounded = new LimitedWriteStream(file, FileLimit);
                await Protocol.ReceiveFrames(stream, bounded, token);
                if (!available()) throw new InvalidDataException("requires_gui_login");
                await file.FlushAsync(token); file.Flush(true);
                batch?.AssertChooser?.Invoke();
            }
            if(batch!=null)
            {
                if(!Batches.TryGetValue(id!,out var current) || !ReferenceEquals(batch,current))throw new InvalidDataException("attachment_cancelled");
                lock(batch)batch.Paths.Add(path);
            }
            await Protocol.Write(stream, new JsonObject { ["ok"] = true, ["scope"] = "attachments", ["path"] = path }, token);
            completed = true;
        }
        finally
        {
            // This random directory was created for this transfer. Failed bytes
            // cannot appear as a completed attachment or replace an older file.
            if(ownsReceiving && batch!=null)lock(batch)batch.Receiving=false;
            var cancelled=ownsReceiving && batch!=null && !Batches.Values.Any(current=>ReferenceEquals(current,batch));
            if ((!completed || cancelled) && directory != null)
            {
                foreach(var pair in Batches.Where(pair=>ReferenceEquals(pair.Value,batch)))Batches.TryRemove(pair.Key,out _);
                try { Directory.Delete(directory, true); } catch { }
            }
            Slots.Release();
        }
    }
    internal sealed class LimitedWriteStream(Stream inner, long limit) : Stream
    {
        private long written;
        public override bool CanRead => false; public override bool CanSeek => false; public override bool CanWrite => true;
        public override long Length => written; public override long Position { get => written; set => throw new NotSupportedException(); }
        public override void Flush() => inner.Flush();
        public override Task FlushAsync(CancellationToken token) => inner.FlushAsync(token);
        public override void Write(byte[] buffer, int offset, int count) { Check(count); inner.Write(buffer, offset, count); }
        public override async ValueTask WriteAsync(ReadOnlyMemory<byte> buffer, CancellationToken token = default) { Check(buffer.Length); await inner.WriteAsync(buffer, token); }
        private void Check(int count) { if (count < 0 || written > limit - count) throw new InvalidDataException("attachment_size_limit"); written += count; }
        public override int Read(byte[] buffer, int offset, int count) => throw new NotSupportedException();
        public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();
        public override void SetLength(long value) => throw new NotSupportedException();
    }
}
