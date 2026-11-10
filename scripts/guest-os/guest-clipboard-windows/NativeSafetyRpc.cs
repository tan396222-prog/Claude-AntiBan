using System.Globalization;
using System.Net;
using System.Net.Http;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using System.Runtime.CompilerServices;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text.Json.Nodes;

namespace Cage.GuestClipboard;

// Bounded probes use fixed destinations. The host accepts their results only
// alongside the current packet gate's own denial and diagnostic counters.
internal static class NativeSafetyRpc
{
    [ModuleInitializer] internal static void Register() => GuestOperations.Register("safety",Run);
    [DllImport("kernel32.dll",CharSet=CharSet.Unicode)] private static extern int GetSystemDefaultLocaleName(char[] value,int capacity);
    private sealed class Progress { internal string Stage="request"; }
    internal static async Task Run(Stream stream,CancellationToken token,Func<bool> available)
    {
        var progress=new Progress();
        try{await RunCore(stream,token,available,progress);}
        catch(Exception error) when(error is not OperationCanceledException)
        {
            var code=error switch { InvalidDataException=>"invalid_probe",UnauthorizedAccessException=>"access_denied",IOException=>"file_io",SocketException=>"socket",_=>"probe_failed" };
            await Protocol.Write(stream,new JsonObject{["error"]="safety_probe_failed",["stage"]=progress.Stage,["error_code"]=code,["hresult"]=unchecked((uint)error.HResult).ToString("x8"),["native_code"]=error is SocketException socket ? socket.NativeErrorCode : 0},token);
        }
    }
    private static async Task RunCore(Stream stream,CancellationToken token,Func<bool> available,Progress progress)
    {
        var input=await Protocol.Read(stream,16384,token) ?? throw new EndOfStreamException();
        if(!available())throw new InvalidDataException("requires_gui_login");
        var stage=input["stage"]?.GetValue<string>();
        if(stage is not ("environment" or "boundary" or "network"))throw new InvalidDataException("safety_request_invalid");
        progress.Stage="environment";TimeZoneInfo.ClearCachedData();
        var timezone=ObservedTimezone(TimeZoneInfo.Local.Id,input["timezone"]?.GetValue<string>());
        var system=new char[85];var count=GetSystemDefaultLocaleName(system,system.Length);
        var result=new JsonObject{["tz"]=timezone,["system_timezone"]=timezone,["process_locale"]=CultureInfo.CurrentCulture.Name,["system_locale"]=count>0 ? new string(system,0,count-1) : "",["locale_valid"]=count>0};
        if(stage=="network")
        {
            progress.Stage="network";
            result["egress"]=await Probe("egress",token);result["dns"]=await Probe("dns",token);
        }
        if(stage=="boundary")
        {
            var port=input["control_port"]?.GetValue<int>() ?? 0;
            if(port is <1 or >65535)throw new InvalidDataException("safety_request_invalid");
            progress.Stage="public-tcp";result["public_connected"]=await Connect("1.1.1.1",443,AddressFamily.InterNetwork,token);
            result["management_connected"]=await Connect("192.168.5.2",port,AddressFamily.InterNetwork,token);
            result["ipv6_connected"]=await Connect("2606:4700:4700::1111",443,AddressFamily.InterNetworkV6,token);
            result["ipv6_default_routes"]=NetworkInterface.GetAllNetworkInterfaces().Sum(n=>n.GetIPProperties().GatewayAddresses.Count(g=>g.Address.AddressFamily==AddressFamily.InterNetworkV6));
            var nonce=Convert.ToHexString(RandomNumberGenerator.GetBytes(8)).ToLowerInvariant();
            progress.Stage="udp";
            using(var udp=new UdpClient(AddressFamily.InterNetwork))
            {
                await udp.SendAsync(new byte[]{1,2,3},new IPEndPoint(IPAddress.Parse("8.8.8.8"),53),token);result["udp_attempted"]=true;
            }
            progress.Stage="dns";result["public_dns_answered"]=await PublicDNS(nonce,token);
            var id=input["project_id"]?.GetValue<string>() ?? "";var grant=input["grant"]?.GetValue<string>() ?? "";
            progress.Stage="project-grant";var directory=ProjectRpc.Resolve(id,grant);
            var name=input["shared_name"]?.GetValue<string>() ?? "";
            var challenge=input["challenge"]?.GetValue<string>() ?? "";
            if(name.Length is <1 or >255 || Path.GetFileName(name)!=name || !name.StartsWith(".cage-share-probe-",StringComparison.Ordinal) || challenge.Length!=64 || challenge.Any(c=>!char.IsAsciiHexDigit(c)))throw new InvalidDataException("safety_file_invalid");
            var file=Path.Combine(directory,name);
            progress.Stage="shared-read";var original=await File.ReadAllTextAsync(file,token);
            result["shared_file"]=original==challenge;
            progress.Stage="shared-write";if(original==challenge)await File.WriteAllTextAsync(file,challenge+":guest",token);
            // Probe the host's private sibling through the confined virtual root.
            // The client can select no absolute path or content to retrieve.
            progress.Stage="private-scope";var privateName=input["private_name"]?.GetValue<string>() ?? "";
            if(Path.GetFileName(privateName)!=privateName || !privateName.StartsWith(".cage-private-probe-",StringComparison.Ordinal))throw new InvalidDataException("safety_file_invalid");
            result["private_file"]=File.Exists(Path.Combine(directory,privateName)) ? "reachable" : "unreachable";
        }
        if(!available())throw new InvalidDataException("requires_gui_login");
        await Protocol.Write(stream,result,token);
    }
    internal static string ObservedTimezone(string windowsZone,string? requested)
    {
        // Several IANA names share one Windows zone. Keep the requested label
        // only when it maps to the zone that the OS actually reports.
        if(requested is { Length: >0 and <=128 } && TimeZoneInfo.TryConvertIanaIdToWindowsId(requested,out var expected) && string.Equals(expected,windowsZone,StringComparison.OrdinalIgnoreCase))return requested;
        return TimeZoneInfo.TryConvertWindowsIdToIanaId(windowsZone,out var iana) ? iana! : windowsZone;
    }
    private static async Task<bool> Connect(string host,int port,AddressFamily family,CancellationToken token)
    {
        using var timeout=CancellationTokenSource.CreateLinkedTokenSource(token);timeout.CancelAfter(TimeSpan.FromSeconds(3));
        using var client=new TcpClient(family);
        try{await client.ConnectAsync(IPAddress.Parse(host),port,timeout.Token);return true;}catch(SocketException){return false;}catch(OperationCanceledException)when(!token.IsCancellationRequested){return false;}
    }
    private static async Task<JsonObject> Probe(string path,CancellationToken token)
    {
        using var handler=new HttpClientHandler{UseProxy=false,AllowAutoRedirect=false};using var client=new HttpClient(handler){Timeout=TimeSpan.FromSeconds(15)};
        try
        {
            using var response=await client.GetAsync("http://198.18.0.254/"+path,HttpCompletionOption.ResponseHeadersRead,token);
            using var body=await response.Content.ReadAsStreamAsync(token);using var buffer=new MemoryStream();var chunk=new byte[1024];int n;
            while((n=await body.ReadAsync(chunk,token))!=0){if(buffer.Length+n>4096)throw new InvalidDataException("safety_response_limit");buffer.Write(chunk,0,n);}
            var data=JsonNode.Parse(buffer.ToArray()) as JsonObject;
            return new JsonObject{["status"]=(int)response.StatusCode,["ip"]=data?["ip"]?.GetValue<string>() ?? ""};
        }
        catch(HttpRequestException){return new JsonObject{["status"]=0,["ip"]=""};}
        catch(TaskCanceledException)when(!token.IsCancellationRequested){return new JsonObject{["status"]=0,["ip"]=""};}
    }
    private static async Task<bool> PublicDNS(string nonce,CancellationToken token)
    {
        using var timeout=CancellationTokenSource.CreateLinkedTokenSource(token);timeout.CancelAfter(TimeSpan.FromSeconds(2));
        using var client=new UdpClient(AddressFamily.InterNetwork);var query=new List<byte>{1,2,1,0,0,1,0,0,0,0,0,0};
        foreach(var label in (nonce+".example.com").Split('.')){query.Add((byte)label.Length);query.AddRange(Protocol.Utf8.GetBytes(label));}query.AddRange(new byte[]{0,0,1,0,1});
        await client.SendAsync(query.ToArray(),new IPEndPoint(IPAddress.Parse("192.168.127.1"),53),token);
        try{var reply=await client.ReceiveAsync(timeout.Token);return reply.Buffer.Length>=12 && (reply.Buffer[6]!=0 || reply.Buffer[7]!=0);}
        catch(SocketException){return false;}catch(OperationCanceledException)when(!token.IsCancellationRequested){return false;}
    }
}
