using System.Collections.Concurrent;
using System.Diagnostics;
using System.Security.Cryptography;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Cage.GuestClipboard;

// Only the authenticated project resolver supplies a folder. The official CLI
// serves a pinned, offline web-server package over authenticated loopback.
internal static class WindowsEditorServer
{
    private static readonly ConcurrentDictionary<string, Editor> Editors = new(StringComparer.Ordinal);
    private static readonly Regex Commit = new("^[a-f0-9]{40}$", RegexOptions.CultureInvariant);
    private static readonly Regex Listening = new(@"Web UI available at (http://127\.0\.0\.1:[1-9][0-9]*)", RegexOptions.CultureInvariant);
    internal sealed record Connection(string ID, string Project, string Grant, string Folder, string URL, bool? TrustDefault = null);

    internal static bool Installed()
    {
        try
        {
            var root = AppContext.BaseDirectory;
            return File.Exists(Path.Combine(root, "vscode-cli", "code.exe")) &&
                File.Exists(Path.Combine(root, "vscode-server", "bin", "code-server.cmd")) &&
                Commit.IsMatch((JsonNode.Parse(File.ReadAllText(Path.Combine(root, "editor-runtime.json"))) as JsonObject)?["commit"]?.GetValue<string>() ?? "");
        }
        catch { return false; }
    }

    internal static async Task<Connection> Open(string project, string grant, bool restart, CancellationToken token)
    {
        var folder = ProjectRpc.Resolve(project, grant);
        if (!Installed()) throw new InvalidDataException("editor_components_missing");
        var home = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Cage", "Editors");
        var trustDefault = WindowsEditorPreferences.TrustDefault(home);
        if (Editors.TryGetValue(project, out var existing))
        {
            if (existing.Grant == grant && !existing.Process.HasExited && existing.Folder == folder && !restart) return existing.Connection with { TrustDefault = trustDefault };
            if (Editors.TryRemove(project, out var old)) old.Dispose();
        }
        var root = AppContext.BaseDirectory;
        var commit = (JsonNode.Parse(await File.ReadAllTextAsync(Path.Combine(root, "editor-runtime.json"), token)) as JsonObject)?["commit"]?.GetValue<string>() ?? "";
        if (!Commit.IsMatch(commit)) throw new InvalidDataException("editor_components_missing");
        var runtime = Path.Combine(home, "runtime");
        var cached = Path.Combine(runtime, "serve-web", commit);
        if (!Directory.Exists(cached))
        {
            var stage = cached + "." + Guid.NewGuid().ToString("N");
            try { CopyPackage(Path.Combine(root, "vscode-server"), stage, token); Directory.Move(stage, cached); }
            finally { if (Directory.Exists(stage)) Directory.Delete(stage, true); }
        }
        if (!File.Exists(Path.Combine(cached, "bin", "code-server.cmd"))) throw new InvalidDataException("editor_components_missing");
        var profile = Path.Combine(home, "profiles", project);
        Directory.CreateDirectory(profile);
        var tokenFile = Path.Combine(profile, "connection-token");
        var portFile = Path.Combine(profile, "port");
        ushort port = 0;
        if (File.Exists(portFile) && new FileInfo(portFile).Length <= 16) ushort.TryParse(await File.ReadAllTextAsync(portFile, token), out port);
        var secret = Convert.ToHexString(RandomNumberGenerator.GetBytes(32)).ToLowerInvariant();
        await File.WriteAllTextAsync(tokenFile, secret, Protocol.Utf8, token);
        var start = new ProcessStartInfo(Path.Combine(root, "vscode-cli", "code.exe"))
        {
            UseShellExecute = false, CreateNoWindow = true, RedirectStandardInput = true,
            RedirectStandardOutput = true, RedirectStandardError = true, WorkingDirectory = folder,
        };
        foreach (var arg in new[] { "serve-web", "--host", "127.0.0.1", "--port", port.ToString(System.Globalization.CultureInfo.InvariantCulture), "--connection-token-file", tokenFile,
            "--default-folder", folder, "--server-data-dir", Path.Combine(home, "server"), "--cli-data-dir", runtime,
            "--commit-id", commit, "--disable-telemetry", "--accept-server-license-terms" }) start.ArgumentList.Add(arg);
        // An offline package and a pinned commit prevent the CLI's version
        // updater. Browser assets/extensions still use Cage's controlled proxy.
        var process = Process.Start(start) ?? throw new InvalidDataException("editor_start_failed");
        var editor = new Editor(project, grant, folder, process);
        try
        {
            editor.Job = new GuestProcessJob(process);
            var address = new TaskCompletionSource<string>(TaskCreationOptions.RunContinuationsAsynchronously);
            editor.Output = Drain(process.StandardOutput, address);
            editor.Errors = Drain(process.StandardError, address);
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(token); timeout.CancelAfter(TimeSpan.FromSeconds(20));
            var url = await address.Task.WaitAsync(timeout.Token);
            var endpoint = new Uri(url);
            await File.WriteAllTextAsync(portFile, endpoint.Port.ToString(System.Globalization.CultureInfo.InvariantCulture), Protocol.Utf8, token);
            editor.Connection = new Connection(Guid.NewGuid().ToString("N"), project, grant, folder, "http://" + project + ".cage-editor.localhost:" + endpoint.Port + "/?tkn=" + Uri.EscapeDataString(secret), trustDefault);
            if (ProjectRpc.Resolve(project, grant) != folder || process.HasExited) throw new InvalidDataException("project_grant_unavailable");
            if (!Editors.TryAdd(project, editor)) throw new InvalidDataException("editor_start_busy");
            return editor.Connection;
        }
        catch { editor.Dispose(); throw; }
    }

