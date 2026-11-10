using Cage.GuestClipboard;

static void Check(bool value, string message) { if(!value) throw new Exception(message); }
var events = new List<InputStroke>();
var unlocked = true; var failKey = -1; var failUp = false; var focused = true;
var state = new NativeInputCoordinator(stroke =>
{
    if(stroke.Value == failKey || stroke.Up && failUp) return false;
    events.Add(stroke); return true;
});
void Validate() { if(!focused) throw new InvalidDataException("unfocused"); }
bool ReleaseAllowed() => unlocked;
void Emit(NativeInputCoordinator.Lease lease, params InputStroke[] strokes) => state.Emit(lease, strokes, Validate, ReleaseAllowed);

var a = state.Open(); var b = state.Open();
Emit(a, InputStroke.Key(17,false), InputStroke.Button(1,false));
Emit(b, InputStroke.Key(17,false), InputStroke.Button(1,false));
var before = events.Count; state.Close(a);
Check(events.Count == before && state.KeyHeld(17) && state.Buttons(b) == 1, "Disconnect released another viewer's key or button.");
state.Close(b);
Check(events.Skip(before).Count() == 2 && events.Skip(before).All(e => e.Up), "The last owner did not emit exactly its two ups.");

var held = state.Open(); Emit(held, InputStroke.Key(17,false));
var foreign = state.Open(); before = events.Count; Emit(foreign, InputStroke.Key(17,true));
Check(events.Count == before && state.KeyHeld(17), "An unowned up released a foreign key.");
state.Close(foreign);
var macro = state.Open(); Emit(macro, InputStroke.Key(17,false), InputStroke.Key(86,false), InputStroke.Key(86,true), InputStroke.Key(17,true)); state.Close(macro);
Check(state.KeyHeld(17), "A paste macro consumed the viewer's held modifier.");
focused = false; before = events.Count; state.Close(held);
Check(events.Count == before+1 && events[^1].Up, "Cleanup required foreground focus or emitted text."); focused = true;

var partial = state.Open(); failKey = 86;
try { Emit(partial, InputStroke.Key(17,false), InputStroke.Key(86,false)); throw new Exception("Partial injection did not fail."); } catch(InvalidDataException) { }
failKey = -1; before = events.Count; state.Close(partial);
Check(events.Count == before+1 && events[^1] == InputStroke.Key(17,true), "Partial injection invented an unobserved down or lost the successful modifier.");

var locked = state.Open(); Emit(locked, InputStroke.Key(17,false), InputStroke.Button(4,false));
unlocked = false; before = events.Count; state.Close(locked); state.Retry();
Check(events.Count == before && state.KeyHeld(17), "Lock cleanup injected into another desktop or discarded a pending up.");
unlocked = true; state.Retry();
Check(events.Skip(before).Count() == 2 && events.Skip(before).All(e => e.Up) && !state.KeyHeld(17), "Unlock did not release pending ups.");

var changedDocument = state.Open(); Emit(changedDocument, InputStroke.Key(17,false), InputStroke.Key(16,false));
unlocked=false; focused=false; before=events.Count; state.ReleaseInputs(changedDocument,[InputStroke.Key(17,true)]);
Check(events.Count==before && state.KeyHeld(17) && state.KeyHeld(16), "A changed document lost the pending modifier or another live key.");
unlocked=true; state.Retry();
Check(!state.KeyHeld(17) && state.KeyHeld(16) && events[^1]==InputStroke.Key(17,true), "A document-change retry released more than the requested own key.");
state.Close(changedDocument); focused=true;

var failedRelease = state.Open(); Emit(failedRelease, InputStroke.Key(18,false)); failUp = true; before = events.Count; state.Close(failedRelease);
Check(events.Count == before && state.KeyHeld(18), "A failed up was recorded as successful."); failUp = false; state.Retry();
Check(!state.KeyHeld(18) && events[^1] == InputStroke.Key(18,true), "A failed up was not retried.");

var reset = state.Open(); Emit(reset, InputStroke.Key(16,false)); state.Reset(reset); Emit(reset, InputStroke.Key(16,false), InputStroke.Key(16,true)); state.Close(reset);
before = events.Count;
try { Emit(reset, InputStroke.Key(17,false)); throw new Exception("A closed lease accepted a late down."); } catch(InvalidDataException) { }
Check(events.Count == before, "A late down was injected after lease cleanup.");
Console.WriteLine("Native input leases passed: shared owners, foreign ups, macro, focus change, partial injection, lock, failed release, reset and late down.");
