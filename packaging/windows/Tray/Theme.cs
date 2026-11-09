using System;
using System.Windows;
using System.Windows.Media;
using Microsoft.Win32;

namespace FleetWarden.Tray
{
    // FleetWarden house style (branding/tokens.json), light or dark like the
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
            return Hex(IsDark ? "#5B7FFF" : "#2F5BEA");
        }

        public static void Apply(ResourceDictionary r, string accent)
        {
            var dark = IsDark;
            var a = Accent(accent);
            r["Surface"] = Brush(dark ? "#111827" : "#FFFFFF");
            r["Surface2"] = Brush(dark ? "#161F31" : "#F1F3F6");
            r["Line"] = Brush(dark ? "#222C3F" : "#E3E6EB");
            r["Fg"] = Brush(dark ? "#E6E9EF" : "#0F1729");
            r["Muted"] = Brush(dark ? "#98A2B3" : "#5B6578");
            r["Track"] = Brush(dark ? "#222C3F" : "#E3E6EB");
            r["Inner"] = Brush("#0E1525");
            r["Dots"] = Brush("#9DB2FF");
            r["Accent"] = Freeze(new SolidColorBrush(a));
            r["Success"] = Brush(dark ? "#2BC46B" : "#12A150");
            r["Warning"] = Brush(dark ? "#F5A524" : "#D98A00");
            r["Danger"] = Brush(dark ? "#F0566A" : "#DC3545");
            r["Sidebar"] = Brush(dark ? "#070A11" : "#0E1525");
            r["SidebarFg"] = Brush(dark ? "#AEB6C4" : "#C3CAD6");
            r["SidebarActive"] = Brush(dark ? "#162038" : "#1C2742");
            r["AccentFg"] = Freeze(new SolidColorBrush(Luminance(a) > 0.6 ? Hex("#0F1729") : Colors.White));
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