    private static async Task Drain(StreamReader reader, TaskCompletionSource<string> listening)
    {
        try
        {
            // Read bounded chunks, never logging a CLI line which can contain
            // the private connection token or an authorized project path.
            var buffer = new char[4096]; var recent = ""; int count;
            while ((count = await reader.ReadAsync(buffer)) != 0)
            {
                recent = (recent + new string(buffer, 0, count));
                if (recent.Length > 8192) recent = recent[^8192..];
                var match = Listening.Match(recent);
                if (match.Success) listening.TrySetResult(match.Groups[1].Value);
            }
            listening.TrySetException(new InvalidDataException("editor_start_failed"));
        }
        catch { listening.TrySetException(new InvalidDataException("editor_start_failed")); }
    }

    private static void CopyPackage(string source, string target, CancellationToken token)
    {
        var directory = new DirectoryInfo(source);
        if (directory.Attributes.HasFlag(FileAttributes.ReparsePoint)) throw new InvalidDataException("editor_package_invalid");
        Directory.CreateDirectory(target);
        foreach (var file in directory.EnumerateFiles())
        {
            token.ThrowIfCancellationRequested();
            if (file.Attributes.HasFlag(FileAttributes.ReparsePoint)) throw new InvalidDataException("editor_package_invalid");
            file.CopyTo(Path.Combine(target, file.Name), false);
        }
        foreach (var child in directory.EnumerateDirectories()) CopyPackage(child.FullName, Path.Combine(target, child.Name), token);
    }

    internal static void Revoke(string project, string grant)
    {
        if (Editors.TryGetValue(project, out var editor) && editor.Grant == grant &&
            ((ICollection<KeyValuePair<string, Editor>>)Editors).Remove(new(project, editor))) editor.Dispose();
    }
    internal static void Close() { foreach (var entry in Editors) if (Editors.TryRemove(entry.Key, out var editor)) editor.Dispose(); }

    private sealed class Editor(string project, string grant, string folder, Process process) : IDisposable
    {
        internal string Grant { get; } = grant;
        internal string Folder { get; } = folder;
        internal Process Process { get; } = process;
        internal Connection Connection { get; set; } = new("", project, grant, folder, "");
        internal Task? Output, Errors;
        internal GuestProcessJob? Job;
        public void Dispose()
        {
            try { Process.StandardInput.Close(); if (!Process.HasExited) Process.Kill(true); Process.WaitForExit(2000); } catch (InvalidOperationException) { }
            Job?.Dispose();Process.Dispose();
            // Profiles, tokens and VS Code hot-exit backups remain intact.
        }
    }
}
