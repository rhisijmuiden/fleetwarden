using System;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Globalization;
using System.IO;
using System.IO.Pipes;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using System.Windows;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using System.Windows.Threading;
using Forms = System.Windows.Forms;

namespace FleetWarden.Tray
{
    public static class Program
    {
        // FleetWardenTray.exe                 runs in the tray (started at logon and by the agent)
        // FleetWardenTray.exe --demo [install|reboot|restart|shutdown|browser|remote|banner] [--lang en|nl] [--dark] [--screenshot file.png]
        //                                     shows an example pop-up, or saves it as an image
        // FleetWardenTray.exe --demo selfservice [--page software|updates|help] ...
        //                                     the self-service window with example data
        // FleetWardenTray.exe --pipe name     uses another pipe (tests); its server need not be a service
        [STAThread]
        public static int Main(string[] args)
        {
            string demo = null, screenshot = null, pipe = null, ssPage = null;
            for (var i = 0; i < args.Length; i++)
            {
                var next = i + 1 < args.Length && !args[i + 1].StartsWith("--") ? args[i + 1] : null;
                switch (args[i])
                {
                    case "--demo": demo = next ?? Prompt.Install; if (next != null) i++; break;
                    case "--lang": Strings.Dutch = next == "nl"; if (next != null) i++; break;
                    case "--dark": Theme.ForceDark = true; break;
                    case "--light": Theme.ForceDark = false; break;
                    case "--pipe": pipe = next; if (next != null) i++; break;
                    case "--screenshot": screenshot = next; if (next != null) i++; break;
                    case "--page": ssPage = next; if (next != null) i++; break;
                }
            }

            var app = new Application { ShutdownMode = ShutdownMode.OnExplicitShutdown };
            if (demo == "selfservice") return SelfServiceDemo.Run(app, ssPage, screenshot);
            if (demo != null) return Demo(app, demo, screenshot);

            using (var single = new Mutex(true, @"Local\FleetWarden.Tray" + (pipe == null ? "" : "." + pipe), out var first))
            {
                if (!first) return 0;
                using (var tray = new TrayController(app.Dispatcher, pipe))
                {
                    tray.Start();
                    app.Run();
                }
            }
            return 0;
        }

        static int Demo(Application app, string kind, string screenshot)
        {
            var remote = kind == Prompt.Remote || kind == "banner";
            var p = new Prompt
            {
                Id = "demo", Kind = kind == Prompt.Reboot || kind == Prompt.Browser || kind == Prompt.Restart || kind == Prompt.Shutdown || remote ? (remote ? Prompt.Remote : kind) : Prompt.Install, Updates = 4,
                CanPostpone = !remote, PostponesLeft = 2, PostponeMinutes = 60, CountdownSeconds = kind == Prompt.Browser ? 60 : remote ? 30 : 300, ProductName = "FleetWarden",
                Apps = kind == Prompt.Browser ? new[] { "Google Chrome", "Microsoft Edge" } : null,
                Operator = remote ? "Reinder" : null,
            };
            Window w;
            if (kind == "banner")
            {
                RemoteBanner b = null;
                b = new RemoteBanner("demo", p, () => { Console.WriteLine("end"); b.CloseQuietly(); });
                w = b;
            }
            else
                w = new PromptWindow(p, postpone => Console.WriteLine(postpone ? "postpone" : "now"), animate: screenshot == null);
            w.Closed += (s, e) => app.Shutdown();
            var code = 0;
            if (screenshot != null)
            {
                w.ContentRendered += (s, e) => app.Dispatcher.BeginInvoke(DispatcherPriority.ApplicationIdle, new Action(() =>
                {
                    try { Save(w, screenshot); }
                    catch (Exception ex) { Console.Error.WriteLine(ex); code = 1; }
                    if (w is PromptWindow pw) pw.CloseQuietly();
                    else ((RemoteBanner)w).CloseQuietly();
                }));
            }
            w.Show();
            app.Run();
            return code;
        }

        static void Save(Window w, string path)
        {
            const double scale = 2;
            var root = (FrameworkElement)w.Content;
            var bmp = new RenderTargetBitmap((int)Math.Ceiling(root.ActualWidth * scale) + 32 * (int)scale, (int)Math.Ceiling(root.ActualHeight * scale) + 32 * (int)scale, 96 * scale, 96 * scale, PixelFormats.Pbgra32);
            bmp.Render(w);
            var enc = new PngBitmapEncoder();
            enc.Frames.Add(BitmapFrame.Create(bmp));
            using (var f = File.Create(path)) enc.Save(f);
        }
    }

