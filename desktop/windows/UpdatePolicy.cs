using System.Globalization;
using System.Text.RegularExpressions;

namespace Cage.Windows;

internal static partial class UpdatePolicy
{
    public static bool IsWorkbench(string? raw, int port) =>
        Uri.TryCreate(raw, UriKind.Absolute, out var url) && port > 0 &&
        url.Scheme == "http" && url.Host == "localhost" && url.Port == port &&
        url.UserInfo.Length == 0;

    public static Uri Entry(string raw)
    {
        if (!Uri.TryCreate(raw, UriKind.Absolute, out var url) ||
            !IsWorkbench(raw, url.Port) || url.Port <= 0 || url.Fragment.Length < 2)
            throw new InvalidDataException("后台返回了无效的工作台地址");
        return url;
    }

    [GeneratedRegex(@"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$")]
    private static partial Regex VersionPattern();

    private static string[]? VersionParts(string value)
    {
        var match = VersionPattern().Match(value);
        if (!match.Success) return null;
        var pre = match.Groups[4].Value;
        if (pre.Split('.').Any(x => x.Length > 1 && x[0] == '0' && x.All(char.IsAsciiDigit))) return null;
        return [match.Groups[1].Value, match.Groups[2].Value, match.Groups[3].Value, pre];
    }

    private static int Number(string a, string b) => a.Length != b.Length
        ? a.Length.CompareTo(b.Length) : string.CompareOrdinal(a, b);

    public static bool IsNewer(string candidate, string current)
    {
        var a = VersionParts(candidate); var b = VersionParts(current);
        if (a == null || b == null) return false;
        for (var i = 0; i < 3; i++)
            if (a[i] != b[i]) return Number(a[i], b[i]) > 0;
        if (a[3].Length == 0 || b[3].Length == 0) return a[3].Length == 0 && b[3].Length != 0;
        var x = a[3].Split('.'); var y = b[3].Split('.');
        for (var i = 0; i < Math.Min(x.Length, y.Length); i++)
        {
            if (x[i] == y[i]) continue;
            var xn = x[i].All(char.IsAsciiDigit); var yn = y[i].All(char.IsAsciiDigit);
            if (xn != yn) return !xn;
            return (xn ? Number(x[i], y[i]) : string.CompareOrdinal(x[i], y[i])) > 0;
        }
        return x.Length > y.Length;
    }

    public static string DownloadPath(string raw, string updateRoot)
    {
        var path = Path.GetFullPath(raw);
        var directory = Path.GetDirectoryName(path) ?? "";
        if (!string.Equals(Path.GetDirectoryName(directory), Path.GetFullPath(updateRoot).TrimEnd(Path.DirectorySeparatorChar), StringComparison.OrdinalIgnoreCase) ||
            !Path.GetFileName(directory).StartsWith("download-", StringComparison.Ordinal) ||
            !string.Equals(Path.GetFileName(path), "Cage-setup.exe", StringComparison.Ordinal))
            throw new InvalidDataException("更新安装包不属于 Cage 的私有下载目录");
        return path;
    }

    public static void PlainPath(string path)
    {
        var entry = Path.GetFullPath(path);
        while (!string.IsNullOrEmpty(entry))
        {
            if ((File.Exists(entry) || Directory.Exists(entry)) && (File.GetAttributes(entry) & FileAttributes.ReparsePoint) != 0)
                throw new InvalidDataException("更新路径包含符号链接或目录联接");
            entry = Path.GetDirectoryName(entry);
        }
    }
}
