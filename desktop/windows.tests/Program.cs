using Cage.Windows;

// The recovery integration tests launch a copy of this executable through the
// same subprocess/JSON boundary used by the shipped updater.
if (args is ["prepare-update", "--json", "--stop"])
{
    File.WriteAllText(Path.Combine(AppContext.BaseDirectory, "controller-invoked"), Environment.ProcessId.ToString());
    File.WriteAllText(Path.Combine(AppContext.BaseDirectory, "profile-invoked"), Environment.GetEnvironmentVariable("CLAUDE_CAGE_CONFIG") ?? "missing");
    if (File.Exists(Path.Combine(AppContext.BaseDirectory, "broken-backend")))
    {
        Console.WriteLine("{\"status\":\"failed\"}"); Environment.Exit(7);
    }
    Console.WriteLine(File.Exists(Path.Combine(AppContext.BaseDirectory, "running-environment"))
        ? "{\"status\":\"stop_required\"}" : "{\"status\":\"prepared\"}");
    return;
}

var assertions = 0;
void Check(bool condition, string message) { assertions++; if (!condition) throw new Exception(message); }
void Reject(Action action, string message)
{
    assertions++;
    try { action(); } catch (InvalidDataException) { return; }
    throw new Exception(message);
}
Check(UpdatePolicy.IsWorkbench("http://localhost:4321/", 4321), "expected local origin rejected");
foreach (var origin in new[] { "https://localhost:4321/", "http://localhost:4322/", "http://localhost.evil:4321/", "http://user@localhost:4321/", "http://127.0.0.1:4321/", "file:///C:/workbench.html" })
    Check(!UpdatePolicy.IsWorkbench(origin, 4321), "foreign native bridge origin accepted: " + origin);
Check(UpdatePolicy.Entry("http://localhost:4321/#token").Port == 4321, "entry URL rejected");
Reject(() => UpdatePolicy.Entry("http://localhost:4321/"), "entry without token accepted");
Check(UpdatePolicy.IsNewer("0.1.0-alpha.10", "0.1.0-alpha.9"), "numeric prerelease ordering");
Check(UpdatePolicy.IsNewer("0.1.0", "0.1.0-alpha.10"), "stable release ordering");
Check(UpdatePolicy.IsNewer("1.0.0-alpha.beta", "1.0.0-alpha.10"), "text prerelease ordering");
Check(!UpdatePolicy.IsNewer("0.1.0-alpha.01", "0.1.0-alpha.0"), "leading zero accepted");
Check(!UpdatePolicy.IsNewer("0.1.0-alpha.1", "0.1.0-alpha.1"), "same version accepted");
Check(!UpdatePolicy.IsNewer("0.1.0-alpha.1", "0.1.0"), "downgrade accepted");
Check(!UpdatePolicy.IsNewer("v0.2.0", "0.1.0"), "noncanonical version accepted");
Check(!UpdatePolicy.IsNewer("0.2.0+metadata", "0.1.0"), "unmatched backend version accepted");
var root = Path.GetFullPath(Path.Combine(Path.GetTempPath(), "cage-update-policy-tests"));
var good = Path.Combine(root, "download-abc", "Cage-setup.exe");
Check(UpdatePolicy.DownloadPath(good, root) == good, "private download rejected");
foreach (var path in new[] { Path.Combine(root + "-other", "download-abc", "Cage-setup.exe"), Path.Combine(root, "verified-abc", "Cage-setup.exe"), Path.Combine(root, "download-abc", "nested", "Cage-setup.exe"), Path.Combine(root, "download-abc", "Cage.exe") })
    Reject(() => UpdatePolicy.DownloadPath(path, root), "untrusted download path accepted: " + path);
Console.WriteLine($"Windows update/bridge policy: {assertions} assertions passed.");

var integration = Path.Combine(Path.GetTempPath(), "cage-recovery-" + Guid.NewGuid());
Directory.CreateDirectory(integration);
var savedProfile = Environment.GetEnvironmentVariable("CLAUDE_CAGE_CONFIG");
try
{
    var selectedProfile = Path.Combine(integration, "selected profile", "config.json");
    Environment.SetEnvironmentVariable("CLAUDE_CAGE_CONFIG", Path.GetRelativePath(Environment.CurrentDirectory, selectedProfile));
    var backup = Path.Combine(integration, "old application");
    var candidate = Path.Combine(integration, "new application");
    void InstallFixture(string app, bool broken)
    {
        var resources = Path.Combine(app, "resources"); Directory.CreateDirectory(resources);
        foreach (var file in Directory.EnumerateFiles(AppContext.BaseDirectory))
            File.Copy(file, Path.Combine(resources, Path.GetFileName(file)));
        var controller = Path.Combine(resources, "cage.exe");
        File.Copy(Environment.ProcessPath!, controller, true);
        if (!OperatingSystem.IsWindows()) File.SetUnixFileMode(controller, File.GetUnixFileMode(Environment.ProcessPath!));
        if (broken) File.WriteAllText(Path.Combine(resources, "broken-backend"), "cannot start");
    }
    InstallFixture(backup, false); InstallFixture(candidate, true);
    await UpdateRecovery.PrepareRollback(backup);
    Check(File.Exists(Path.Combine(backup, "resources", "controller-invoked")), "recovery did not execute the old backend");
    Check(!File.Exists(Path.Combine(candidate, "resources", "controller-invoked")), "recovery executed the broken candidate backend");
    Check(File.ReadAllText(Path.Combine(backup, "resources", "profile-invoked")) == selectedProfile, "recovery child switched away from the original relative profile");
    File.WriteAllText(Path.Combine(backup, "resources", "running-environment"), "still running");
    var rejected = false;
    try { await UpdateRecovery.PrepareRollback(backup); } catch (IOException) { rejected = true; }
    Check(rejected, "recovery continued without a prepared response from the old backend");

    var garbage = Path.Combine(integration, "locked backup"); Directory.CreateDirectory(garbage);
    var lockedFile = Path.Combine(garbage, "old.dll"); File.WriteAllText(lockedFile, "old version");
    var download = Path.Combine(integration, "download"); Directory.CreateDirectory(download);
    var marker = Path.Combine(integration, "ack.json"); File.WriteAllText(marker, "confirmed");
    var liveApp = Path.Combine(candidate, "Cage.exe"); File.WriteAllText(liveApp, "healthy new application");
    FileStream? locked = null;
    if (OperatingSystem.IsWindows()) locked = new FileStream(lockedFile, FileMode.Open, FileAccess.Read, FileShare.Read);
    else File.SetUnixFileMode(garbage, UnixFileMode.UserRead | UnixFileMode.UserExecute);
    try
    {
        UpdateRecovery.CleanupAfterSuccess(garbage, download, marker);
        Check(File.ReadAllText(liveApp) == "healthy new application", "cleanup changed the confirmed new application");
        Check(File.Exists(lockedFile), "fixture did not cause the intended cleanup failure");
        Check(!Directory.Exists(download) && !File.Exists(marker), "one cleanup failure prevented independent cleanup");
    }
    finally
    {
        locked?.Dispose();
        if (!OperatingSystem.IsWindows()) File.SetUnixFileMode(garbage, UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute);
    }
    Console.WriteLine($"Windows recovery integration: {assertions - 22} assertions passed; actual child process and filesystem failures exercised.");
}
finally {
    Environment.SetEnvironmentVariable("CLAUDE_CAGE_CONFIG", savedProfile);
    Directory.Delete(integration, true);
}
