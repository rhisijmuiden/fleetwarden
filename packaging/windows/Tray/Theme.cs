using System;
using System.Windows;
using System.Windows.Media;
using Microsoft.Win32;

namespace FleetWarden.Tray
{
    // IJmond360 house style (branding/ijmond360/tokens.json), light or dark like the
    // user's Windows apps. The accent follows the server's branding colour.
    public static class Theme
    {
        public static bool Dark
        {
            get
            {
                try
                {
                    using (var k = Registry.CurrentUser.OpenSubKey(@"Software\Microsoft\Windows\CurrentVersion\Themes\Personalize"))
                        return k?.GetValue("AppsUseLightTheme") is int v && v == 0;
                }
                catch { return false; }
            }
        }

        public static bool? ForceDark;

        static bool IsDark => ForceDark ?? Dark;

        public static Color Accent(string configured)
        {
            if (!string.IsNullOrWhiteSpace(configured))
            {
                try
                {
                    var c = (Color)ColorConverter.ConvertFromString(configured.Trim());
                    c.A = 255;
                    return c;
                }
                catch (FormatException) { }
            }
            return Hex(IsDark ? "#9A5BAE" : "#713C80");
        }

        public static void Apply(ResourceDictionary r, string accent)
        {
            var dark = IsDark;
            var a = Accent(accent);
            r["Surface"] = Brush(dark ? "#18101C" : "#FFFFFF");
            r["Surface2"] = Brush(dark ? "#221727" : "#F0ECF3");
            r["Line"] = Brush(dark ? "#352840" : "#E3DCE8");
            r["Fg"] = Brush(dark ? "#EFE9F2" : "#1B1320");
            r["Muted"] = Brush(dark ? "#A396AA" : "#6B5F72");
            r["Track"] = Brush(dark ? "#352840" : "#E3DCE8");
            r["Inner"] = Brush("#2A1430");
            r["Dots"] = Brush("#E92E7F");
            r["Accent"] = Freeze(new SolidColorBrush(a));
            r["Success"] = Brush(dark ? "#1DB97A" : "#15A06A");
            r["Warning"] = Brush(dark ? "#F5A524" : "#D98A00");
            r["Danger"] = Brush(dark ? "#F0566A" : "#DC2F45");
            r["Sidebar"] = Brush(dark ? "#120A15" : "#2A1430");
            r["SidebarFg"] = Brush(dark ? "#DCCFE2" : "#DCCFE2");
            r["SidebarActive"] = Brush(dark ? "#2A1830" : "#3E2147");
            r["AccentFg"] = Freeze(new SolidColorBrush(Luminance(a) > 0.6 ? Hex("#1B1320") : Colors.White));
        }

        static double Luminance(Color c) => (0.2126 * c.R + 0.7152 * c.G + 0.0722 * c.B) / 255.0;

        static Color Hex(string s) => (Color)ColorConverter.ConvertFromString(s);

        static SolidColorBrush Brush(string hex) => Freeze(new SolidColorBrush(Hex(hex)));

        static SolidColorBrush Freeze(SolidColorBrush b)
        {
            b.Freeze();
            return b;
        }
    }
}
