package server

// Self-service through the tray app. Administrators publish programs from
// the software library in a catalog, for every device group or some of them,
// with or without approval. The logged-on user opens the tray's self-service
// window: the agent forwards its requests (catalog, install, updates, help)
// over the signed WebSocket and the server answers each with the new state.
//
// An install without approval becomes a deployment with one job right away;
// with approval it waits until a technician approves it. "Install updates
// now" starts a patch run with the updates the group's patch policy has
// approved (and the repository drivers), without the install pop-up: the
// user asked for it. Help requests land in the web interface, and the
// technician's reply shows in the tray.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const (
	keySelfService = "selfservice"
	// selfServicePerHour bounds the install and help requests of one device.
	selfServicePerHour     = 20
	selfServiceRetention   = 365 * 24 * time.Hour
	maxSelfServiceItems    = 500
	maxSelfServiceRcpt     = 20
	selfServiceExpireDays  = 3 // an install that could not start in time expires
	selfServiceUpdateShown = 24 * time.Hour
)

// selfServiceSettings are the server-wide self-service switches.
type selfServiceSettings struct {
	// Software shows the catalog in the tray.
	Software bool `json:"software"`
	// Updates lets users install the approved updates themselves.
	Updates bool `json:"updates"`
	// Help lets users send help requests; HelpText shows above the form.
	Help     bool   `json:"help"`
	HelpText string `json:"helpText"`
	// Recipients get an e-mail for each help request and each install
	// request that needs approval, in Language.
	Recipients []string `json:"recipients"`
	Language   string   `json:"language"`
	// Destination is where a help request goes as a ticket (agentproto
	// Ticket*). TicketIntegration is the integration for "integration";
	// TicketAddress the service desk mailbox for "mail" and "outlook".
	Destination       string `json:"destination"`
	TicketIntegration int64  `json:"ticketIntegration"`
	TicketAddress     string `json:"ticketAddress"`
	// FallbackMail e-mails TicketAddress (or the recipients) when the
	// integration fails.
	FallbackMail bool `json:"fallbackMail"`
	// The form in the tray.
	RequireMessage bool     `json:"requireMessage"`
	MinMessage     int      `json:"minMessage"`
	AskUrgency     bool     `json:"askUrgency"`
	AskCategory    bool     `json:"askCategory"`
	Categories     []string `json:"categories"`
	AskContact     bool     `json:"askContact"`
	ShowSent       bool     `json:"showSent"`
	// Report lists the parts of the device report that go along.
	Report []string `json:"report"`
	// LinkDevice adds a link to the device page; LinkActions the quick
	// actions next to it.
	LinkDevice  bool     `json:"linkDevice"`
	LinkActions []string `json:"linkActions"`
}

func defaultSelfService() selfServiceSettings {
	return selfServiceSettings{Software: true, Updates: true, Help: true, Recipients: []string{}, Language: "en",
		Destination: agentproto.TicketFleetWarden, AskContact: true, AskUrgency: true, ShowSent: true,
		Categories: []string{}, Report: slices.Clone(reportParts), LinkDevice: true, LinkActions: []string{"chat", "remote", "scan"}}
}

func (s *Server) loadSelfService(ctx context.Context) selfServiceSettings {
	v := defaultSelfService()
	_ = s.st.GetSetting(ctx, keySelfService, &v)
	if v.Recipients == nil {
		v.Recipients = []string{}
	}
	if v.Categories == nil {
		v.Categories = []string{}
	}
	if v.Report == nil {
		v.Report = []string{}
	}
	if v.LinkActions == nil {
		v.LinkActions = []string{}
	}
	if v.Destination == "" {
		v.Destination = agentproto.TicketFleetWarden
	}
	return v
}

// ---- agent messages ----

func (s *Server) handleSelfServiceMsg(ctx context.Context, c *agentConn, e *agentproto.Envelope) error {
	var req agentproto.SelfServiceRequest
	if err := e.Decode(&req); err != nil {
		return err
	}
	reply := s.selfService(ctx, c.deviceID, req)
	reply.RequestID = req.RequestID
	s.hub.send(c.deviceID, agentproto.TypeSelfServiceReply, reply)
	return nil
}

