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
        // Self-service: "ss" requests and their "ss-reply".
        [DataMember(Name = "op", EmitDefaultValue = false)] public string Op;
        [DataMember(Name = "itemId", EmitDefaultValue = false)] public long ItemId;
        [DataMember(Name = "reason", EmitDefaultValue = false)] public string Reason;
        [DataMember(Name = "help", EmitDefaultValue = false)] public HelpRequest Help;
        [DataMember(Name = "reply", EmitDefaultValue = false)] public SelfServiceReply Reply;
        // Chat: the user's message (op "chat") and the chat the agent shows.
        [DataMember(Name = "text", EmitDefaultValue = false)] public string Text;
        [DataMember(Name = "chat", EmitDefaultValue = false)] public ChatState Chat;

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
        public const string Restart = "restart";   // a restart asked by the IT department (power management)
        public const string Shutdown = "shutdown"; // a shutdown asked by the IT department

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

    // Self-service (internal/agentproto/selfservice.go). Times are RFC 3339
    // strings: the JSON serializer only knows its own date format.
    [DataContract]
    public sealed class HelpRequest
    {
        [DataMember(Name = "subject")] public string Subject;
        [DataMember(Name = "message")] public string Message;
        [DataMember(Name = "contact", EmitDefaultValue = false)] public string Contact;
        [DataMember(Name = "urgency", EmitDefaultValue = false)] public string Urgency;
        [DataMember(Name = "category", EmitDefaultValue = false)] public string Category;
        // The user's e-mail address (UPN) when Windows knows it: the requester in the service desk.
        [DataMember(Name = "email", EmitDefaultValue = false)] public string Email;
    }

    // TicketForm is how the IT department set up the ticket form.
    [DataContract]
    public sealed class TicketForm
    {
        public const string FleetWarden = "fleetwarden", Outlook = "outlook", Mail = "mail", Integration = "integration";

        [DataMember(Name = "requireMessage")] public bool RequireMessage;
        [DataMember(Name = "minMessage")] public int MinMessage;
        [DataMember(Name = "askUrgency")] public bool AskUrgency;
        [DataMember(Name = "askCategory")] public bool AskCategory;
        [DataMember(Name = "categories", EmitDefaultValue = false)] public string[] Categories;
        [DataMember(Name = "askContact")] public bool AskContact;
        [DataMember(Name = "sent", EmitDefaultValue = false)] public InfoLine[] Sent;
        [DataMember(Name = "destination")] public string Destination;
        [DataMember(Name = "destinationName", EmitDefaultValue = false)] public string DestinationName;
    }

    [DataContract]
    public sealed class InfoLine
    {
        [DataMember(Name = "label")] public string Label;
        [DataMember(Name = "value")] public string Value;
    }

    // OutlookDraft is a ticket the tray opens as a new message in Outlook.
    [DataContract]
    public sealed class OutlookDraft
    {
        [DataMember(Name = "to")] public string[] To;
        [DataMember(Name = "subject")] public string Subject;
        [DataMember(Name = "html")] public string Html;
        [DataMember(Name = "text")] public string Text;
        [DataMember(Name = "attachmentName", EmitDefaultValue = false)] public string AttachmentName;
        [DataMember(Name = "attachment", EmitDefaultValue = false)] public string Attachment;
    }

    [DataContract]
    public sealed class SelfServiceReply
    {
        [DataMember(Name = "error", EmitDefaultValue = false)] public string Error;
        [DataMember(Name = "message", EmitDefaultValue = false)] public string Message;
        [DataMember(Name = "catalog", EmitDefaultValue = false)] public SelfServiceCatalog Catalog;
        [DataMember(Name = "outlook", EmitDefaultValue = false)] public OutlookDraft Outlook;
    }

    [DataContract]
    public sealed class SelfServiceCatalog
    {
        [DataMember(Name = "productName")] public string ProductName;
        [DataMember(Name = "accentColor")] public string AccentColor;
        [DataMember(Name = "software")] public bool Software;
        [DataMember(Name = "items")] public SelfServiceItem[] Items;
        [DataMember(Name = "updates")] public bool Updates;
        [DataMember(Name = "pendingUpdates")] public int PendingUpdates;
        [DataMember(Name = "rebootPending")] public bool RebootPending;
        [DataMember(Name = "updateState", EmitDefaultValue = false)] public string UpdateState;
        [DataMember(Name = "updateAt", EmitDefaultValue = false)] public string UpdateAt;
        [DataMember(Name = "lastScan", EmitDefaultValue = false)] public string LastScan;
        [DataMember(Name = "help")] public bool Help;
        [DataMember(Name = "helpText", EmitDefaultValue = false)] public string HelpText;
        [DataMember(Name = "helpRequests")] public HelpTicket[] HelpRequests;
        [DataMember(Name = "ticket", EmitDefaultValue = false)] public TicketForm Ticket;
        [DataMember(Name = "chat", EmitDefaultValue = false)] public ChatState Chat;
    }

    // ChatState is a chat with the IT department (internal/agentproto/chat.go).
    [DataContract]
    public sealed class ChatState
    {
        [DataMember(Name = "id")] public long Id;
        [DataMember(Name = "operator")] public string Operator;
        [DataMember(Name = "open")] public bool Open;
        [DataMember(Name = "lines")] public ChatLine[] Lines;
        [DataMember(Name = "productName", EmitDefaultValue = false)] public string ProductName;
        [DataMember(Name = "accentColor", EmitDefaultValue = false)] public string AccentColor;
    }

    [DataContract]
    public sealed class ChatLine
    {
        [DataMember(Name = "id")] public long Id;
        [DataMember(Name = "from")] public string From;
        [DataMember(Name = "name")] public string Name;
        [DataMember(Name = "text")] public string Text;
        [DataMember(Name = "at")] public string At;
    }

    [DataContract]
    public sealed class SelfServiceItem
    {
        public const string Available = "available", Pending = "pending", Rejected = "rejected", Queued = "queued",
            Installing = "installing", Installed = "installed", Failed = "failed";

        [DataMember(Name = "id")] public long Id;
        [DataMember(Name = "name")] public string Name;
        [DataMember(Name = "publisher", EmitDefaultValue = false)] public string Publisher;
        [DataMember(Name = "description", EmitDefaultValue = false)] public string Description;
        [DataMember(Name = "category", EmitDefaultValue = false)] public string Category;
        [DataMember(Name = "version", EmitDefaultValue = false)] public string Version;
        [DataMember(Name = "approval")] public bool Approval;
        [DataMember(Name = "state")] public string State;
        [DataMember(Name = "message", EmitDefaultValue = false)] public string Message;
    }

    [DataContract]
    public sealed class HelpTicket
    {
        [DataMember(Name = "id")] public long Id;
        [DataMember(Name = "subject")] public string Subject;
        [DataMember(Name = "state")] public string State;
        [DataMember(Name = "reply", EmitDefaultValue = false)] public string Reply;
        [DataMember(Name = "createdAt")] public string CreatedAt;
        [DataMember(Name = "updatedAt")] public string UpdatedAt;
        // The ticket number in the service desk and its state there.
        [DataMember(Name = "number", EmitDefaultValue = false)] public string Number;
        [DataMember(Name = "extState", EmitDefaultValue = false)] public string ExtState;
    }
}
