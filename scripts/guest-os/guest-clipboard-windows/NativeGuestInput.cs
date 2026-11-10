using System.Runtime.InteropServices;

namespace Cage.GuestClipboard;

internal readonly record struct InputStroke(ushort Value, bool Up, bool Unicode = false, int MouseButton = 0, int WheelDelta = 0, bool Horizontal = false)
{
    internal static InputStroke Key(ushort value, bool up, bool unicode = false) => new(value, up, unicode);
    internal static InputStroke Button(int bit, bool up) => new(0, up, false, bit);
    internal static InputStroke Wheel(int delta, bool horizontal = false) => new(0, false, false, 0, delta, horizontal);
    internal InputStroke Press => this with { Up = false };
}

// One coordinator covers the GUI process's kernel input state. A lease never
// appears on the wire. Callbacks contain immutable, server-verified ownership;
// release callbacks must not acquire an RPC lock or focus a window.
internal sealed class NativeInputCoordinator(Func<InputStroke, bool> inject)
{
    internal sealed class Lease
    {
        internal readonly Dictionary<InputStroke, Func<bool>> Pressed = [];
        internal readonly Dictionary<uint, InputStroke[]> Symbols = [];
        internal readonly HashSet<InputStroke> Releasing = [];
        internal bool Closed, Resetting;
    }
    private readonly object sync = new();
    private readonly HashSet<Lease> leases = [];
    private readonly Dictionary<InputStroke, HashSet<Lease>> owners = [];
    internal Lease Open() { lock(sync) { var lease = new Lease(); leases.Add(lease); return lease; } }
    internal bool KeyHeld(ushort key) { lock(sync) return owners.ContainsKey(InputStroke.Key(key, false)); }
    internal int Buttons(Lease lease) { lock(sync) return lease.Pressed.Keys.Aggregate(0, (mask, key) => mask | key.MouseButton); }
    internal bool Owns(Lease lease, InputStroke stroke) { lock(sync) return lease.Pressed.ContainsKey(stroke.Press); }
    internal void Emit(Lease lease, InputStroke[] strokes, Action validate, Func<bool> releaseAllowed)
    {
        lock(sync)
        {
            if(!leases.Contains(lease) || lease.Closed || lease.Resetting) throw new InvalidDataException("native_input_lease_closed");
            validate();
            foreach(var stroke in strokes)
            {
                var key = stroke.Press;
                if(stroke.WheelDelta == 0 && stroke.Up)
                {
                    // A viewer cannot release another viewer's input. When both
                    // pressed the same key, the last owner sends the physical up.
                    if(!lease.Pressed.ContainsKey(key)) continue;
                    if(owners[key].Count > 1) { Forget(lease, key); continue; }
                }
                if(!inject(stroke)) throw new InvalidDataException("native_input_failed");
                if(stroke.WheelDelta != 0) continue;
                if(stroke.Up) Forget(lease, key);
                else
                {
                    if(!owners.TryGetValue(key, out var set)) owners[key] = set = [];
                    set.Add(lease); lease.Pressed[key] = releaseAllowed; lease.Releasing.Remove(key);
                }
            }
            validate();
        }
    }
    internal void Close(Lease lease) { lock(sync) { lease.Closed = true; Release(lease); } }
    internal void Reset(Lease lease) { lock(sync) { lease.Resetting = true; Release(lease); } }
    internal void ReleaseInputs(Lease lease,InputStroke[] strokes){lock(sync){foreach(var stroke in strokes)if(lease.Pressed.ContainsKey(stroke.Press))lease.Releasing.Add(stroke.Press);Release(lease);}}
    internal void Retry() { lock(sync) foreach(var lease in leases.Where(l => l.Closed || l.Resetting || l.Releasing.Count>0).ToArray()) Release(lease); }
    internal void CloseAll() { lock(sync) foreach(var lease in leases.ToArray()) { lease.Closed = true; Release(lease); } }
    private void Release(Lease lease)
    {
        foreach(var item in lease.Pressed.Where(item=>lease.Closed||lease.Resetting||lease.Releasing.Contains(item.Key)).ToArray())
        {
            if(owners[item.Key].Count > 1) { Forget(lease, item.Key); continue; }
            try { if(item.Value() && inject(item.Key with { Up = true })) Forget(lease, item.Key); }
            catch { /* Keep the successful down until this GUI can release it. */ }
        }
        if(lease.Pressed.Count == 0)
        {
            lease.Symbols.Clear(); lease.Resetting = false;
            if(lease.Closed) leases.Remove(lease);
        }
    }
    private void Forget(Lease lease, InputStroke key)
    {
        lease.Pressed.Remove(key);lease.Releasing.Remove(key);
        if(owners.TryGetValue(key, out var set) && set.Remove(lease) && set.Count == 0) owners.Remove(key);
    }
}