// selfService handles one request of a device and returns the reply with
// the new catalog.
func (s *Server) selfService(ctx context.Context, deviceID string, req agentproto.SelfServiceRequest) agentproto.SelfServiceReply {
	user := cleanText(req.User, 255)
	lang := langOf(req.Lang)
	set := s.loadSelfService(ctx)
	dev, err := s.st.DeviceByID(ctx, deviceID)
	if err != nil {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInternal}
	}
	var out agentproto.SelfServiceReply
	switch req.Op {
	case agentproto.SelfServiceOpCatalog:
	case agentproto.SelfServiceOpInstall:
		out = s.selfServiceInstall(ctx, dev, set, user, lang, req)
	case agentproto.SelfServiceOpUpdates:
		out = s.selfServiceUpdates(ctx, dev, set, user, lang)
	case agentproto.SelfServiceOpHelp:
		out = s.selfServiceHelp(ctx, dev, set, user, lang, req.Help)
	case agentproto.SelfServiceOpChat:
		out = s.selfServiceChat(ctx, dev, user, lang, req.Text)
	default:
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInvalid}
	}
	cat, err := s.selfServiceCatalog(ctx, dev, set, user, lang)
	if err != nil {
		s.log.Warn("self-service catalog", "device", deviceID, "err", err)
		if out.Error == "" {
			out.Error = agentproto.SelfServiceErrInternal
		}
		return out
	}
	out.Catalog = &cat
	return out
}

func langOf(v string) string {
	if strings.HasPrefix(strings.ToLower(v), "nl") {
		return "nl"
	}
	return "en"
}

// ssTexts are the texts the server writes for tray users and in e-mails.
var ssTexts = map[string]map[string]string{
	"en": {
		"requested":        "Your request for %s was sent to the IT department. You see here when it is approved.",
		"installing":       "%s is being installed. This can take a few minutes.",
		"updating":         "%d updates are being installed.",
		"upToDate":         "There are no approved updates to install.",
		"helpSent":         "Your help request #%d was sent to the IT department.",
		"mail.reason":      "Reason",
		"mail.subject":     "Subject",
		"mail.message":     "Message",
		"mail.contact":     "Contact",
		"mail.user":        "User",
		"mail.device":      "Device",
		"mail.unknownUser": "A user",
		"chatTranscript":   "Chat with %s in FleetWarden:",
		"chatEnded":        "The chat has ended.",
		"testTicket":       "FleetWarden test ticket",
		"testTicketBody":   "This ticket was created by FleetWarden to test the integration. You can close it.",
		"previewSubject":   "Outlook does not start",
		"previewMessage":   "Since this morning Outlook closes right after it starts. I already restarted the computer.",
		"ticketSent":       "Your ticket %s was created. The IT department will contact you.",
		"outlookDraft":     "An e-mail was opened in Outlook. Check it and click Send.",
	},
	"nl": {
		"requested":        "Je aanvraag voor %s is naar de IT-afdeling gestuurd. Je ziet hier wanneer die is goedgekeurd.",
		"installing":       "%s wordt geïnstalleerd. Dit kan een paar minuten duren.",
		"updating":         "Er worden %d updates geïnstalleerd.",
		"upToDate":         "Er zijn geen goedgekeurde updates om te installeren.",
		"helpSent":         "Je hulpvraag #%d is naar de IT-afdeling gestuurd.",
		"mail.reason":      "Reden",
		"mail.subject":     "Onderwerp",
		"mail.message":     "Bericht",
		"mail.contact":     "Contact",
		"mail.user":        "Gebruiker",
		"mail.device":      "Apparaat",
		"mail.unknownUser": "Een gebruiker",
		"chatTranscript":   "Chat met %s in FleetWarden:",
		"chatEnded":        "De chat is beëindigd.",
		"testTicket":       "FleetWarden testticket",
		"testTicketBody":   "Dit ticket is door FleetWarden aangemaakt om de koppeling te testen. Je kunt het sluiten.",
		"previewSubject":   "Outlook start niet",
		"previewMessage":   "Sinds vanochtend sluit Outlook direct na het starten. Ik heb de computer al opnieuw opgestart.",
		"ticketSent":       "Je ticket %s is aangemaakt. De IT-afdeling neemt contact met je op.",
		"outlookDraft":     "Er is een e-mail geopend in Outlook. Controleer die en klik op Verzenden.",
	},
}

func ssText(lang, key string, args ...any) string {
	t, ok := ssTexts[lang][key]
	if !ok {
		t = ssTexts["en"][key]
	}
	if len(args) == 0 {
		return t
	}
	return fmt.Sprintf(t, args...)
}

// selfServiceText is a short message for the tray user.
func selfServiceText(lang, key string, args ...any) string { return ssText(lang, key, args...) }

