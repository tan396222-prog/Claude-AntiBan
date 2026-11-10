using System.Collections.Concurrent;
using System.Diagnostics;
using System.Runtime.CompilerServices;
using System.Security.Principal;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;
using Microsoft.Win32;

namespace Cage.GuestClipboard;

internal static class ProjectRpc
{
    private static readonly ConcurrentDictionary<string, Mount> Mounts = new(StringComparer.Ordinal);
    private static readonly SemaphoreSlim Slots = new(32, 32);
    internal static event Action<string, string>? Revoked;
    private static readonly Regex ID = new("^[a-f0-9]{16}$", RegexOptions.CultureInvariant);
    private static readonly Regex Grant = new("^[a-f0-9]{64}$", RegexOptions.CultureInvariant);
    private static readonly Regex Probe = new("^\\.cage-mount-probe-[A-Za-z0-9_-]{1,100}$", RegexOptions.CultureInvariant);
    [ModuleInitializer]
    internal static void Register() { if (ToolsPresent()) GuestOperations.Register("project-mount", Run); }
    internal static bool ToolsPresent()
    {
        try
        {
            using var registry = RegistryKey.OpenBaseKey(RegistryHive.LocalMachine, RegistryView.Registry32);
            using var key = registry.OpenSubKey(@"SOFTWARE\WinFsp");
            var install = key?.GetValue("InstallDir") as string;
            return install is not null && File.Exists(Path.Combine(install, "bin", "winfsp-x64.dll")) &&
                File.Exists(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles), "SSHFS-Win", "bin", "sshfs-win.exe"));
        }
        catch { return false; }
    }
    internal static string Resolve(string id, string grant)
    {
        if (!ID.IsMatch(id) || !Grant.IsMatch(grant) || !Mounts.TryGetValue(id, out var mount) ||
            mount.Grant != grant || !mount.Ready || mount.Child.HasExited || mount.Lifetime.IsCancellationRequested)
            throw new InvalidDataException("project_grant_unavailable");
        return mount.Path;
    }
    internal static async Task Run(Stream stream, CancellationToken token, Func<bool> available)
    {
        if (!available()) throw new InvalidDataException("requires_gui_login");
        var value = await Protocol.Read(stream, 16384, token) ?? throw new EndOfStreamException();
        var id = value["id"]?.GetValue<string>() ?? "";
        var grant = value["grant"]?.GetValue<string>() ?? "";
        if (!ID.IsMatch(id) || !Grant.IsMatch(grant)) throw new InvalidDataException("project_grant_invalid");
        switch (value["action"]?.GetValue<string>())
        {
            case "mount": await Start(stream, id, grant, token, available); break;
            case "probe":
                if (!Mounts.TryGetValue(id, out var mount) || mount.Grant != grant || mount.Child.HasExited)
                    throw new InvalidDataException("project_grant_unavailable");
                var name = value["probe_name"]?.GetValue<string>() ?? "";
                if (!Probe.IsMatch(name)) throw new InvalidDataException("project_probe_invalid");
                try
                {
                    using var file = File.OpenRead(Path.Combine(mount.Path, name));
                    if (file.Length is < 1 or > 4096) throw new InvalidDataException("project_probe_invalid");
                    using var reader = new StreamReader(file, Protocol.Utf8);
                    var proof = await reader.ReadToEndAsync(token);
                    if (!available() || mount.Lifetime.IsCancellationRequested || mount.Child.HasExited) throw new InvalidDataException("requires_gui_login");
                    mount.Ready = true;
                    await Protocol.Write(stream, new() { ["mounted"] = true, ["id"] = id, ["path"] = mount.Path, ["proof"] = proof }, token);
                }
                catch (IOException) { await Protocol.Write(stream, new() { ["mounted"] = false }, token); }
                break;
            case "unmount":
                if (!Mounts.TryGetValue(id, out var selected) || selected.Grant != grant) throw new InvalidDataException("project_grant_unavailable");
                Revoke(id, selected);
                selected.Lifetime.Cancel();
                await selected.Done.Task.WaitAsync(token);
                await Protocol.Write(stream, new() { ["unmounted"] = true }, token); break;
            default: throw new InvalidDataException("project_operation_invalid");
        }
    }
    private static async Task Start(Stream stream, string id, string grant, CancellationToken token, Func<bool> available)
    {
        if (!await Slots.WaitAsync(0, token)) throw new InvalidDataException("project_limit");
        try { await StartCore(stream, id, grant, token, available); } finally { Slots.Release(); }
    }
    private static async Task StartCore(Stream stream, string id, string grant, CancellationToken token, Func<bool> available)
    {
        if (Mounts.TryGetValue(id, out var previous))
        {
            if (previous.Grant == grant) throw new InvalidDataException("project_mount_busy");
            Revoke(id, previous); previous.Lifetime.Cancel();
            await previous.Done.Task.WaitAsync(token);
        }
        var home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        using var identity = WindowsIdentity.GetCurrent();
        var parent = Path.Combine(home, "Cage", "Projects");
        foreach (var directory in new[] { Path.Combine(home, "Cage"), parent })
        {
            Directory.CreateDirectory(directory);
            var info = new DirectoryInfo(directory);
            if (info.Attributes.HasFlag(FileAttributes.ReparsePoint) || info.GetAccessControl().GetOwner(typeof(SecurityIdentifier))?.Value != identity.User?.Value)
                throw new InvalidDataException("project_mount_parent_invalid");
        }
        var path = Path.Combine(parent, id);
        // WinFsp owns mount-point creation and removal; never replace user files.
        if (Path.Exists(path)) throw new InvalidDataException("project_mount_path_exists");
        var executable = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles), "SSHFS-Win", "bin", "sshfs-win.exe");
        if (!File.Exists(executable)) throw new InvalidDataException("project_tools_missing");
        var start = new ProcessStartInfo(executable) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardInput = true, RedirectStandardOutput = true, RedirectStandardError = true };
        foreach (var arg in new[] { "cmd", ":/", CygwinPath(path), "-f", "-o", "slave,uid=-1,gid=-1,cache=no,nodev,nosuid" }) start.ArgumentList.Add(arg);
        using var child = new Process { StartInfo = start };
        using var lifetime = CancellationTokenSource.CreateLinkedTokenSource(token);
        var mount = new Mount(grant, path, child, lifetime);
        if (!Mounts.TryAdd(id, mount)) throw new InvalidDataException("project_mount_busy");
        Task? input = null, output = null, stderr = null, exited = null;
        try
        {
            if (!available() || !child.Start()) throw new InvalidDataException("requires_gui_login");
            using var stop = lifetime.Token.Register(() => { try { child.Kill(true); } catch { } });
            await Protocol.Write(stream, new() { ["started"] = true, ["id"] = id, ["path"] = path }, lifetime.Token);
            input = stream.CopyToAsync(child.StandardInput.BaseStream, lifetime.Token);
            output = child.StandardOutput.BaseStream.CopyToAsync(stream, lifetime.Token);
            stderr = child.StandardError.BaseStream.CopyToAsync(Stream.Null, lifetime.Token);
            exited = child.WaitForExitAsync(lifetime.Token);
            await Task.WhenAny(input, output, exited);
        }
        finally
        {
            Revoke(id, mount);
            lifetime.Cancel();
            try { child.StandardInput.Close(); } catch { }
            try { if (!child.HasExited) child.Kill(true); await child.WaitForExitAsync(); } catch { }
            foreach (var task in new[] { input, output, stderr, exited }) if (task is not null) try { await task; } catch { }
            mount.Done.TrySetResult();
        }
    }
    private static void Revoke(string id, Mount mount)
    {
        mount.Ready = false;
        if (!((ICollection<KeyValuePair<string, Mount>>)Mounts).Remove(new(id, mount))) return;
        if (Revoked is { } handlers)
            foreach (Action<string, string> handler in handlers.GetInvocationList())
                try { handler(id, mount.Grant); } catch { /* all consumers must receive revocation */ }
    }
    private static string CygwinPath(string path)
    {
        if (path.Length < 4 || !char.IsAsciiLetter(path[0]) || path[1] != ':' || path[2] != '\\') throw new InvalidDataException("project_mount_path_invalid");
        return "/cygdrive/" + char.ToLowerInvariant(path[0]) + path[2..].Replace('\\', '/');
    }
    private sealed class Mount(string grant, string path, Process child, CancellationTokenSource lifetime)
    {
        internal string Grant { get; } = grant;
        internal string Path { get; } = path;
        internal Process Child { get; } = child;
        internal CancellationTokenSource Lifetime { get; } = lifetime;
        internal readonly TaskCompletionSource Done = new(TaskCreationOptions.RunContinuationsAsynchronously);
        internal volatile bool Ready;
    }
}
