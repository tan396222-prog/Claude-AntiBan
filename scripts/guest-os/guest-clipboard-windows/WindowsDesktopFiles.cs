using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text.Json.Nodes;
using System.Windows.Automation;

namespace Cage.GuestClipboard;

internal static class WindowsDesktopFiles
{
    private sealed record Chooser(WindowsFileDialogNative.Owner Owner, IntPtr Window, IntPtr Stamp,
        bool Directory, bool Multiple, string Session, long Epoch, string SaveName = "")
    {
        internal string ID = "";
        internal bool Submitting;
        internal readonly DateTimeOffset Created = DateTimeOffset.UtcNow;
        internal bool SessionValid => SessionGuard.Available() && ClipboardAgent.Current is { SessionEventsRegistered: true } agent && agent.Session == Session && agent.SessionEpoch == Epoch;
        internal void Assert(bool foreground = false)
        {
            if (!SessionValid) throw new InvalidDataException("file_dialog_changed");
            WindowsFileDialogNative.Assert(Owner, Window, Stamp, foreground);
        }
        internal void AcceptSelection()
        {
            Assert(true);
            // Windows 11 can expose the native Open split button as a UIA
            // Pane without InvokePattern. BM_CLICK addresses that exact button.
            WindowsFileDialogNative.Accept(Owner, Window, Stamp);
        }
    }
    private static readonly object Gate = new();
    private static readonly Dictionary<string, Chooser> Choosers = new();
    private sealed record OpenRequest(string Path, string Action, string Session, long Epoch, Action Validate)
    {
        internal readonly DateTimeOffset Created = DateTimeOffset.UtcNow;
        internal bool Claimed;
    }
    private static readonly Dictionary<string, OpenRequest> Opens = new();
    [DllImport("user32.dll", ExactSpelling = true)] private static extern bool PostMessageW(IntPtr window, uint message, IntPtr wparam, IntPtr lparam);

    private static string ButtonLabel(string name) => System.Text.RegularExpressions.Regex.Replace(name.Replace("&", ""), @"\s*\([A-Za-z]\)\s*$", "").Trim().TrimEnd('.', '…');

    private static bool FilenameControl(AutomationElement element, IntPtr dialog)
    {
        AutomationElement? current = element;
        for (var depth = 0; current is not null && depth < 4; depth++, current = TreeWalker.ControlViewWalker.GetParent(current))
        {
            var handle = new IntPtr(current.Current.NativeWindowHandle);
            if (handle == dialog) return false;
            var id = handle == IntPtr.Zero ? 0 : WindowsFileDialogNative.GetDlgCtrlID(handle);
            if (id is 1148 or 1152 || current.Current.AutomationId is "1148" or "1152" or "FileNameControlHost") return true;
        }
        return false;
    }

    private static IntPtr FilenameHandle(Chooser chooser)
    {
        chooser.Assert(true);
        // Windows Shell dialogs can replace their UIA edit provider
        // while uploads are in flight. Reacquire it inside the stamped dialog.
        var root = AutomationElement.FromHandle(chooser.Window);
        var fields = root.FindAll(TreeScope.Descendants, new PropertyCondition(AutomationElement.ControlTypeProperty, ControlType.Edit));
        var result = IntPtr.Zero;
        foreach (AutomationElement element in fields)
        {
            var current = element.Current;
            if (current.ProcessId != chooser.Owner.PID || current.IsOffscreen || !FilenameControl(element, chooser.Window) ||
                !element.TryGetCurrentPattern(ValuePattern.Pattern, out var candidate) || ((ValuePattern)candidate).Current.IsReadOnly) continue;
            if (result != IntPtr.Zero) throw new InvalidDataException("file_dialog_selection_invalid");
            result = new IntPtr(current.NativeWindowHandle);
        }
        chooser.Assert(true);
        return result != IntPtr.Zero ? result : throw new InvalidDataException("file_dialog_selection_invalid");
    }

