using System;
using System.Globalization;

namespace FleetWarden.Tray
{
    // English and Dutch texts of the self-service window (see Strings for
    // how the language is chosen).
    public static class SsText
    {
        static string T(string en, string nl) => Strings.Dutch ? nl : en;

        public static string MenuOpen => T("Software and help", "Software en hulp");
        public static string WindowTitle(string product) => T($"{product} – Software and help", $"{product} – Software en hulp");
        public static string SelfService => T("Self-service", "Selfservice");
        public static string ManagedBy => T("Managed by your IT department", "Beheerd door je IT-afdeling");

        // Navigation
        public static string NavSoftware => T("Software", "Software");
        public static string NavUpdates => T("Updates", "Updates");
        public static string NavHelp => T("Help", "Hulp");

        // Software
        public static string SoftwareTitle => T("Software", "Software");
        public static string SoftwareSubtitle => T("Programs your IT department made available for this computer. Install them yourself, no administrator needed.",
                                                   "Programma's die je IT-afdeling voor deze computer beschikbaar stelt. Installeer ze zelf, zonder beheerder.");
        public static string Search => T("Search programs", "Programma's zoeken");
        public static string AllCategories => T("All", "Alles");
        public static string Other => T("Other", "Overig");
        public static string NoItems => T("There are no programs in the catalog yet.", "Er staan nog geen programma's in de catalogus.");
        public static string NoMatch => T("No program matches your search.", "Geen programma gevonden.");
        public static string SoftwareOff => T("The software catalog is turned off by your IT department.", "De softwarecatalogus is uitgezet door je IT-afdeling.");
        public static string Install => T("Install", "Installeren");
        public static string Request => T("Request", "Aanvragen");
        public static string NeedsApproval => T("Needs approval", "Goedkeuring nodig");
        public static string Pending => T("Waiting for approval", "Wacht op goedkeuring");
        public static string Queued => T("Queued", "In de wachtrij");
        public static string Installing => T("Installing…", "Wordt geïnstalleerd…");
        public static string Installed => T("Installed", "Geïnstalleerd");
        public static string TryAgain => T("Try again", "Opnieuw proberen");
        public static string AskAgain => T("Ask again", "Opnieuw aanvragen");
        public static string Failed(string msg) => string.IsNullOrWhiteSpace(msg) ? T("The installation failed.", "De installatie is mislukt.")
            : T($"The installation failed: {msg}", $"De installatie is mislukt: {msg}");
        public static string Rejected(string note) => string.IsNullOrWhiteSpace(note) ? T("Your request was rejected.", "Je aanvraag is afgewezen.")
            : T($"Your request was rejected: {note}", $"Je aanvraag is afgewezen: {note}");
        public static string ReasonLabel => T("Why do you need this program? (optional)", "Waarvoor heb je dit programma nodig? (optioneel)");
        public static string SendRequest => T("Send request", "Aanvraag versturen");
        public static string Cancel => T("Cancel", "Annuleren");

        // Updates
        public static string UpdatesTitle => T("Updates", "Updates");
        public static string UpdatesSubtitle => T("Install the updates your IT department approved now, instead of waiting for the maintenance window.",
                                                  "Installeer de updates die je IT-afdeling heeft goedgekeurd nu, in plaats van te wachten op het onderhoudsvenster.");
        public static string UpdatesOff => T("Updates on this computer are managed by your IT department.", "Updates op deze computer worden beheerd door je IT-afdeling.");
        public static string UpdatesReady(int n) => n == 1 ? T("1 update is ready to install", "1 update staat klaar")
            : T($"{n} updates are ready to install", $"{n} updates staan klaar");
        public static string UpToDate => T("This computer is up to date", "Deze computer is bijgewerkt");
        public static string RestartPending => T("A restart is needed to finish earlier updates.", "Een herstart is nodig om eerdere updates af te ronden.");
        public static string LastScan(string when) => T($"Last checked {when}", $"Laatst gecontroleerd {when}");
        public static string InstallUpdates => T("Install updates now", "Updates nu installeren");
        public static string RestartNow => T("Finish with a restart", "Afronden met herstart");
        public static string UpdatesHint => T("Save your work first. The computer can be slower while updates install, and may restart afterwards if needed.",
                                              "Sla eerst je werk op. De computer kan trager zijn tijdens de installatie en start zo nodig daarna opnieuw op.");
        public static string UpdateState(string state)
        {
            switch (state)
            {
                case "sent": return T("Starting the updates…", "De updates worden gestart…");
                case "waiting-user": return T("Waiting for your answer in the pop-up.", "Wacht op je antwoord in de melding.");
                case "postponed": return T("The updates were postponed.", "De updates zijn uitgesteld.");
                case "installing": return T("Installing updates…", "Updates worden geïnstalleerd…");
                case "rebooting": return T("Restarting…", "Bezig met herstarten…");
                case "completed": return T("The updates were installed.", "De updates zijn geïnstalleerd.");
                case "failed": return T("Installing the updates failed. Your IT department can see what went wrong.", "Het installeren van de updates is mislukt. Je IT-afdeling kan zien wat er misging.");
                default: return "";
            }
        }
        public static bool UpdateRunning(string state) => state == "sent" || state == "waiting-user" || state == "postponed" || state == "installing" || state == "rebooting";