    // TrayController keeps the connection to the agent and shows its prompts.
    sealed class TrayController : IDisposable
    {
        const string DefaultPipe = "FleetWardenAgent.Tray";

        readonly Dispatcher ui;
        readonly string pipeName;
        readonly bool anyServer;
        readonly Forms.NotifyIcon icon;
        readonly Forms.ToolStripMenuItem status, show, selfService;
        readonly object writeLock = new object();
        readonly AppCloser closer = new AppCloser();
        StreamWriter writer;
        PromptWindow window;
        SelfServiceWindow ssWindow;
        int ssSeq;
        readonly System.Collections.Generic.Dictionary<string, TaskCompletionSource<SelfServiceReply>> ssPending =
            new System.Collections.Generic.Dictionary<string, TaskCompletionSource<SelfServiceReply>>();
        readonly System.Collections.Generic.Dictionary<string, RemoteBanner> banners = new System.Collections.Generic.Dictionary<string, RemoteBanner>();
        string product = "FleetWarden", accent;
        bool connected;

        public TrayController(Dispatcher dispatcher, string pipe)
        {
            ui = dispatcher;
            pipeName = string.IsNullOrEmpty(pipe) ? DefaultPipe : pipe;
            anyServer = pipeName != DefaultPipe;
            status = new Forms.ToolStripMenuItem(Strings.TrayDisconnected) { Enabled = false };
            show = new Forms.ToolStripMenuItem(Strings.ShowPrompt) { Visible = false };
            show.Click += (s, e) => window?.Flash();
            selfService = new Forms.ToolStripMenuItem(SsText.MenuOpen) { Font = new Font(Forms.Control.DefaultFont, System.Drawing.FontStyle.Bold) };
            selfService.Click += (s, e) => OpenSelfService();
            var menu = new Forms.ContextMenuStrip();
            menu.Items.Add(selfService);
            menu.Items.Add(new Forms.ToolStripSeparator());
            menu.Items.Add(status);
            menu.Items.Add(show);
            icon = new Forms.NotifyIcon { Icon = TrayIcon.Create(null), ContextMenuStrip = menu, Visible = true };
            icon.MouseClick += (s, e) =>
            {
                if (e.Button != Forms.MouseButtons.Left) return;
                if (window != null) window.Flash();
                else OpenSelfService();
            };
            UpdateStatus();
        }

        public void Start() => new Thread(Loop) { IsBackground = true, Name = "agent pipe" }.Start();

        void Loop()
        {
            while (true)
            {
                try
                {
                    using (var pipe = new NamedPipeClientStream(".", pipeName, PipeDirection.InOut, PipeOptions.Asynchronous))
                    {
                        pipe.Connect(5000);
                        if (!anyServer && !FromService(pipe))
                        {
                            Thread.Sleep(TimeSpan.FromSeconds(30)); // not the agent service: do not trust it
                            continue;
                        }
                        var reader = new StreamReader(pipe, new UTF8Encoding(false));
                        lock (writeLock) writer = new StreamWriter(pipe, new UTF8Encoding(false)) { AutoFlush = true, NewLine = "\n" };
                        Send(new TrayMessage { Type = "hello", Lang = CultureInfo.CurrentUICulture.Name });
                        ui.BeginInvoke(new Action(() => { connected = true; UpdateStatus(); }));
                        string line;
                        while ((line = reader.ReadLine()) != null) Handle(line);
                    }
                }
                catch (TimeoutException) { }
                catch (IOException) { }
                catch (Exception ex) { System.Diagnostics.Trace.WriteLine(ex); }
                lock (writeLock) writer = null;
                FailSelfService();
                ui.BeginInvoke(new Action(() =>
                {
                    connected = false;
                    window?.CloseQuietly();
                    window = null;
                    foreach (var b in banners.Values) b.CloseQuietly();
                    banners.Clear();
                    UpdateStatus();
                }));
                Thread.Sleep(TimeSpan.FromSeconds(5));
            }
        }

        // FromService reports whether the pipe server runs in session 0, where
        // services run and users cannot start programs.
        static bool FromService(NamedPipeClientStream pipe)
        {
            return GetNamedPipeServerSessionId(pipe.SafePipeHandle.DangerousGetHandle(), out var session) && session == 0;
        }