internal static class NativeGuestInput
{
    private static readonly NativeInputCoordinator State = new(Inject);
    private static readonly System.Threading.Timer Retry = new(_ => State.Retry(), null, 250, 250);
    static NativeGuestInput() => AppDomain.CurrentDomain.ProcessExit += (_, _) => { Retry.Dispose(); State.CloseAll(); };
    internal static NativeInputCoordinator.Lease OpenLease() => State.Open();
    internal static void CloseLease(NativeInputCoordinator.Lease lease) => State.Close(lease);
    internal static void ResetLease(NativeInputCoordinator.Lease lease) => State.Reset(lease);
    internal static void ReleaseInputs(NativeInputCoordinator.Lease lease,InputStroke[] strokes)=>State.ReleaseInputs(lease,strokes);
    internal static bool PressedKey(ushort key) => State.KeyHeld(key);
    internal static int PressedButtons(NativeInputCoordinator.Lease lease) => State.Buttons(lease);
    internal static bool Owns(NativeInputCoordinator.Lease lease, InputStroke stroke) => State.Owns(lease, stroke);
    internal static void Emit(NativeInputCoordinator.Lease lease, InputStroke[] strokes, Action validateBeforeAfter, Func<bool> releaseAllowed) => State.Emit(lease, strokes, validateBeforeAfter, releaseAllowed);
    private static bool Inject(InputStroke stroke)
    {
        var input = new Input();
        if(stroke.WheelDelta != 0) input.Data.Mouse = new Mouse { Flags = stroke.Horizontal ? 0x1000u : 0x800u, Data = unchecked((uint)stroke.WheelDelta) };
        else if(stroke.MouseButton != 0) input.Data.Mouse = new Mouse { Flags = (stroke.MouseButton, stroke.Up) switch { (1,false) => 2u, (1,true) => 4u, (2,false) => 32u, (2,true) => 64u, (4,false) => 8u, (4,true) => 16u, _ => throw new InvalidDataException("native_input_invalid") } };
        else { input.Type = 1; input.Data.Key = new Key { Virtual = stroke.Unicode ? (ushort)0 : stroke.Value, Scan = stroke.Unicode ? stroke.Value : (ushort)0, Flags = (stroke.Unicode ? 4u : 0) | (stroke.Up ? 2u : 0) }; }
        return SendInput(1, [input], Marshal.SizeOf<Input>()) == 1;
    }
    [StructLayout(LayoutKind.Sequential)] private struct Mouse { internal int X,Y; internal uint Data,Flags,Time; internal UIntPtr Extra; }
    [StructLayout(LayoutKind.Sequential)] private struct Key { internal ushort Virtual,Scan; internal uint Flags,Time; internal UIntPtr Extra; }
    [StructLayout(LayoutKind.Explicit)] private struct Data { [FieldOffset(0)] internal Mouse Mouse; [FieldOffset(0)] internal Key Key; }
    [StructLayout(LayoutKind.Sequential)] private struct Input { internal uint Type; internal Data Data; }
    [DllImport("user32.dll")] private static extern uint SendInput(uint count, Input[] inputs, int size);
}
