using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;

namespace Cage.Windows;

// A real Win32 edit control supplies TSF/IMM the caret while Cage owns no
// remote document text. Only this control's transient composition is read.
internal sealed class NativeInput : TextBox
{
    private readonly Action<Dictionary<string, object?>> publish;
    private string owner = "";
    private bool composing, resetting;
    private Keys lastKey;
    private bool repeating;
    private char? highSurrogate;

    internal NativeInput(Action<Dictionary<string, object?>> publish)
    {
        this.publish = publish;
        BorderStyle = BorderStyle.None; Multiline = true; TabStop = false;
        ImeMode = ImeMode.On; Visible = false; Size = new Size(1, 24);
        LostFocus += (_, _) => {
            Cancel(); Emit("deactivated"); owner = ""; Visible = false;
        };
    }

    private void Emit(string kind, string? text = null, int start = 0, int end = 0)
    {
        if (owner.Length == 0) return;
        publish(new() { ["owner"] = owner, ["kind"] = kind, ["text"] = text,
            ["selectionStart"] = start, ["selectionEnd"] = end });
    }

    internal bool ProcessRequest(JsonElement value, Control web, double zoom)
    {
        var requestedOwner = value.Text("owner");
        if (!Guid.TryParse(requestedOwner, out _)) return false;
        var operation = value.Text("operation");
        if (operation == "activate")
        {
            if (owner != requestedOwner) { Cancel(); owner = requestedOwner; }
            Visible = true; BringToFront();
        }
        else if (owner != requestedOwner) return false;
        if (operation is "activate" or "caret")
        {
            if (!value.TryGetProperty("x", out var vx) || !vx.TryGetDouble(out var x) ||
                !value.TryGetProperty("y", out var vy) || !vy.TryGetDouble(out var y) ||
                !value.TryGetProperty("height", out var vh) || !vh.TryGetDouble(out var height) ||
                !double.IsFinite(x) || !double.IsFinite(y) || !double.IsFinite(height) ||
                Math.Abs(x) > 32768 || Math.Abs(y) > 32768 || height <= 0 || height > 4096) return false;
            var scale = web.DeviceDpi / 96.0 * zoom;
            var point = Parent!.PointToClient(web.PointToScreen(new Point((int)(x * scale), (int)(y * scale))));
            Bounds = new Rectangle(point, new Size(1, Math.Max(1, (int)(height * scale))));
            if (operation == "activate") Focus();
            PositionCandidates();
        }
        else if (operation == "cancel") Cancel();
        else if (operation == "deactivate") { Cancel(); owner = ""; Visible = false; web.Focus(); }
        else return false;
        return true;
    }

    private void Cancel()
    {
        if (composing) Emit("compositionend", "");
        composing = false; resetting = true;
        if (IsHandleCreated)
        {
            var context = ImmGetContext(Handle);
            if (context != IntPtr.Zero) { ImmNotifyIME(context, 0x15, 4, 0); ImmReleaseContext(Handle, context); }
        }
        Clear(); highSurrogate = null; resetting = false;
    }

    protected override bool IsInputKey(Keys keyData) => true;
    protected override bool ProcessCmdKey(ref Message msg, Keys keyData)
    {
        if (!composing && (keyData & (Keys.Control | Keys.Alt)) != 0)
        {
            Key(keyData & Keys.KeyCode, true);
            return true;
        }
        return base.ProcessCmdKey(ref msg, keyData);
    }
    protected override void OnKeyDown(KeyEventArgs e)
    {
        lastKey = e.KeyCode;
        if (!composing && (e.Control || e.Alt || IsCommand(e.KeyCode)))
        { Key(e.KeyCode, true); e.Handled = true; }
    }
    protected override void OnKeyUp(KeyEventArgs e)
    { Key(e.KeyCode, false); e.Handled = true; }
    protected override void OnKeyPress(KeyPressEventArgs e)
    {
        if (composing || resetting) return;
        e.Handled = true;
        if (char.IsHighSurrogate(e.KeyChar)) { highSurrogate = e.KeyChar; return; }
        if (char.IsLowSurrogate(e.KeyChar) && highSurrogate is char first)
        { Emit("input", new string([first, e.KeyChar])); highSurrogate = null; return; }
        highSurrogate = null;
        if (!char.IsControl(e.KeyChar))
        {
            if (e.KeyChar <= 127) Key(lastKey, true, e.KeyChar.ToString());
            else Emit("input", e.KeyChar.ToString());
        }
    }
    protected override void OnTextChanged(EventArgs e)
    {
        base.OnTextChanged(e);
        if (!composing && !resetting && Text.Length > 0) Clear();
    }
    private static bool IsCommand(Keys key) => key is Keys.Enter or Keys.Tab or Keys.Back or Keys.Delete or Keys.Escape or
        Keys.Left or Keys.Right or Keys.Up or Keys.Down or Keys.Home or Keys.End or Keys.PageUp or Keys.PageDown or
        Keys.ShiftKey or Keys.ControlKey or Keys.Menu or Keys.LWin or Keys.RWin or Keys.CapsLock || key >= Keys.F1 && key <= Keys.F24;