// limited reports whether the device sent too many requests in the last hour.
func (s *Server) selfServiceLimited(ctx context.Context, deviceID string) bool {
	n, err := s.st.RecentSelfServiceActions(ctx, deviceID, s.now().Add(-time.Hour))
	return err != nil || n >= selfServicePerHour
}

func (s *Server) selfServiceInstall(ctx context.Context, dev store.Device, set selfServiceSettings, user, lang string,
	req agentproto.SelfServiceRequest) agentproto.SelfServiceReply {
	if !set.Software {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrDisabled}
	}
	entries, err := s.st.DeviceCatalog(ctx, dev.ID)
	if err != nil {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInternal}
	}
	i := slices.IndexFunc(entries, func(e store.CatalogEntry) bool { return e.Item.ID == req.ItemID })
	if i < 0 {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrNotFound}
	}
	entry := entries[i]
	switch st, _ := itemState(entry); st {
	case agentproto.ItemPending, agentproto.ItemQueued, agentproto.ItemInstalling:
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrBusy}
	}
	if s.selfServiceLimited(ctx, dev.ID) {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrLimit}
	}
	item := entry.Item
	reason := cleanText(req.Reason, 500)
	r := store.SelfServiceRequest{ItemID: &item.ID, PackageID: &item.PackageID, Name: item.Name(), DeviceID: dev.ID, Username: user, Reason: reason}
	actor := selfServiceActor(user, dev.Hostname)
	if item.Approval {
		r.State = "pending"
		id, err := s.st.CreateSelfServiceRequest(ctx, r)
		if err != nil {
			return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInternal}
		}
		_ = s.st.Audit(ctx, store.AuditEntry{Actor: actor, Action: "selfservice.request", Target: item.Name(), Success: true,
			Details: map[string]any{"request": id, "device": dev.Hostname}})
		s.mailSelfService(ctx, set, "ss_request", map[string]string{"user": userOrUnknown(user, set.Language), "device": dev.Hostname, "name": item.Name()},
			[]mail.Fact{{Label: ssText(set.Language, "mail.reason"), Value: orDash(reason)}})
		return agentproto.SelfServiceReply{Message: selfServiceText(lang, "requested", item.Name())}
	}
	depID, err := s.selfServiceDeploy(ctx, item, dev, actor)
	if err != nil {
		s.log.Warn("self-service install", "device", dev.ID, "item", item.ID, "err", err)
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInternal}
	}
	now := s.now()
	r.State, r.DeploymentID, r.DecidedBy, r.DecidedAt = "approved", &depID, "automatic", &now
	if _, err := s.st.CreateSelfServiceRequest(ctx, r); err != nil {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInternal}
	}
	_ = s.st.Audit(ctx, store.AuditEntry{Actor: actor, Action: "selfservice.install", Target: item.Name(), Success: true,
		Details: map[string]any{"deployment": depID, "device": dev.Hostname}})
	go s.sendSoftwareJobs(context.WithoutCancel(ctx), []string{dev.ID})
	return agentproto.SelfServiceReply{Message: selfServiceText(lang, "installing", item.Name())}
}

// selfServiceDeploy creates the deployment (one job) for an install.
func (s *Server) selfServiceDeploy(ctx context.Context, item store.SelfServiceItem, dev store.Device, by string) (int64, error) {
	pid := item.PackageID
	id, _, err := s.st.CreateDeployment(ctx, store.NewDeployment{
		Deployment: store.Deployment{Name: item.Name(), Action: agentproto.ActionInstall, PackageID: &pid, Platform: dev.Platform,
			StartAt: s.now(), ExpireDays: selfServiceExpireDays, RequestedBy: by},
		DeviceIDs: []string{dev.ID},
	})
	return id, err
}

func selfServiceActor(user, host string) string {
	if user == "" {
		user = "unknown user"
	}
	return user + " on " + host + " (self-service)"
}

func userOrUnknown(user, lang string) string {
	if user != "" {
		return user
	}
	return ssText(lang, "mail.unknownUser")
}

func orDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "–"
	}
	return v
}