        [DllImport("kernel32.dll", SetLastError = true)]
        static extern bool GetNamedPipeServerSessionId(IntPtr pipe, out uint serverSessionId);

        void Handle(string line)
        {
            TrayMessage m;
            try { m = TrayMessage.FromJson(line); }
            catch (Exception) { return; }
            switch (m.Type)
            {
                case "prompt" when m.Prompt != null && !string.IsNullOrEmpty(m.Prompt.Id):
                    ui.BeginInvoke(new Action(() => ShowPrompt(m.Prompt)));
                    break;
                case "close-apps":
                    Task.Run(() =>
                    {
                        var keys = closer.Close(m.Id, m.Apps, m.TimeoutSeconds);
                        Send(new TrayMessage { Type = "apps-closed", Id = m.Id, Closed = keys });
                    });
                    break;
                case "remote-active" when m.Prompt != null && !string.IsNullOrEmpty(m.Id):
                    ui.BeginInvoke(new Action(() => ShowBanner(m.Id, m.Prompt)));
                    break;
                case "remote-ended":
                    ui.BeginInvoke(new Action(() =>
                    {
                        if (banners.TryGetValue(m.Id ?? "", out var b))
                        {
                            banners.Remove(m.Id);
                            b.CloseQuietly();
                            UpdateStatus();
                        }
                    }));
                    break;
                case "ss-reply":
                    TaskCompletionSource<SelfServiceReply> tcs;
                    lock (ssPending)
                    {
                        if (!ssPending.TryGetValue(m.Id ?? "", out tcs)) break;
                        ssPending.Remove(m.Id);
                    }
                    tcs.TrySetResult(m.Reply ?? new SelfServiceReply { Error = "internal" });
                    break;
                case "launch-apps":
                    Task.Run(() => closer.Launch(m.Id));
                    break;
                case "close":
                    ui.BeginInvoke(new Action(() =>
                    {
                        if (window != null && window.Prompt.Id == m.Id)
                        {
                            window.CloseQuietly();
                            window = null;
                            UpdateStatus();
                        }
                    }));
                    break;
            }
        }

        void ShowPrompt(Prompt p)
        {
            if (window != null && window.Prompt.Id == p.Id) return;
            window?.CloseQuietly();
            product = Strings.Product(p);
            if (p.AccentColor != accent)
            {
                accent = p.AccentColor;
                var old = icon.Icon;
                icon.Icon = TrayIcon.Create(accent);
                old?.Dispose();
            }
            PromptWindow w = null;
            w = new PromptWindow(p, postpone =>
            {
                // A remote prompt: "postpone" is the Deny button.
                var answer = p.Kind == Prompt.Remote
                    ? new TrayMessage { Type = "answer", Id = p.Id, Allow = !postpone }
                    : new TrayMessage { Type = "answer", Id = p.Id, Postpone = postpone };
                Task.Run(() => Send(answer));
                if (window == w) window = null;
                UpdateStatus();
            });
            w.Closed += (s, e) =>
            {
                if (window != w) return;
                window = null;
                UpdateStatus();
            };
            window = w;
            w.Show();
            UpdateStatus();
        }

        void ShowBanner(string id, Prompt p)
        {
            if (banners.ContainsKey(id)) return;
            var b = new RemoteBanner(id, p, () => Task.Run(() => Send(new TrayMessage { Type = "remote-stop", Id = id })));
            banners[id] = b;
            b.Show();
            UpdateStatus();
        }

        // OpenSelfService shows the self-service window (one per tray).
        void OpenSelfService()
        {
            if (ssWindow != null)
            {
                ssWindow.Activate(SelfServiceWindow.Tab.Software);
                return;
            }
            ssWindow = new SelfServiceWindow(SelfServiceRequest, accent);
            try { ssWindow.Icon = System.Windows.Interop.Imaging.CreateBitmapSourceFromHIcon(icon.Icon.Handle, Int32Rect.Empty, BitmapSizeOptions.FromEmptyOptions()); }
            catch (Exception) { }
            ssWindow.Closed += (s, e) => ssWindow = null;
            ssWindow.Show();
            ssWindow.Activate();
        }

