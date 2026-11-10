#requires -Version 5.1
<#
.SYNOPSIS
Save one PNG of the signed-in guest user's registered Claude Desktop window.
.DESCRIPTION
Run in 64-bit Windows PowerShell in an unlocked Hyper-V guest desktop session.
Pass the VM ID obtained independently on the host. OutputPath must name a new
.png directly inside this guest user's temporary directory. A failed capture
does not publish a PNG.
#>
[CmdletBinding()]
param([Guid]$ExpectedVMId = [Guid]::Empty, [string]$OutputPath)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'claude-desktop-windows.ps1') -ExpectedVMId $ExpectedVMId

$cageWindowSource = @'
using System;
using System.Collections.Generic;
using System.Drawing;
using System.Drawing.Imaging;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading.Tasks;

public static class CageClaudeWindow {
    [StructLayout(LayoutKind.Sequential)]
    private struct Rect { public int left, top, right, bottom; }

    [DllImport("user32.dll")] private static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] private static extern bool IsIconic(IntPtr window);
    [DllImport("user32.dll")] private static extern bool GetClientRect(IntPtr window, out Rect rect);
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr window, out uint process);
    [DllImport("user32.dll")] private static extern bool PrintWindow(IntPtr window, IntPtr dc, uint flags);
    [DllImport("dwmapi.dll")] private static extern int DwmGetWindowAttribute(IntPtr window, int attr, out int value, int bytes);
    [DllImport("user32.dll")] private static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
    [DllImport("user32.dll")] private static extern IntPtr OpenInputDesktop(uint flags, bool inherit, uint access);
    [DllImport("user32.dll")] private static extern bool CloseDesktop(IntPtr desktop);
    [DllImport("user32.dll", CharSet=CharSet.Unicode)]
    private static extern bool GetUserObjectInformation(IntPtr obj, int index, StringBuilder name, int size, out int needed);

    public static bool IsDefaultInputDesktop() {
        IntPtr desktop = OpenInputDesktop(0, false, 1); // DESKTOP_READOBJECTS
        if (desktop == IntPtr.Zero) return false;
        try {
            var name = new StringBuilder(256);
            int needed;
            return GetUserObjectInformation(desktop, 2, name, name.Capacity * 2, out needed)
                && String.Equals(name.ToString(), "Default", StringComparison.OrdinalIgnoreCase);
        } finally { CloseDesktop(desktop); }
    }

    private static bool IsClaudeWindow(IntPtr window, uint pid) {
        uint owner;
        int cloaked;
        return GetWindowThreadProcessId(window, out owner) != 0 && owner == pid
            && IsWindowVisible(window) && !IsIconic(window)
            && DwmGetWindowAttribute(window, 14, out cloaked, 4) == 0 && cloaked == 0;
    }

    public static string FrameProblem(int[] rgb) {
        if (rgb.Length == 0) return "empty_frame";
        int first = rgb[0];
        bool black = true, varied = false;
        foreach (int pixel in rgb) {
            for (int shift = 0; shift <= 16; shift += 8) {
                int value = (pixel >> shift) & 255;
                if (value > 4) black = false;
                if (Math.Abs(value - ((first >> shift) & 255)) > 4) varied = true;
            }
        }
        return black ? "black_frame" : (varied ? null : "uniform_frame");
    }

    private static byte[] Capture(IntPtr window, uint pid) {
        if (!IsClaudeWindow(window, pid)) throw new InvalidOperationException("no_visible_window");
        IntPtr previousDpi = SetThreadDpiAwarenessContext(new IntPtr(-4)); // Per-monitor aware V2
        try {
            Rect bounds;
            if (!GetClientRect(window, out bounds) || bounds.right <= 0 || bounds.bottom <= 0)
                throw new InvalidOperationException("no_visible_window");
            using (var bitmap = new Bitmap(bounds.right, bounds.bottom, PixelFormat.Format24bppRgb)) {
                using (var graphics = Graphics.FromImage(bitmap)) {
                    graphics.Clear(Color.Black);
                    IntPtr dc = graphics.GetHdc();
                    try {
                        // PW_CLIENTONLY | PW_RENDERFULLCONTENT, also used by Chromium
                        // for its DirectComposition windows. Never read the desktop DC.
                        if (!PrintWindow(window, dc, 3)) throw new InvalidOperationException("window_render_failed");
                    } finally { graphics.ReleaseHdc(dc); }
                }
                if (!IsClaudeWindow(window, pid) || !IsDefaultInputDesktop())
                    throw new InvalidOperationException("window_or_desktop_changed_during_capture");
                var samples = new List<int>();
                for (int y = bitmap.Height / 20; y < bitmap.Height - bitmap.Height / 20; y += Math.Max(1, bitmap.Height / 128))
                    for (int x = bitmap.Width / 20; x < bitmap.Width - bitmap.Width / 20; x += Math.Max(1, bitmap.Width / 128))
                        samples.Add(bitmap.GetPixel(x, y).ToArgb());
                var problem = FrameProblem(samples.ToArray());
                if (problem != null) throw new InvalidOperationException(problem);
                using (var png = new MemoryStream()) {
                    bitmap.Save(png, ImageFormat.Png);
                    return png.ToArray();
                }
            }
        } finally { if (previousDpi != IntPtr.Zero) SetThreadDpiAwarenessContext(previousDpi); }
    }

    public static byte[] CaptureWithTimeout(IntPtr window, uint pid) {
        // PrintWindow waits synchronously for the window owner. A late render
        // has no filesystem side effects and cannot publish a stale PNG.
        var task = Task.Run(() => Capture(window, pid));
        if (Task.WaitAny(new Task[] { task }, 10000) < 0) throw new TimeoutException("window_render_timeout");
        return task.GetAwaiter().GetResult();
    }
}
'@