func (s *Server) selfServiceUpdates(ctx context.Context, dev store.Device, set selfServiceSettings, user, lang string) agentproto.SelfServiceReply {
	if !set.Updates {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrDisabled}
	}
	pd, pol, err := s.selfServicePatchState(ctx, dev.ID)
	if err != nil {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInternal}
	}
	if pol == nil {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrDisabled}
	}
	if pd.OpenRun != nil {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrBusy}
	}
	ids, drivers, err := s.approvedForDevice(ctx, dev.ID, pol)
	if err != nil {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInternal}
	}
	if len(ids) == 0 && len(drivers) == 0 && !(pd.RebootPending && pol.Reboot != agentproto.RebootNever) {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrNothing, Message: selfServiceText(lang, "upToDate")}
	}
	if pd.Cluster != "" {
		if busy, err := s.st.ClusterBusy(ctx, pd.Cluster, dev.ID); err != nil || busy {
			return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrBusy}
		}
	}
	// The user asked for it: no "updates are ready" pop-up. A restart still
	// follows the policy (and asks the user when the policy says so).
	p := *pol
	p.NotifyUsers = false
	actor := selfServiceActor(user, dev.Hostname)
	run, err := s.startPatchRun(ctx, dev.ID, &p, ids, drivers, "selfservice", actor)
	switch {
	case errors.Is(err, store.ErrConflict):
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrBusy}
	case err != nil:
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInternal}
	}
	_ = s.st.Audit(ctx, store.AuditEntry{Actor: actor, Action: "selfservice.updates", Target: dev.Hostname, Success: true,
		Details: map[string]any{"run": run.ID, "updates": len(ids), "drivers": len(drivers)}})
	return agentproto.SelfServiceReply{Message: selfServiceText(lang, "updating", len(ids)+len(drivers))}
}

// selfServicePatchState returns the scheduler view of a device and its
// enabled patch policy (nil without one).
func (s *Server) selfServicePatchState(ctx context.Context, deviceID string) (store.PatchDevice, *store.PatchPolicy, error) {
	devs, err := s.st.PatchDevices(ctx, []string{deviceID})
	if err != nil || len(devs) == 0 {
		if err == nil {
			err = store.ErrNotFound
		}
		return store.PatchDevice{}, nil, err
	}
	policies, err := s.loadPolicies(ctx)
	if err != nil {
		return devs[0], nil, err
	}
	return devs[0], devicePolicy(devs[0], policies), nil
}

// approvedForDevice lists the updates the policy approves now and the
// repository drivers the device may install.
func (s *Server) approvedForDevice(ctx context.Context, deviceID string, pol *store.PatchPolicy) ([]string, []agentproto.DriverInstall, error) {
	rows, err := s.st.DevicePatches(ctx, deviceID)
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	var ids []string
	for _, r := range rows {
		if ok, _ := approval(*pol, r, now); ok {
			ids = append(ids, r.UpdateID)
		}
	}
	sort.Strings(ids)
	drivers, err := s.deviceDriverJobs(ctx, deviceID, pol, nil)
	return ids, drivers, err
}

func (s *Server) selfServiceHelp(ctx context.Context, dev store.Device, set selfServiceSettings, user, lang string, h *agentproto.HelpRequest) agentproto.SelfServiceReply {
	if !set.Help {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrDisabled}
	}
	if h == nil {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInvalid}
	}
	subject, message, contact, urgency, category, email, ok := validTicket(set, h)
	if !ok {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInvalid}
	}
	if s.selfServiceLimited(ctx, dev.ID) {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrLimit}
	}
	req := store.HelpRequest{DeviceID: dev.ID, Username: user, Subject: subject, Message: message, Contact: contact,
		Urgency: urgency, Category: category, Email: email, Destination: set.Destination}
	if set.Destination == agentproto.TicketIntegration && set.TicketIntegration != 0 {
		iid := set.TicketIntegration
		req.IntegrationID = &iid
	}
	id, err := s.st.CreateHelpRequest(ctx, req)
	if err != nil {
		return agentproto.SelfServiceReply{Error: agentproto.SelfServiceErrInternal}
	}
	in := ticketInput{User: user, Email: email, Subject: subject, Message: message, Contact: contact, Urgency: urgency,
		Category: category, At: s.now()}
	rep := s.buildTicketReport(ctx, dev, set, in, id, set.Language)
	if b, err := json.Marshal(rep); err == nil {
		if err := s.st.SetHelpReport(ctx, id, b); err != nil {
			s.log.Warn("help report", "err", err)
		}
	}
	_ = s.st.Audit(ctx, store.AuditEntry{Actor: selfServiceActor(user, dev.Hostname), Action: "selfservice.help", Target: subject, Success: true,
		Details: map[string]any{"request": id, "device": dev.Hostname, "destination": set.Destination}})
	ml := set.Language
	s.mailSelfService(ctx, set, "help_request", map[string]string{"user": userOrUnknown(user, ml), "device": dev.Hostname, "subject": subject},
		[]mail.Fact{
			{Label: ssText(ml, "mail.subject"), Value: subject},
			{Label: ssText(ml, "mail.message"), Value: orDash(message)},
			{Label: ssText(ml, "mail.contact"), Value: orDash(contact)},
			{Label: ssText(ml, "mail.user"), Value: userOrUnknown(user, ml)},
			{Label: ssText(ml, "mail.device"), Value: dev.Hostname + " (" + dev.GroupName + ")"},
		})
	draft := s.sendTicket(ctx, set, dev, id, in, rep)
	if draft != nil {
		return agentproto.SelfServiceReply{Message: selfServiceText(lang, "outlookDraft"), Outlook: draft}
	}
	return agentproto.SelfServiceReply{Message: selfServiceText(lang, "helpSent", id)}
}

