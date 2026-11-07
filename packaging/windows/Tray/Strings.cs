using System;
using System.Globalization;

namespace FleetWarden.Tray
{
    // English and Dutch texts. The language follows the user's Windows
    // display language; --lang en|nl overrides it.
    public static class Strings
    {
        public static bool Dutch = CultureInfo.CurrentUICulture.TwoLetterISOLanguageName == "nl";

        static string T(string en, string nl) => Dutch ? nl : en;

        public static string Product(Prompt p) => string.IsNullOrWhiteSpace(p?.ProductName) ? "FleetWarden" : p.ProductName.Trim();

        public static string Title(Prompt p) => p.Kind == Prompt.Reboot
            ? T("Restart required", "Herstart nodig")
            : T("Updates are ready to install", "Updates staan klaar");

        public static string Body(Prompt p)
        {
            if (p.Kind == Prompt.Reboot)
                return T("Updates have been installed. Your computer has to restart to finish them. Save your work and close your programs.",
                         "Er zijn updates geïnstalleerd. De computer moet opnieuw opstarten om ze af te ronden. Sla je werk op en sluit je programma's.");
            var n = Math.Max(p.Updates, 1);
            return Dutch
                ? $"Er {(n == 1 ? "wordt 1 update" : $"worden {n} updates")} op deze computer geïnstalleerd. Sla je werk op; de computer kan tijdens de installatie trager zijn."
                : $"{(n == 1 ? "1 update is" : $"{n} updates are")} going to be installed on this computer. Save your work; the computer may be slower while they install.";
        }

        public static string Countdown(Prompt p, TimeSpan left)
        {
            var time = left.TotalHours >= 1 ? left.ToString(@"h\:mm\:ss") : left.ToString(@"m\:ss");
            return p.Kind == Prompt.Reboot ? T($"Restarting in {time}", $"Herstart over {time}") : T($"Installing in {time}", $"Installatie start over {time}");
        }

        public static string Now(Prompt p) => p.Kind == Prompt.Reboot ? T("Restart now", "Nu herstarten") : T("Install now", "Nu installeren");

        public static string Postpone(Prompt p) => T($"Postpone {Duration(p.PostponeMinutes)}", $"{Duration(p.PostponeMinutes)} uitstellen");

        public static string Duration(int minutes)
        {
            if (minutes < 60 || minutes % 60 != 0) return $"{minutes} min";
            var h = minutes / 60;
            return Dutch ? $"{h} uur" : (h == 1 ? "1 hour" : $"{h} hours");
        }

        public static string PostponesLeft(Prompt p)
        {
            if (!p.CanPostpone) return T("This can no longer be postponed.", "Dit kan niet meer worden uitgesteld.");
            return p.PostponesLeft == 1
                ? T("You can postpone 1 more time.", "Je kunt nog 1 keer uitstellen.")
                : T($"You can postpone {p.PostponesLeft} more times.", $"Je kunt nog {p.PostponesLeft} keer uitstellen.");
        }

        public static string ManagedBy(Prompt p) => T($"{Product(p)} · managed by your IT department", $"{Product(p)} · beheerd door je IT-afdeling");

        public static string TrayConnected => T("Connected to the agent. No action needed.", "Verbonden met de agent. Geen actie nodig.");
        public static string TrayWaiting(Prompt p) => T("An update needs your attention.", "Een update vraagt je aandacht.");
        public static string TrayDisconnected => T("Not connected to the agent.", "Niet verbonden met de agent.");
        public static string ShowPrompt => T("Show notification", "Melding tonen");
    }
}
