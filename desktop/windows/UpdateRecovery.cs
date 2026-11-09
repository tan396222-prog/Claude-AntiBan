namespace Cage.Windows;

internal static class UpdateRecovery
{
    public static string BackendPath(string backup) => Path.Combine(backup, "resources", "cage.exe");

    // The installed candidate may be unable to start. Use the existing backup's
    // verified controller to check the shared profile and stop any new process.
    public static async Task PrepareRollback(string backup, CancellationToken cancellation = default)
    {
        var (exit, result) = await Backend.JsonAt(BackendPath(backup), ["prepare-update", "--json", "--stop"], cancellation);
        if (exit != 0 || result.Text("status") != "prepared")
            throw new IOException("旧版后台无法确认环境已停止，原应用备份已保留");
    }

    public static void CleanupDirectory(string? path)
    {
        if (path == null) return;
        try { Directory.Delete(path, true); }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }

    // This only runs after workbench acknowledgement. Cleanup failures leave
    // files for later removal and cannot change the installation result.
    public static void CleanupAfterSuccess(string backup, string download, string marker)
    {
        CleanupDirectory(backup);
        CleanupDirectory(download);
        try { File.Delete(marker); }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
    }
}
