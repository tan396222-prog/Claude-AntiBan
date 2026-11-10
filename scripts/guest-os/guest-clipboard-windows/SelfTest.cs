using System.Buffers.Binary;
using System.Security.Cryptography;
using System.Runtime.InteropServices;
using System.Text.Json.Nodes;

namespace Cage.GuestClipboard;

internal static class SelfTest
{
    static void Assert(bool value) { if (!value) throw new Exception("self_test_failed"); }
    static void Reject(Action action) { try { action(); } catch { return; } throw new Exception("self_test_failed"); }
    internal static void Run()
    {
        ReceiptPublication();
        var driver = Path.Combine(AppContext.BaseDirectory, "wintun.dll");
        if (File.Exists(driver))
        {
            var library = NativeLibrary.Load(driver);
            try
            {
                foreach (var symbol in new[] { "WintunCreateAdapter", "WintunCloseAdapter", "WintunGetAdapterLUID", "WintunStartSession", "WintunEndSession", "WintunReceivePacket", "WintunReleaseReceivePacket", "WintunAllocateSendPacket", "WintunSendPacket" })
                    Assert(NativeLibrary.GetExport(library, symbol) != IntPtr.Zero);
            }
            finally { NativeLibrary.Free(library); }
        }
        var vm = Guid.NewGuid(); var endpoint = new HyperVEndPoint(vm, Protocol.Service);
        Assert(WindowsSystemSettings.BrowserLanguage(new JsonObject { ["locale"]="zh_CN.UTF-8",["browser_language"]="en-GB" })=="en-GB");
        Reject(()=>WindowsSystemSettings.BrowserLanguage(new JsonObject { ["browser_language"]="en-US';exit" }));
        Reject(()=>WindowsSystemSettings.BrowserLanguage(new JsonObject { ["locale"]="en_US.UTF-8" }));
        Assert(NativeSafetyRpc.ObservedTimezone("China Standard Time","Asia/Hong_Kong")=="Asia/Hong_Kong");
        Assert(NativeSafetyRpc.ObservedTimezone("China Standard Time","America/New_York")!="America/New_York");
        var publicHello = Protocol.PublicHello(new JsonObject { ["version"] = 1, ["os"] = "windows", ["available"] = true });
        Assert(publicHello["hello"]?["os"]?.GetValue<string>() == "windows" && !publicHello.ContainsKey("version"));
        using var native = HyperVEndPoint.Socket();
        var roundtrip = (HyperVEndPoint)endpoint.Create(endpoint.Serialize()); Assert(roundtrip.VM == vm && roundtrip.Service == Protocol.Service && endpoint.Serialize().Size == 36);
        var secret = RandomNumberGenerator.GetBytes(32); var sid = "S-1-5-21-1-2-3-1001";
        var proof = Protocol.Proof(secret, "server", "nonce", vm, sid, "session", "", null);
        Assert(Protocol.EqualProof(proof, proof)); Assert(!Protocol.EqualProof(proof, "invalid"));
        Assert(!Protocol.EqualProof(proof, Protocol.Proof(secret, "client", "nonce", vm, sid, "session", "", null)));
        Assert(!Protocol.EqualProof(proof, Protocol.Proof(secret, "server", "nonce", Guid.NewGuid(), sid, "session", "", null)));
        Assert(!Protocol.EqualProof(proof, Protocol.Proof(secret, "server", "nonce", vm, sid + "2", "session", "", null)));
        var selection = new JsonArray(@"C:\test\中文😀.txt");
        var first = Protocol.Proof(secret, "client", "nonce", vm, sid, "session", "export", selection);
        Assert(!Protocol.EqualProof(first, Protocol.Proof(secret, "client", "nonce", vm, sid, "session", "import", selection)));
        Assert(!Protocol.EqualProof(first, Protocol.Proof(secret, "client", "nonce", vm, sid, "session", "export", new JsonArray(@"C:\other.txt"))));
        Reject(() => Protocol.Paths(new JsonArray())); Reject(() => Protocol.Paths(new JsonArray("relative.txt")));
        Assert(Protocol.Paths(selection).Length == 1);
        var image = new byte[33]; new byte[] { 137, 80, 78, 71, 13, 10, 26, 10 }.CopyTo(image, 0); "IHDR"u8.CopyTo(image.AsSpan(12));
        BinaryPrimitives.WriteUInt32BigEndian(image.AsSpan(16, 4), 8000); BinaryPrimitives.WriteUInt32BigEndian(image.AsSpan(20, 4), 5000); Protocol.Png(image);
        BinaryPrimitives.WriteUInt32BigEndian(image.AsSpan(20, 4), 5001); Reject(() => Protocol.Png(image));
        var data = RandomNumberGenerator.GetBytes(200_000); using var encoded = new MemoryStream();
        Protocol.SendFrames(new MemoryStream(data), encoded, default).GetAwaiter().GetResult();
        encoded.Position = 0; using var decoded = new MemoryStream(); Protocol.ReceiveFrames(encoded, decoded, default).GetAwaiter().GetResult(); Assert(data.SequenceEqual(decoded.ToArray()));
        Reject(() => Protocol.ReceiveFrames(new MemoryStream(encoded.ToArray()[..^1]), Stream.Null, default).GetAwaiter().GetResult());
        Assert(AttachmentsRpc.NameValid("中文 O'Brien.txt"));
        foreach (var name in new[] { "../outside.txt", "CON.txt", "file.txt:stream", "trailing.", "C:\\private.txt", "bad\nname" }) Assert(!AttachmentsRpc.NameValid(name));
        using (var limited = new AttachmentsRpc.LimitedWriteStream(Stream.Null, 3))
        { limited.Write([1, 2, 3], 0, 3); Reject(() => limited.Write([4], 0, 1)); }
        using var json = new ReadAheadStream(new MemoryStream("{\"text\":\"中文😀\\nsecond\"}\n{\"ack\":2}\n"u8.ToArray()));
        Assert(Protocol.Read(json, 1024, default).GetAwaiter().GetResult()!["text"]!.GetValue<string>() == "中文😀\nsecond");
        Assert(Protocol.Read(json, 1024, default).GetAwaiter().GetResult()!["ack"]!.GetValue<int>() == 2);
        using var blank=new Bitmap(240,240);
        using(var paint=Graphics.FromImage(blank)){paint.Clear(Color.White);paint.FillRectangle(Brushes.Black,210,0,25,16);}
        Assert(!ClaudeGraphicsCapture.HasContent(blank));
        using(var paint=Graphics.FromImage(blank)){paint.FillRectangle(Brushes.Black,70,110,100,30);}
        Assert(ClaudeGraphicsCapture.HasContent(blank));
        var browserFrame = new JsonObject { ["jpeg"] = "page-pixels", ["target"] = "native-window", ["sequence"] = "cdp-first", ["width"] = 800, ["height"] = 600 };
        var published = BrowserWindowCapture.FrameReply((JsonObject)browserFrame.DeepClone(), null);
        var sequence = published["sequence"]!.GetValue<string>();
        Assert(published["target"]!.GetValue<string>() == "native-window");
        browserFrame["sequence"] = "cdp-next";
        Assert(BrowserWindowCapture.FrameReply((JsonObject)browserFrame.DeepClone(), sequence)["unchanged"]?.GetValue<bool>() == true);
        browserFrame["overlays"] = new JsonArray(new JsonObject { ["png"] = "native-menu-pixels", ["x"] = 20, ["y"] = 30, ["width"] = 50, ["height"] = 60 });
        var menu = BrowserWindowCapture.FrameReply((JsonObject)browserFrame.DeepClone(), sequence);
        Assert(menu["unchanged"] is null && menu["sequence"]!.GetValue<string>() != sequence);
        var withoutMenu = (JsonObject)browserFrame.DeepClone(); withoutMenu.Remove("overlays");
        Assert(BrowserWindowCapture.FrameReply(withoutMenu, menu["sequence"]!.GetValue<string>())["unchanged"] is null);
        browserFrame["target"] = "resized-native-window";
        Assert(BrowserWindowCapture.FrameReply(browserFrame, menu["sequence"]!.GetValue<string>())["unchanged"] is null);
        CryptographicOperations.ZeroMemory(secret);
    }