// mailSelfService e-mails the self-service recipients.
func (s *Server) mailSelfService(ctx context.Context, set selfServiceSettings, kind string, vars map[string]string, facts []mail.Fact) {
	if len(set.Recipients) == 0 {
		return
	}
	if cfg, err := s.loadMail(ctx); err != nil || !cfg.Enabled {
		return
	}
	b := s.loadBranding(ctx)
	var g generalSettings
	_ = s.st.GetSetting(ctx, keyGeneral, &g)
	brand := mailBrandOf(b, g)
	if brand.URL != "" {
		brand.URL = strings.TrimRight(brand.URL, "/") + "/selfservice"
	}
	for _, to := range set.Recipients {
		msg := mail.RenderFacts(kind, set.Language, to, brand, vars, facts)
		if _, err := s.st.EnqueueMail(ctx, store.Mail{Kind: kind, To: to, Subject: msg.Subject, BodyText: msg.Text, BodyHTML: msg.HTML}); err != nil {
			s.log.Warn("self-service mail", "err", err)
			return
		}
	}
}

// itemState is the state of a catalog item for the user, with a message.
func itemState(e store.CatalogEntry) (string, string) {
	switch e.RequestState {
	case "pending":
		return agentproto.ItemPending, ""
	case "approved":
		switch e.JobState {
		case agentproto.SoftwarePending, agentproto.SoftwareSent:
			return agentproto.ItemQueued, ""
		case agentproto.SoftwareDownloading, agentproto.SoftwareInstalling, agentproto.SoftwareWaiting:
			return agentproto.ItemInstalling, ""
		case agentproto.SoftwareCompleted, agentproto.SoftwareSkipped:
			return agentproto.ItemInstalled, ""
		case agentproto.SoftwareFailed, agentproto.SoftwareExpired, agentproto.SoftwareCancelled:
			if !e.Installed {
				return agentproto.ItemFailed, e.JobMessage
			}
		}
	case "rejected":
		if !e.Installed {
			return agentproto.ItemRejected, e.RequestNote
		}
	}
	if e.Installed {
		return agentproto.ItemInstalled, ""
	}
	return agentproto.ItemAvailable, ""
}

