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

        public static string Title(Prompt p) => p.Kind == Prompt.Remote
            ? T("Remote support request", "Verzoek om hulp op afstand")
            : p.Kind == Prompt.Reboot
            ? T("Restart required", "Herstart nodig")
            : p.Kind == Prompt.Browser
                ? T($"{Apps(p)} will be updated", $"{Apps(p)} {(Many(p) ? "worden" : "wordt")} bijgewerkt")
                : T("Updates are ready to install", "Updates staan klaar");

        // Apps joins the browser names: "Google Chrome and Microsoft Edge".
        public static string Apps(Prompt p)
        {
            var a = p.Apps != null && p.Apps.Length > 0 ? p.Apps : new[] { T("Your browser", "Je browser") };
            if (a.Length == 1) return a[0];
            return string.Join(", ", a, 0, a.Length - 1) + T(" and ", " en ") + a[a.Length - 1];
        }

        static bool Many(Prompt p) => p.Apps != null && p.Apps.Length > 1;

        public static string Operator(Prompt p) => string.IsNullOrWhiteSpace(p?.Operator) ? T("A technician", "Een medewerker") : p.Operator.Trim();

        public static string Body(Prompt p)
        {
            if (p.Kind == Prompt.Remote)
                return T($"{Operator(p)} from your IT department wants to see and control this computer. Only allow this if you expect it.",
                         $"{Operator(p)} van je IT-afdeling wil meekijken en deze computer bedienen. Sta dit alleen toe als je dit verwacht.");
            if (p.Kind == Prompt.Browser)
                return Many(p)
                    ? T("The browsers close for a moment to install an update and open again by themselves. Your windows and tabs come back. Finish what you are typing first.",
                        "De browsers sluiten even om een update te installeren en gaan daarna vanzelf weer open. Je vensters en tabbladen komen terug. Maak eerst af waar je mee bezig bent.")
                    : T("The browser closes for a moment to install an update and opens again by itself. Your windows and tabs come back. Finish what you are typing first.",
                        "De browser sluit even om een update te installeren en gaat daarna vanzelf weer open. Je vensters en tabbladen komen terug. Maak eerst af waar je mee bezig bent.");
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
            if (p.Kind == Prompt.Browser) return T($"Closing in {time}", $"Sluit over {time}");
            if (p.Kind == Prompt.Remote) return T($"Declined automatically in {time}", $"Wordt automatisch geweigerd over {time}");
            return p.Kind == Prompt.Reboot ? T($"Restarting in {time}", $"Herstart over {time}") : T($"Installing in {time}", $"Installatie start over {time}");
        }

        public static string Now(Prompt p) => p.Kind == Prompt.Remote ? T("Allow", "Toestaan")
            : p.Kind == Prompt.Reboot ? T("Restart now", "Nu herstarten")
            : p.Kind == Prompt.Browser ? T("Update now", "Nu bijwerken") : T("Install now", "Nu installeren");

        public static string Postpone(Prompt p) => p.Kind == Prompt.Remote ? T("Deny", "Weigeren") : T($"Postpone {Duration(p.PostponeMinutes)}", $"{Duration(p.PostponeMinutes)} uitstellen");

        public static string Duration(int minutes)
        {
            if (minutes < 60 || minutes % 60 != 0) return $"{minutes} min";
            var h = minutes / 60;
            return Dutch ? $"{h} uur" : (h == 1 ? "1 hour" : $"{h} hours");
        }

        public static string PostponesLeft(Prompt p)
        {
            if (p.Kind == Prompt.Remote) return "";
            if (!p.CanPostpone) return T("This can no longer be postponed.", "Dit kan niet meer worden uitgesteld.");
            return p.PostponesLeft == 1
                ? T("You can postpone 1 more time.", "Je kunt nog 1 keer uitstellen.")
                : T($"You can postpone {p.PostponesLeft} more times.", $"Je kunt nog {p.PostponesLeft} keer uitstellen.");
        }

        public static string ManagedBy(Prompt p) => T($"{Product(p)} · managed by your IT department", $"{Product(p)} · beheerd door je IT-afdeling");

        public static string TrayConnected => T("Connected to the agent. No action needed.", "Verbonden met de agent. Geen actie nodig.");
        public static string TrayWaiting(Prompt p) => p.Kind == Prompt.Remote
            ? T("Someone asks to see your screen.", "Iemand vraagt om mee te kijken.")
            : T("An update needs your attention.", "Een update vraagt je aandacht.");
        public static string TrayRemote(Prompt p) => T($"{Operator(p)} is connected to this computer.", $"{Operator(p)} is verbonden met deze computer.");
        public static string BannerText(Prompt p) => T($"{Operator(p)} is viewing and controlling this screen", $"{Operator(p)} kijkt mee en bedient dit scherm");
        public static string EndSession => T("End session", "Sessie beëindigen");
        public static string TrayDisconnected => T("Not connected to the agent.", "Niet verbonden met de agent.");
        public static string ShowPrompt => T("Show notification", "Melding tonen");
    }
}