    private static void ReceiptPublication()
    {
        var directory=Path.Combine(Path.GetTempPath(),Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(directory);
        var destination=Path.Combine(directory,"receipt.json");var stage=Path.Combine(directory,"receipt.stage");
        try
        {
            File.WriteAllText(destination,"old-complete");
            using(var snapshot=new FileStream(destination,FileMode.Open,FileAccess.Read,FileShare.Read|FileShare.Delete))
            {
                NetworkRuntime.PublishReceipt(stage,destination,"new-complete",default).GetAwaiter().GetResult();
                using var reader=new StreamReader(snapshot);
                Assert(reader.ReadToEnd()=="old-complete" && File.ReadAllText(destination)=="new-complete");
            }
            using(var blocker=File.OpenRead(destination))
            {
                var release=Task.Run(async()=>{await Task.Delay(100);blocker.Dispose();});
                try { NetworkRuntime.PublishReceipt(stage,destination,"after-short-reader",default).GetAwaiter().GetResult(); }
                finally { release.GetAwaiter().GetResult(); }
            }
            Assert(File.ReadAllText(destination)=="after-short-reader");
            using(var blocker=File.OpenRead(destination))
                Reject(()=>NetworkRuntime.PublishReceipt(stage,destination,"must-not-publish",default).GetAwaiter().GetResult());
            Assert(File.ReadAllText(destination)=="after-short-reader");
        }
        finally { Directory.Delete(directory,true); }
    }
}