    private static Chooser? Inspect(WindowsFileDialogNative.Owner owner, IntPtr window)
    {
        var root = AutomationElement.FromHandle(window);
        var elements = root.FindAll(TreeScope.Descendants, Condition.TrueCondition);
        if (elements.Count > 4096) throw new InvalidDataException("file_dialog_control_limit");
        AutomationElement? accept = null, field = null;
        bool multiple = false;
        foreach (AutomationElement element in elements)
        {
            var current = element.Current;
            if (current.ProcessId != owner.PID || current.IsOffscreen) continue;
            var controlID = current.NativeWindowHandle == 0 ? 0 : WindowsFileDialogNative.GetDlgCtrlID(new IntPtr(current.NativeWindowHandle));
            if ((controlID == 1 || current.AutomationId == "1") &&
                (element.TryGetCurrentPattern(InvokePattern.Pattern, out _) ||
                 controlID == 1 && current.ClassName == "Button")) accept = element;
            if (current.ControlType == ControlType.Edit && FilenameControl(element, window) &&
                element.TryGetCurrentPattern(ValuePattern.Pattern, out var editable) && !((ValuePattern)editable).Current.IsReadOnly) field = element;
            if (current.ControlType is { } type && (type == ControlType.List || type == ControlType.DataGrid) &&
                element.TryGetCurrentPattern(SelectionPattern.Pattern, out var selection)) multiple |= ((SelectionPattern)selection).Current.CanSelectMultiple;
        }
        if (accept is null || field is null) return null;
        var label = ButtonLabel(accept.Current.Name);
        bool directory = label is "Select Folder" or "Choose Folder" or "选择文件夹" or "選擇資料夾";
        bool save = label is "Save" or "保存" or "儲存" or "Enregistrer" or "Speichern" or "Guardar" or "Salvar" or "Salva" or "Сохранить" or "저장";
        if (!directory && !save && label is not ("Open" or "打开" or "開啟" or "Ouvrir" or "Öffnen" or "Abrir" or "Apri" or "Openen" or "開く" or "Открыть" or "열기")) return null;
        var saveName = save ? ((ValuePattern)field.GetCurrentPattern(ValuePattern.Pattern)).Current.Value : "";
        if (save && !AttachmentsRpc.NameValid(saveName)) return null;
        var agent = ClipboardAgent.Current;
        if (agent is null || !agent.SessionEventsRegistered) throw new InvalidDataException("file_dialog_session_unavailable");
        var stamp = WindowsFileDialogNative.Stamp(owner, window);
        return new Chooser(owner, window, stamp, directory, multiple && !directory, agent.Session, agent.SessionEpoch, saveName);
    }

    internal static JsonObject Poll(WindowsFileDialogNative.Owner owner)
    {
        lock (Gate)
        {
            foreach (var pair in Choosers.ToArray())
                if (DateTimeOffset.UtcNow - pair.Value.Created > TimeSpan.FromMinutes(30) ||
                    !pair.Value.SessionValid ||
                    !WindowsFileDialogNative.Alive(pair.Value.Owner, pair.Value.Window, pair.Value.Stamp)) Retire(pair.Key);
            foreach (var window in WindowsFileDialogNative.Candidates(owner))
            {
                if (Choosers.Values.Any(value => value.Window == window)) continue;
                if (Choosers.Count >= 12) throw new InvalidDataException("file_dialog_request_limit");
                var chooser = Inspect(owner, window);
                if (chooser is null) continue;
                try
                {
                    if (chooser.SaveName != "")
                    {
                        chooser.ID = WindowsDesktopDownloads.Reserve(chooser.SaveName, path =>
                        {
                            chooser.Assert(true);
                            var field = FilenameHandle(chooser);
                            WindowsFileDialogNative.SetFilename(chooser.Owner, chooser.Window, chooser.Stamp, field, path);
                            chooser.Assert(true);
                            chooser.AcceptSelection();
                        });
                    }
                    else chooser.ID = chooser.Directory ? Guid.NewGuid().ToString("D") :
                        AttachmentsRpc.StartChooserBatch(Guid.NewGuid().ToString("N"), () => chooser.Assert())["id"]!.GetValue<string>();
                    Choosers.Add(chooser.ID, chooser);
                }
                catch { WindowsFileDialogNative.Remove(window, chooser.Stamp); throw; }
            }
            return new JsonObject
            {
                ["available"] = true,
                ["uploads"] = new JsonArray(Choosers.Values.Where(value => !value.Submitting && value.SaveName == "").Select(value => (JsonNode)new JsonObject
                {
                    ["id"] = value.ID, ["target"] = "desktop", ["kind"] = "chooser", ["multiple"] = value.Multiple, ["directory"] = value.Directory,
                }).ToArray()),
                ["downloads"] = WindowsDesktopDownloads.Snapshot(), ["downloadAck"] = true, ["opens"] = OpenSnapshot(),
            };
        }
    }

    private static void ExpireOpens()
    {
        var agent = ClipboardAgent.Current;
        var available = SessionGuard.Available();
        foreach (var pair in Opens.ToArray())
            if (agent is null || !agent.SessionEventsRegistered || !available ||
                pair.Value.Session != agent.Session || pair.Value.Epoch != agent.SessionEpoch ||
                DateTimeOffset.UtcNow - pair.Value.Created >= (pair.Value.Claimed ? TimeSpan.FromMinutes(30) : TimeSpan.FromSeconds(60))) Opens.Remove(pair.Key);
    }

    internal static void QueueOpen(string path, string action, Action validate)
    {
        QueueOpen([path], action, validate);
    }

