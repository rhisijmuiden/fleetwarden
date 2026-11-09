package server

// Configuration management. A configuration holds settings of one type
// (browser policies, registry values, a script) for one platform and is
// assigned to device groups. Every minute the scheduler works out which
// configurations apply to each online device and sends the list when it
// changed. Agents apply it, put back what was changed and report the state
// per configuration.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const (
	configPolicyKey   = "config.policy"
	maxConfigSettings = 64 << 10
	maxScriptSize     = 64 << 10
	configRunDays     = 90
	maxConfigOutput   = 8000
)

// ---- scheduler ----

// configTick sends every online device the configurations that apply to it
// and drops states of configurations that no longer apply.
func (s *Server) configTick(ctx context.Context) {
	if err := s.st.PruneDeviceConfigurations(ctx); err != nil {
		s.log.Warn("configurations", "err", err)
	}
	s.pushConfigPolicies(ctx, s.hub.onlineIDs())
}

func (s *Server) pushConfigPolicies(ctx context.Context, ids []string) {
	if len(ids) == 0 {
		return
	}
	configs, err := s.st.EnabledConfigurations(ctx)
	if err != nil {
		if !canceled(err) {
			s.log.Warn("configurations", "err", err)
		}
		return
	}
	targets, err := s.st.ConfigTargets(ctx, ids)
	if err != nil {
		if !canceled(err) {
			s.log.Warn("configurations", "err", err)
		}
		return
	}
	cache := map[string]agentproto.ConfigPolicy{}
	for _, t := range targets {
		key := t.Platform + "/" + strconv.FormatInt(t.GroupID, 10)
		p, ok := cache[key]
		if !ok {
			p = configPolicyFor(configs, t.GroupID, t.Platform)
			cache[key] = p
		}
		s.hub.sendChanged(t.DeviceID, configPolicyKey, agentproto.TypeConfigPolicy, p)
	}
}

// configPolicyFor builds the policy of the devices of a group on a platform.
func configPolicyFor(configs []store.Configuration, groupID int64, platform string) agentproto.ConfigPolicy {
	p := agentproto.ConfigPolicy{Items: []agentproto.ConfigItem{}, ReapplyMinutes: agentproto.DefaultReapplyMinutes}
	for _, c := range configs {
		if c.Platform != platform || !slices.Contains(c.GroupIDs, groupID) {
			continue
		}
		it, err := configItem(c)
		if err != nil {
			continue
		}
		p.Items = append(p.Items, it)
	}
	return p
}

// configItem turns a stored configuration into what the agent gets.
func configItem(c store.Configuration) (agentproto.ConfigItem, error) {
	it := agentproto.ConfigItem{ID: c.ID, Revision: c.Revision, Name: c.Name, Type: c.Type}
	var err error
	switch c.Type {
	case agentproto.ConfigBrowser:
		it.Browser = &agentproto.BrowserPolicy{}
		err = json.Unmarshal(c.Settings, it.Browser)
	case agentproto.ConfigRegistry:
		it.Registry = &agentproto.RegistrySettings{}
		err = json.Unmarshal(c.Settings, it.Registry)
	case agentproto.ConfigScript:
		it.Script = &agentproto.ScriptConfig{}
		err = json.Unmarshal(c.Settings, it.Script)
	case agentproto.ConfigDrives:
		it.Drives = &agentproto.DriveSettings{}
		err = json.Unmarshal(c.Settings, it.Drives)
	case agentproto.ConfigPrinters:
		it.Printers = &agentproto.PrinterSettings{}
		err = json.Unmarshal(c.Settings, it.Printers)
	case agentproto.ConfigDesktop:
		it.Desktop = &agentproto.DesktopSettings{}
		err = json.Unmarshal(c.Settings, it.Desktop)
	case agentproto.ConfigPower:
		it.Power = &agentproto.PowerSettings{}
		err = json.Unmarshal(c.Settings, it.Power)
	case agentproto.ConfigAdmins:
		it.Admins = &agentproto.AdminSettings{}
		err = json.Unmarshal(c.Settings, it.Admins)
	case agentproto.ConfigFirewall:
		it.Firewall = &agentproto.FirewallSettings{}
		err = json.Unmarshal(c.Settings, it.Firewall)
	default:
		err = errors.New("unknown configuration type")
	}
	return it, err
}

