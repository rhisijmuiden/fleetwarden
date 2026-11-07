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
        [DataMember(Name = "lang", EmitDefaultValue = false)] public string Lang;

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

        [DataMember(Name = "id")] public string Id;
        [DataMember(Name = "kind")] public string Kind;
        [DataMember(Name = "updates")] public int Updates;
        [DataMember(Name = "canPostpone")] public bool CanPostpone;
        [DataMember(Name = "postponesLeft")] public int PostponesLeft;
        [DataMember(Name = "postponeMinutes")] public int PostponeMinutes;
        [DataMember(Name = "countdownSeconds")] public int CountdownSeconds;
        [DataMember(Name = "productName")] public string ProductName;
        [DataMember(Name = "accentColor")] public string AccentColor;
    }
}
