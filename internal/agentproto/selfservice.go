package agentproto

import "time"

// Self-service messages.
//
// The tray app on Windows lets the logged-on user install approved programs,
// start the approved updates and ask the IT department for help. The tray
// talks to the agent over its local pipe; the agent forwards each request to
// the server and passes the answer back. The agent, not the tray, names the
// user: it reads the Windows account of the process on the other end of the
// pipe.
const (
	// agent -> server
	TypeSelfServiceRequest = "selfservice.request"
	// server -> agent
	TypeSelfServiceReply = "selfservice.reply"
)

// Self-service operations.
const (
	SelfServiceOpCatalog = "catalog" // the catalog, the updates and the user's help requests
	SelfServiceOpInstall = "install" // install (or ask approval for) a catalog item
	SelfServiceOpUpdates = "updates" // install the approved updates now
	SelfServiceOpHelp    = "help"    // send a help request
)

// SelfServiceRequest is one request from the tray, forwarded by the agent.
type SelfServiceRequest struct {
	// RequestID pairs the reply with the request (unique per agent run).
	RequestID string `json:"requestId"`
	Op        string `json:"op"`
	// User is the Windows account of the tray (DOMAIN\name), set by the agent.
	User string `json:"user"`
	// Lang is the user's display language (nl-NL), for texts in the reply.
	Lang string `json:"lang,omitempty"`
	// ItemID and Reason are for SelfServiceInstall: why the user needs a
	// program that needs approval.
	ItemID int64  `json:"itemId,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Help is for SelfServiceHelp.
	Help *HelpRequest `json:"help,omitempty"`
}

// HelpRequest is what the user writes when asking for help.
type HelpRequest struct {
	Subject string `json:"subject"`
	Message string `json:"message"`
	// Contact is how the IT department can reach the user (phone, e-mail).
	Contact string `json:"contact,omitempty"`
	// Urgency (low, normal, high) and Category when the form asks for them.
	Urgency  string `json:"urgency,omitempty"`
	Category string `json:"category,omitempty"`
	// Email is the user's e-mail address or UPN as the tray knows it; the
	// service desk looks the requester up with it.
	Email string `json:"email,omitempty"`
}

// Ticket destinations.
const (
	TicketFleetWarden = "fleetwarden" // only in the web interface (and e-mail to the recipients)
	TicketOutlook     = "outlook"     // the tray opens a new message in the user's Outlook
	TicketMail        = "mail"        // the server e-mails the service desk
	TicketIntegration = "integration" // the server creates a ticket through an integration
)

// TicketForm tells the tray how the help form looks and what goes along.
type TicketForm struct {
	RequireMessage bool     `json:"requireMessage"`
	MinMessage     int      `json:"minMessage"`
	AskUrgency     bool     `json:"askUrgency"`
	AskCategory    bool     `json:"askCategory"`
	Categories     []string `json:"categories"`
	AskContact     bool     `json:"askContact"`
	// Sent lists the device information that goes along, when the user may
	// see it.
	Sent []InfoLine `json:"sent,omitempty"`
	// Destination is a Ticket* value; DestinationName names the service
	// desk ("Freshservice") or the e-mail address.
	Destination     string `json:"destination"`
	DestinationName string `json:"destinationName,omitempty"`
}

// InfoLine is one line of device information.
type InfoLine struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// OutlookDraft is a message the tray opens in the user's Outlook.
type OutlookDraft struct {
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
	Text    string   `json:"text"`
	// Attachment is the full device report as an HTML file.
	AttachmentName string `json:"attachmentName,omitempty"`
	Attachment     string `json:"attachment,omitempty"`
}

// Self-service error codes in SelfServiceReply.Error.
const (
	SelfServiceErrDisabled  = "disabled"    // self-service (or this part of it) is off
	SelfServiceErrNotFound  = "not_found"   // no such catalog item for this device
	SelfServiceErrBusy      = "busy"        // already installing, or an update run is open
	SelfServiceErrNothing   = "nothing"     // no approved updates to install
	SelfServiceErrInvalid   = "invalid"     // missing or too long fields
	SelfServiceErrLimit     = "limit"       // too many requests in a short time
	SelfServiceErrOffline   = "offline"     // agent side: no connection to the server
	SelfServiceErrNoUser    = "no_user"     // agent side: the user of the tray is unknown
	SelfServiceErrInternal  = "internal"    // the server failed
	SelfServiceErrNoService = "unsupported" // agent side: the server does not know self-service
)

// SelfServiceReply answers a SelfServiceRequest.
type SelfServiceReply struct {
	RequestID string `json:"requestId"`
	// Error is a SelfServiceErr code; empty means success.
	Error string `json:"error,omitempty"`
	// Message is a short text for the user in their language.
	Message string `json:"message,omitempty"`
	// Catalog is the state after the request (every operation returns it).
	Catalog *SelfServiceCatalog `json:"catalog,omitempty"`
	// Outlook is set after a help request when the ticket goes through the
	// user's own Outlook.
	Outlook *OutlookDraft `json:"outlook,omitempty"`
}

// SelfServiceCatalog is what the tray's self-service window shows.
type SelfServiceCatalog struct {
	// Branding (white label).
	ProductName string `json:"productName"`
	AccentColor string `json:"accentColor"`
	// Software is false when the catalog is turned off.
	Software bool              `json:"software"`
	Items    []SelfServiceItem `json:"items"`
	// Updates: whether the user may start updates, how many approved updates
	// wait, and whether an update run is open.
	Updates        bool       `json:"updates"`
	PendingUpdates int        `json:"pendingUpdates"`
	RebootPending  bool       `json:"rebootPending"`
	UpdateState    string     `json:"updateState,omitempty"` // state of the open or last run
	UpdateAt       *time.Time `json:"updateAt,omitempty"`
	LastScan       *time.Time `json:"lastScan,omitempty"`
	// Help: whether help requests are on, and the user's own recent ones.
	Help         bool         `json:"help"`
	HelpText     string       `json:"helpText,omitempty"` // shown above the form (opening hours, phone)
	HelpRequests []HelpTicket `json:"helpRequests"`
	// Ticket describes the help form (nil from servers before tickets).
	Ticket *TicketForm `json:"ticket,omitempty"`
}

// Catalog item states for the user.
const (
	ItemAvailable  = "available"  // can be installed or requested
	ItemPending    = "pending"    // waiting for approval
	ItemRejected   = "rejected"   // the last request was rejected (may ask again)
	ItemQueued     = "queued"     // approved, waiting to start
	ItemInstalling = "installing" // downloading or installing
	ItemInstalled  = "installed"  // installed (by self-service or found in the inventory)
	ItemFailed     = "failed"     // the last install failed (may try again)
)

// SelfServiceItem is one program in the catalog.
type SelfServiceItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Publisher   string `json:"publisher,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Version     string `json:"version,omitempty"`
	// Approval: the IT department approves each request first.
	Approval bool   `json:"approval"`
	State    string `json:"state"`
	// Message explains a rejected or failed request.
	Message string `json:"message,omitempty"`
}

// Help request states.
const (
	HelpOpen       = "open"
	HelpInProgress = "in_progress"
	HelpClosed     = "closed"
)

// HelpTicket is one of the user's help requests.
type HelpTicket struct {
	ID      int64  `json:"id"`
	Subject string `json:"subject"`
	State   string `json:"state"`
	Reply   string `json:"reply,omitempty"` // the IT department's note
	// Number is the ticket number in the service desk, and ExtState its
	// state there.
	Number    string    `json:"number,omitempty"`
	ExtState  string    `json:"extState,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