        // SelfServiceRequest sends a request over the pipe and waits for the
        // agent's reply.
        Task<SelfServiceReply> SelfServiceRequest(TrayMessage m)
        {
            var tcs = new TaskCompletionSource<SelfServiceReply>(TaskCreationOptions.RunContinuationsAsynchronously);
            bool online;
            lock (writeLock) online = writer != null;
            if (!online)
            {
                tcs.SetResult(new SelfServiceReply { Error = "offline" });
                return tcs.Task;
            }
            var id = "ss" + Interlocked.Increment(ref ssSeq);
            m.Id = id;
            lock (ssPending) ssPending[id] = tcs;
            Task.Run(() => Send(m));
            Task.Delay(TimeSpan.FromSeconds(45)).ContinueWith(_ =>
            {
                lock (ssPending) ssPending.Remove(id);
                tcs.TrySetResult(new SelfServiceReply { Error = "offline" });
            });
            return tcs.Task;
        }

        void FailSelfService()
        {
            TaskCompletionSource<SelfServiceReply>[] all;
            lock (ssPending)
            {
                all = new TaskCompletionSource<SelfServiceReply>[ssPending.Count];
                ssPending.Values.CopyTo(all, 0);
                ssPending.Clear();
            }
            foreach (var t in all) t.TrySetResult(new SelfServiceReply { Error = "offline" });
        }

        void Send(TrayMessage m)
        {
            lock (writeLock)
            {
                try { writer?.WriteLine(m.ToJson()); }
                catch (IOException) { }
                catch (ObjectDisposedException) { }
            }
        }

        void UpdateStatus()
        {
            RemoteBanner banner = null;
            foreach (var b in banners.Values) { banner = b; break; }
            var text = !connected ? Strings.TrayDisconnected : window != null ? Strings.TrayWaiting(window.Prompt)
                : banner != null ? Strings.TrayRemote(banner.Prompt) : Strings.TrayConnected;
            status.Text = text;
            show.Visible = window != null;
            var tip = product + " – " + text;
            icon.Text = tip.Length > 63 ? tip.Substring(0, 63) : tip; // NotifyIcon's limit
        }

        public void Dispose()
        {
            icon.Visible = false;
            icon.Dispose();
        }
    }

    // TrayIcon draws the FleetWarden shield in the accent colour.
    static class TrayIcon
    {
        [DllImport("user32.dll")]
        static extern bool DestroyIcon(IntPtr icon);

        public static Icon Create(string accent)
        {
            var c = Theme.Accent(accent);
            const int size = 32;
            using (var bmp = new Bitmap(size, size))
            using (var g = Graphics.FromImage(bmp))
            {
                g.SmoothingMode = SmoothingMode.AntiAlias;
                g.ScaleTransform(size / 64f, size / 64f);
                using (var outer = Shield(32, 4, 8, 13, 30, 45.5f, 18.2f, 60, 56))
                using (var inner = Shield(32, 10.5f, 14, 17.3f, 30, 42, 21.6f, 53.9f, 50))
                using (var accentBrush = new SolidBrush(System.Drawing.Color.FromArgb(c.R, c.G, c.B)))
                using (var innerBrush = new SolidBrush(System.Drawing.Color.FromArgb(0x0E, 0x15, 0x25)))
                using (var dots = new SolidBrush(System.Drawing.Color.FromArgb(0x9D, 0xB2, 0xFF)))
                {
                    g.FillPath(accentBrush, outer);
                    g.FillPath(innerBrush, inner);
                    g.FillEllipse(System.Drawing.Brushes.White, 26, 18, 12, 12);
                    g.FillEllipse(dots, 17, 33, 10, 10);
                    g.FillEllipse(dots, 37, 33, 10, 10);
                }
                var h = bmp.GetHicon();
                try { return (Icon)Icon.FromHandle(h).Clone(); }
                finally { DestroyIcon(h); }
            }
        }

        // Shield is the logo's shield outline (branding/logo.svg).
        static GraphicsPath Shield(float cx, float top, float left, float shoulder, float waist, float c1, float c2, float bottom, float right)
        {
            var p = new GraphicsPath();
            p.AddLine(cx, top, left, shoulder);
            p.AddLine(left, shoulder, left, waist);
            p.AddBezier(left, waist, left, c1, c2, bottom - 3.4f, cx, bottom);
            p.AddBezier(cx, bottom, 2 * cx - c2, bottom - 3.4f, right, c1, right, waist);
            p.AddLine(right, waist, right, shoulder);
            p.CloseFigure();
            return p;
        }
    }
}
