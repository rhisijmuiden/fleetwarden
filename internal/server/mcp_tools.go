package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// mcpLevel is how much a tool may change. A token and the MCP settings each
// allow up to one level.
type mcpLevel int

const (
	mcpRead  mcpLevel = iota // only reads
	mcpWrite                 // starts scans, approves updates, installs software
	mcpFull                  // restarts or shuts down devices, stops services, ends processes
)

var mcpLevelNames = []string{"read", "write", "full"}

func (l mcpLevel) String() string { return mcpLevelNames[l] }

func parseMCPLevel(s string) (mcpLevel, bool) {
	i := slices.Index(mcpLevelNames, s)
	return mcpLevel(max(i, 0)), i >= 0
}

// mcpParam is one argument of a tool.
type mcpParam struct {
	name  string
	typ   string // string, integer, boolean, array (of strings)
	desc  string
	enum  []string
	query string // the query parameter it fills, for the simple list tools
}

// mcpTool is one tool of the MCP server. Every tool calls the HTTP API as the
// token's user, so the permissions, group scopes, checks and audit entries of
// the web interface apply unchanged.
type mcpTool struct {
	name     string
	title    string
	desc     string
	group    string // for the settings page
	level    mcpLevel
	perm     auth.Permission // the tool is hidden for users without it
	params   []mcpParam
	required []string
	run      func(c *mcpCall, a mcpArgs) (any, error)
}

func (t mcpTool) schema() map[string]any {
	props := map[string]any{}
	for _, p := range t.params {
		s := map[string]any{"type": p.typ, "description": p.desc}
		if p.typ == "array" {
			s["items"] = map[string]any{"type": "string"}
		}
		if len(p.enum) > 0 {
			s["enum"] = p.enum
		}
		props[p.name] = s
	}
	out := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(t.required) > 0 {
		out["required"] = t.required
	}
	return out
}

// mcpCall is one request of an assistant: the token, its user and the HTTP
// request it came in on.
type mcpCall struct {
	s     *Server
	user  store.User
	token store.MCPToken
	ctx   context.Context
	req   *http.Request
}

// mcpAPIError is an error answer of the HTTP API, passed on to the assistant.
type mcpAPIError struct {
	Status int
	Code   string
	Detail string
}

func (e *mcpAPIError) Error() string {
	msg := fmt.Sprintf("FleetWarden answered %d %s", e.Status, e.Code)
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	switch e.Code {
	case "forbidden":
		msg += " (the user of this token lacks the permission)"
	case "not_found":
		msg += " (it does not exist or lies outside the user's device groups)"
	case "device_offline":
		msg += " (the agent is not connected)"
	}
	return msg
}

// api runs one request against the HTTP API as the token's user.
func (c *mcpCall) api(method, path string, q url.Values, body any) (json.RawMessage, error) {
	var rd *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	// The client address, scheme and host of the assistant's request stay, so
	// audit entries show where the call came from.
	ctx := context.WithValue(c.req.Context(), mcpCallKey, c)
	ctx = context.WithoutCancel(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	r, err := http.NewRequestWithContext(ctx, method, path, rd)
	if err != nil {
		return nil, err
	}
	r.Header.Set("X-Requested-With", "fleetwarden")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", c.req.UserAgent())
	r.RemoteAddr = c.req.RemoteAddr
	w := &mcpRecorder{header: http.Header{}, code: http.StatusOK}
	c.s.appHandler().ServeHTTP(w, r)
	if w.code >= 400 {
		var e apiErr
		_ = json.Unmarshal(w.body.Bytes(), &e)
		if e.Error == "" {
			e.Error = http.StatusText(w.code)
		}
		return nil, &mcpAPIError{Status: w.code, Code: e.Error, Detail: e.Detail}
	}
	if w.body.Len() == 0 {
		return json.RawMessage(`{"ok":true}`), nil
	}
	return json.RawMessage(w.body.Bytes()), nil
}

func (c *mcpCall) get(path string, q url.Values) (json.RawMessage, error) {
	return c.api(http.MethodGet, path, q, nil)
}

type mcpRecorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
	wrote  bool
}

func (m *mcpRecorder) Header() http.Header { return m.header }
func (m *mcpRecorder) WriteHeader(code int) {
	if !m.wrote {
		m.code, m.wrote = code, true
	}
}
func (m *mcpRecorder) Write(b []byte) (int, error) {
	m.wrote = true
	if m.body.Len() > mcpMaxResult*2 {
		return len(b), nil // the result is cut anyway
	}
	return m.body.Write(b)
}

