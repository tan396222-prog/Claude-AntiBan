namespace Cage.GuestClipboard;

// The caller pins one owned HWND/PID and supplies its lifetime validation.
// The shared worker provides only WGC CreateForWindow, never monitor capture.
internal static class GuestWindowCapture
{
    internal static Task<byte[]> Capture(IntPtr window, int pid, int width, int height, Action validate, bool allowSolid = false, bool preserveAlpha = false)
        => ClaudeGraphicsCapture.Capture(window, pid, width, height, validate, allowSolid, preserveAlpha);
}
