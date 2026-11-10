using System.Text.Json.Nodes;

namespace Cage.GuestClipboard;

// Browser clipboard staging. It accepts bytes and an already focused tab proof,
// never a filename, HWND, clipboard path or a general clipboard write command.
internal static class BrowserPasteInput
{
    private sealed record Pending(string Tab,string Target,string Focus,uint Revision,int Length,DateTime Created,MemoryStream Bytes)
    { internal int Sequence; internal string Mime = ""; internal readonly long Epoch = ClipboardAgent.Current?.SessionEpoch ?? 0; }
    private static readonly Dictionary<string,Pending> Transfers = new(StringComparer.Ordinal);
    internal static async Task<JsonObject> Start(JsonObject input,JsonObject proof,JsonObject focus)
    {
        Expire();
        var tab=input["id"]?.GetValue<string>()??"";var length=input["length"]?.GetValue<int>()??0;
        var mime=input["mime"]?.GetValue<string>()??"";
        if(Transfers.Count>=4||tab!=proof["id"]?.GetValue<string>()||length<0||length>(mime=="text/plain"?Protocol.TextLimit:Protocol.PngLimit)||mime is not ("" or "text/plain" or "image/png"))throw new InvalidDataException("image_target_invalid");
        var clipboard=ClipboardAgent.Current??throw new InvalidDataException("clipboard_unavailable");
        var revision=await clipboard.Revision();
        var id=Guid.NewGuid().ToString("N");
        Transfers[id]=new Pending(tab,proof["target"]!.GetValue<string>(),focus["focus"]!.GetValue<string>(),revision,length,DateTime.UtcNow,new MemoryStream(Math.Min(length,64<<10))){Mime=mime};
        return new JsonObject{["transfer"]=id};
    }
    internal static JsonObject Chunk(JsonObject input)
    {
        var pending=Get(input);var text=input["data"]?.GetValue<string>()??"";
        if(pending.Sequence==0&&pending.Mime=="")pending.Mime=input["mime"]?.GetValue<string>()??"";
        if(pending.Mime is not ("text/plain" or "image/png")||input["mime"] is not null&&input["mime"]!.GetValue<string>()!=pending.Mime)throw new InvalidDataException("image_chunk_invalid");
        var limit=pending.Mime=="text/plain"?Protocol.TextLimit:Protocol.PngLimit;
        if(pending.Length==0&&pending.Sequence==0)
        {
            var length=input["length"]?.GetValue<int>()??0;
            if(length<1||length>limit)throw new InvalidDataException("image_chunk_invalid");
            pending=pending with {Length=length};Transfers[input["transfer"]!.GetValue<string>()]=pending;
        }
        if(pending.Length>limit)throw new InvalidDataException("image_chunk_invalid");
        if(text.Length>87384||text.Any(char.IsWhiteSpace))throw new InvalidDataException("image_chunk_invalid");
        var bytes=Convert.FromBase64String(text);
        if(bytes.Length is <1 or >65536||pending.Bytes.Length+bytes.Length>pending.Length)throw new InvalidDataException("image_chunk_invalid");
        pending.Bytes.Write(bytes);pending.Sequence++;
        return new JsonObject{["ack"]=pending.Sequence};
    }
    internal static async Task<JsonObject> Finish(JsonObject input,JsonObject proof,JsonObject focus,Func<Task<JsonObject>> paste)
    {
        var pending=Get(input);Transfers.Remove(input["transfer"]!.GetValue<string>());
        using(pending.Bytes)
        {
            if(pending.Bytes.Length!=pending.Length||pending.Tab!=proof["id"]?.GetValue<string>()||pending.Target!=proof["target"]?.GetValue<string>()||pending.Focus!=focus["focus"]?.GetValue<string>())throw new InvalidDataException("image_target_changed");
            var clipboard=ClipboardAgent.Current??throw new InvalidDataException("clipboard_unavailable");
            var bytes=pending.Bytes.ToArray();
            JsonObject receipt;
            if(pending.Mime=="text/plain")receipt=await clipboard.WriteBrowserText(pending.Revision,Protocol.Utf8.GetString(bytes));
            else {Protocol.Png(bytes);receipt=await clipboard.WriteBrowserPng(pending.Revision,bytes);}
            await clipboard.PreparePaste();
            var result=await paste();
            if(result["error"] is not null)throw new InvalidDataException("image_paste_failed");
            result["clipboard"]=receipt;return result;
        }
    }
    internal static void Cancel(string id){if(Transfers.Remove(id,out var pending))pending.Bytes.Dispose();}
    private static Pending Get(JsonObject input)
    {
        Expire();var id=input["transfer"]?.GetValue<string>()??"";
        if(!Transfers.TryGetValue(id,out var pending)||input["sequence"]?.GetValue<int>()!=pending.Sequence||pending.Epoch!=ClipboardAgent.Current?.SessionEpoch)throw new InvalidDataException("image_transfer_changed");return pending;
    }
    private static void Expire(){foreach(var item in Transfers.ToArray())if(DateTime.UtcNow-item.Value.Created>TimeSpan.FromMinutes(2))Cancel(item.Key);}
    internal static void Close(){foreach(var item in Transfers.Values)item.Bytes.Dispose();Transfers.Clear();}
}
