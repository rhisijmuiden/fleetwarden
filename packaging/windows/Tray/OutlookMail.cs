using System;
using System.Diagnostics;
using System.IO;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Text;

namespace FleetWarden.Tray
{
    // OutlookMail opens a ticket as a new message in the user's own Outlook,
    // with the device report attached; the user checks it and clicks Send.
    // Without classic Outlook (not installed, or the new Outlook without COM)
    // it opens the default mail program with mailto: and shows the report
    // file in Explorer so the user can attach it.
    static class OutlookMail
    {
        public enum Result { Outlook, MailTo, Failed }

        public static Result Open(OutlookDraft d, out string reportPath)
        {
            reportPath = SaveAttachment(d);
            try
            {
                var type = Type.GetTypeFromProgID("Outlook.Application");
                if (type != null)
                {
                    var app = Activator.CreateInstance(type);
                    var mail = Call(app, "CreateItem", 0); // olMailItem
                    Set(mail, "To", string.Join("; ", d.To ?? new string[0]));
                    Set(mail, "Subject", d.Subject ?? "");
                    Set(mail, "HTMLBody", d.Html ?? "");
                    if (reportPath != null) Call(Get(mail, "Attachments"), "Add", reportPath);
                    Call(mail, "Display", false);
                    return Result.Outlook;
                }
            }
            catch (Exception)
            {
                // Fall back to mailto below.
            }
            try
            {
                var body = d.Text ?? "";
                if (body.Length > 1500) body = body.Substring(0, 1500) + "\n…";
                var url = "mailto:" + Uri.EscapeDataString(string.Join(";", d.To ?? new string[0])) +
                    "?subject=" + Uri.EscapeDataString(d.Subject ?? "") + "&body=" + Uri.EscapeDataString(body);
                Process.Start(new ProcessStartInfo(url) { UseShellExecute = true });
                if (reportPath != null) Process.Start("explorer.exe", "/select,\"" + reportPath + "\"");
                return Result.MailTo;
            }
            catch (Exception)
            {
                return Result.Failed;
            }
        }

        // SaveAttachment writes the report to the user's temp folder.
        static string SaveAttachment(OutlookDraft d)
        {
            if (string.IsNullOrEmpty(d.Attachment)) return null;
            try
            {
                var dir = Path.Combine(Path.GetTempPath(), "FleetWarden");
                Directory.CreateDirectory(dir);
                var name = string.IsNullOrEmpty(d.AttachmentName) ? "report.html" : d.AttachmentName;
                foreach (var c in Path.GetInvalidFileNameChars()) name = name.Replace(c, '_');
                var path = Path.Combine(dir, name);
                File.WriteAllText(path, d.Attachment, new UTF8Encoding(false));
                return path;
            }
            catch (Exception)
            {
                return null;
            }
        }

        static object Call(object o, string name, params object[] args) =>
            o.GetType().InvokeMember(name, BindingFlags.InvokeMethod, null, o, args);

        static object Get(object o, string name) =>
            o.GetType().InvokeMember(name, BindingFlags.GetProperty, null, o, null);

        static void Set(object o, string name, object value) =>
            o.GetType().InvokeMember(name, BindingFlags.SetProperty, null, o, new[] { value });

        [DllImport("secur32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        static extern bool GetUserNameEx(int format, StringBuilder name, ref uint size);

        // UserPrincipalName returns the e-mail style sign-in name of the user
        // (domain and Entra ID accounts), or null for a local account.
        public static string UserPrincipalName()
        {
            try
            {
                uint size = 256;
                var sb = new StringBuilder((int)size);
                if (!GetUserNameEx(8 /* NameUserPrincipal */, sb, ref size)) return null;
                var upn = sb.ToString().Trim();
                return upn.Contains("@") && !upn.Contains(" ") ? upn : null;
            }
            catch (Exception)
            {
                return null;
            }
        }
    }
}