    internal static void QueueOpen(string[] paths, string action, Action validate)
    {
        if (paths.Length is < 1 or > 32 || action is not ("open" or "reveal") ||
            paths.Any(path => string.IsNullOrEmpty(path) || !Path.IsPathFullyQualified(path) || path.Length > 32767 || path.Contains('\0')))
            throw new InvalidDataException("file_open_request_invalid");
        var normalized = paths.Select(Path.GetFullPath).ToArray();
        lock (Gate)
        {
            SessionGuard.Assert(); ExpireOpens();
            var agent = ClipboardAgent.Current;
            if (agent is null || !agent.SessionEventsRegistered) throw new InvalidDataException("file_open_request_unavailable");
            var epoch = agent.SessionEpoch;
            validate();
            if (ClipboardAgent.Current != agent || agent.SessionEpoch != epoch) throw new InvalidDataException("file_open_target_changed");
            if (Opens.Count + normalized.Length > 32) throw new InvalidDataException("file_open_request_limit");
            foreach (var path in normalized)
                Opens.Add(Guid.NewGuid().ToString("D"), new OpenRequest(path, action, agent.Session, epoch, validate));
        }
    }

    internal static JsonArray OpenSnapshot()
    {
        lock (Gate)
        {
            ExpireOpens();
            return new JsonArray(Opens.Where(pair => !pair.Value.Claimed).Select(pair => (JsonNode)new JsonObject { ["id"] = pair.Key }).ToArray());
        }
    }

    internal static JsonObject OpenAction(string id, bool complete)
    {
        lock (Gate)
        {
            ExpireOpens();
            if (!Opens.TryGetValue(id, out var request)) return new JsonObject { [complete ? "ok" : "handled"] = true };
            if (complete)
            {
                Opens.Remove(id);
                try { request.Validate(); } catch { }
                return new JsonObject { ["ok"] = true };
            }
            if (request.Claimed) return new JsonObject { ["handled"] = true };
            try { request.Validate(); }
            catch { Opens.Remove(id); throw new InvalidDataException("file_open_target_changed"); }
            var result = new JsonObject { ["id"] = id, ["action"] = request.Action };
            if (WindowsDesktopDownloads.FindPath(request.Path) is { } download)
            { result["download_id"] = download.ID; result["directory"] = download.Directory; }
            else result["path"] = request.Path;
            request.Claimed = true;
            return result;
        }
    }

    private static void Retire(string id)
    {
        if (!Choosers.Remove(id, out var chooser)) return;
        WindowsFileDialogNative.Remove(chooser.Window, chooser.Stamp);
        if (!chooser.Submitting) AttachmentsRpc.CancelBatch(id);
    }

    internal static bool Contains(string id) { lock (Gate) return Choosers.ContainsKey(id); }

    internal static void Cancel(string id)
    {
        lock (Gate)
        {
            if (!Choosers.TryGetValue(id, out var chooser)) return;
            if (chooser.SessionValid && WindowsFileDialogNative.Alive(chooser.Owner, chooser.Window, chooser.Stamp))
                PostMessageW(chooser.Window, 0x0111, new IntPtr(2), IntPtr.Zero);
            Retire(id);
            AttachmentsRpc.CancelBatch(id);
        }
    }

    internal static async Task Finish(string id, string[] paths, CancellationToken token)
    {
        Chooser chooser;
        lock (Gate)
        {
            if (!Choosers.TryGetValue(id, out chooser!) || chooser.Submitting || paths.Length == 0 ||
                paths.Length > (chooser.Multiple ? 100 : 1)) throw new InvalidDataException("file_dialog_selection_invalid");
            chooser.Assert(true);
            chooser.Submitting = true;
        }
        try
        {
            foreach (var path in paths)
                if (!Path.IsPathFullyQualified(path) || path.Contains('"') || path.Contains('\0') ||
                    (chooser.Directory ? !Directory.Exists(path) : !File.Exists(path))) throw new InvalidDataException("file_dialog_selection_invalid");
            string selection = paths.Length == 1 ? paths[0] : string.Join(" ", paths.Select(path => "\"" + path + "\""));
            chooser.Assert(true);
            var field = FilenameHandle(chooser);
            WindowsFileDialogNative.SetFilename(chooser.Owner, chooser.Window, chooser.Stamp, field, selection);
            chooser.Assert(true);
            chooser.AcceptSelection();
            var deadline = Stopwatch.StartNew();
            while (WindowsFileDialogNative.Alive(chooser.Owner, chooser.Window, chooser.Stamp))
            {
                chooser.Assert();
                if (deadline.Elapsed > TimeSpan.FromSeconds(4)) throw new InvalidDataException("file_dialog_still_open");
                await Task.Delay(50, token);
            }
            lock (Gate) Retire(id);
        }
        finally { lock (Gate) chooser.Submitting = false; }
    }
}
