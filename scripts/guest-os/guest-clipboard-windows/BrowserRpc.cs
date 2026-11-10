using System.Diagnostics;
using System.Collections.Concurrent;
using System.Runtime.CompilerServices;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text.Json.Nodes;

namespace Cage.GuestClipboard;

// A viewer connection owns its requests. The GUI agent owns the persistent
// browser process, so reconnecting or locking the desktop preserves its tabs.
internal static class BrowserRpc
{
    private static readonly SemaphoreSlim Gate = new(1, 1);
    private static Process? browser;
    private static GuestProcessJob? browserJob;
    private static Stream? replies;
    private static Task? errors;
    private static bool ready;
    private static bool wirePending;
    private static JsonObject? cachedPageFrame;
    private static NativeInputCoordinator.Lease? pagePointerOwner;
    private static bool pagePointerReleasePending;
    private static JsonObject features = new();
    private static Func<bool>? packetReadiness;
    private static string active = "";
    private static readonly ConcurrentDictionary<string, WindowsEditorServer.Connection> EditorTabs = new(StringComparer.Ordinal);
    private static readonly ConcurrentDictionary<string, WindowsEditorServer.Connection> EditorConnections = new(StringComparer.Ordinal);
    private static readonly ConcurrentDictionary<string, uint> CopyRevisions = new(StringComparer.Ordinal);
    private static readonly HashSet<string> Routes = ["health", "tabs", "events", "frame", "new", "activate", "close", "navigate", "back", "forward", "reload", "homepage", "resize", "insert-text", "copy-context", "ime", "copy", "cut", "paste", "select-all", "key", "pointer", "click", "wheel", "input", "editor", "editor-restart", "upload-start", "upload-finish", "upload-cancel", "download-retry", "info", "cookies", "settings", "internal-page", "extensions", "split", "audit", "vault", "login-open", "login-finish", "file-open", "file-chunk", "file-finish", "file-read", "file-close", "image-start", "image-chunk", "image-finish", "image-cancel"];

