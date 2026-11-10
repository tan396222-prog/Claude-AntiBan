using System.Runtime.InteropServices;

namespace Cage.Windows;

internal sealed class WindowChrome(Color background, Color foreground, bool dark)
{
    [DllImport("dwmapi.dll")]
    private static extern int DwmSetWindowAttribute(IntPtr hwnd, int attribute, ref int value, int size);

    internal static Color ContrastingText(Color value) =>
        value.GetBrightness() < 0.5 ? Color.White : Color.Black;

    internal void Apply(IntPtr window, MenuStrip? menus)
    {
        bool accessible = SystemInformation.HighContrast;
        int darkFrame = dark && !accessible ? 1 : 0;
        DwmSetWindowAttribute(window, 20, ref darkFrame, sizeof(int)); // DWMWA_USE_IMMERSIVE_DARK_MODE
        if (OperatingSystem.IsWindowsVersionAtLeast(10, 0, 22000))
        {
            int caption = accessible ? -1 : ColorTranslator.ToWin32(background);
            int text = accessible ? -1 : ColorTranslator.ToWin32(foreground);
            DwmSetWindowAttribute(window, 35, ref caption, sizeof(int)); // DWMWA_CAPTION_COLOR
            DwmSetWindowAttribute(window, 36, ref text, sizeof(int));    // DWMWA_TEXT_COLOR
        }

        if (menus != null) ApplyMenu(menus);
    }

    internal void ApplyMenu(ToolStrip menu)
    {
        bool accessible = SystemInformation.HighContrast;
        ToolStripRenderer renderer = accessible ? new ToolStripSystemRenderer()
            : new MenuRenderer(background, foreground);
        ApplyMenu(menu, renderer, accessible);
    }

    private void ApplyMenu(ToolStrip menu, ToolStripRenderer renderer, bool accessible)
    {
        menu.Renderer = renderer;
        menu.BackColor = accessible ? SystemColors.Menu : background;
        menu.ForeColor = accessible ? SystemColors.MenuText : foreground;
        foreach (ToolStripItem item in menu.Items)
        {
            item.ForeColor = menu.ForeColor;
            if (item is ToolStripMenuItem { HasDropDownItems: true } child)
                ApplyMenu(child.DropDown, renderer, accessible);
        }
        menu.Invalidate();
    }

    private sealed class MenuRenderer(Color fill, Color ink) : ToolStripProfessionalRenderer
    {
        private readonly Color highlight = Color.FromArgb((fill.R * 4 + ink.R) / 5,
            (fill.G * 4 + ink.G) / 5, (fill.B * 4 + ink.B) / 5);

        protected override void OnRenderToolStripBackground(ToolStripRenderEventArgs e) =>
            e.Graphics.Clear(fill);

        protected override void OnRenderImageMargin(ToolStripRenderEventArgs e) { }

        protected override void OnRenderMenuItemBackground(ToolStripItemRenderEventArgs e)
        {
            if (!e.Item.Selected && !e.Item.Pressed) return;
            using var brush = new SolidBrush(highlight);
            var bounds = new Rectangle(Point.Empty, e.Item.Size);
            if (e.ToolStrip is ToolStripDropDown)
            {
                bounds.Inflate(-2, -1);
                e.Graphics.FillRoundedRectangle(brush, bounds, new Size(5, 5));
            }
            else e.Graphics.FillRectangle(brush, bounds);
        }

        protected override void OnRenderSeparator(ToolStripSeparatorRenderEventArgs e)
        {
            using var pen = new Pen(highlight);
            int y = e.Item.Height / 2;
            e.Graphics.DrawLine(pen, 10, y, e.Item.Width - 10, y);
        }

        protected override void OnRenderToolStripBorder(ToolStripRenderEventArgs e)
        {
            if (e.ToolStrip is not ToolStripDropDown) return;
            using var pen = new Pen(highlight);
            e.Graphics.DrawRectangle(pen, 0, 0, e.ToolStrip.Width - 1, e.ToolStrip.Height - 1);
        }

        protected override void OnRenderArrow(ToolStripArrowRenderEventArgs e)
        {
            e.ArrowColor = ink;
            base.OnRenderArrow(e);
        }
    }
}
