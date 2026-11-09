package agentproto

import "time"

// Management without taking over the screen: processes, services, event
// logs and files.
//
// The server sends a TypeManageRequest with the envelope ID set to a request
// id; the agent answers with one TypeManageResult carrying the same ID. Both
// are signed like every control message. Files do not travel over the
// control channel: for a download the agent posts the file in chunks to
// PathTransfer+transfer id (a signed request, offset in the query) and then
// reports the size and SHA-256 in its result; for an upload it fetches
// PathTransfer+transfer id with a signed GET and checks the SHA-256 from the
// request. Chunks stay below the body limit of the secure gateway.
const (
	TypeManageRequest = "manage.request" // server -> agent
	TypeManageResult  = "manage.result"  // agent -> server

	PathTransfer = "/agent/v1/transfer/" // + transfer id

	// TransferChunk is the size of one uploaded chunk.
	TransferChunk = 8 << 20
	// MaxTransferSize is the largest file either way.
	MaxTransferSize = 4 << 30

	// Limits on what one answer carries.
	MaxManageEvents = 2000
	MaxManageFiles  = 10000
)

// Operations.
const (
	ManageProcesses     = "processes.list"
	ManageKillProcess   = "process.kill"
	ManageServices      = "services.list"
	ManageServiceAction = "service.action"
	ManageLogSources    = "logs.sources"
	ManageLogQuery      = "logs.query"
	ManageFilesList     = "files.list" // an empty path lists the roots (drives)
	ManageFilesMkdir    = "files.mkdir"
	ManageFilesDelete   = "files.delete"
	ManageFilesRename   = "files.rename"
	ManageFilesDownload = "files.download" // device -> server
	ManageFilesUpload   = "files.upload"   // server -> device
)

// Service actions.
const (
	ServiceStart    = "start"
	ServiceStop     = "stop"
	ServiceRestart  = "restart"
	ServiceAuto     = "automatic" // start with the system (systemctl enable)
	ServiceManual   = "manual"
	ServiceDisabled = "disabled"
)

// Error codes in ManageResult.Error.
const (
	ManageErrUnsupported = "unsupported"
	ManageErrNotFound    = "not_found"
	ManageErrDenied      = "access_denied"
	ManageErrExists      = "exists"
	ManageErrInvalid     = "invalid"
	ManageErrProtected   = "protected" // the agent itself, or a critical process
	ManageErrBusy        = "busy"
	ManageErrFailed      = "failed"
	ManageErrTooLarge    = "too_large"
)

// ManageRequest is one operation. Only the fields of the operation are set.
type ManageRequest struct {
	Op string `json:"op"`

	PID    int    `json:"pid,omitempty"`
	Name   string `json:"name,omitempty"`   // service
	Action string `json:"action,omitempty"` // service action

	Log LogQuery `json:"log,omitzero"`

	Path      string `json:"path,omitempty"`
	NewPath   string `json:"newPath,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
	Overwrite bool   `json:"overwrite,omitempty"`
	Transfer  string `json:"transfer,omitempty"`
	Size      int64  `json:"size,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

// ManageResult answers a request.
type ManageResult struct {
	// Error is empty on success, else one of the ManageErr codes.
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`

	Processes []Process   `json:"processes,omitempty"`
	Services  []Service   `json:"services,omitempty"`
	Sources   []LogSource `json:"sources,omitempty"`
	Events    []LogEvent  `json:"events,omitempty"`
	Files     []FileEntry `json:"files,omitempty"`
	// Path is the cleaned path that was listed or written.
	Path      string `json:"path,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Size      int64  `json:"size,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	// Zip is set when a folder was downloaded as a zip file.
	Zip bool `json:"zip,omitempty"`
}

// Process is a running program.
type Process struct {
	PID       int        `json:"pid"`
	PPID      int        `json:"ppid"`
	Name      string     `json:"name"`
	Path      string     `json:"path,omitempty"`
	Command   string     `json:"command,omitempty"`
	User      string     `json:"user,omitempty"`
	Session   int        `json:"session"` // Windows session id
	CPU       float64    `json:"cpu"`     // percent of the whole machine
	Memory    int64      `json:"memory"`  // working set / RSS in bytes
	Threads   int        `json:"threads"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	Protected bool       `json:"protected,omitempty"` // may not be stopped
	Agent     bool       `json:"agent,omitempty"`     // this agent
}

// Service is a Windows service or a systemd unit.
type Service struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	// State: running, stopped, starting, stopping, paused, failed.
	State string `json:"state"`
	// StartType: automatic, automatic-delayed, manual, disabled, static.
	StartType string `json:"startType"`
	Account   string `json:"account,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Path      string `json:"path,omitempty"`
	CanStop   bool   `json:"canStop"`
	Protected bool   `json:"protected,omitempty"` // the agent itself
}

// LogSource is an event log (Windows) or the journal (Linux).
type LogSource struct {
	Name    string `json:"name"`
	Records int64  `json:"records,omitempty"`
}

// Event levels, as Windows numbers them (journald priorities are mapped).
const (
	LevelCritical = 1
	LevelError    = 2
	LevelWarning  = 3
	LevelInfo     = 4
	LevelVerbose  = 5
)

// LogQuery selects events. Newest first.
type LogQuery struct {
	Source string `json:"source"`
	// MaxLevel keeps events at this level or more severe (1-5; 0: all).
	MaxLevel int       `json:"maxLevel,omitempty"`
	Since    time.Time `json:"since,omitzero"`
	Search   string    `json:"search,omitempty"`   // case-insensitive, in message and provider
	Provider string    `json:"provider,omitempty"` // event source or systemd unit
	EventID  int       `json:"eventId,omitempty"`
	Max      int       `json:"max,omitempty"`
}

// LogEvent is one entry.
type LogEvent struct {
	Time     time.Time `json:"time"`
	Level    int       `json:"level"`
	Provider string    `json:"provider"`
	EventID  int       `json:"eventId,omitempty"`
	Message  string    `json:"message"`
	User     string    `json:"user,omitempty"`
	PID      int       `json:"pid,omitempty"`
}

// FileEntry is a file, folder or drive.
type FileEntry struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Dir      bool       `json:"dir"`
	Size     int64      `json:"size"`
	Modified *time.Time `json:"modified,omitempty"`
	Mode     string     `json:"mode,omitempty"`
	Link     bool       `json:"link,omitempty"`
	Hidden   bool       `json:"hidden,omitempty"`
	// For drives: the kind (fixed, removable, network, cdrom) and free space.
	Drive string `json:"drive,omitempty"`
	Free  int64  `json:"free,omitempty"`
}