// selfServiceCatalog builds what the tray window shows for a device and user.
func (s *Server) selfServiceCatalog(ctx context.Context, dev store.Device, set selfServiceSettings, user, lang string) (agentproto.SelfServiceCatalog, error) {
	b := s.loadBranding(ctx)
	cat := agentproto.SelfServiceCatalog{ProductName: b.ProductName, AccentColor: b.PrimaryColor, Software: set.Software,
		Items: []agentproto.SelfServiceItem{}, Help: set.Help, HelpText: set.HelpText, HelpRequests: []agentproto.HelpTicket{}}
	if set.Software {
		entries, err := s.st.DeviceCatalog(ctx, dev.ID)
		if err != nil {
			return cat, err
		}
		for _, e := range entries {
			st, msg := itemState(e)
			cat.Items = append(cat.Items, agentproto.SelfServiceItem{ID: e.Item.ID, Name: e.Item.Name(), Publisher: e.Item.Publisher,
				Description: e.Item.Text(), Category: e.Item.Category, Version: e.Item.Version, Approval: e.Item.Approval, State: st, Message: msg})
		}
	}
	if set.Updates {
		pd, pol, err := s.selfServicePatchState(ctx, dev.ID)
		if err != nil {
			return cat, err
		}
		if pol != nil {
			cat.Updates = true
			ids, drivers, err := s.approvedForDevice(ctx, dev.ID, pol)
			if err != nil {
				return cat, err
			}
			cat.PendingUpdates, cat.RebootPending, cat.LastScan = len(ids)+len(drivers), pd.RebootPending, pd.ScanAt
			runs, err := s.st.ListPatchRuns(ctx, store.RunQuery{Scope: store.Scope{All: true}, DeviceID: dev.ID, Limit: 1})
			if err != nil {
				return cat, err
			}
			if len(runs) > 0 {
				r := runs[0]
				if r.FinishedAt == nil || s.now().Sub(*r.FinishedAt) < selfServiceUpdateShown {
					at := r.UpdatedAt
					cat.UpdateState, cat.UpdateAt = r.State, &at
				}
			}
		}
	}
	if set.Help {
		cat.Ticket = s.ticketForm(ctx, dev, set, user, lang)
	}
	cat.Chat = s.openChatState(ctx, dev.ID)
	if set.Help && user != "" {
		list, err := s.st.ListHelpRequests(ctx, store.HelpQuery{Scope: store.Scope{All: true}, DeviceID: dev.ID, Username: user, Limit: 10})
		if err != nil {
			return cat, err
		}
		for _, h := range list {
			cat.HelpRequests = append(cat.HelpRequests, agentproto.HelpTicket{ID: h.ID, Subject: h.Subject, State: h.State, Reply: h.Reply,
				CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt, Number: h.TicketNumber, ExtState: h.TicketState})
		}
	}
	return cat, nil
}

// ---- web API ----

func (s *Server) selfServiceRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	handle := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermSelfServiceHandle, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermSelfServiceManage, 0, h) }
	mux.Handle("GET /api/v1/selfservice/count", view(s.handleSelfServiceCount))
	mux.Handle("GET /api/v1/selfservice/items", view(s.handleListSelfServiceItems))
	mux.Handle("POST /api/v1/selfservice/items", manage(s.handleCreateSelfServiceItem))
	mux.Handle("PUT /api/v1/selfservice/items/{id}", manage(s.handleUpdateSelfServiceItem))
	mux.Handle("DELETE /api/v1/selfservice/items/{id}", manage(s.handleDeleteSelfServiceItem))
	mux.Handle("GET /api/v1/selfservice/requests", view(s.handleListSelfServiceRequests))
	mux.Handle("POST /api/v1/selfservice/requests/{id}/approve", handle(s.handleDecideSelfServiceRequest(true)))
	mux.Handle("POST /api/v1/selfservice/requests/{id}/reject", handle(s.handleDecideSelfServiceRequest(false)))
	mux.Handle("GET /api/v1/selfservice/help", view(s.handleListHelpRequests))
	mux.Handle("PUT /api/v1/selfservice/help/{id}", handle(s.handleUpdateHelpRequest))
	mux.Handle("GET /api/v1/selfservice/settings", view(s.handleGetSelfServiceSettings))
	mux.Handle("PUT /api/v1/selfservice/settings", manage(s.handlePutSelfServiceSettings))
	mux.Handle("POST /api/v1/selfservice/ticket-preview", manage(s.handleTicketPreview))
}

