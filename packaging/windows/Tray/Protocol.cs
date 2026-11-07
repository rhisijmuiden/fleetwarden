using System.IO;
using System.Runtime.Serialization;
using System.Runtime.Serialization.Json;
using System.Text;

namespace FleetWarden.Tray
{
    // The JSON-lines protocol of the agent's tray pipe (internal/agent/notify_tray.go).
    [DataContract]
    public sealed class TrayMessage
    {
        [DataMember(Name = "type", EmitDefaultValue = false)] public string Type;
        [DataMember(Name = "prompt", EmitDefaultValue = false)] public Prompt Prompt;
        [DataMember(Name = "id", EmitDefaultValue = false)] public string Id;
        [DataMember(Name = "postpone", EmitDefaultValue = false)] public bool Postpone;
        [DataMember(Name = "allow", EmitDefaultValue = false)] public bool Allow;
        [DataMember(Name = "lang", EmitDefaultValue = false)] public string Lang;
        [DataMember(Name = "apps", EmitDefaultValue = false)] public CloseApp[] Apps;
        [DataMember(Name = "closed", EmitDefaultValue = false)] public string[] Closed;
        [DataMember(Name = "timeoutSeconds", EmitDefaultValue = false)] public int TimeoutSeconds;

        static readonly DataContractJsonSerializer Serializer = new DataContractJsonSerializer(typeof(TrayMessage));

        public string ToJson()
        {
            using (var ms = new MemoryStream())
            {
                Serializer.WriteObject(ms, this);
                return Encoding.UTF8.GetString(ms.ToArray());
            }
        }

        public static TrayMessage FromJson(string line)
        {
            using (var ms = new MemoryStream(Encoding.UTF8.GetBytes(line)))
                return (TrayMessage)Serializer.ReadObject(ms);
        }
    }

    [DataContract]
    public sealed class Prompt
    {
        public const string Install = "install";
        public const string Reboot = "reboot";
        public const string Browser = "browser";
        public const string Remote = "remote";

        [DataMember(Name = "id")] public string Id;
        [DataMember(Name = "kind")] public string Kind;
        [DataMember(Name = "updates")] public int Updates;
        [DataMember(Name = "canPostpone")] public bool CanPostpone;
        [DataMember(Name = "postponesLeft")] public int PostponesLeft;
        [DataMember(Name = "postponeMinutes")] public int PostponeMinutes;
        [DataMember(Name = "countdownSeconds")] public int CountdownSeconds;
        [DataMember(Name = "productName")] public string ProductName;
        [DataMember(Name = "accentColor")] public string AccentColor;
        [DataMember(Name = "apps", EmitDefaultValue = false)] public string[] Apps;
        [DataMember(Name = "operator", EmitDefaultValue = false)] public string Operator;
    }

    // A program to close before an update and open again afterwards.
    [DataContract]
    public sealed class CloseApp
    {
        [DataMember(Name = "key")] public string Key;
        [DataMember(Name = "name")] public string Name;
        [DataMember(Name = "process")] public string Process;
        [DataMember(Name = "restoreArgs", EmitDefaultValue = false)] public string RestoreArgs;
        [DataMember(Name = "firefox", EmitDefaultValue = false)] public bool Firefox;
    }
}