// mcpArgs are the arguments of a tool call.
type mcpArgs map[string]any

func (a mcpArgs) str(k string) string {
	switch v := a[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func (a mcpArgs) int(k string) (int64, bool) {
	switch v := a[k].(type) {
	case float64:
		return int64(v), v == float64(int64(v))
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n, err == nil
	}
	return 0, false
}

func (a mcpArgs) bool(k string) bool {
	switch v := a[k].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

func (a mcpArgs) strs(k string) []string {
	var out []string
	switch v := a[k].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case string:
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func (a mcpArgs) need(k string) (string, error) {
	v := a.str(k)
	if v == "" {
		return "", fmt.Errorf("argument %q is required", k)
	}
	return v, nil
}

// query copies the arguments of a simple list tool to query parameters.
func (a mcpArgs) query(params []mcpParam) url.Values {
	q := url.Values{}
	for _, p := range params {
		if p.query == "" {
			continue
		}
		v := a.str(p.name)
		if p.typ == "boolean" {
			if a.bool(p.name) {
				q.Set(p.query, "1")
			}
			continue
		}
		if v != "" {
			q.Set(p.query, v)
		}
	}
	return q
}

// mcpDevice is a device a tool works on.
type mcpDevice struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	Online   bool   `json:"online"`
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// device finds a device by its ID or host name within the user's scope.
func (c *mcpCall) device(a mcpArgs) (mcpDevice, error) {
	ref, err := a.need("device")
	if err != nil {
		return mcpDevice{}, err
	}
	var d mcpDevice
	if uuidRE.MatchString(ref) {
		raw, err := c.get("/api/v1/devices/"+url.PathEscape(ref), nil)
		if err == nil {
			err = json.Unmarshal(raw, &d)
			return d, err
		}
		var ae *mcpAPIError
		if !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
			return d, err
		}
	}
	raw, err := c.get("/api/v1/devices", url.Values{"search": {ref}, "limit": {"25"}})
	if err != nil {
		return d, err
	}
	var list struct {
		Items []mcpDevice `json:"items"`
		Total int         `json:"total"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return d, err
	}
	var exact []mcpDevice
	for _, x := range list.Items {
		if strings.EqualFold(x.Hostname, ref) {
			exact = append(exact, x)
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(exact) == 0 && list.Total == 1:
		return list.Items[0], nil
	case len(exact) == 0 && list.Total == 0:
		return d, fmt.Errorf("no device %q found (or it lies outside the user's device groups); use list_devices to search", ref)
	}
	names := []string{}
	cands := list.Items
	if len(exact) > 1 {
		cands = exact
	}
	for _, x := range cands {
		names = append(names, x.Hostname+" ("+x.ID+")")
	}
	return d, fmt.Errorf("%q matches several devices: %s; pass the device ID", ref, strings.Join(names, ", "))
}

func (d mcpDevice) path(sub string) string {
	return "/api/v1/devices/" + url.PathEscape(d.ID) + sub
}

// confirmed checks the confirm argument of a destructive tool: it must be the
// device's host name, so a model cannot act on the wrong device by accident.
func confirmed(a mcpArgs, d mcpDevice) error {
	if !strings.EqualFold(a.str("confirm"), d.Hostname) {
		return fmt.Errorf("this action needs confirm set to the exact host name %q; ask the user before you confirm", d.Hostname)
	}
	return nil
}

var (
	pDevice  = mcpParam{name: "device", typ: "string", desc: "Device ID or host name"}
	pConfirm = mcpParam{name: "confirm", typ: "string", desc: "The exact host name of the device, to confirm. Ask the user first."}
	pLimit   = mcpParam{name: "limit", typ: "integer", desc: "Maximum number of results", query: "limit"}
	pGroup   = mcpParam{name: "group_id", typ: "integer", desc: "Only this device group (see list_groups)", query: "group"}
	pPlat    = mcpParam{name: "platform", typ: "string", desc: "windows or linux", enum: []string{"windows", "linux"}, query: "platform"}
	pSearch  = mcpParam{name: "search", typ: "string", desc: "Text to search for", query: "search"}
)

// listTool calls one GET endpoint with the arguments as query parameters.
func listTool(name, title, group, desc, path string, perm auth.Permission, params ...mcpParam) mcpTool {
	return mcpTool{name: name, title: title, group: group, desc: desc, perm: perm, params: params,
		run: func(c *mcpCall, a mcpArgs) (any, error) { return c.get(path, a.query(params)) }}
}

// deviceTool calls one GET endpoint of a device.
func deviceTool(name, title, group, desc, sub string, perm auth.Permission, params ...mcpParam) mcpTool {
	return mcpTool{name: name, title: title, group: group, desc: desc, perm: perm, params: append([]mcpParam{pDevice}, params...),
		required: []string{"device"},
		run: func(c *mcpCall, a mcpArgs) (any, error) {
			d, err := c.device(a)
			if err != nil {
				return nil, err
			}
			return c.get(d.path(sub), a.query(params))
		}}
}

var cveRE = regexp.MustCompile(`^(?i)(CVE-\d{4}-\d{4,}|GHSA-[0-9a-z-]{14}|[A-Z]+-[0-9A-Za-z:._-]{1,40})$`)

// mcpTools lists every tool. Order is the order in tools/list.
func mcpTools() []mcpTool {
	view, manage := auth.PermDevicesView, auth.PermDevicesManage
	return []mcpTool{
		{name: "get_overview", title: "Fleet overview", group: "overview", perm: view,
			desc: "Summary of the fleet: device counts, missing updates, vulnerabilities, security status and open alerts. Start here.",
			run:  mcpOverview},
		listTool("list_devices", "List devices", "devices",
			"Lists devices with host name, OS, group, agent version, last seen and online state. Filters combine.",
			"/api/v1/devices", view, pSearch, pGroup, pPlat,
			mcpParam{name: "status", typ: "string", desc: "online or offline", enum: []string{"online", "offline"}, query: "status"},
			mcpParam{name: "software", typ: "string", desc: "Only devices with installed software matching this name", query: "software"},
			pLimit, mcpParam{name: "offset", typ: "integer", desc: "Skip this many results (paging)", query: "offset"}),
		deviceTool("get_device", "Device details", "devices",
			"Full inventory of one device: hardware, OS, network, logged-on user, group and agent.", "", view),
		deviceTool("get_device_software", "Installed software", "devices", "Software installed on one device.", "/software", view),
		deviceTool("get_device_asset", "Asset details", "devices", "Owner, location, purchase and warranty of one device.", "/asset", view),
		listTool("list_groups", "List device groups", "devices", "Device groups with their policies.", "/api/v1/groups", view),
		listTool("find_software", "Find installed software", "software",
			"Software installed across the fleet with the number of devices per version.", "/api/v1/software/installed", view, pSearch, pPlat),
		{name: "find_software_devices", title: "Devices with software", group: "software", perm: view,
			desc:     "Devices that have a given program installed, optionally one version.",
			params:   []mcpParam{{name: "name", typ: "string", desc: "Exact software name as find_software shows it"}, {name: "version", typ: "string", desc: "Only this version"}, pPlat},
			required: []string{"name"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				return c.get("/api/v1/software/installed/devices", url.Values{"name": {a.str("name")}, "version": {a.str("version")}, "platform": {a.str("platform")}})
			}},
		listTool("list_software_packages", "Software library", "software", "Packages in the software library that can be deployed.", "/api/v1/software/packages", view),
		listTool("list_deployments", "List deployments", "software", "Recent software deployments with their progress.", "/api/v1/deployments", view, pLimit),
		{name: "get_deployment", title: "Deployment details", group: "software", perm: view,
			desc: "One deployment with the result per device.", params: []mcpParam{{name: "deployment_id", typ: "integer", desc: "Deployment ID"}},
			required: []string{"deployment_id"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				id, ok := a.int("deployment_id")
				if !ok {
					return nil, errors.New("deployment_id must be a number")
				}
				return c.get("/api/v1/deployments/"+strconv.FormatInt(id, 10), nil)
			}},
		listTool("list_updates", "Missing updates", "patches",
			"Updates (Windows Update and apt) missing on devices, with the approval decision and the number of devices.",
			"/api/v1/patches/updates", view, pSearch, pPlat,
			mcpParam{name: "category", typ: "string", desc: "Update category, for example security", query: "category"},
			mcpParam{name: "decision", typ: "string", desc: "approved, declined or none (not decided: follows the patch policy)", enum: []string{"approved", "declined", "none"}, query: "decision"}),
		deviceTool("get_device_patches", "Device updates", "patches", "Updates missing or pending on one device.", "/patches", view),
		listTool("list_patch_runs", "Patch runs", "patches", "Recent patch installations and their result.", "/api/v1/patches/runs",
			view, mcpParam{name: "device", typ: "string", desc: "Only this device ID", query: "device"}, pLimit),
		listTool("list_vulnerabilities", "List vulnerabilities", "vulnerabilities",
			"Known vulnerabilities (CVEs) found on devices, with severity, KEV status, fix and number of devices.",
			"/api/v1/vulnerabilities", view, pSearch, pGroup,
			mcpParam{name: "severity", typ: "string", desc: "Severity", enum: []string{"critical", "high", "medium", "low"}, query: "severity"},
			mcpParam{name: "kev", typ: "boolean", desc: "Only those on the CISA KEV list (exploited in the wild)", query: "kev"},
			mcpParam{name: "fixable", typ: "boolean", desc: "Only those with a fix available", query: "fixable"}, pLimit),
		{name: "get_vulnerability", title: "Vulnerability details", group: "vulnerabilities", perm: view,
			desc:   "One vulnerability with its description, score, fix and the affected devices.",
			params: []mcpParam{{name: "cve", typ: "string", desc: "CVE ID such as CVE-2024-3094"}}, required: []string{"cve"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				id := a.str("cve")
				if !cveRE.MatchString(id) {
					return nil, errors.New("cve must look like CVE-2024-3094")
				}
				return c.get("/api/v1/vulnerabilities/cve/"+url.PathEscape(strings.ToUpper(id)), nil)
			}},
		deviceTool("get_device_vulnerabilities", "Device vulnerabilities", "vulnerabilities", "Vulnerabilities found on one device.", "/vulnerabilities", view),
		listTool("list_security_status", "Security status", "security",
			"BitLocker/LUKS, antivirus, firewall and Secure Boot status per device.", "/api/v1/security/devices", view, pSearch, pGroup,
			mcpParam{name: "filter", typ: "string", desc: "Only devices with this problem", enum: securityFilters[1:], query: "filter"}),
		deviceTool("get_device_security", "Device security", "security", "Encryption, antivirus, firewall and Secure Boot of one device.", "/security", view),
		listTool("list_alerts", "List alerts", "alerts", "Open alerts (or all with state=all).", "/api/v1/alerts", view,
			mcpParam{name: "state", typ: "string", desc: "open (default) or all", enum: []string{"open", "all"}, query: "state"},
			mcpParam{name: "device", typ: "string", desc: "Only this device ID", query: "device"}, pLimit),
		listTool("list_assets", "List assets", "assets", "Asset data of devices: owner, location, warranty.", "/api/v1/assets", view, pSearch, pGroup,
			mcpParam{name: "warranty", typ: "string", desc: "expired, expiring or unknown", query: "warranty"}),
		listTool("list_licenses", "Software licences", "assets", "Software licences with seats used and free.", "/api/v1/licenses", view),
		{name: "get_report", title: "Report", group: "overview", perm: view,
			desc:   "A report as shown on the Reports page.",
			params: []mcpParam{{name: "kind", typ: "string", desc: "Report", enum: reportKinds}}, required: []string{"kind"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				k := a.str("kind")
				if !slices.Contains(reportKinds, k) {
					return nil, fmt.Errorf("kind must be one of %s", strings.Join(reportKinds, ", "))
				}
				return c.get("/api/v1/reports/"+k, nil)
			}},
		listTool("list_power_actions", "Power actions", "power", "Recent wake, restart and shutdown actions.", "/api/v1/power/actions", view,
			mcpParam{name: "device", typ: "string", desc: "Only this device ID", query: "device"}, pLimit),
		deviceTool("list_processes", "Running processes", "manage", "Processes running on one device (the agent must be online).",
			"/manage/processes", auth.PermRemoteManage),
		deviceTool("list_services", "Services", "manage", "Services or systemd units on one device (the agent must be online).",
			"/manage/services", auth.PermRemoteManage),
		deviceTool("read_event_log", "Read event log", "manage",
			"Recent entries of the Windows Event Log or the systemd journal of one device (the agent must be online).",
			"/manage/logs/events", auth.PermRemoteManage,
			mcpParam{name: "source", typ: "string", desc: "Log, for example System or Application (Windows) or a unit (Linux)", query: "source"},
			mcpParam{name: "level", typ: "string", desc: "Minimum level: error, warning or info", query: "level"},
			mcpParam{name: "hours", typ: "integer", desc: "How many hours back", query: "hours"},
			mcpParam{name: "search", typ: "string", desc: "Text in the message", query: "search"},
			mcpParam{name: "max", typ: "integer", desc: "Maximum number of entries", query: "max"}),
		listTool("get_audit_log", "Audit log", "overview", "Recent entries of the audit log, also those of AI assistants.", "/api/v1/audit",
			auth.PermAuditView, mcpParam{name: "search", typ: "string", desc: "Text to search for", query: "q"}, pLimit),

		// Changes.
		{name: "request_scan", title: "Scan a device", group: "actions", level: mcpWrite, perm: manage,
			desc:     "Asks the agent of a device to send fresh inventory, look for updates, or report its security status now.",
			params:   []mcpParam{pDevice, {name: "kind", typ: "string", desc: "What to scan", enum: []string{"inventory", "updates", "security"}}},
			required: []string{"device", "kind"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				sub := map[string]string{"inventory": "/inventory", "updates": "/patches/scan", "security": "/security/refresh"}[a.str("kind")]
				if sub == "" {
					return nil, errors.New("kind must be inventory, updates or security")
				}
				d, err := c.device(a)
				if err != nil {
					return nil, err
				}
				if _, err := c.api(http.MethodPost, d.path(sub), nil, struct{}{}); err != nil {
					return nil, err
				}
				return map[string]any{"requested": a.str("kind"), "device": d.Hostname}, nil
			}},
		{name: "set_update_decision", title: "Approve or decline an update", group: "actions", level: mcpWrite, perm: auth.PermPatchesApprove,
			desc: "Approves or declines one update for every device, or returns it to the patch policy (decision \"policy\").",
			params: []mcpParam{{name: "update_id", typ: "string", desc: "Update ID from list_updates"},
				{name: "decision", typ: "string", desc: "approved, declined or policy", enum: []string{"approved", "declined", "policy"}}},
			required: []string{"update_id", "decision"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				dec := a.str("decision")
				if dec == "policy" {
					dec = ""
				} else if dec != "approved" && dec != "declined" {
					return nil, errors.New("decision must be approved, declined or policy")
				}
				return c.api(http.MethodPut, "/api/v1/patches/decision", nil, decisionReq{UpdateID: a.str("update_id"), Decision: dec})
			}},
		{name: "install_updates", title: "Install updates now", group: "actions", level: mcpWrite, perm: manage,
			desc:     "Installs updates on one device now, outside the maintenance window. Without update_ids the approved updates are installed. Restarts follow the patch policy and always ask a logged-on user.",
			params:   []mcpParam{pDevice, {name: "update_ids", typ: "array", desc: "Update IDs from get_device_patches; empty for the approved updates"}},
			required: []string{"device"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				d, err := c.device(a)
				if err != nil {
					return nil, err
				}
				return c.api(http.MethodPost, d.path("/patches/install"), nil, installNowReq{UpdateIDs: a.strs("update_ids")})
			}},
		{name: "deploy_software", title: "Install software", group: "actions", level: mcpWrite, perm: auth.PermSoftwareDeploy,
			desc: "Installs a package from the software library on devices. Returns the deployment; follow it with get_deployment.",
			params: []mcpParam{{name: "package_id", typ: "integer", desc: "Package ID from list_software_packages"},
				{name: "devices", typ: "array", desc: "Device IDs or host names"}},
			required: []string{"package_id", "devices"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				pkg, ok := a.int("package_id")
				if !ok {
					return nil, errors.New("package_id must be a number")
				}
				refs := a.strs("devices")
				if len(refs) == 0 || len(refs) > 200 {
					return nil, errors.New("give between 1 and 200 devices")
				}
				ids := []string{}
				for _, ref := range refs {
					d, err := c.device(mcpArgs{"device": ref})
					if err != nil {
						return nil, err
					}
					ids = append(ids, d.ID)
				}
				return c.api(http.MethodPost, "/api/v1/deployments", nil, map[string]any{"action": "install", "packageId": pkg, "deviceIds": ids})
			}},
		{name: "acknowledge_alert", title: "Acknowledge an alert", group: "actions", level: mcpWrite, perm: auth.PermAlertsManage,
			desc: "Marks an open alert as seen.", params: []mcpParam{{name: "alert_id", typ: "integer", desc: "Alert ID from list_alerts"}},
			required: []string{"alert_id"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				id, ok := a.int("alert_id")
				if !ok {
					return nil, errors.New("alert_id must be a number")
				}
				return c.api(http.MethodPost, "/api/v1/alerts/"+strconv.FormatInt(id, 10)+"/acknowledge", nil, struct{}{})
			}},
		{name: "wake_device", title: "Wake a device", group: "actions", level: mcpWrite, perm: auth.PermPowerControl,
			desc: "Sends Wake-on-LAN to an offline device through agents in the same network.", params: []mcpParam{pDevice},
			required: []string{"device"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				d, err := c.device(a)
				if err != nil {
					return nil, err
				}
				return c.api(http.MethodPost, d.path("/power"), nil, map[string]any{"action": "wake"})
			}},

		// Destructive.
		{name: "restart_device", title: "Restart or shut down a device", group: "destructive", level: mcpFull, perm: auth.PermPowerControl,
			desc: "Restarts or shuts down a device. A logged-on user is warned and may postpone. Needs confirm set to the host name; always ask the user first.",
			params: []mcpParam{pDevice, {name: "action", typ: "string", desc: "restart or shutdown", enum: []string{"restart", "shutdown"}},
				{name: "skip_if_user_logged_on", typ: "boolean", desc: "Do nothing when someone is logged on"}, pConfirm},
			required: []string{"device", "action", "confirm"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				act := a.str("action")
				if act != "restart" && act != "shutdown" {
					return nil, errors.New("action must be restart or shutdown")
				}
				d, err := c.device(a)
				if err != nil {
					return nil, err
				}
				if err := confirmed(a, d); err != nil {
					return nil, err
				}
				return c.api(http.MethodPost, d.path("/power"), nil, map[string]any{"action": act, "notify": true, "maxPostpones": 2,
					"postponeMinutes": 10, "countdownSeconds": 300, "skipIfUserLoggedOn": a.bool("skip_if_user_logged_on")})
			}},
		{name: "service_action", title: "Start, stop or restart a service", group: "destructive", level: mcpFull, perm: auth.PermRemoteManage,
			desc: "Starts, stops or restarts a service (or changes its start type) on one device. Needs confirm set to the host name; always ask the user first.",
			params: []mcpParam{pDevice, {name: "service", typ: "string", desc: "Service name as list_services shows it"},
				{name: "action", typ: "string", desc: "What to do", enum: []string{"start", "stop", "restart", "auto", "manual", "disabled"}}, pConfirm},
			required: []string{"device", "service", "action", "confirm"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				d, err := c.device(a)
				if err != nil {
					return nil, err
				}
				if err := confirmed(a, d); err != nil {
					return nil, err
				}
				return c.api(http.MethodPost, d.path("/manage/services/action"), nil, serviceActionReq{Name: a.str("service"), Action: a.str("action")})
			}},
		{name: "kill_process", title: "End a process", group: "destructive", level: mcpFull, perm: auth.PermRemoteManage,
			desc: "Ends a process on one device. Needs confirm set to the host name; always ask the user first.",
			params: []mcpParam{pDevice, {name: "pid", typ: "integer", desc: "Process ID from list_processes"},
				{name: "name", typ: "string", desc: "Process name, for the audit log"}, pConfirm},
			required: []string{"device", "pid", "confirm"},
			run: func(c *mcpCall, a mcpArgs) (any, error) {
				pid, ok := a.int("pid")
				if !ok || pid <= 0 {
					return nil, errors.New("pid must be a positive number")
				}
				d, err := c.device(a)
				if err != nil {
					return nil, err
				}
				if err := confirmed(a, d); err != nil {
					return nil, err
				}
				return c.api(http.MethodPost, d.path("/manage/processes/"+strconv.FormatInt(pid, 10)+"/kill"), nil, map[string]string{"name": a.str("name")})
			}},
	}
}

// mcpOverview combines the summaries of the dashboard pages the user may see.
func mcpOverview(c *mcpCall, _ mcpArgs) (any, error) {
	parts := []struct{ key, path string }{
		{"devices", "/api/v1/devices/stats"},
		{"patches", "/api/v1/patches/summary"},
		{"vulnerabilities", "/api/v1/vulnerabilities/summary"},
		{"security", "/api/v1/security/summary"},
		{"openAlerts", "/api/v1/alerts/count"},
	}
	out := map[string]any{}
	for _, p := range parts {
		raw, err := c.get(p.path, nil)
		if err != nil {
			out[p.key] = map[string]string{"error": err.Error()}
			continue
		}
		out[p.key] = raw
	}
	return out, nil
}

// mcpToolNames is the set of tool names.
func mcpToolNames() map[string]bool {
	m := map[string]bool{}
	for _, t := range mcpTools() {
		m[t.name] = true
	}
	return m
}
