using System;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using System.Windows.Threading;

namespace FleetWarden.Tray
{
    // SelfServiceDemo shows the self-service window with example data
    // (--demo selfservice), or saves it as an image for the CI artifact.
    static class SelfServiceDemo
    {
        public static int Run(Application app, string page, string screenshot)
        {
            var now = DateTime.UtcNow;
            string Iso(TimeSpan ago) => (now - ago).ToString("yyyy-MM-dd'T'HH:mm:ss'Z'");
            var nl = Strings.Dutch;
            var catalog = new SelfServiceCatalog
            {
                ProductName = "FleetWarden", Software = true, Updates = true, PendingUpdates = 4, LastScan = Iso(TimeSpan.FromHours(2)),
                Help = true, HelpText = nl ? "Spoed? Bel de servicedesk op 020 123 4567 (ma–vr 8:00–17:00)." : "Urgent? Call the service desk on 020 123 4567 (Mon–Fri 8:00–17:00).",
                Items = new[]
                {
                    new SelfServiceItem { Id = 1, Name = "Google Chrome", Publisher = "Google LLC", Version = "131.0", Category = nl ? "Browsers" : "Browsers",
                        Description = nl ? "Snelle webbrowser van Google." : "Fast web browser from Google.", State = SelfServiceItem.Available },
                    new SelfServiceItem { Id = 2, Name = "Adobe Acrobat Reader", Publisher = "Adobe", Category = "Office",
                        Description = nl ? "PDF-bestanden bekijken, ondertekenen en afdrukken." : "View, sign and print PDF files.", State = SelfServiceItem.Installing },
                    new SelfServiceItem { Id = 3, Name = "7-Zip", Publisher = "Igor Pavlov", Version = "24.08", Category = nl ? "Hulpmiddelen" : "Utilities",
                        Description = nl ? "Bestanden in- en uitpakken (zip, 7z, rar)." : "Pack and unpack archives (zip, 7z, rar).", State = SelfServiceItem.Installed },
                    new SelfServiceItem { Id = 4, Name = "Visual Studio Code", Publisher = "Microsoft", Category = nl ? "Ontwikkeling" : "Development", Approval = true,
                        Description = nl ? "Code-editor voor ontwikkelaars." : "Code editor for developers.", State = SelfServiceItem.Pending },
                    new SelfServiceItem { Id = 5, Name = "Microsoft Visio", Publisher = "Microsoft", Category = "Office", Approval = true,
                        Description = nl ? "Diagrammen en stroomschema's. Licentie nodig." : "Diagrams and flowcharts. Needs a licence.", State = SelfServiceItem.Available },
                    new SelfServiceItem { Id = 6, Name = "Notepad++", Publisher = "Notepad++ Team", Version = "8.7", Category = nl ? "Hulpmiddelen" : "Utilities",
                        Description = nl ? "Teksteditor met kleuren voor code." : "Text editor with syntax highlighting.", State = SelfServiceItem.Failed,
                        Message = nl ? "exitcode 1603" : "exit code 1603" },
                },
                Ticket = new TicketForm
                {
                    RequireMessage = true, MinMessage = 20, AskUrgency = true, AskCategory = true, AskContact = true,
                    Categories = nl ? new[] { "Hardware", "Software", "Printen", "Netwerk", "Account", "Overig" } : new[] { "Hardware", "Software", "Printing", "Network", "Account", "Other" },
                    Destination = TicketForm.Integration, DestinationName = "Freshservice",
                    Sent = new[]
                    {
                        new InfoLine { Label = nl ? "Computernaam" : "Computer name", Value = "LT-0142" },
                        new InfoLine { Label = nl ? "Gebruiker" : "User", Value = @"ACME\kdevries" },
                        new InfoLine { Label = nl ? "IP-adressen" : "IP addresses", Value = "10.20.4.37" },
                        new InfoLine { Label = nl ? "Besturingssysteem" : "Operating system", Value = "Windows 11 Pro 24H2 (26100.2314)" },
                        new InfoLine { Label = nl ? "Herstart nodig" : "Reboot pending", Value = nl ? "Nee" : "No" },
                        new InfoLine { Label = nl ? "Wachtende updates" : "Updates waiting", Value = "KB5046617" },
                    },
                },
                HelpRequests = new[]
                {
                    new HelpTicket { Id = 42, Number = "#4821", ExtState = nl ? "In behandeling" : "In progress", Subject = nl ? "Printer op de tweede verdieping drukt niet af" : "Printer on the second floor does not print", State = "in_progress",
                        Reply = nl ? "We kijken ernaar. De printer krijgt vanmiddag een nieuwe driver." : "We are on it. The printer gets a new driver this afternoon.",
                        CreatedAt = Iso(TimeSpan.FromHours(3)), UpdatedAt = Iso(TimeSpan.FromHours(1)) },
                    new HelpTicket { Id = 37, Subject = nl ? "Outlook vraagt steeds om mijn wachtwoord" : "Outlook keeps asking for my password", State = "closed",
                        CreatedAt = Iso(TimeSpan.FromDays(6)), UpdatedAt = Iso(TimeSpan.FromDays(5)) },
                },
            };
            var tab = page == "updates" ? SelfServiceWindow.Tab.Updates : page == "help" ? SelfServiceWindow.Tab.Help : SelfServiceWindow.Tab.Software;
            var w = new SelfServiceWindow(m => Task.FromResult(new SelfServiceReply { Catalog = catalog }), null, tab);
            w.Closed += (s, e) => app.Shutdown();
            var code = 0;
            if (screenshot != null)
            {
                w.ContentRendered += (s, e) => app.Dispatcher.BeginInvoke(DispatcherPriority.ApplicationIdle, new Action(() =>
                {
                    try { Save(w, screenshot); }
                    catch (Exception ex) { Console.Error.WriteLine(ex); code = 1; }
                    w.Close();
                }));
            }
            w.Show();
            w.Apply(catalog);
            app.Run();
            return code;
        }

        static void Save(Window w, string path)
        {
            const double scale = 2;
            var root = (FrameworkElement)w.Content;
            root.UpdateLayout();
            var bmp = new RenderTargetBitmap((int)Math.Ceiling(root.ActualWidth * scale), (int)Math.Ceiling(root.ActualHeight * scale), 96 * scale, 96 * scale, PixelFormats.Pbgra32);
            bmp.Render(root);
            var enc = new PngBitmapEncoder();
            enc.Frames.Add(BitmapFrame.Create(bmp));
            using (var f = System.IO.File.Create(path)) enc.Save(f);
        }
    }
}
