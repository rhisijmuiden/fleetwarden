using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;

namespace FleetWarden.Tray
{
    // AppCloser closes browsers in this user's session before an update and
    // opens them again afterwards with their tabs.
    //
    // Chrome and Edge get WM_CLOSE on all their windows at once (what
    // "taskkill" without /F does). Chromium keeps windows that close together
    // in the session, and --restore-last-session brings them all back.
    // Firefox would only keep the last window that way, so it is ended at
    // once instead: it saves its session continuously and restores it after
    // an unexpected exit; resume_session_once makes sure it does.
    sealed class AppCloser
    {
        sealed class Relaunch
        {
            public string Exe;
            public string Args;
        }

        readonly Dictionary<string, List<Relaunch>> closed = new Dictionary<string, List<Relaunch>>();
        readonly object gate = new object();

        public string[] Close(string id, CloseApp[] apps, int timeoutSeconds)
        {
            var session = Process.GetCurrentProcess().SessionId;
            var deadline = DateTime.UtcNow.AddSeconds(Math.Max(timeoutSeconds, 5));
            var keys = new List<string>();
            var relaunch = new List<Relaunch>();
            foreach (var app in apps ?? new CloseApp[0])
            {
                if (string.IsNullOrEmpty(app?.Process)) continue;
                var mine = Mine(app.Process, session);
                if (mine.Length == 0) continue;
                var exe = mine.Select(ImagePath).FirstOrDefault(p => !string.IsNullOrEmpty(p));
                if (app.Firefox)
                {
                    foreach (var p in mine) { try { p.Kill(); } catch (Exception) { } }
                }
                else
                {
                    var pids = new HashSet<uint>(mine.Select(p => (uint)p.Id));
                    foreach (var w in TopWindows(pids)) PostMessage(w, WM_CLOSE, IntPtr.Zero, IntPtr.Zero);
                }
                while (DateTime.UtcNow < deadline && Mine(app.Process, session).Length > 0) Thread.Sleep(500);
                foreach (var p in Mine(app.Process, session)) { try { p.Kill(); } catch (Exception) { } } // a "Leave site?" dialog, or a background app
                if (app.Firefox) ResumeFirefoxSession();
                keys.Add(app.Key);
                if (exe != null) relaunch.Add(new Relaunch { Exe = exe, Args = app.RestoreArgs ?? "" });
            }
            lock (gate) closed[id ?? ""] = relaunch;
            return keys.ToArray();
        }

        public void Launch(string id)
        {
            List<Relaunch> list;
            lock (gate)
            {
                if (!closed.TryGetValue(id ?? "", out list)) return;
                closed.Remove(id ?? "");
            }
            foreach (var r in list)
            {
                // Arguments are only the ones the agent allows (--restore-last-session).
                var args = r.Args == "--restore-last-session" ? r.Args : "";
                try { Process.Start(new ProcessStartInfo(r.Exe, args) { UseShellExecute = false, WorkingDirectory = Path.GetDirectoryName(r.Exe) }); }
                catch (Exception ex) { Trace.WriteLine(ex); }
            }
        }

        static Process[] Mine(string name, int session)
        {
            try { return Process.GetProcessesByName(name).Where(p => SafeSession(p) == session).ToArray(); }
            catch (Exception) { return new Process[0]; }
        }

        static int SafeSession(Process p)
        {
            try { return p.SessionId; }
            catch (Exception) { return -1; }
        }

        static string ImagePath(Process p)
        {
            IntPtr h = IntPtr.Zero;
            try
            {
                h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, p.Id);
                if (h == IntPtr.Zero) return null;
                var sb = new StringBuilder(1024);
                var size = sb.Capacity;
                return QueryFullProcessImageName(h, 0, sb, ref size) ? sb.ToString() : null;
            }
            catch (Exception) { return null; }
            finally { if (h != IntPtr.Zero) CloseHandle(h); }
        }

        static List<IntPtr> TopWindows(HashSet<uint> pids)
        {
            var list = new List<IntPtr>();
            EnumWindows((w, _) =>
            {
                GetWindowThreadProcessId(w, out var pid);
                if (pids.Contains(pid) && IsWindowVisible(w)) list.Add(w);
                return true;
            }, IntPtr.Zero);
            return list;
        }

        // ResumeFirefoxSession sets browser.sessionstore.resume_session_once
        // in every profile, so the next start restores the windows and tabs.
        static void ResumeFirefoxSession()
        {
            try
            {
                var root = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "Mozilla", "Firefox", "Profiles");
                if (!Directory.Exists(root)) return;
                foreach (var dir in Directory.GetDirectories(root))
                {
                    var prefs = Path.Combine(dir, "prefs.js");
                    if (!File.Exists(prefs)) continue;
                    File.AppendAllText(prefs, "\nuser_pref(\"browser.sessionstore.resume_session_once\", true);\n");
                }
            }
            catch (Exception ex) { Trace.WriteLine(ex); }
        }

        const uint WM_CLOSE = 0x0010;
        const uint PROCESS_QUERY_LIMITED_INFORMATION = 0x1000;

        delegate bool EnumWindowsProc(IntPtr hWnd, IntPtr lParam);

        [DllImport("user32.dll")] static extern bool EnumWindows(EnumWindowsProc cb, IntPtr lParam);
        [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint pid);
        [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr hWnd);
        [DllImport("user32.dll")] static extern bool PostMessage(IntPtr hWnd, uint msg, IntPtr wParam, IntPtr lParam);
        [DllImport("kernel32.dll", SetLastError = true)] static extern IntPtr OpenProcess(uint access, bool inherit, int pid);
        [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr h);
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        static extern bool QueryFullProcessImageName(IntPtr h, int flags, StringBuilder name, ref int size);
    }
}