func (s *Server) handleSelfServiceCount(w http.ResponseWriter, r *http.Request) {
	c, err := s.st.CountSelfService(r.Context(), s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// selfServiceItemsView is the catalog with the packages that can be added.
type selfServiceItemsView struct {
	Items []store.SelfServiceItem `json:"items"`
}

func (s *Server) handleListSelfServiceItems(w http.ResponseWriter, r *http.Request) {
	items, err := s.st.ListSelfServiceItems(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// Users scoped to groups see the items for every device and those of their groups.
	sc := s.scope(r)
	if !sc.All {
		items = slices.DeleteFunc(items, func(i store.SelfServiceItem) bool {
			return len(i.GroupIDs) > 0 && !slices.ContainsFunc(i.GroupIDs, sc.Allows)
		})
	}
	writeJSON(w, http.StatusOK, selfServiceItemsView{Items: items})
}

type selfServiceItemReq struct {
	PackageID   int64   `json:"packageId"`
	DisplayName string  `json:"displayName"`
	Description string  `json:"description"`
	Category    string  `json:"category"`
	GroupIDs    []int64 `json:"groupIds"`
	Approval    bool    `json:"approval"`
	Enabled     *bool   `json:"enabled"`
}

// validItem cleans an item request and checks its groups; it returns an
// error code.
func (s *Server) validItem(ctx context.Context, req *selfServiceItemReq) string {
	req.DisplayName = cleanText(req.DisplayName, 120)
	req.Description = cleanOutput(req.Description, 1000)
	req.Category = cleanText(req.Category, 60)
	var groups []int64
	for _, g := range req.GroupIDs {
		if !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}
	if len(groups) > 0 {
		all, err := s.st.ListGroups(ctx, store.Scope{All: true})
		if err != nil {
			return "internal"
		}
		for _, g := range groups {
			if !slices.ContainsFunc(all, func(x store.DeviceGroup) bool { return x.ID == g }) {
				return "unknown_group"
			}
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	req.GroupIDs = groups
	return ""
}

func (s *Server) handleCreateSelfServiceItem(w http.ResponseWriter, r *http.Request) {
	var req selfServiceItemReq
	if !decode(w, r, &req) {
		return
	}
	if code := s.validItem(r.Context(), &req); code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	pkg, err := s.st.SoftwarePackageByID(r.Context(), req.PackageID)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusBadRequest, "unknown_package")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if items, err := s.st.ListSelfServiceItems(r.Context()); err == nil && len(items) >= maxSelfServiceItems {
		apiError(w, http.StatusBadRequest, "too_many_items")
		return
	}
	u, _ := currentUser(r)
	enabled := req.Enabled == nil || *req.Enabled
	id, err := s.st.CreateSelfServiceItem(r.Context(), store.SelfServiceItem{PackageID: pkg.ID, DisplayName: req.DisplayName,
		Description: req.Description, Category: req.Category, GroupIDs: req.GroupIDs, Approval: req.Approval, Enabled: enabled, CreatedBy: u.Username})
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "already_in_catalog")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	item, err := s.st.SelfServiceItemByID(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "selfservice.item_add", item.Name(), true, map[string]any{"item": id, "package": pkg.Name, "approval": req.Approval, "groups": req.GroupIDs})
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleUpdateSelfServiceItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var req selfServiceItemReq
	if !decode(w, r, &req) {
		return
	}
	if code := s.validItem(r.Context(), &req); code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	cur, err := s.st.SelfServiceItemByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	cur.DisplayName, cur.Description, cur.Category, cur.GroupIDs, cur.Approval = req.DisplayName, req.Description, req.Category, req.GroupIDs, req.Approval
	if req.Enabled != nil {
		cur.Enabled = *req.Enabled
	}
	if err := s.st.UpdateSelfServiceItem(r.Context(), cur); err != nil {
		s.internalError(w, r, err)
		return
	}
	item, err := s.st.SelfServiceItemByID(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "selfservice.item_update", item.Name(), true, map[string]any{"item": id, "approval": item.Approval, "enabled": item.Enabled,
		"groups": item.GroupIDs})
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleDeleteSelfServiceItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	item, err := s.st.SelfServiceItemByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	u, _ := currentUser(r)
	if err := s.st.DeleteSelfServiceItem(r.Context(), id, u.Username); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "selfservice.item_remove", item.Name(), true, map[string]any{"item": id})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListSelfServiceRequests(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	switch state {
	case "", "pending", "approved", "rejected", "cancelled":
	default:
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.st.ListSelfServiceRequests(r.Context(), store.SelfServiceRequestQuery{Scope: s.scope(r), State: state, Limit: limit})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

type ssDecisionReq struct {
	Note string `json:"note"`
}

func (s *Server) handleDecideSelfServiceRequest(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r)
		if !ok {
			apiError(w, http.StatusNotFound, "not_found")
			return
		}
		var req ssDecisionReq
		if r.ContentLength != 0 && !decode(w, r, &req) {
			return
		}
		note := cleanText(req.Note, 500)
		ctx := r.Context()
		ssr, err := s.st.SelfServiceRequestByID(ctx, id)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !s.scope(r).Allows(ssr.GroupID)) {
			apiError(w, http.StatusNotFound, "not_found")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if ssr.State != "pending" {
			apiError(w, http.StatusConflict, "already_decided")
			return
		}
		u, _ := currentUser(r)
		if !approve {
			if ok, err := s.st.DecideSelfServiceRequest(ctx, id, "rejected", u.Username, note, nil); err != nil {
				s.internalError(w, r, err)
				return
			} else if !ok {
				apiError(w, http.StatusConflict, "already_decided")
				return
			}
			s.audit(r, "selfservice.reject", ssr.Name, true, map[string]any{"request": id, "device": ssr.Hostname, "user": ssr.Username})
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if ssr.ItemID == nil {
			apiError(w, http.StatusConflict, "item_removed")
			return
		}
		item, err := s.st.SelfServiceItemByID(ctx, *ssr.ItemID)
		if errors.Is(err, store.ErrNotFound) {
			apiError(w, http.StatusConflict, "item_removed")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		dev, err := s.st.DeviceByID(ctx, ssr.DeviceID)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		depID, err := s.selfServiceDeploy(ctx, item, dev, u.Username+" (self-service: "+orDash(ssr.Username)+")")
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		if ok, err := s.st.DecideSelfServiceRequest(ctx, id, "approved", u.Username, note, &depID); err != nil || !ok {
			_ = s.st.CancelDeployment(ctx, depID, u.Username)
			if err != nil {
				s.internalError(w, r, err)
			} else {
				apiError(w, http.StatusConflict, "already_decided")
			}
			return
		}
		s.audit(r, "selfservice.approve", ssr.Name, true, map[string]any{"request": id, "device": ssr.Hostname, "user": ssr.Username, "deployment": depID})
		go s.sendSoftwareJobs(context.WithoutCancel(ctx), []string{dev.ID})
		writeJSON(w, http.StatusOK, map[string]int64{"deploymentId": depID})
	}
}

func (s *Server) handleListHelpRequests(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	switch state {
	case "", "active", agentproto.HelpOpen, agentproto.HelpInProgress, agentproto.HelpClosed:
	default:
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.st.ListHelpRequests(r.Context(), store.HelpQuery{Scope: s.scope(r), State: state, DeviceID: r.URL.Query().Get("device"), Limit: limit})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for i := range list {
		list[i].Online = s.hub.online(list[i].DeviceID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

type helpUpdateReq struct {
	State      string `json:"state"`
	AssignedTo string `json:"assignedTo"`
	Reply      string `json:"reply"`
}

func (s *Server) handleUpdateHelpRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var req helpUpdateReq
	if !decode(w, r, &req) {
		return
	}
	switch req.State {
	case agentproto.HelpOpen, agentproto.HelpInProgress, agentproto.HelpClosed:
	default:
		apiError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	h, err := s.st.HelpRequestByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !s.scope(r).Allows(h.GroupID)) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	assigned := cleanText(req.AssignedTo, 120)
	if req.State == agentproto.HelpInProgress && assigned == "" {
		u, _ := currentUser(r)
		assigned = u.Username
	}
	reply := cleanOutput(req.Reply, 2000)
	if err := s.st.UpdateHelpRequest(r.Context(), id, req.State, assigned, reply); err != nil {
		s.internalError(w, r, err)
		return
	}
	if reply != "" && reply != h.Reply {
		s.ticketNote(r.Context(), h, reply)
	}
	s.audit(r, "selfservice.help_update", h.Subject, true, map[string]any{"request": id, "state": req.State, "assignedTo": assigned,
		"device": h.Hostname})
	out, err := s.st.HelpRequestByID(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out.Online = s.hub.online(out.DeviceID)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetSelfServiceSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.loadSelfService(r.Context()))
}

func (s *Server) handlePutSelfServiceSettings(w http.ResponseWriter, r *http.Request) {
	var req selfServiceSettings
	if !decode(w, r, &req) {
		return
	}
	req.HelpText = cleanOutput(req.HelpText, 500)
	if req.Language != "nl" {
		req.Language = "en"
	}
	rcpt := []string{}
	for _, a := range req.Recipients {
		a = strings.TrimSpace(a)
		if a == "" || slices.Contains(rcpt, a) {
			continue
		}
		if !mail.ValidAddress(a) {
			apiError(w, http.StatusBadRequest, "invalid_email")
			return
		}
		rcpt = append(rcpt, a)
	}
	if len(rcpt) > maxSelfServiceRcpt {
		apiError(w, http.StatusBadRequest, "invalid_recipients")
		return
	}
	req.Recipients = rcpt
	if code := s.normalizeTickets(r.Context(), &req); code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	if err := s.st.PutSetting(r.Context(), keySelfService, req); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "selfservice.settings", "", true, map[string]any{"software": req.Software, "updates": req.Updates, "help": req.Help,
		"recipients": req.Recipients, "destination": req.Destination, "integration": req.TicketIntegration})
	writeJSON(w, http.StatusOK, req)
}
