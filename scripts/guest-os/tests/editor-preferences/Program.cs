using Cage.GuestClipboard;
using System.Text;

var root = Path.Combine(Path.GetTempPath(), "cage-editor-preferences-" + Guid.NewGuid().ToString("N"));
var user = Path.Combine(root, "User");
var file = Path.Combine(user, "settings.json");
try
{
    if (WindowsEditorPreferences.TrustDefault(root) != false) throw new Exception("new Cage preference was not initialized");
    var original = "{\n // retain this comment 中文\n \"editor.fontSize\": 19,\n}\n";
    File.WriteAllText(file, original);
    if (WindowsEditorPreferences.TrustDefault(root) != false || !File.ReadAllText(file).Contains(original[1..], StringComparison.Ordinal)) throw new Exception("existing comments/settings were replaced");
    foreach (var encoding in new Encoding[] { new UTF8Encoding(true), Encoding.Unicode })
    {
        File.WriteAllText(file, "{\"security.workspace.trust.enabled\":true,\"editor.fontSize\":19}", encoding);
        var before = File.ReadAllBytes(file);
        if (WindowsEditorPreferences.TrustDefault(root) != true || !File.ReadAllBytes(file).SequenceEqual(before)) throw new Exception("explicit user true or its encoding changed");
    }
    File.WriteAllText(file, "{\"security.workspace.trust.enabled\":null}");
    if (WindowsEditorPreferences.TrustDefault(root) is not null) throw new Exception("invalid explicit preference was overwritten");
    File.WriteAllText(file, "{malformed user file}");
    if (WindowsEditorPreferences.TrustDefault(root) is not null || File.ReadAllText(file) != "{malformed user file}") throw new Exception("malformed settings were replaced");
    Console.WriteLine("Cage editor preference initialization/preservation passed.");
}
finally { Directory.Delete(root, true); }