func (s *Server) configOnConnect(ctx context.Context, deviceID string) {
	s.pushConfigPolicies(ctx, []string{deviceID})
}

func (s *Server) handleConfigStatusMsg(ctx context.Context, c *agentConn, e *agentproto.Envelope) error {
	var st agentproto.ConfigStatus
	if err := e.Decode(&st); err != nil {
		return err
	}
	if len(st.Items) > 500 {
		st.Items = st.Items[:500]
	}
	at := s.now()
	items := make([]store.ConfigItemReport, 0, len(st.Items))
	for _, it := range st.Items {
		switch it.State {
		case agentproto.ConfigApplied, agentproto.ConfigFailed, agentproto.ConfigWaiting, agentproto.ConfigUnsupported:
		default:
			continue
		}
		r := store.ConfigItemReport{ConfigurationID: it.ID, Revision: it.Revision, State: it.State, Message: cleanOutput(it.Message, 1000),
			Repaired: max(min(it.Repaired, 10000), 0), AppliedAt: validTime(it.AppliedAt)}
		runs := it.Runs
		if len(runs) > 20 {
			runs = runs[len(runs)-20:]
		}
		for _, run := range runs {
			when := at
			if t := validTime(run.At); t != nil {
				when = *t
			}
			r.Runs = append(r.Runs, store.ConfigRun{At: when, User: cleanText(run.User, 255), ExitCode: run.ExitCode,
				Output: cleanOutput(run.Output, maxConfigOutput), Error: cleanText(run.Error, 1000), DurationMs: max(run.DurationMs, 0)})
		}
		items = append(items, r)
	}
	return s.st.SaveConfigReport(ctx, c.deviceID, items, at)
}

// ---- validation ----

