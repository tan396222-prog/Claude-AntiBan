using System.Drawing;

namespace Cage.Windows;

internal sealed record WindowPlacement(int X, int Y, int Width, int Height, int Dpi, bool Maximized);

internal static class WindowLayout
{
    public static Size Minimum(Rectangle work, int dpi) => new(
        Math.Min(work.Width, (int)Math.Round(820 * dpi / 96.0)),
        Math.Min(work.Height, (int)Math.Round(580 * dpi / 96.0)));

    public static Rectangle Bounds(Rectangle work, int dpi, WindowPlacement? saved)
    {
        var minimum = Minimum(work, dpi);
        int width, height, x, y;
        if (saved is { Width: > 0, Height: > 0, Dpi: > 0 })
        {
            width = (int)Math.Round(saved.Width * dpi / (double)saved.Dpi);
            height = (int)Math.Round(saved.Height * dpi / (double)saved.Dpi);
            x = saved.X; y = saved.Y;
        }
        else
        {
            var scale = Math.Min(work.Width * .85 / 1280, work.Height * .85 / 850);
            width = (int)Math.Round(1280 * scale); height = (int)Math.Round(850 * scale);
            x = work.Left + (work.Width - width) / 2;
            y = work.Top + (work.Height - height) / 2;
        }
        width = Math.Clamp(width, minimum.Width, work.Width);
        height = Math.Clamp(height, minimum.Height, work.Height);
        return new Rectangle(Math.Clamp(x, work.Left, work.Right - width),
            Math.Clamp(y, work.Top, work.Bottom - height), width, height);
    }
}
