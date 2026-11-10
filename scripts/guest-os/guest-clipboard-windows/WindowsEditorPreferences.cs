using System.Text;
using System.Text.Json;

namespace Cage.GuestClipboard;

// Cage owns this preference file. Official serve-web does not consume disk
// User settings; the broker applies this one key as a low-priority web default.
internal static class WindowsEditorPreferences
{
    internal const string TrustKey = "security.workspace.trust.enabled";
    internal static bool? TrustDefault(string cageEditorHome)
    {
        var user = Path.Combine(cageEditorHome, "User");
        if (Directory.Exists(cageEditorHome) && new DirectoryInfo(cageEditorHome).Attributes.HasFlag(FileAttributes.ReparsePoint) || Directory.Exists(user) && new DirectoryInfo(user).Attributes.HasFlag(FileAttributes.ReparsePoint))
            throw new InvalidDataException("editor_package_invalid");
        Directory.CreateDirectory(user);
        var file = Path.Combine(user, "settings.json");
        var original = "{}";
        if (File.Exists(file))
        {
            var info = new FileInfo(file);
            if (info.Attributes.HasFlag(FileAttributes.ReparsePoint) || info.Length > 1 << 20) return null;
            using var snapshot = new FileStream(file, FileMode.Open, FileAccess.Read, FileShare.Read | FileShare.Delete);
            if (snapshot.Length > 1 << 20) return null;
            using var reader = new StreamReader(snapshot);
            original = reader.ReadToEnd();
        }
        try
        {
            using var document = JsonDocument.Parse(original, new JsonDocumentOptions { AllowTrailingCommas = true, CommentHandling = JsonCommentHandling.Skip });
            if (document.RootElement.ValueKind != JsonValueKind.Object) return null;
            if (document.RootElement.TryGetProperty(TrustKey, out var existing))
                return existing.ValueKind is JsonValueKind.True or JsonValueKind.False ? existing.GetBoolean() : null;
            // Insert after the opening brace, preserving comments, whitespace,
            // trailing commas and every existing setting value.
            var bytes = Encoding.UTF8.GetBytes(original);
            var reader = new Utf8JsonReader(bytes, new JsonReaderOptions { AllowTrailingCommas = true, CommentHandling = JsonCommentHandling.Skip });
            if (!reader.Read() || reader.TokenType != JsonTokenType.StartObject) return null;
            var offset = Encoding.UTF8.GetCharCount(bytes.AsSpan(0, (int)reader.BytesConsumed));
            var property = "\n  \"" + TrustKey + "\": false" + (document.RootElement.EnumerateObject().Any() ? "," : "") + "\n";
            var updated = original.Insert(offset, property);
            var temporary = file + "." + Guid.NewGuid().ToString("N") + ".tmp";
            try
            {
                File.WriteAllText(temporary, updated, new UTF8Encoding(false));
                WindowsFilePublication.Replace(temporary, file, default).GetAwaiter().GetResult();
            }
            finally { if (File.Exists(temporary)) File.Delete(temporary); }
            return false;
        }
        catch (JsonException) { return null; } // Preserve a malformed user file rather than replacing it.
    }
}