var (
	extensionIDRE = regexp.MustCompile(`^[a-p]{32}$`)
	policyNameRE  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{1,99}$`)
	regSegmentRE  = regexp.MustCompile(`^[^\\/*?"<>|\x00-\x1f]{1,255}$`)
)

func validConfigURL(v string, schemes ...string) bool {
	u, err := url.Parse(v)
	if err != nil || len(v) > 2000 || u.Host == "" {
		return false
	}
	return slices.Contains(schemes, u.Scheme)
}

// validateConfig checks and normalises a configuration; it returns an error
// code for the API.
func validateConfig(c *store.Configuration) string {
	c.Name = cleanText(c.Name, 200)
	c.Description = cleanText(c.Description, 1000)
	switch {
	case c.Name == "":
		return "invalid_name"
	case !validPlatform(c.Platform):
		return "invalid_platform"
	case !agentproto.ValidConfigType(c.Platform, c.Type):
		return "invalid_config_type"
	case c.Priority < 0 || c.Priority > 1000:
		return "invalid_priority"
	case len(c.Settings) > maxConfigSettings:
		return "settings_too_large"
	}
	slices.Sort(c.ProfileIDs)
	c.ProfileIDs = slices.Compact(c.ProfileIDs)
	dec := json.NewDecoder(bytes.NewReader(c.Settings))
	dec.DisallowUnknownFields()
	var v any
	var bad string
	switch c.Type {
	case agentproto.ConfigBrowser:
		var p agentproto.BrowserPolicy
		if dec.Decode(&p) != nil {
			return "invalid_settings"
		}
		bad, v = validBrowserPolicy(&p), p
	case agentproto.ConfigRegistry:
		var p agentproto.RegistrySettings
		if dec.Decode(&p) != nil {
			return "invalid_settings"
		}
		bad, v = validRegistry(&p), p
	case agentproto.ConfigScript:
		var p agentproto.ScriptConfig
		if dec.Decode(&p) != nil {
			return "invalid_settings"
		}
		bad, v = validScript(&p, c.Platform), p
	case agentproto.ConfigDrives:
		var p agentproto.DriveSettings
		if dec.Decode(&p) != nil {
			return "invalid_settings"
		}
		bad, v = validDrives(&p), p
	case agentproto.ConfigPrinters:
		var p agentproto.PrinterSettings
		if dec.Decode(&p) != nil {
			return "invalid_settings"
		}
		bad, v = validPrinters(&p), p
	case agentproto.ConfigDesktop:
		var p agentproto.DesktopSettings
		if dec.Decode(&p) != nil {
			return "invalid_settings"
		}
		bad, v = validDesktop(&p), p
	case agentproto.ConfigPower:
		var p agentproto.PowerSettings
		if dec.Decode(&p) != nil {
			return "invalid_settings"
		}
		bad, v = validPower(&p), p
	case agentproto.ConfigAdmins:
		var p agentproto.AdminSettings
		if dec.Decode(&p) != nil {
			return "invalid_settings"
		}
		bad, v = validAdmins(&p), p
	case agentproto.ConfigFirewall:
		var p agentproto.FirewallSettings
		if dec.Decode(&p) != nil {
			return "invalid_settings"
		}
		bad, v = validFirewall(&p), p
	}
	if bad != "" {
		return bad
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "invalid_settings"
	}
	c.Settings = raw
	return ""
}

func cleanURLs(in []string, max int, schemes ...string) ([]string, bool) {
	out := []string{}
	for _, u := range in {
		if u = strings.TrimSpace(u); u == "" {
			continue
		}
		if !validConfigURL(u, schemes...) || len(out) >= max {
			return nil, false
		}
		out = append(out, u)
	}
	return out, true
}

func validBrowserPolicy(p *agentproto.BrowserPolicy) string {
	var bs []string
	for _, b := range p.Browsers {
		if !slices.Contains([]string{agentproto.BrowserChrome, agentproto.BrowserEdge, agentproto.BrowserFirefox}, b) {
			return "invalid_browser"
		}
		if !slices.Contains(bs, b) {
			bs = append(bs, b)
		}
	}
	if len(bs) == 0 {
		return "no_browsers"
	}
	p.Browsers = bs
	if p.Homepage = strings.TrimSpace(p.Homepage); p.Homepage != "" && !validConfigURL(p.Homepage, "http", "https") {
		return "invalid_url"
	}
	urls, ok := cleanURLs(p.StartupURLs, 20, "http", "https")
	if !ok {
		return "invalid_url"
	}
	p.StartupURLs = urls
	switch p.StartupMode {
	case agentproto.StartupDefault, agentproto.StartupNewTab, agentproto.StartupRestore:
		p.StartupURLs = nil
	case agentproto.StartupURLs:
		if len(p.StartupURLs) == 0 {
			return "startup_urls_required"
		}
	default:
		return "invalid_startup_mode"
	}
	clean := func(list []string, ok func(string) bool) ([]string, bool) {
		var out []string
		for _, e := range list {
			if e = strings.TrimSpace(e); e == "" {
				continue
			}
			if !ok(e) || len(out) >= 50 {
				return nil, false
			}
			out = append(out, e)
		}
		return out, true
	}
	chromium := func(e string) bool {
		id, update, hasURL := strings.Cut(e, ";")
		return extensionIDRE.MatchString(id) && (!hasURL || validConfigURL(update, "https"))
	}
	if p.ChromeExtensions, ok = clean(p.ChromeExtensions, chromium); !ok {
		return "invalid_extension"
	}
	if p.EdgeExtensions, ok = clean(p.EdgeExtensions, chromium); !ok {
		return "invalid_extension"
	}
	if p.FirefoxExtensions, ok = clean(p.FirefoxExtensions, func(e string) bool { return validConfigURL(e, "https") }); !ok {
		return "invalid_extension"
	}
	for b, pol := range p.Extra {
		if !slices.Contains(p.Browsers, b) {
			delete(p.Extra, b)
			continue
		}
		if len(pol) > 100 {
			return "invalid_policy_name"
		}
		for name := range pol {
			if !policyNameRE.MatchString(name) {
				return "invalid_policy_name"
			}
		}
		if len(pol) == 0 {
			delete(p.Extra, b)
		}
	}
	if len(p.Extra) == 0 {
		p.Extra = nil
	}
	if p.Homepage == "" && p.ShowHomeButton == nil && p.StartupMode == "" && p.PasswordManager == nil && len(p.ChromeExtensions) == 0 &&
		len(p.EdgeExtensions) == 0 && len(p.FirefoxExtensions) == 0 && len(p.Extra) == 0 {
		return "nothing_configured"
	}
	return ""
}

// normalizeRegKey accepts keys with the hive in front (HKEY_LOCAL_MACHINE\...,
// HKLM:\...) and forward slashes, and returns hive and key.
func normalizeRegKey(hive, key string) (string, string, bool) {
	key = strings.Trim(strings.ReplaceAll(strings.TrimSpace(key), "/", `\`), `\`)
	first, rest, _ := strings.Cut(key, `\`)
	switch strings.TrimSuffix(strings.ToUpper(first), ":") {
	case "HKLM", "HKEY_LOCAL_MACHINE":
		hive, key = agentproto.HiveMachine, rest
	case "HKCU", "HKEY_CURRENT_USER":
		hive, key = agentproto.HiveUser, rest
	case "HKU", "HKEY_USERS", "HKCR", "HKEY_CLASSES_ROOT", "HKCC", "HKEY_CURRENT_CONFIG":
		return "", "", false
	}
	if hive != agentproto.HiveMachine && hive != agentproto.HiveUser {
		return "", "", false
	}
	for strings.Contains(key, `\\`) {
		key = strings.ReplaceAll(key, `\\`, `\`)
	}
	if key == "" || len(key) > 1000 {
		return "", "", false
	}
	for _, seg := range strings.Split(key, `\`) {
		if !regSegmentRE.MatchString(seg) {
			return "", "", false
		}
	}
	return hive, key, true
}

func validRegistry(p *agentproto.RegistrySettings) string {
	if len(p.Values) == 0 {
		return "nothing_configured"
	}
	if len(p.Values) > 200 {
		return "too_many_values"
	}
	seen := map[string]bool{}
	for i := range p.Values {
		v := &p.Values[i]
		hive, key, ok := normalizeRegKey(v.Hive, v.Key)
		if !ok {
			return "invalid_registry_key"
		}
		v.Hive, v.Key = hive, key
		v.Name = strings.TrimSpace(v.Name)
		if len(v.Name) > 255 || strings.ContainsAny(v.Name, "\x00") || strings.Contains(v.Name, `\\`) {
			return "invalid_registry_name"
		}
		switch v.Type {
		case agentproto.RegString, agentproto.RegExpandString:
			if len(v.Value) > 4096 || strings.ContainsAny(v.Value, "\x00") {
				return "invalid_registry_value"
			}
		case agentproto.RegMultiString:
			if len(v.Value) > 16384 || strings.ContainsAny(v.Value, "\x00") {
				return "invalid_registry_value"
			}
		case agentproto.RegDWord, agentproto.RegQWord:
			bits := 32
			if v.Type == agentproto.RegQWord {
				bits = 64
			}
			v.Value = strings.TrimSpace(v.Value)
			n, err := strconv.ParseUint(v.Value, 0, bits)
			if err != nil {
				return "invalid_registry_value"
			}
			v.Value = strconv.FormatUint(n, 10)
		case agentproto.RegDelete:
			v.Value = ""
		default:
			return "invalid_registry_type"
		}
		k := strings.ToLower(v.Hive + `\` + v.Key + `\\` + v.Name)
		if seen[k] {
			return "duplicate_registry_value"
		}
		seen[k] = true
	}
	return ""
}

func validScript(p *agentproto.ScriptConfig, platform string) string {
	if !slices.Contains(agentproto.ScriptShells[platform], p.Shell) {
		return "invalid_shell"
	}
	p.Content = strings.ToValidUTF8(p.Content, "")
	if strings.TrimSpace(p.Content) == "" {
		return "script_required"
	}
	if len(p.Content) > maxScriptSize {
		return "script_too_large"
	}
	switch p.RunAs {
	case agentproto.RunAsSystem:
	case agentproto.RunAsUser:
		if platform != "windows" {
			return "invalid_run_as"
		}
	default:
		return "invalid_run_as"
	}
	switch p.Trigger {
	case agentproto.TriggerOnce, agentproto.TriggerStartup:
		p.IntervalMinutes = 0
	case agentproto.TriggerLogon:
		if platform != "windows" {
			return "invalid_trigger"
		}
		p.IntervalMinutes = 0
	case agentproto.TriggerInterval:
		if p.IntervalMinutes < 5 || p.IntervalMinutes > 7*24*60 {
			return "invalid_interval"
		}
	default:
		return "invalid_trigger"
	}
	if p.TimeoutMinutes == 0 {
		p.TimeoutMinutes = 10
	}
	if p.TimeoutMinutes < 1 || p.TimeoutMinutes > 120 {
		return "invalid_timeout"
	}
	return ""
}

var (
	// A share path: \\server\share\folder, where %USERNAME% and
	// %USERDOMAIN% may stand for parts.
	drivePathRE   = regexp.MustCompile(`^\\\\[A-Za-z0-9._%-]{1,253}(\\[^\\/:*?"<>|\x00-\x1f]+)+$`)
	printerPathRE = regexp.MustCompile(`^\\\\[A-Za-z0-9._-]{1,253}\\[^\\/,"\x00-\x1f]{1,200}$`)
	printerNameRE = regexp.MustCompile(`^[^\\/,"!\x00-\x1f]{1,100}$`)
	hostRE        = regexp.MustCompile(`^[A-Za-z0-9.:-]{1,253}$`)
	lprQueueRE    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	driverNameRE  = regexp.MustCompile(`^[^"\x00-\x1f]{1,200}$`)
)

func validDrives(p *agentproto.DriveSettings) string {
	if len(p.Drives) == 0 {
		return "nothing_configured"
	}
	if len(p.Drives) > 23 {
		return "duplicate_drive"
	}
	seen := map[string]bool{}
	for i := range p.Drives {
		d := &p.Drives[i]
		d.Letter = strings.ToUpper(strings.TrimRight(strings.TrimSpace(d.Letter), ":"))
		if len(d.Letter) != 1 || d.Letter[0] < 'D' || d.Letter[0] > 'Z' {
			return "invalid_drive_letter"
		}
		if seen[d.Letter] {
			return "duplicate_drive"
		}
		seen[d.Letter] = true
		d.Path = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(d.Path), "/", `\`), `\`)
		if !drivePathRE.MatchString(d.Path) || len(d.Path) > 260 {
			return "invalid_drive_path"
		}
		d.Label = cleanText(d.Label, 64)
	}
	return ""
}

func validPrinters(p *agentproto.PrinterSettings) string {
	if len(p.Printers) == 0 {
		return "nothing_configured"
	}
	if len(p.Printers) > 50 {
		return "too_many_printers"
	}
	seen := map[string]bool{}
	defaults := 0
	for i := range p.Printers {
		pr := &p.Printers[i]
		pr.Driver = strings.TrimSpace(pr.Driver)
		pr.DriverINF = strings.TrimSpace(pr.DriverINF)
		if pr.Driver != "" && !driverNameRE.MatchString(pr.Driver) {
			return "invalid_driver"
		}
		if pr.DriverINF != "" && (pr.Driver == "" || len(pr.DriverINF) > 260 || strings.ContainsAny(pr.DriverINF, "\"\x00\n\r") ||
			!strings.HasSuffix(strings.ToLower(pr.DriverINF), ".inf")) {
			return "invalid_driver_inf"
		}
		var key string
		switch pr.Kind {
		case agentproto.PrinterShared:
			pr.Path = strings.TrimSpace(strings.ReplaceAll(pr.Path, "/", `\`))
			if !printerPathRE.MatchString(pr.Path) {
				return "invalid_printer_path"
			}
			if pr.Scope == "" {
				pr.Scope = agentproto.PrinterDevice
			}
			if pr.Scope != agentproto.PrinterDevice && pr.Scope != agentproto.PrinterUser {
				return "invalid_printer_scope"
			}
			pr.Name, pr.Address, pr.Protocol, pr.Port, pr.Queue = "", "", "", 0, ""
			key = "shared:" + strings.ToLower(pr.Path)
		case agentproto.PrinterIP:
			pr.Name = strings.TrimSpace(pr.Name)
			if !printerNameRE.MatchString(pr.Name) {
				return "invalid_printer_name"
			}
			pr.Address = strings.TrimSpace(pr.Address)
			if !hostRE.MatchString(pr.Address) {
				return "invalid_printer_address"
			}
			if pr.Driver == "" {
				return "driver_required"
			}
			switch pr.Protocol {
			case "", agentproto.PrinterRaw:
				pr.Protocol, pr.Queue = agentproto.PrinterRaw, ""
				if pr.Port == 0 {
					pr.Port = 9100
				}
				if pr.Port < 1 || pr.Port > 65535 {
					return "invalid_printer_port"
				}
			case agentproto.PrinterLPR:
				pr.Port = 0
				pr.Queue = strings.TrimSpace(pr.Queue)
				if !lprQueueRE.MatchString(pr.Queue) {
					return "invalid_printer_queue"
				}
			default:
				return "invalid_printer_protocol"
			}
			pr.Path, pr.Scope = "", ""
			key = "ip:" + strings.ToLower(pr.Name)
		default:
			return "invalid_printer_kind"
		}
		if seen[key] {
			return "duplicate_printer"
		}
		seen[key] = true
		if pr.Default {
			defaults++
		}
	}
	if defaults > 1 {
		return "multiple_default_printers"
	}
	return ""
}

// ---- API ----

func (s *Server) configRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermConfigManage, 0, h) }
	mux.Handle("GET /api/v1/configurations", view(s.handleListConfigs))
	mux.Handle("POST /api/v1/configurations", manage(s.handleCreateConfig))
	mux.Handle("GET /api/v1/configurations/{id}", view(s.handleGetConfig))
	mux.Handle("PUT /api/v1/configurations/{id}", manage(s.handleUpdateConfig))
	mux.Handle("DELETE /api/v1/configurations/{id}", manage(s.handleDeleteConfig))
	mux.Handle("POST /api/v1/configurations/{id}/apply", manage(s.handleApplyConfig))
	mux.Handle("GET /api/v1/configurations/{id}/runs", view(s.handleConfigRuns))
	mux.Handle("GET /api/v1/devices/{id}/configurations", view(s.handleDeviceConfigs))
	mux.Handle("GET /api/v1/config-profiles", view(s.handleListProfiles))
	mux.Handle("POST /api/v1/config-profiles", manage(s.handleCreateProfile))
	mux.Handle("GET /api/v1/config-profiles/{id}", view(s.handleGetProfile))
	mux.Handle("PUT /api/v1/config-profiles/{id}", manage(s.handleUpdateProfile))
	mux.Handle("DELETE /api/v1/config-profiles/{id}", manage(s.handleDeleteProfile))
	mux.Handle("POST /api/v1/devices/{id}/configurations/apply", s.authed(auth.PermDevicesManage, 0, s.handleApplyDeviceConfigs))
}

type configTypesView struct {
	Types  map[string][]string `json:"types"`
	Shells map[string][]string `json:"shells"`
}

type configList struct {
	Items          []store.Configuration `json:"items"`
	ReapplyMinutes int                   `json:"reapplyMinutes"`
	configTypesView
}

// canSeeSettings reports whether the user may read the settings: scripts
// and registry values can hold secrets.
func canSeeSettings(r *http.Request) bool {
	u, _ := currentUser(r)
	return auth.Role(u.Role).Has(auth.PermConfigManage)
}

func hideSettings(c *store.Configuration) { c.Settings = json.RawMessage("null") }

func (s *Server) handleListConfigs(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListConfigurations(r.Context(), s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !canSeeSettings(r) {
		for i := range list {
			hideSettings(&list[i])
		}
	}
	writeJSON(w, http.StatusOK, configList{Items: list, ReapplyMinutes: agentproto.DefaultReapplyMinutes,
		configTypesView: configTypesView{Types: agentproto.ConfigTypes, Shells: agentproto.ScriptShells}})
}

type configReq struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Type        string          `json:"type"`
	Platform    string          `json:"platform"`
	Settings    json.RawMessage `json:"settings"`
	Enabled     *bool           `json:"enabled"`
	// ProfileIDs are the profiles the configuration is in; left out, they
	// stay as they are.
	ProfileIDs *[]int64 `json:"profileIds"`
	Priority   *int     `json:"priority"`
}

func (s *Server) configFromReq(w http.ResponseWriter, r *http.Request, req configReq, c *store.Configuration) bool {
	c.Name, c.Description, c.Settings = req.Name, req.Description, req.Settings
	if req.ProfileIDs != nil {
		c.ProfileIDs = *req.ProfileIDs
	}
	if req.Enabled != nil {
		c.Enabled = *req.Enabled
	}
	if req.Priority != nil {
		c.Priority = *req.Priority
	}
	if bad := validateConfig(c); bad != "" {
		apiError(w, http.StatusBadRequest, bad)
		return false
	}
	for _, id := range c.ProfileIDs {
		if _, err := s.st.ConfigProfileByID(r.Context(), id, store.Scope{All: true}); err != nil {
			apiError(w, http.StatusBadRequest, "unknown_profile")
			return false
		}
	}
	return true
}

func (s *Server) handleCreateConfig(w http.ResponseWriter, r *http.Request) {
	var req configReq
	if !decode(w, r, &req) {
		return
	}
	c := store.Configuration{Type: req.Type, Platform: req.Platform, Enabled: true, Priority: 100}
	if !s.configFromReq(w, r, req, &c) {
		return
	}
	u, _ := currentUser(r)
	c.CreatedBy = u.Username
	c, err := s.st.CreateConfiguration(r.Context(), c)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "configuration_exists")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "config.create", c.Name, true, map[string]any{"type": c.Type, "platform": c.Platform, "profiles": c.ProfileIDs})
	s.pushConfigsSoon()
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) loadConfig(w http.ResponseWriter, r *http.Request) (store.Configuration, bool) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return store.Configuration{}, false
	}
	c, err := s.st.ConfigurationByID(r.Context(), id, s.scope(r))
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return c, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return c, false
	}
	return c, true
}

type configView struct {
	Configuration store.Configuration       `json:"configuration"`
	Devices       []store.DeviceConfigState `json:"devices"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadConfig(w, r)
	if !ok {
		return
	}
	devs, err := s.st.ConfigurationDevices(r.Context(), c.ID, s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for i := range devs {
		devs[i].Online = s.hub.online(devs[i].DeviceID)
		if !c.Enabled {
			devs[i].State = "disabled"
		}
	}
	if !canSeeSettings(r) {
		hideSettings(&c)
	}
	writeJSON(w, http.StatusOK, configView{Configuration: c, Devices: devs})
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadConfig(w, r)
	if !ok {
		return
	}
	var req configReq
	if !decode(w, r, &req) {
		return
	}
	if (req.Type != "" && req.Type != c.Type) || (req.Platform != "" && req.Platform != c.Platform) {
		apiError(w, http.StatusBadRequest, "type_cannot_change")
		return
	}
	if !s.configFromReq(w, r, req, &c) {
		return
	}
	u, _ := currentUser(r)
	c.UpdatedBy = u.Username
	c, err := s.st.UpdateConfiguration(r.Context(), c)
	switch {
	case errors.Is(err, store.ErrConflict):
		apiError(w, http.StatusConflict, "configuration_exists")
		return
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "config.update", c.Name, true, map[string]any{"id": c.ID, "revision": c.Revision, "enabled": c.Enabled, "profiles": c.ProfileIDs})
	s.pushConfigsSoon()
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleDeleteConfig(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadConfig(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteConfiguration(r.Context(), c.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "config.delete", c.Name, true, map[string]any{"id": c.ID})
	s.pushConfigsSoon()
	w.WriteHeader(http.StatusNoContent)
}

// pushConfigsSoon sends changed policies to online agents right away
// instead of on the next scheduler tick.
func (s *Server) pushConfigsSoon() {
	if s.bgCtx == nil {
		return
	}
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		ctx, cancel := context.WithTimeout(s.bgCtx, time.Minute)
		defer cancel()
		s.configTick(ctx)
	}()
}

type applyResult struct {
	Sent int `json:"sent"`
}

func (s *Server) handleApplyConfig(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadConfig(w, r)
	if !ok {
		return
	}
	devs, err := s.st.ConfigurationDevices(r.Context(), c.ID, s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ids := make([]string, 0, len(devs))
	for _, d := range devs {
		ids = append(ids, d.DeviceID)
	}
	// The newest policy first, so the agent applies what is configured now.
	s.pushConfigPolicies(r.Context(), ids)
	sent := 0
	for _, id := range ids {
		if s.hub.send(id, agentproto.TypeConfigApply, nil) {
			sent++
		}
	}
	s.audit(r, "config.apply", c.Name, true, map[string]any{"id": c.ID, "devices": sent})
	writeJSON(w, http.StatusOK, applyResult{Sent: sent})
}

func (s *Server) handleConfigRuns(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadConfig(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.st.ConfigurationRuns(r.Context(), store.ConfigRunQuery{Scope: s.scope(r), ConfigurationID: c.ID,
		DeviceID: cleanText(r.URL.Query().Get("device"), 64), Limit: limit})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

type deviceConfigsView struct {
	Items []store.DeviceConfigState `json:"items"`
	Runs  []store.ConfigRun         `json:"runs"`
}

func (s *Server) handleDeviceConfigs(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	items, err := s.st.DeviceConfigurations(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	runs, err := s.st.ConfigurationRuns(r.Context(), store.ConfigRunQuery{Scope: store.Scope{All: true}, DeviceID: d.ID, Limit: 50})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, deviceConfigsView{Items: items, Runs: runs})
}

func (s *Server) handleApplyDeviceConfigs(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	if !d.Online {
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	s.pushConfigPolicies(r.Context(), []string{d.ID})
	if !s.hub.send(d.ID, agentproto.TypeConfigApply, nil) {
		apiError(w, http.StatusConflict, "device_offline")
		return
	}
	s.audit(r, "config.apply", d.Hostname, true, map[string]any{"deviceId": d.ID})
	writeJSON(w, http.StatusOK, applyResult{Sent: 1})
}
