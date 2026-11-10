using System.Diagnostics;
using System.Runtime.CompilerServices;
using System.Runtime.InteropServices;
using System.Text.Json.Nodes;

namespace Cage.GuestClipboard;

internal static class ResourcesRpc
{
    [ModuleInitializer]
    internal static void Register() { if (GuestOperations.Names.Contains("resources")) GuestOperations.Register("resources", Run); }
    internal static async Task Run(Stream stream, CancellationToken token, Func<bool> available)
    {
        if (!available()) throw new InvalidDataException("requires_gui_login");
        var input = await Protocol.Read(stream, 4096, token) ?? throw new EndOfStreamException();
        if (input["action"]?.GetValue<string>() != "sample") throw new InvalidDataException("resource_operation_invalid");
        if (!GetSystemTimes(out var idleBefore, out var kernelBefore, out var userBefore)) throw new InvalidDataException("resource_sample_failed");
        await Task.Delay(150, token);
        if (!available() || !GetSystemTimes(out var idle, out var kernel, out var user)) throw new InvalidDataException("requires_gui_login");
        var state = new MemoryStatus { Length = (uint)Marshal.SizeOf<MemoryStatus>() };
        if (!GlobalMemoryStatusEx(ref state) || state.AvailablePhysical > state.TotalPhysical) throw new InvalidDataException("resource_sample_failed");
        var total = kernel + user - kernelBefore - userBefore;
        var idleDelta = idle - idleBefore;
        var cpu = total == 0 ? 0 : Math.Clamp(100.0 * (total - Math.Min(total, idleDelta)) / total, 0, 100);
        var now = DateTimeOffset.UtcNow;
        await Protocol.Write(stream, new()
        {
            ["version"] = 1, ["pid"] = Environment.ProcessId, ["cpus"] = Environment.ProcessorCount,
            ["updated_at"] = now.ToString("O"), ["observed_at"] = now.ToString("O"), ["cpu_percent"] = cpu,
            ["guest"] = new JsonObject { ["total_bytes"] = state.TotalPhysical, ["available_bytes"] = state.AvailablePhysical },
            ["unavailable_observations"] = new JsonArray("oom_kills", "memory_stall_percent", "swap_total_bytes", "swap_free_bytes", "host_pressure")
        }, token);
    }
    [StructLayout(LayoutKind.Sequential)] private struct MemoryStatus
    {
        internal uint Length, Load;
        internal ulong TotalPhysical, AvailablePhysical, TotalPageFile, AvailablePageFile, TotalVirtual, AvailableVirtual, AvailableExtendedVirtual;
    }
    [DllImport("kernel32.dll", SetLastError = true)] [return: MarshalAs(UnmanagedType.Bool)] private static extern bool GlobalMemoryStatusEx(ref MemoryStatus status);
    [DllImport("kernel32.dll", SetLastError = true)] [return: MarshalAs(UnmanagedType.Bool)] private static extern bool GetSystemTimes(out ulong idle, out ulong kernel, out ulong user);
}