        // Help
        public static string HelpTitle => T("Ask for help", "Hulp vragen");
        public static string HelpSubtitle => T("Describe your problem. Your request goes to the IT department together with the name of this computer.",
                                               "Beschrijf je probleem. Je vraag gaat naar de IT-afdeling, samen met de naam van deze computer.");
        public static string HelpOff => T("Help requests are turned off by your IT department.", "Hulpvragen zijn uitgezet door je IT-afdeling.");
        public static string Subject => T("Subject", "Onderwerp");
        public static string SubjectHint => T("For example: Outlook does not start", "Bijvoorbeeld: Outlook start niet");
        public static string Message => T("What happens, and since when?", "Wat gebeurt er, en sinds wanneer?");
        public static string Contact => T("How can we reach you? (phone or e-mail, optional)", "Hoe kunnen we je bereiken? (telefoon of e-mail, optioneel)");
        public static string Send => T("Send", "Versturen");
        public static string YourRequests => T("Your requests", "Je hulpvragen");
        public static string NoRequests => T("You have not asked for help from this computer yet.", "Je hebt vanaf deze computer nog geen hulp gevraagd.");
        public static string SubjectRequired => T("Fill in a subject.", "Vul een onderwerp in.");
        public static string TicketState(string state) => state == "in_progress" ? T("In progress", "In behandeling")
            : state == "closed" ? T("Closed", "Gesloten") : T("Sent", "Verstuurd");
        public static string ReplyFromIT => T("Reply from IT", "Antwoord van IT");
        public static string Urgency => T("How urgent is it?", "Hoe dringend is het?");
        public static string UrgencyName(string u) => u == "low" ? T("Can wait", "Kan wachten") : u == "high" ? T("I cannot work", "Ik kan niet werken") : T("Normal", "Normaal");
        public static string Category => T("What is it about?", "Waar gaat het over?");
        public static string CategoryRequired => T("Choose what it is about.", "Kies waar het over gaat.");
        public static string MessageRequired(int min) => T($"Describe the problem in at least {min} characters.", $"Beschrijf het probleem in minstens {min} tekens.");
        public static string MessageMore(int n) => T($"{n} more characters needed", $"Nog {n} tekens nodig");
        public static string MessageOk => T("Thank you, that helps.", "Dank je, dat helpt.");
        public static string WhatIsSent => T("What is sent along with your request", "Wat er met je melding meegaat");
        public static string TicketNumber(string n) => T("ticket ", "ticket ") + n;
        public static string Destination(string dest, string name)
        {
            switch (dest)
            {
                case "integration": return string.IsNullOrWhiteSpace(name) ? T("Goes to the service desk.", "Gaat naar de servicedesk.") : T($"Goes to the service desk ({name}).", $"Gaat naar de servicedesk ({name}).");
                case "outlook": return T("Opens as an e-mail in Outlook; you send it yourself.", "Opent als e-mail in Outlook; je verstuurt die zelf.");
                case "mail": return T("Goes by e-mail to the service desk.", "Gaat per e-mail naar de servicedesk.");
                default: return T("Goes to the IT department.", "Gaat naar de IT-afdeling.");
            }
        }
        public static string MailToOpened => T("Your mail program opened with the e-mail. Attach the report that is selected in Explorer and send it.",
                                               "Je mailprogramma is geopend met de e-mail. Voeg het rapport toe dat in Verkenner is geselecteerd en verstuur de e-mail.");
        public static string OutlookFailed(string to, string file) => T($"No mail program could be opened. E-mail your problem to {to}" + (file != null ? $" and attach {file}." : "."),
                                                                         $"Er kon geen mailprogramma worden geopend. Mail je probleem naar {to}" + (file != null ? $" en voeg {file} toe." : "."));

        // Status and errors
        public static string Loading => T("Loading…", "Laden…");
        public static string Error(string code)
        {
            switch (code)
            {
                case "disabled": return T("Your IT department turned this off.", "Je IT-afdeling heeft dit uitgezet.");
                case "not_found": return T("This program is no longer available.", "Dit programma is niet meer beschikbaar.");
                case "busy": return T("This is already in progress.", "Dit is al bezig.");
                case "nothing": return T("There is nothing to install.", "Er is niets te installeren.");
                case "invalid": return T("Check what you filled in.", "Controleer wat je hebt ingevuld.");
                case "limit": return T("You sent many requests in a short time. Try again later.", "Je hebt veel verzoeken in korte tijd gestuurd. Probeer het later opnieuw.");
                case "offline": return T("This computer has no connection with the management server right now. Try again later.", "Deze computer heeft nu geen verbinding met de beheerserver. Probeer het later opnieuw.");
                case "no_user": return T("Your account could not be determined.", "Je account kon niet worden vastgesteld.");
                case "unsupported": return T("The management server does not offer self-service yet.", "De beheerserver biedt nog geen selfservice aan.");
                default: return T("Something went wrong. Try again later.", "Er ging iets mis. Probeer het later opnieuw.");
            }
        }

        // When formats an RFC 3339 time for the user: "today 14:05", "3 Nov 09:12".
        public static string When(string iso)
        {
            if (string.IsNullOrEmpty(iso) || !DateTime.TryParse(iso, CultureInfo.InvariantCulture, DateTimeStyles.AdjustToUniversal | DateTimeStyles.AssumeUniversal, out var t))
                return "";
            var local = t.ToLocalTime();
            var culture = Strings.Dutch ? new CultureInfo("nl-NL") : new CultureInfo("en-GB");
            if (local.Date == DateTime.Now.Date) return T("today ", "vandaag ") + local.ToString("HH:mm", culture);
            if (local.Date == DateTime.Now.Date.AddDays(-1)) return T("yesterday ", "gisteren ") + local.ToString("HH:mm", culture);
            return local.ToString("d MMM HH:mm", culture);
        }
    }
}
