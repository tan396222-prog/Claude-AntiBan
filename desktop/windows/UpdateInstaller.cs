using System.Diagnostics;
using System.Security.Cryptography;
using System.Text.Json;

namespace Cage.Windows;

internal static class UpdateInstaller
{
    [System.Runtime.InteropServices.DllImport("user32.dll", CharSet = System.Runtime.InteropServices.CharSet.Unicode)]
    private static extern int MessageBoxW(IntPtr owner, string text, string title, uint flags);
    public static void VerifyArchive(string path, string hash, string version, string publisher)
    {
        UpdatePolicy.DownloadPath(path, AppUpdater.Root); UpdatePolicy.PlainPath(path);
        if (new FileInfo(path).Length is <= 0 or > (512L << 20)) throw new IOException("安装包大小无效");
        using var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read);
        var actual = Convert.ToHexString(SHA256.HashData(stream));
        if (hash.Length != 64 || !string.Equals(actual, hash, StringComparison.OrdinalIgnoreCase))
            throw new IOException("安装包下载后发生变化，请重新下载");
        Authenticode.SamePublisher(path, publisher);
        var metadata = FileVersionInfo.GetVersionInfo(path);
        if (metadata.ProductName != "Cage" || metadata.ProductVersion != version)
            throw new IOException("安装包产品身份或版本不匹配");
    }

    public static async Task<int> Run()
    {
        InstallPlan? plan = null;
        string? backup = null;
        var replaced = false;
        var committed = false;
        var succeeded = false;
        Process? launched = null;
        try
        {
            using var inputTimeout = new CancellationTokenSource(TimeSpan.FromMinutes(5));
            var line = await Console.In.ReadLineAsync(inputTimeout.Token);
            if (line == null || line.Length > 16384) return 1;
            plan = JsonSerializer.Deserialize<InstallPlan>(line) ?? throw new IOException("安装计划无效");
            if (!Guid.TryParse(plan.Confirmation, out _) || !Path.IsPathFullyQualified(plan.Target) || plan.Target.Contains('"') ||
                plan.Target.TrimEnd(Path.DirectorySeparatorChar) != plan.Target ||
                string.Equals(Path.GetPathRoot(plan.Target), plan.Target, StringComparison.OrdinalIgnoreCase))
                throw new IOException("安装目标无效");
            UpdatePolicy.PlainPath(plan.Target); UpdatePolicy.PlainPath(AppUpdater.Root);
            if (File.ReadAllText(Path.Combine(plan.Target, "Cage.install-id")).Trim() != "io.github.lee-xeo.claude-cage")
                throw new IOException("请先使用 Cage 安装程序安装应用，再自动更新");
            var original = Path.Combine(plan.Target, "Cage.exe");
            var ownPublisher = Authenticode.Publisher(Environment.ProcessPath!);
            if (plan.Publisher != ownPublisher) throw new IOException("安装计划发布者不匹配");
            Authenticode.SamePublisher(original, ownPublisher);
            if (!UpdatePolicy.IsNewer(plan.Version, FileVersionInfo.GetVersionInfo(original).ProductVersion ?? ""))
                throw new IOException("更新版本必须高于当前安装版本");
            VerifyArchive(plan.Archive, plan.Digest, plan.Version, ownPublisher);
            // Hold the verified file open without write/delete sharing until the
            // installer exits. Another process cannot swap it after verification.
            await using var archiveLock = new FileStream(plan.Archive, FileMode.Open, FileAccess.Read, FileShare.Read);
            VerifyArchive(plan.Archive, plan.Digest, plan.Version, ownPublisher);
            using var parent = Process.GetProcessById(plan.ParentPid);
            if (parent.StartTime.ToUniversalTime().Ticks != plan.ParentStartTicks ||
                !string.Equals(parent.MainModule?.FileName, original, StringComparison.OrdinalIgnoreCase))
                throw new IOException("更新请求进程身份不匹配");
            backup = Path.Combine(AppUpdater.Root, "previous-" + plan.Confirmation);
            if (Directory.Exists(backup)) throw new IOException("更新备份目录已存在");
            // Preparation is reversible and completes before the controller stops.
            CopyTree(plan.Target, backup);
            Authenticode.SamePublisher(UpdateRecovery.BackendPath(backup), ownPublisher);
            Console.WriteLine("READY"); await Console.Out.FlushAsync();
            if (await Console.In.ReadLineAsync(inputTimeout.Token) != "COMMIT") return 1;
            committed = true;
            await parent.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(45));
            VerifyArchive(plan.Archive, plan.Digest, plan.Version, ownPublisher);
            // NSIS requires /D= to be the final, unquoted argument; the executable
            // path is passed separately and no command shell is involved.
            using (var installer = Process.Start(new ProcessStartInfo(plan.Archive) {
                UseShellExecute = false, CreateNoWindow = true,
                Arguments = "/S /UPDATE /D=" + plan.Target,
                WorkingDirectory = Path.GetDirectoryName(plan.Archive),
            }) ?? throw new IOException("无法启动 Windows 安装器"))
            {
                replaced = true;
                try { await installer.WaitForExitAsync().WaitAsync(TimeSpan.FromMinutes(10)); }
                catch (TimeoutException) { if (!installer.HasExited) installer.Kill(true); await installer.WaitForExitAsync(); throw; }
                if (installer.ExitCode != 0) throw new IOException("Windows 安装程序未完成安装");
            }
            EnsureInstallationComplete(plan.Target);
            Authenticode.SamePublisher(original, ownPublisher);
            if (FileVersionInfo.GetVersionInfo(original).ProductVersion != plan.Version)
                throw new IOException("安装后版本不匹配");
            var marker = Path.Combine(AppUpdater.Root, "launch-" + plan.Confirmation + ".json");
            if (File.Exists(marker)) File.Delete(marker);
            var launch = new ProcessStartInfo(original) { UseShellExecute = false, WorkingDirectory = plan.Target };
            launch.Environment["CAGE_UPDATE_CONFIRMATION"] = plan.Confirmation;
            launched = Process.Start(launch) ?? throw new IOException("新版 Cage 无法启动");
            var deadline = DateTime.UtcNow.AddSeconds(120);
            var ready = false;
            while (DateTime.UtcNow < deadline && !launched.HasExited)
            {
                if (File.Exists(marker) && new FileInfo(marker).Length < 16384)
                {
                    try
                    {
                        var confirmation = JsonDocument.Parse(await File.ReadAllTextAsync(marker)).RootElement;
                        ready = confirmation.Text("version") == plan.Version;
                    }
                    catch (IOException) { }
                    catch (JsonException) { }
                    if (ready) break;
                }
                await Task.Delay(500);
            }
            if (!ready) throw new IOException("新版工作台未能完成启动确认");
            succeeded = true;
            Result(true, "Cage 已更新至 " + plan.Version);
            // The currently executing helper cannot delete itself on Windows.
            // Keep this small signed file; the next successful update may remove it.
            return 0;
        }
        catch (Exception error)
        {
            if (succeeded) return 0;
            var detail = error.Message;
            var recoveryIncomplete = false;
            if (replaced && plan != null && backup != null)
            {
                try
                {
                    // NSIS and this helper both require a newly created named
                    // object and retain its handle for the entire transaction.
                    using var installationLock = new Mutex(false, "Global\\Cage.Installation", out var ownsInstallation);
                    if (!ownsInstallation) throw new IOException("另一个安装或卸载正在进行，已保留更新备份，请等待完成后恢复。");
                    EnsureInstallationComplete(plan.Target);
                    // Stop the exact new controller through its lifecycle API before
                    // restoring files. Never kill other Cage instances by name.
                    Authenticode.SamePublisher(UpdateRecovery.BackendPath(backup), plan.Publisher);
                    await UpdateRecovery.PrepareRollback(backup);
                    if (launched is { HasExited: false }) { launched.Kill(); await launched.WaitForExitAsync(); }
                    var restored = plan.Target + ".restore-" + plan.Confirmation;
                    var failed = plan.Target + ".failed-" + plan.Confirmation;
                    CopyTree(backup, restored);
                    var receipt = plan.Target + ".cage-installation.json";
                    await PreserveFiles("prepare", plan.Target, restored, receipt, failed);
                    Directory.Move(plan.Target, failed);
                    try
                    {
                        await PreserveFiles("move", failed, restored, receipt);
                        Directory.Move(restored, plan.Target);
                    }
                    catch
                    {
                        await PreserveFiles("restore", restored, failed, receipt);
                        Directory.Move(failed, plan.Target);
                        await PreserveFiles("complete", "", plan.Target, receipt);
                        throw;
                    }
                    await PreserveFiles("cleanup", failed, plan.Target, receipt);
                    EnsureInstallationComplete(plan.Target);
                    UpdateRecovery.CleanupDirectory(backup); backup = null;
                    Result(false, "更新未完成，已恢复原应用：" + detail);
                    Process.Start(new ProcessStartInfo(Path.Combine(plan.Target, "Cage.exe")) { UseShellExecute = false, WorkingDirectory = plan.Target });
                    return 1;
                }
                catch (Exception recovery)
                {
                    recoveryIncomplete = true;
                    detail += "。自动恢复未完成。" + recovery.Message;
                    if (File.Exists(plan.Target + ".cage-installation.json"))
                        detail += "。恢复目录记录在 " + plan.Target + ".cage-installation.json";
                    if (backup != null) detail += "。程序备份保留在 " + backup;
                }
            }
            Result(false, "更新未完成：" + detail);
            if (recoveryIncomplete && OperatingSystem.IsWindows()) MessageBoxW(IntPtr.Zero, detail, "Cage 更新需要恢复", 0x00010010);
            if (committed && !replaced && plan != null)
            {
                try
                {
                    var original = Path.Combine(plan.Target, "Cage.exe");
                    Authenticode.SamePublisher(original, plan.Publisher);
                    Process.Start(new ProcessStartInfo(original) { UseShellExecute = false, WorkingDirectory = plan.Target });
                }
                catch (Exception restart) when (restart is IOException or System.ComponentModel.Win32Exception) { }
            }
            return 1;
        }
        finally
        {
            launched?.Dispose();
            if (succeeded && plan != null && backup != null)
                UpdateRecovery.CleanupAfterSuccess(backup, Path.GetDirectoryName(plan.Archive)!, Path.Combine(AppUpdater.Root, "launch-" + plan.Confirmation + ".json"));
            else if (!replaced) UpdateRecovery.CleanupDirectory(backup);
        }
    }

    private static void Result(bool success, string message)
    {
        try
        {
            Directory.CreateDirectory(AppUpdater.Root); UpdatePolicy.PlainPath(AppUpdater.Root);
            File.WriteAllText(Path.Combine(AppUpdater.Root, "last-result.json"), JsonSerializer.Serialize(new { success, message }));
        }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }

    private static void EnsureInstallationComplete(string target)
    {
        var receipt = target + ".cage-installation.json";
        if (!File.Exists(receipt) && Directory.Exists(target)) return;
        var message = "安装目录或个人文件仍需恢复，已停止自动启动。请保留并检查 " + receipt;
        if (File.Exists(receipt) && new FileInfo(receipt).Length <= 16 * 1024 * 1024)
        {
            try
            {
                using var document = JsonDocument.Parse(File.ReadAllText(receipt));
                foreach (var name in new[] { "stagePath", "backupPath" })
                    if (document.RootElement.Text(name) is { Length: > 0 } path) message += "。保留目录 " + path;
            }
            catch (Exception error) when (error is IOException or JsonException or InvalidOperationException) { }
        }
        throw new IOException(message);
    }

    private static async Task PreserveFiles(string operation, string source, string target, string receipt, string? backupPath = null)
    {
        var directory = receipt + ".helper-" + Guid.NewGuid().ToString("N");
        Directory.CreateDirectory(directory);
        var script = Path.Combine(directory, "installation-files.ps1");
        try
        {
            await using (var resource = typeof(UpdateInstaller).Assembly.GetManifestResourceStream("Cage.InstallationFiles.ps1")
                ?? throw new IOException("缺少安装文件保留组件"))
            await using (var output = File.Create(script)) { await resource.CopyToAsync(output); }
            await using (var resource = typeof(UpdateInstaller).Assembly.GetManifestResourceStream("Cage.VerifiedFileDeletion.cs")
                ?? throw new IOException("缺少安装文件校验组件"))
            await using (var output = File.Create(Path.Combine(directory, "verified-file-deletion.cs"))) { await resource.CopyToAsync(output); }
            using var deadline = new CancellationTokenSource(TimeSpan.FromMinutes(10));
            var executable = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.System), "WindowsPowerShell", "v1.0", "powershell.exe");
            var arguments = new List<string> { "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script,
                "-Operation", operation, "-Source", source, "-Target", target, "-Receipt", receipt, "-LegacyTarget" };
            if (backupPath != null) { arguments.Add("-Backup"); arguments.Add(backupPath); }
            using var process = new Process { StartInfo = Backend.StartInfo(executable, arguments.ToArray()) };
            if (!process.Start()) throw new IOException("无法启动安装文件保留组件");
            using var abort = deadline.Token.Register(() => { try { if (!process.HasExited) process.Kill(); } catch (InvalidOperationException) { } });
            var outputText = Backend.ReadLimited(process.StandardOutput, 65536, deadline.Token);
            var errorText = Backend.ReadLimited(process.StandardError, 65536, deadline.Token);
            await process.WaitForExitAsync(deadline.Token);
            await outputText;
            var error = await errorText;
            if (process.ExitCode != 0) throw new IOException("安装文件保留未完成：" + error.Trim());
        }
        finally
        {
            UpdateRecovery.CleanupDirectory(directory);
        }
    }

    private static void CopyTree(string source, string target)
    {
        source = Path.GetFullPath(source).TrimEnd(Path.DirectorySeparatorChar);
        target = Path.GetFullPath(target).TrimEnd(Path.DirectorySeparatorChar);
        var comparison = OperatingSystem.IsWindows() ? StringComparison.OrdinalIgnoreCase : StringComparison.Ordinal;
        if (string.Equals(source, target, comparison) || target.StartsWith(source + Path.DirectorySeparatorChar, comparison))
            throw new InvalidDataException("更新备份目录位于程序目录内，无法安全备份。请使用完整安装包手动更新。");
        UpdatePolicy.PlainPath(source); UpdatePolicy.PlainPath(target); Directory.CreateDirectory(target);
        HashSet<string>? owned = null;
        var manifest = Path.Combine(source, "Cage.install-files.json");
        if (File.Exists(manifest) && (File.GetAttributes(manifest) & FileAttributes.ReparsePoint) == 0)
        {
            try
            {
                using var document = JsonDocument.Parse(File.ReadAllText(manifest));
                if (document.RootElement.GetProperty("version").GetInt32() == 1)
                {
                    var paths = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
                    foreach (var entry in document.RootElement.GetProperty("files").EnumerateArray())
                    {
                        var path = entry.GetProperty("path").GetString();
                        var digest = entry.GetProperty("sha256").GetString();
                        if (string.IsNullOrEmpty(path) || path.StartsWith('/') || path.Contains('\\') || path.Contains(':') ||
                            path.Split('/').Any(part => part is "." or "..") || digest?.Length != 64 || !digest.All(char.IsAsciiHexDigit))
                            throw new JsonException("Invalid installation file record");
                        paths.Add(path);
                    }
                    if (paths.Count > 0) { paths.Add("Cage.install-files.json"); owned = paths; }
                }
            }
            catch (Exception error) when (error is IOException or JsonException or InvalidOperationException or KeyNotFoundException or FormatException) { }
        }
        long size = 0; var count = 0;
        var pending = new Queue<string>(); pending.Enqueue(source);
        while (pending.TryDequeue(out var directory))
        foreach (var entry in Directory.EnumerateFileSystemEntries(directory))
        {
            var attributes = File.GetAttributes(entry);
            if (++count > 100000) throw new IOException("安装目录文件过多，无法安全备份");
            // User links remain in the installed tree. NSIS and rollback move
            // them with their user subtree, preserving link identity and targets.
            if ((attributes & FileAttributes.ReparsePoint) != 0) continue;
            var relative = Path.GetRelativePath(source, entry);
            var key = relative.Replace(Path.DirectorySeparatorChar, '/');
            if (owned != null && !owned.Contains(key) && !owned.Any(path => path.StartsWith(key + "/", StringComparison.OrdinalIgnoreCase))) continue;
            var destination = Path.Combine(target, relative);
            if ((attributes & FileAttributes.Directory) != 0) { Directory.CreateDirectory(destination); pending.Enqueue(entry); }
            else
            {
                size += new FileInfo(entry).Length;
                if (size > (4L << 30)) throw new IOException("安装目录超过允许的备份大小");
                File.Copy(entry, destination, false);
            }
        }
    }
}