    private void Key(Keys key, bool down, string? text = null)
    {
        if (owner.Length == 0) return;
        var pair = key switch {
            Keys.Enter => ("Enter", "Enter"), Keys.Tab => ("Tab", "Tab"), Keys.Back => ("Backspace", "Backspace"),
            Keys.Delete => ("Delete", "Delete"), Keys.Escape => ("Escape", "Escape"), Keys.Left => ("ArrowLeft", "ArrowLeft"),
            Keys.Right => ("ArrowRight", "ArrowRight"), Keys.Up => ("ArrowUp", "ArrowUp"), Keys.Down => ("ArrowDown", "ArrowDown"),
            Keys.ShiftKey => ("Shift", "ShiftLeft"), Keys.ControlKey => ("Control", "ControlLeft"), Keys.Menu => ("Alt", "AltLeft"),
            Keys.LWin => ("Meta", "MetaLeft"), Keys.RWin => ("Meta", "MetaRight"), Keys.CapsLock => ("CapsLock", "CapsLock"),
            Keys.Home => ("Home", "Home"), Keys.End => ("End", "End"), Keys.PageUp => ("PageUp", "PageUp"), Keys.PageDown => ("PageDown", "PageDown"),
            _ when key >= Keys.A && key <= Keys.Z => (key.ToString().ToLowerInvariant(), "Key" + key),
            _ when key >= Keys.D0 && key <= Keys.D9 => (((int)key - (int)Keys.D0).ToString(), "Digit" + ((int)key - (int)Keys.D0)),
            _ when key >= Keys.F1 && key <= Keys.F24 => (key.ToString(), key.ToString()),
            _ => (text ?? "Unidentified", "Unidentified")
        };
        publish(new() { ["owner"] = owner, ["kind"] = down ? "keydown" : "keyup", ["key"] = text ?? pair.Item1,
            ["code"] = pair.Item2, ["textKey"] = text != null, ["repeat"] = repeating,
            ["ctrlKey"] = (ModifierKeys & Keys.Control) != 0, ["altKey"] = (ModifierKeys & Keys.Alt) != 0,
            ["shiftKey"] = (ModifierKeys & Keys.Shift) != 0, ["metaKey"] = false });
    }

    protected override void WndProc(ref Message message)
    {
        const int Start = 0x10d, End = 0x10e, Composition = 0x10f;
        bool committed = false;
        if (message.Msg == 0x100) repeating = ((long)message.LParam & (1L << 30)) != 0;
        if (!resetting && owner.Length > 0)
        {
            if (message.Msg == Start) { composing = true; Emit("compositionstart"); }
            if (message.Msg == Composition)
            {
                var context = ImmGetContext(Handle);
                if (context != IntPtr.Zero)
                {
                    var flags = (int)message.LParam;
                    if ((flags & 0x800) != 0)
                    {
                        Emit("compositionend", CompositionText(context, 0x800)); composing = false;
                        committed = true;
                        BeginInvoke(() => { if (!composing && !IsDisposed) Clear(); });
                    }
                    if ((flags & 8) != 0)
                    {
                        if (!composing) { composing = true; Emit("compositionstart"); }
                        var text = CompositionText(context, 8);
                        var cursor = Math.Clamp(ImmGetCompositionString(context, 0x80, null, 0), 0, text.Length);
                        Emit("compositionupdate", text, cursor, cursor);
                    }
                    ImmReleaseContext(Handle, context);
                }
                PositionCandidates();
            }
            if (message.Msg == End && composing) { composing = false; Emit("compositionend", ""); }
        }
        // Cage already delivered RESULTSTR. DefWindowProc would turn it into
        // WM_IME_CHAR/WM_CHAR and send a second copy through OnKeyPress.
        if (committed) { message.Result = IntPtr.Zero; return; }
        // The control participates in native text services but paints no text.
        if (message.Msg == 0xf) { ValidateRect(Handle, IntPtr.Zero); message.Result = IntPtr.Zero; return; }
        base.WndProc(ref message);
        if (IsHandleCreated) HideCaret(Handle);
    }

    private static string CompositionText(IntPtr context, int index)
    {
        var length = ImmGetCompositionString(context, index, null, 0);
        if (length <= 0 || length > 16384) return "";
        var bytes = new byte[length];
        var count = ImmGetCompositionString(context, index, bytes, bytes.Length);
        return count > 0 ? Encoding.Unicode.GetString(bytes, 0, count) : "";
    }
    private void PositionCandidates()
    {
        if (!IsHandleCreated) return;
        var context = ImmGetContext(Handle);
        if (context == IntPtr.Zero) return;
        var candidate = new Candidate { Style = 0x80, Point = new Point(0, Height), Area = new Rect { Bottom = Height, Right = 1 } };
        ImmSetCandidateWindow(context, ref candidate);
        ImmReleaseContext(Handle, context);
    }
    [StructLayout(LayoutKind.Sequential)] private struct Rect { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)] private struct Candidate { public int Index, Style; public Point Point; public Rect Area; }
    [DllImport("imm32.dll")] private static extern IntPtr ImmGetContext(IntPtr window);
    [DllImport("imm32.dll")] private static extern bool ImmReleaseContext(IntPtr window, IntPtr context);
    [DllImport("imm32.dll", EntryPoint = "ImmGetCompositionStringW")] private static extern int ImmGetCompositionString(IntPtr context, int index, byte[]? data, int length);
    [DllImport("imm32.dll")] private static extern bool ImmSetCandidateWindow(IntPtr context, ref Candidate candidate);
    [DllImport("imm32.dll")] private static extern bool ImmNotifyIME(IntPtr context, int action, int index, int value);
    [DllImport("user32.dll")] private static extern bool HideCaret(IntPtr window);
    [DllImport("user32.dll")] private static extern bool ValidateRect(IntPtr window, IntPtr rect);
}