function Get-ClaudeCapturePath([string]$path) {
    if (!$path -or ![IO.Path]::IsPathRooted($path)) { throw 'Supply an absolute guest temporary .png path.' }
    $full = [IO.Path]::GetFullPath($path)
    $temp = [IO.Path]::GetTempPath().TrimEnd([IO.Path]::DirectorySeparatorChar)
    if ([IO.Path]::GetDirectoryName($full) -ine $temp -or [IO.Path]::GetExtension($full) -ine '.png' -or
        [IO.Path]::GetFileName($full).IndexOfAny([IO.Path]::GetInvalidFileNameChars()) -ge 0) {
        throw 'OutputPath must be a PNG directly inside this guest user''s temporary directory.'
    }
    if (Test-Path -LiteralPath $full) { throw 'Select a new PNG path.' }
    if ([Environment]::OSVersion.Platform -eq 'Win32NT' -and
        [IO.DriveInfo]::new([IO.Path]::GetPathRoot($full)).DriveType -ne 'Fixed') {
        throw 'Use the guest user''s local temporary drive.'
    }
    return $full
}

if ($MyInvocation.InvocationName -eq '.') { return }
if ([Environment]::OSVersion.Platform -ne 'Win32NT' -or ![Environment]::Is64BitProcess -or $PSVersionTable.PSEdition -ne 'Desktop') {
    throw 'Run this script in 64-bit Windows PowerShell 5.1 inside the guest.'
}
Assert-TargetGuest $ExpectedVMId
$file = Get-ClaudeCapturePath $OutputPath
$session = [Diagnostics.Process]::GetCurrentProcess().SessionId
if ($session -eq 0 -or ![Environment]::UserInteractive -or
    !(Get-Process -Name explorer -ErrorAction SilentlyContinue | Where-Object SessionId -eq $session)) {
    throw 'requires_gui_login: Run in the signed-in guest user''s desktop session.'
}
if (!('CageClaudeWindow' -as [type])) { Add-Type -TypeDefinition $cageWindowSource -ReferencedAssemblies System.Drawing }
if (![CageClaudeWindow]::IsDefaultInputDesktop()) { throw 'requires_gui_login: Unlock the guest desktop.' }
$registered = Get-RegisteredClaude
if (!$registered) { throw 'not_registered: Install the official x64 Claude MSIX for this guest user.' }
$target = Get-Process -Name Claude -ErrorAction SilentlyContinue | Where-Object {
    $_.SessionId -eq $session -and $_.MainWindowHandle -ne 0 -and $_.Path -eq $registered.Executable
} | Select-Object -First 1
if (!$target) { throw 'no_visible_window: Open Claude Desktop for this guest user.' }
$png = [CageClaudeWindow]::CaptureWithTimeout($target.MainWindowHandle, $target.Id)
$staged = Join-Path ([IO.Path]::GetDirectoryName($file)) ([IO.Path]::GetRandomFileName())
$stream = [IO.File]::Open($staged, 'CreateNew', 'Write', 'None')
try {
    try { $stream.Write($png, 0, $png.Length) } finally { $stream.Dispose() }
    [IO.File]::Move($staged, $file)
} finally { [IO.File]::Delete($staged) }
[pscustomobject]@{ Path = $file; Bytes = $png.Length; ProcessId = $target.Id; Version = $registered.Version } | ConvertTo-Json
