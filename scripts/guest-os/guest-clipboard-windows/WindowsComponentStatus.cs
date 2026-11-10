using System.Text.Json.Nodes;

namespace Cage.GuestClipboard;

// Readiness describes verified installed prerequisites for on-demand use. It
// does not start applications, inspect accounts, or record an audio sample.
internal static class WindowsComponentStatus
{
    private sealed class Probe(Func<JsonObject> read)
    {
        internal readonly Func<JsonObject> Read=read;
        internal JsonObject? Value;
        internal bool Pending;
        internal long Checked;
        internal long CheckedTick;
    }
    private static readonly object Gate=new();
    private static readonly Dictionary<string,Probe> Probes=new(StringComparer.Ordinal)
    {
        ["desktop"]=new(ClaudeWindowRpc.ComponentStatus),
        ["cli"]=new(WindowsCliRpc.ComponentStatus),
        ["audio"]=new(WindowsAudioRpc.ComponentStatus),
        ["terminal"]=new(()=>State(File.Exists(TerminalRpc.PowerShellPath()),"terminal_components_missing")),
        ["project-mount"]=new(()=>State(ProjectRpc.ToolsPresent(),"project_components_missing")),
    };
    private static JsonObject State(bool installed,string reason)=>new(){["installed"]=installed,["ready"]=installed,["reason"]=installed?"":reason};
    internal static JsonObject Snapshot(JsonObject browser)
    {
        var available=SessionGuard.Available();
        var registered=GuestOperations.Capabilities().ToHashSet(StringComparer.Ordinal);
        var tick=Environment.TickCount64;
        var result=new JsonObject();
        lock(Gate)
        {
            foreach(var pair in Probes)
            {
                var probe=pair.Value;
                if(available&&!probe.Pending&&(probe.Value==null||tick-probe.CheckedTick>=15000))
                {
                    probe.Pending=true;
                    // Each category retains one worker until its native call
                    // finishes. A stalled COM provider cannot queue more work
                    // or block the authenticated GUI status reply.
                    _=Task.Factory.StartNew(()=>Refresh(pair.Key,probe),CancellationToken.None,TaskCreationOptions.LongRunning,TaskScheduler.Default);
                }
                var state=(JsonObject?)probe.Value?.DeepClone()??new JsonObject{["installed"]=false,["ready"]=false,["reason"]="component_check_pending"};
                if(pair.Key=="audio")
                {
                    var supported=Environment.OSVersion.Version.Build>=20348;
                    state["supported"]=supported;
                    if(probe.Value==null)state["installed"]=supported&&registered.Contains("audio");
                }
                state["registered"]=registered.Contains(pair.Key);state["checked_at_ms"]=probe.Checked;
                if(probe.Value!=null&&tick-probe.CheckedTick>30000){state["ready"]=false;state["reason"]="component_check_pending";}
                if(!registered.Contains(pair.Key)){state["ready"]=false;if(state["installed"]?.GetValue<bool>()==true)state["reason"]="component_restart_required";}
                if(!available){state["ready"]=false;state["reason"]="requires_gui_login";}
                result[pair.Key]=state;
            }
        }
        result["clipboard"]=new JsonObject{["registered"]=true,["installed"]=true,["ready"]=available,["reason"]=available?"":"requires_gui_login"};
        foreach(var name in new[]{"attachments","resources"})
            result[name]=new JsonObject{["registered"]=registered.Contains(name),["installed"]=registered.Contains(name),["ready"]=available&&registered.Contains(name),["reason"]=available?"":"requires_gui_login"};
        bool Flag(string name)=>browser[name]?.GetValue<bool>()==true;
        var browserInstalled=Flag("installed");var browserReady=available&&registered.Contains("browser")&&browserInstalled&&Flag("network_configured");
        result["browser"]=new JsonObject{["registered"]=registered.Contains("browser"),["installed"]=browserInstalled,["ready"]=browserReady,["reason"]=browserReady?"":!available?"requires_gui_login":!browserInstalled?"browser_components_missing":"browser_network_unavailable"};
        var editorInstalled=Flag("editor_installed");
        result["editor"]=new JsonObject{["registered"]=registered.Contains("browser"),["installed"]=editorInstalled,["ready"]=browserReady&&editorInstalled,["reason"]=!editorInstalled?"editor_components_missing":!browserReady?"browser_unavailable":""};
        var phoneInstalled=Flag("phone_installed");var phoneReady=available&&registered.Contains("browser")&&phoneInstalled&&Flag("phone_network_configured");
        result["phone-browser"]=new JsonObject{["supported"]=true,["registered"]=registered.Contains("browser"),["installed"]=phoneInstalled,["ready"]=phoneReady,["reason"]=!available?"requires_gui_login":!phoneInstalled?"phone_browser_components_missing":!Flag("phone_network_configured")?"browser_network_unavailable":!registered.Contains("browser")?"component_restart_required":""};
        var phoneDesktop=(JsonObject)result["desktop"]!.DeepClone();
        phoneDesktop["supported"]=true;
        result["phone-desktop"]=phoneDesktop;
        foreach (var name in new[] { "desktop-file-chooser", "desktop-downloads" })
        {
            var files = (JsonObject)result["desktop"]!.DeepClone();
            files["supported"] = true;
            if (files["ready"]?.GetValue<bool>() == true && ClipboardAgent.Current?.SessionEventsRegistered != true)
            {
                files["ready"] = false;
                files["reason"] = "component_restart_required";
            }
            result[name] = files;
        }
        result["legacy-mobile-migration"]=new JsonObject{["registered"]=false,["installed"]=false,["ready"]=false,["reason"]="not_implemented"};
        return result;
    }
    private static void Refresh(string name,Probe probe)
    {
        JsonObject value;
        try{value=probe.Read();}
        catch{value=new JsonObject{["installed"]=name=="audio"&&Environment.OSVersion.Version.Build>=20348,["ready"]=false,["reason"]=name+"_status_unavailable"};}
        lock(Gate){probe.Value=value;probe.Checked=DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();probe.CheckedTick=Environment.TickCount64;probe.Pending=false;}
    }
}
