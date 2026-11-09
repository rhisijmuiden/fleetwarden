using System;
using System.IO;
using System.Net;
using System.Security.Cryptography.X509Certificates;
using System.Text.RegularExpressions;
using WixToolset.Dtf.WindowsInstaller;

namespace FleetWarden.Installer
{
    // Used by the wizard's "Test connection" button: checks that a FleetWarden
    // server answers at SERVER and reports its TLS certificate. Results go to
    // FW_TEST_OK ("1"/""), FW_TEST_MSG and FW_CERT_* for the result dialog.
    public static class CustomActions
    {
        [CustomAction]
        public static ActionResult TestConnection(Session session)
        {
            session["FW_TEST_OK"] = "";
            session["FW_CERT_SUBJECT"] = "";
            session["FW_CERT_ISSUER"] = "";
            session["FW_CERT_EXPIRES"] = "";
            session["FW_CERT_THUMB"] = "";
            string server = (session["SERVER"] ?? "").Trim().TrimEnd('/');
            if (server.Length == 0)
            {
                session["FW_TEST_MSG"] = "Enter the server address first.";
                return ActionResult.Success;
            }
            if (!server.Contains("://")) server = "https://" + server;
            session["SERVER"] = server;
            X509Certificate2 cert = null;
            try
            {
                ServicePointManager.SecurityProtocol |= SecurityProtocolType.Tls12;
                var req = (HttpWebRequest)WebRequest.Create(server + "/agent/v1/hello");
                req.Timeout = 15000;
                req.UserAgent = "fleetwarden-agent-installer";
                req.ServerCertificateValidationCallback = (sender, c, chain, errors) =>
                {
                    if (c != null) cert = new X509Certificate2(c);
                    return errors == System.Net.Security.SslPolicyErrors.None;
                };
                string body;
                using (var res = (HttpWebResponse)req.GetResponse())
                using (var reader = new StreamReader(res.GetResponseStream()))
                {
                    body = reader.ReadToEnd();
                }
                if (!Regex.IsMatch(body, "\"protocolVersion\"\\s*:\\s*[1-9]"))
                {
                    session["FW_TEST_MSG"] = "This address answers, but not as a FleetWarden server.";
                }
                else
                {
                    var m = Regex.Match(body, "\"version\"\\s*:\\s*\"([^\"]*)\"");
                    session["FW_TEST_OK"] = "1";
                    session["FW_TEST_MSG"] = "Connected to FleetWarden " + (m.Success ? m.Groups[1].Value : "") + ".";
                }
            }
            catch (WebException ex)
            {
                session["FW_TEST_MSG"] = "Connection failed: " + ex.Message;
            }
            catch (Exception ex)
            {
                session["FW_TEST_MSG"] = "Connection failed: " + ex.Message;
            }
            if (cert != null)
            {
                session["FW_CERT_SUBJECT"] = cert.Subject;
                session["FW_CERT_ISSUER"] = cert.Issuer;
                session["FW_CERT_EXPIRES"] = cert.NotAfter.ToString("yyyy-MM-dd");
                session["FW_CERT_THUMB"] = Sha256Hex(cert.RawData);
            }
            else if (server.StartsWith("http://", StringComparison.OrdinalIgnoreCase))
            {
                session["FW_CERT_SUBJECT"] = "No TLS: the token is sent unencrypted. Use https:// outside a test lab.";
            }
            return ActionResult.Success;
        }

        static string Sha256Hex(byte[] data)
        {
            using (var sha = System.Security.Cryptography.SHA256.Create())
            {
                return BitConverter.ToString(sha.ComputeHash(data)).Replace("-", ":");
            }
        }
    }
}