    [ModuleInitializer]
    internal static void Register()
    {
        Routes.UnionWith(["paste-start", "paste-chunk", "paste-finish", "paste-cancel", "tailnet", "phone-browser/open", "phone-browser/frame", "phone-browser/action", "phone-browser/close"]);
        GuestOperations.Register("browser", Run);
        ProjectRpc.Revoked += (project, grant) =>
        {
            WindowsEditorServer.Revoke(project, grant);
            _ = RevokeNode(project, grant);
        };
        AppDomain.CurrentDomain.ProcessExit += (_, _) => { WindowsPhoneBrowser.Close(); WindowsEditorServer.Close(); Close(); };
    }
    // The packet runtime supplies its System-owned receipt verifier. A config
    // string alone can never enable direct TCP access in a fresh browser.
    internal static void RegisterPacketReadiness(Func<bool> verifier)
    {
        if (Interlocked.CompareExchange(ref packetReadiness, verifier, null) is not null) throw new InvalidOperationException("packet_verifier_already_registered");
    }
    private static bool PacketReady()
    {
        if (packetReadiness is not null) return packetReadiness();
        try
        {
            var vm = GuestContext.VM;
            if (vm == Guid.Empty || GuestContext.SID.Length == 0) return false;
            var root = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles), "Cage Guest Network", vm.ToString("D"));
            var file = new FileInfo(Path.Combine(root, "network-ready.json"));
            if (!file.Exists || file.Length is < 1 or > 16384 || file.Attributes.HasFlag(FileAttributes.ReparsePoint) || new DirectoryInfo(root).Attributes.HasFlag(FileAttributes.ReparsePoint)) return false;
            var acl = file.GetAccessControl();
            var owner = acl.GetOwner(typeof(SecurityIdentifier))?.Value;
            if (owner is not ("S-1-5-18" or "S-1-5-32-544")) return false;
            const FileSystemRights writes = FileSystemRights.WriteData | FileSystemRights.AppendData | FileSystemRights.WriteAttributes | FileSystemRights.WriteExtendedAttributes | FileSystemRights.Delete | FileSystemRights.ChangePermissions | FileSystemRights.TakeOwnership;
            foreach (FileSystemAccessRule rule in acl.GetAccessRules(true, true, typeof(SecurityIdentifier)))
                if (rule.AccessControlType == AccessControlType.Allow && (rule.FileSystemRights & writes) != 0 && rule.IdentityReference.Value is not ("S-1-5-18" or "S-1-5-32-544")) return false;
            // The SYSTEM writer atomically replaces this file every second.
            // Keep one immutable snapshot while permitting that replacement;
            // File.ReadAllText's default sharing can block the writer on NTFS.
            using var snapshot = new FileStream(file.FullName, FileMode.Open, FileAccess.Read, FileShare.Read | FileShare.Delete);
            if (snapshot.Length is < 1 or > 16384) return false;
            using var reader = new StreamReader(snapshot);
            var receipt = JsonNode.Parse(reader.ReadToEnd()) as JsonObject;
            var updated = receipt?["updated_at"]?.GetValue<long>() ?? 0;
            var age = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() - updated;
            return receipt?["version"]?.GetValue<int>() == 1 && Guid.TryParse(receipt["vm_id"]?.GetValue<string>(), out var receivedVM) && receivedVM == vm &&
                receipt["gui_user_sid"]?.GetValue<string>() == GuestContext.SID && receipt["network"]?.GetValue<string>() == "packet" &&
                receipt["adapter"]?.GetValue<string>() == "Cage-" + vm.ToString("N")[..8] && receipt["interface_index"]?.GetValue<int>() > 0 &&
                receipt["session"]?.GetValue<string>() is { Length: > 0 and <= 128 } && age is >= -1000 and <= 5000;
        }
        catch { return false; }
    }

    internal static async Task Run(Stream stream, CancellationToken token, Func<bool> sessionAvailable)
    {
        var lease=BrowserWindowCapture.OpenLease();Task<JsonObject>? transaction=null;
        try
        {
          while (await Protocol.Read(stream, 128 << 10, token) is { } request)
          {
            if (!sessionAvailable()) throw new InvalidDataException("requires_gui_login");
            Validate(request);
            // A canceled viewer leaves the reply drain running. Otherwise its
            // pending reply could become the next viewer's browser response.
            transaction = Exchange(request, token, lease);
            JsonObject result;
            try { result = await transaction.WaitAsync(token); }
            catch (OperationCanceledException)
            {
                throw;
            }
            catch (InvalidDataException error)
            {
                var known = new HashSet<string> { "project_grant_unavailable", "editor_components_missing", "editor_start_failed", "editor_start_busy", "clipboard_copy_not_observed", "selection_not_authorized", "browser_network_unavailable", "browser_components_missing", "requires_gui_login", "editor_package_invalid", "browser_unavailable", "browser_response_timeout", "browser_target_changed", "browser_window_unavailable", "browser_process_unavailable", "browser_capture_surface_unavailable", "browser_focus_required", "browser_frame_limit", "desktop_target_changed", "desktop_empty_frame", "desktop_capture_buffer_invalid", "desktop_graphics_device_unavailable", "desktop_capture_permission_required", "desktop_capture_geometry_invalid" };
                var code = known.Contains(error.Message) ? error.Message : "browser_operation_failed";
                GuestDiagnostics.Record("browser", code);
                result = new JsonObject { ["error"] = code, ["code"] = code };
            }
            if (!sessionAvailable()) throw new InvalidDataException("requires_gui_login");
            await Protocol.Write(stream, result, token);
          }
        }
        finally
        {
            // No transaction may inject a late down after this lease is closed.
            if(transaction is not null)await Observe(transaction);
            await Gate.WaitAsync();
            try
            {
                if (pagePointerOwner == lease)
                {
                    pagePointerOwner = null;
                    pagePointerReleasePending = true;
                    if (sessionAvailable())
                    {
                        using var releaseTimeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
                        try { await ReleasePagePointer(releaseTimeout.Token); }
                        catch { Close(); }
                    }
                }
                BrowserWindowCapture.CloseLease(lease);
            }
            finally { Gate.Release(); }
            await WindowsPhoneBrowser.Release(lease);
        }
    }

    private static void Validate(JsonObject value)
    {
        var method = value["method"]?.GetValue<string>();
        var path = value["path"]?.GetValue<string>() ?? "";
        if (value.ContainsKey("operation") || method is not ("GET" or "POST") ||
            path.Length < 2 || !path.StartsWith('/') || !Routes.Contains(path[1..]) ||
            value["input"] is not (null or JsonObject) ||
            method == "GET" && path is not ("/health" or "/tabs" or "/events" or "/frame"))
            throw new InvalidDataException("browser_request_invalid");
    }

    private static void AssertEditorTab(string? id)
    {
        if(id is not null && EditorTabs.TryGetValue(id,out var bound) && ProjectRpc.Resolve(bound.Project,bound.Grant)!=bound.Folder)
            throw new InvalidDataException("project_grant_unavailable");
    }
    private static void AssertFrameEditors(JsonObject frame)
    {
        AssertEditorTab(frame["id"]?.GetValue<string>());
        AssertEditorTab((frame["native"] as JsonObject)?["id"]?.GetValue<string>());
        if(frame["panes"] is JsonArray panes)foreach(var pane in panes.OfType<JsonObject>())AssertEditorTab(pane["id"]?.GetValue<string>());
    }
    private static void AssertRequestedTab(JsonObject input,JsonObject proof)
    {AssertEditorTab(proof["id"]?.GetValue<string>());if(input["id"]?.GetValue<string>() is {Length:>0} id&&id!=proof["id"]?.GetValue<string>())throw new InvalidDataException("browser_target_changed");}

    private static async Task ReleasePagePointer(CancellationToken token)
    {
        if (browser is not null && !browser.HasExited)
            await Control(new JsonObject { ["operation"] = "input-release-pointer" }, token);
        pagePointerOwner = null;
        pagePointerReleasePending = false;
    }

    private static async Task<JsonObject> Exchange(JsonObject request, CancellationToken connection, NativeInputCoordinator.Lease lease)
    {
        if (request["path"]?.GetValue<string>()?.StartsWith("/phone-browser/", StringComparison.Ordinal) == true)
            return await WindowsPhoneBrowser.Exchange(request, connection, lease);
        await Gate.WaitAsync(connection);
        try
        {
            // The connection token must not interrupt the child's reply drain.
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(35));
            var route = request["path"]?.GetValue<string>() ?? "";
            var input = request["input"] as JsonObject ?? new JsonObject();
            if(route is "/paste-cancel" or "/image-cancel")
            {
                BrowserPasteInput.Cancel(input["transfer"]?.GetValue<string>() ?? "");
                return new JsonObject { ["ok"] = true };
            }
            if(route=="/input"&&BrowserWindowCapture.ReleaseInput(lease,input))return new JsonObject { ["ok"] = true };
            await Start(timeout.Token);
            if (pagePointerReleasePending) await ReleasePagePointer(timeout.Token);
            if (pagePointerOwner is not null && route is "/resize" or "/navigate" or "/new" or "/close" or "/activate" or "/split" or "/back" or "/forward" or "/reload" or "/paste" or "/cut" or "/copy" or "/select-all" or "/insert-text")
                await ReleasePagePointer(timeout.Token);
            connection.ThrowIfCancellationRequested();
            WindowsEditorServer.Connection? editor = null;
            if (route is "/editor" or "/editor-restart")
            {
                var project = input["project_id"]?.GetValue<string>() ?? "";
                var grant = input["grant"]?.GetValue<string>() ?? "";
                editor = await WindowsEditorServer.Open(project, grant, route == "/editor-restart", timeout.Token);
                connection.ThrowIfCancellationRequested();
                EditorConnections[editor.ID] = editor;
                await Control(new JsonObject { ["operation"] = "editor-connect", ["input"] = new JsonObject
                {
                    ["workspace"] = project, ["folder"] = editor.Folder, ["url"] = editor.URL, ["connection"] = editor.ID,
                    ["trust_default"] = editor.TrustDefault.HasValue ? JsonValue.Create(editor.TrustDefault.Value) : null,
                } }, timeout.Token);
                // Authorization uses only Resolve; a wire Workspace never selects
                // an executable, path or browser URL.
                input = new JsonObject { ["workspace"] = project };
                request = new JsonObject { ["method"] = "POST", ["path"] = route, ["input"] = input };
            }
            var selected = input["id"]?.GetValue<string>() ?? active;
            if (route is not ("/tabs" or "/events" or "/audit" or "/close" or "/new" or "/settings" or "/extensions" or "/internal-page" or "/editor" or "/editor-restart"))AssertEditorTab(selected);
            if(route=="/split" && input["left"] is not null){AssertEditorTab(input["left"]?.GetValue<string>());AssertEditorTab(input["right"]?.GetValue<string>());}
            if (route.StartsWith("/image-", StringComparison.Ordinal) || route.StartsWith("/paste-", StringComparison.Ordinal))
            {
                if(route=="/image-start")input["mime"]="image/png";
                route=route.Replace("/paste-","/image-",StringComparison.Ordinal);
                if (route == "/image-cancel") { BrowserPasteInput.Cancel(input["transfer"]?.GetValue<string>() ?? ""); return new JsonObject { ["ok"] = true }; }
                if (route == "/image-chunk") return BrowserPasteInput.Chunk(input);
                var proof = await Control(new JsonObject { ["method"] = "GET", ["path"] = "/frame-proof" }, timeout.Token);
                AssertEditorTab(proof["id"]?.GetValue<string>());
                var focus = await Control(new JsonObject { ["method"] = "GET", ["path"] = "/focus-proof" }, timeout.Token);
                if (route == "/image-start") return await BrowserPasteInput.Start(input, proof, focus);
                if (route == "/image-finish") return await BrowserPasteInput.Finish(input, proof, focus, async () =>
                {
                    var current = await Control(new JsonObject { ["method"] = "GET", ["path"] = "/frame-proof" }, timeout.Token);
                    AssertEditorTab(current["id"]?.GetValue<string>());
                    var field = await Control(new JsonObject { ["method"] = "GET", ["path"] = "/focus-proof" }, timeout.Token);
                    if (current["target"]?.GetValue<string>() != proof["target"]?.GetValue<string>() || current["id"]?.GetValue<string>() != proof["id"]?.GetValue<string>() || field["focus"]?.GetValue<string>() != focus["focus"]?.GetValue<string>()) throw new InvalidDataException("image_target_changed");
                    if (BrowserWindowCapture.Edit(browser!, proof, "paste")) return new JsonObject { ["ok"] = true };
                    return await Control(new JsonObject { ["method"] = "POST", ["path"] = "/paste", ["input"] = new JsonObject { ["id"] = proof["id"]!.DeepClone(), ["document"] = proof["document"]!.DeepClone() } }, timeout.Token);
                });
                throw new InvalidDataException("image_request_invalid");
            }
            var clipboard = ClipboardAgent.Current;
            uint? previous = null;
            if (clipboard is not null && route is "/copy" or "/cut" or "/copy-context") previous = await clipboard.Revision();
            if (clipboard is not null && route == "/paste") await clipboard.PreparePaste();
            if (route == "/copy-context" && previous.HasValue && input["context"]?.GetValue<string>() is { } copyContext)
            {
                if (CopyRevisions.Count > 200) CopyRevisions.Clear();
                CopyRevisions[copyContext] = previous.Value;
            }
            connection.ThrowIfCancellationRequested();
            if (route == "/input")
            {
                if (input["kind"]?.GetValue<string>() == "pointer" && pagePointerOwner is not null)
                {
                    if (pagePointerOwner != lease) throw new InvalidDataException("browser_target_changed");
                    if (input["mask"]?.GetValue<int>() == 0)
                    {
                        await ReleasePagePointer(timeout.Token);
                        return new JsonObject{["ok"]=true};
                    }
                }
                var proof = await Control(new JsonObject { ["method"] = "GET", ["path"] = "/frame-proof" }, timeout.Token);
                AssertEditorTab(proof["id"]?.GetValue<string>());
                if(BrowserWindowCapture.FocusRenderer(browser!,proof))
                {proof=await Control(new JsonObject{["method"]="GET",["path"]="/frame-proof"},timeout.Token);AssertEditorTab(proof["id"]?.GetValue<string>());}
                if (pagePointerOwner != lease && BrowserWindowCapture.SelectPane(browser!, lease, input, proof))
                {
                    await Control(new JsonObject { ["operation"] = "input-select-pane", ["input"] = new JsonObject { ["target"] = proof["target"]!.DeepClone(), ["x"] = input["x"]!.DeepClone() } }, timeout.Token);
                    proof = await Control(new JsonObject { ["method"] = "GET", ["path"] = "/frame-proof" }, timeout.Token);
                    AssertEditorTab(proof["id"]?.GetValue<string>());
                    BrowserWindowCapture.Transition(browser!, proof);
                }
                if (BrowserWindowCapture.HandleInput(browser!, lease, input, proof, pagePointerOwner == lease))
                {
                    if (input["kind"]?.GetValue<string>() == "pointer")
                        await Control(new JsonObject { ["operation"] = "input-reset" }, timeout.Token);
                    BrowserWindowCapture.Transition(browser!, await Control(new JsonObject { ["method"] = "GET", ["path"] = "/frame-proof" }, timeout.Token));
                    active = proof["id"]?.GetValue<string>() ?? active;
                    return new JsonObject { ["ok"] = true, ["active"] = active };
                }
                if (input["kind"]?.GetValue<string>() == "pointer" && ((input["mask"]?.GetValue<int>() ?? 0) & 7) != 0)
                    pagePointerOwner = lease;
            }
            if (route is "/paste" or "/copy" or "/cut" or "/select-all" or "/insert-text")
            {
                var proof = await Control(new JsonObject { ["method"] = "GET", ["path"] = "/frame-proof" }, timeout.Token);
                AssertRequestedTab(input,proof);
                if (BrowserWindowCapture.Edit(browser!, proof, route[1..], input["text"]?.GetValue<string>() ?? ""))
                {
                    await Control(new JsonObject { ["operation"] = "input-reset" }, timeout.Token);
                    var receipt = new JsonObject { ["ok"] = true };
                    if (previous.HasValue && route is "/copy" or "/cut") receipt["clipboard"] = await clipboard!.Receipt(previous.Value, true);
                    return receipt;
                }
            }
            if (route == "/ime")
            {
                var proof = await Control(new JsonObject { ["method"] = "GET", ["path"] = "/frame-proof" }, timeout.Token);
                AssertRequestedTab(input,proof);
                if (input["id"]?.GetValue<string>() != proof["id"]?.GetValue<string>()) throw new InvalidDataException("browser_target_changed");
                var native = await BrowserWindowCapture.Ime(browser!, proof, input, timeout.Token);
                if (native is not null) return native;
            }
            var afterFrame = route == "/frame" ? input["after"]?.GetValue<string>() : null;
            if (route == "/frame")
            {
                // Cached page pixels and the current native-window proof have
                // separate lifetimes. Native menus are inspected on every call.
                input["after"] = cachedPageFrame?["sequence"]?.DeepClone();
                input["nativeProof"] = true;
                if (request["input"] is null) request["input"] = input;
            }
            var result = await Control(request, timeout.Token);
            if (route == "/frame" && result["error"] is null)
            {
                if (result["unchanged"]?.GetValue<bool>() == true)
                {
                    if (cachedPageFrame is null || result["sequence"]?.GetValue<string>() != cachedPageFrame["sequence"]?.GetValue<string>())
                        throw new InvalidDataException("browser_target_changed");
                    var proof = result["native"]?.DeepClone() ?? await Control(new JsonObject { ["method"] = "GET", ["path"] = "/frame-proof" }, timeout.Token);
                    result = (JsonObject)cachedPageFrame.DeepClone();
                    result["native"] = proof;
                }
                else
                {
                    AssertFrameEditors(result);
                    cachedPageFrame = (JsonObject)result.DeepClone();
                }
                AssertFrameEditors(result);
                await BrowserWindowCapture.Decorate(browser!, result, async ()=>{var current=await Control(new JsonObject { ["method"] = "GET", ["path"] = "/frame-proof" }, timeout.Token);AssertEditorTab(current["id"]?.GetValue<string>());return current;});
                AssertFrameEditors(result);
                result = BrowserWindowCapture.FrameReply(result, afterFrame);
            }
            if (result["error"] is null)
            {
                ready = true;
                if (result["capabilities"] is JsonObject capability) features = (JsonObject)capability.DeepClone();
                else if (result["state"]?["capabilities"] is JsonObject stateCapability) features = (JsonObject)stateCapability.DeepClone();
                if (result["active"]?.GetValue<string>() is { } current) active = current;
                if (route is "/activate" or "/new" or "/editor" or "/editor-restart") active = result["id"]?.GetValue<string>() ?? input["id"]?.GetValue<string>() ?? active;
                if (editor is not null && result["id"]?.GetValue<string>() is { } tabID)
                {
                    if (ProjectRpc.Resolve(editor.Project, editor.Grant) != editor.Folder) throw new InvalidDataException("project_grant_unavailable");
                    EditorTabs[tabID] = editor;
                }
                if (route == "/close") EditorTabs.TryRemove(selected, out _);
                if (clipboard is not null && previous.HasValue && route is "/copy" or "/cut")
                    result["clipboard"] = await clipboard.Receipt(previous.Value, true);
                if (clipboard is not null && result["events"] is JsonArray events)
                {
                    foreach (var value in events.OfType<JsonObject>()) if (value["type"]?.GetValue<string>() == "clipboard")
                    {
                        var payload = value["value"] as JsonObject;
                        var context = payload?["context"]?.GetValue<string>() ?? "";
                        payload?.Remove("text"); value["type"] = "native-clipboard";
                        try
                        {
                            if (!CopyRevisions.TryGetValue(context, out var revision)) throw new InvalidDataException("copy_context_expired");
                            payload!["receipt"] = await clipboard.Receipt(revision, true);
                        }
                        catch { value["type"] = "copy-expired"; }
                    }
                }
            }
            return result;
        }
        catch (OperationCanceledException)
        { if (wirePending) Close(); throw new InvalidDataException("browser_response_timeout"); }
        catch (EndOfStreamException) { Close(); throw new InvalidDataException("browser_unavailable"); }
        finally { Gate.Release(); }
    }

    internal static async Task<JsonObject> ReadConfiguration(CancellationToken token, bool packetOnly = false)
    {
        var root = AppContext.BaseDirectory;
        var configPath = Path.Combine(root, "guest-config.json");
        var info = new FileInfo(configPath);
        if (!info.Exists || info.Length is <= 0 or > 16384 || info.Attributes.HasFlag(FileAttributes.ReparsePoint))
            throw new InvalidDataException("browser_network_unavailable");
        using var snapshot = OpenPublishedFile(configPath);
        using var reader = new StreamReader(snapshot);
        var config = JsonNode.Parse(await reader.ReadToEndAsync(token)) as JsonObject
            ?? throw new InvalidDataException("browser_network_unavailable");
        var packet = config["network_mode"]?.GetValue<string>() == "packet";
        if (packetOnly && !packet) throw new InvalidDataException("browser_network_unavailable");
        var proxy = config["browser_proxy"]?.GetValue<string>();
        Uri? address = null;
        if (packet ? !PacketReady() : !Uri.TryCreate(proxy, UriKind.Absolute, out address) || address.Scheme != "http" ||
            address.Host != "127.0.0.1" || address.IsDefaultPort || address.UserInfo != "" ||
            address.AbsolutePath != "/" || address.Query != "" || address.Fragment != "")
            throw new InvalidDataException("browser_network_unavailable");
        var settings = packet ? new JsonObject { ["networkMode"] = "packet" } : new JsonObject { ["proxy"] = address!.GetLeftPart(UriPartial.Authority) };
        if (config["timezone"] is JsonValue zone) settings["timezone"] = zone.DeepClone();
        if (config["language"] is JsonValue lang) settings["language"] = lang.DeepClone();
        return settings;
    }
    private static async Task Start(CancellationToken token)
    {
        if (browser is { HasExited: false }) return;
        Close();
        var root = AppContext.BaseDirectory;
        var executable = Path.Combine(root, "node.exe");
        var script = Path.Combine(root, "windows-browser.mjs");
        if (!File.Exists(executable) || !File.Exists(script)) throw new InvalidDataException("browser_components_missing");
        var settings = await ReadConfiguration(token);
        var start = new ProcessStartInfo(executable)
        {
            UseShellExecute = false, RedirectStandardInput = true, RedirectStandardOutput = true,
            RedirectStandardError = true, CreateNoWindow = true, WorkingDirectory = root,
            StandardInputEncoding = Protocol.Utf8, StandardOutputEncoding = Protocol.Utf8, StandardErrorEncoding = Protocol.Utf8,
        };
        start.ArgumentList.Add(script);
        browser = Process.Start(start) ?? throw new InvalidDataException("browser_unavailable");
        var process = browser;
        try { browserJob = new GuestProcessJob(process); }
        catch { Close(); throw new InvalidDataException("browser_process_unavailable"); }
        replies = new ReadAheadStream(process.StandardOutput.BaseStream);
        errors = Task.Run(async () =>
        {
            var discard = new char[4096];
            try { while (await process.StandardError.ReadAsync(discard) != 0) { } }
            catch (ObjectDisposedException) { }
        });
        var result = await Control(new JsonObject
        {
            ["operation"] = "configure", ["input"] = settings,
        }, token);
        if (result?["ok"]?.GetValue<bool>() != true) { Close(); throw new InvalidDataException("browser_network_unavailable"); }
    }

    private static async Task<JsonObject> Control(JsonObject value, CancellationToken token)
    {
        wirePending = true;
        await Protocol.Write(browser!.StandardInput.BaseStream, value, token);
        var result = await Protocol.Read(replies!, Protocol.WireLimit, token) ?? throw new EndOfStreamException();
        wirePending = false;
        return result;
    }

    private static async Task RevokeNode(string project, string grant)
    {
        await Gate.WaitAsync();
        try
        {
            if (browser is not { HasExited: false }) return;
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
            foreach (var editor in EditorConnections.Values.Where(e => e.Project == project && e.Grant == grant))
            {
                await Control(new JsonObject { ["operation"] = "editor-revoke", ["input"] = new JsonObject { ["workspace"] = project, ["connection"] = editor.ID } }, timeout.Token);
                EditorConnections.TryRemove(editor.ID, out _);
            }
        }
        catch { /* Every editor request also checks the live project resolver. */ }
        finally { Gate.Release(); }
    }

    internal static bool BrowserExecutableInstalled() =>
        File.Exists(Path.Combine(AppContext.BaseDirectory, "chrome", "chrome.exe")) ||
        new[] { Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles), Environment.GetFolderPath(Environment.SpecialFolder.ProgramFilesX86) }
            .Any(basePath => new[] { "Google/Chrome/Application/chrome.exe", "Microsoft/Edge/Application/msedge.exe" }.Any(relative => File.Exists(Path.Combine(basePath, relative))));

    internal static JsonObject Status()
    {
        var root = AppContext.BaseDirectory;
        var installed = new[] { "node.exe", "windows-browser.mjs", "windows-browser-extensions.mjs", "playwright/index.mjs", "modules/browser-clipboard.mjs", "modules/browser-uploads.mjs", "modules/browser-downloads.mjs", "modules/browser-cookies.mjs", "modules/browser-extensions.mjs", "modules/browser-vault.mjs", "split-extension/manifest.json", "split-extension/control.html", "split-extension/control.js" }.All(file => File.Exists(Path.Combine(root, file)));
        installed &= BrowserExecutableInstalled();
        var network = false;var phoneNetwork = false;
        try
        {
            var file = new FileInfo(Path.Combine(root, "guest-config.json"));
            JsonObject? config = null;
            if (file.Exists && file.Length is > 0 and <= 16384)
            {
                using var snapshot = OpenPublishedFile(file.FullName);
                using var reader = new StreamReader(snapshot);
                config = JsonNode.Parse(reader.ReadToEnd()) as JsonObject;
            }
            phoneNetwork = config?["network_mode"]?.GetValue<string>() == "packet" && PacketReady();
            network = config?["network_mode"]?.GetValue<string>() == "packet" ? phoneNetwork : Uri.TryCreate(config?["browser_proxy"]?.GetValue<string>(), UriKind.Absolute, out var proxy) && proxy.Scheme == "http" && proxy.Host == "127.0.0.1" && !proxy.IsDefaultPort && proxy.UserInfo == "" && proxy.AbsolutePath == "/" && proxy.Query == "" && proxy.Fragment == "";
        }
        catch { }
        return new JsonObject { ["installed"] = installed, ["network_configured"] = network, ["ready"] = ready && browser is { HasExited: false } && SessionGuard.Available(), ["editor_installed"] = WindowsEditorServer.Installed(), ["phone_installed"] = WindowsPhoneBrowser.Installed(), ["phone_network_configured"] = phoneNetwork, ["capabilities"] = features.DeepClone() };
    }

    private static async Task Observe(Task task) { try { await task; } catch { /* reply drain owns cleanup */ } }

    private static FileStream OpenPublishedFile(string path)
    {
        var snapshot = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read | FileShare.Delete);
        if (snapshot.Length is > 0 and <= 16384) return snapshot;
        snapshot.Dispose();
        throw new InvalidDataException("browser_network_unavailable");
    }

    internal static void Close()
    {
        ready = false;
        wirePending = false;
        cachedPageFrame = null;
        pagePointerOwner = null;
        pagePointerReleasePending = false;
        BrowserWindowCapture.Close();
        BrowserPasteInput.Close();
        var process = Interlocked.Exchange(ref browser, null);
        if (process is null) return;
        try
        {
            process.StandardInput.Close();
            if (!process.WaitForExit(2000)) process.Kill(true);
            process.WaitForExit(2000);
        }
        catch (InvalidOperationException) { }
        finally { browserJob?.Dispose(); browserJob = null; replies?.Dispose(); replies = null; process.Dispose(); }
        if (errors is { IsCompleted: true }) { try { errors.GetAwaiter().GetResult(); } catch { } }
        errors = null;
    }
}
