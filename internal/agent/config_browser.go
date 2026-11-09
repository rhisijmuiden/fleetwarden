package agent

import (
	"strings"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// Default update URLs of the extension stores, for force-installed
// extensions given by ID only.
const (
	chromeStoreUpdateURL = "https://clients2.google.com/service/update2/crx"
	edgeStoreUpdateURL   = "https://edge.microsoft.com/extensionwebstorebase/v1/crx"
)

// browserPolicies turns the browser configurations into the policies of each
// browser, by their documented names. Later configurations win when two set
// the same policy. It returns, per browser, the IDs of the configurations
// that contribute.
func browserPolicies(items []agentproto.ConfigItem) (map[string]map[string]any, map[string][]int64) {
	out := map[string]map[string]any{}
	from := map[string][]int64{}
	for _, it := range items {
		if it.Type != agentproto.ConfigBrowser || it.Browser == nil {
			continue
		}
		for _, b := range it.Browser.Browsers {
			var p map[string]any
			switch b {
			case agentproto.BrowserChrome:
				p = chromiumPolicies(*it.Browser, it.Browser.ChromeExtensions, chromeStoreUpdateURL)
			case agentproto.BrowserEdge:
				p = chromiumPolicies(*it.Browser, it.Browser.EdgeExtensions, edgeStoreUpdateURL)
			case agentproto.BrowserFirefox:
				p = firefoxPolicies(*it.Browser)
			default:
				continue
			}
			for k, v := range it.Browser.Extra[b] {
				p[k] = v
			}
			if out[b] == nil {
				out[b] = map[string]any{}
			}
			for k, v := range p {
				out[b][k] = v
			}
			from[b] = append(from[b], it.ID)
		}
	}
	return out, from
}

// chromiumPolicies are the policies for Chrome and Edge, which share names.
func chromiumPolicies(p agentproto.BrowserPolicy, exts []string, storeURL string) map[string]any {
	out := map[string]any{}
	if p.Homepage != "" {
		out["HomepageLocation"] = p.Homepage
		out["HomepageIsNewTabPage"] = false
	}
	if p.ShowHomeButton != nil {
		out["ShowHomeButton"] = *p.ShowHomeButton
	}
	switch p.StartupMode {
	case agentproto.StartupRestore:
		out["RestoreOnStartup"] = 1
	case agentproto.StartupURLs:
		out["RestoreOnStartup"] = 4
		out["RestoreOnStartupURLs"] = toAny(p.StartupURLs)
	case agentproto.StartupNewTab:
		out["RestoreOnStartup"] = 5
	}
	if p.PasswordManager != nil {
		out["PasswordManagerEnabled"] = *p.PasswordManager
	}
	if len(exts) > 0 {
		list := make([]any, 0, len(exts))
		for _, e := range exts {
			if !strings.Contains(e, ";") {
				e += ";" + storeURL
			}
			list = append(list, e)
		}
		out["ExtensionInstallForcelist"] = list
	}
	return out
}

// firefoxPolicies follow Mozilla's policy templates (policies.json, or the
// same structure under the registry key on Windows).
func firefoxPolicies(p agentproto.BrowserPolicy) map[string]any {
	out := map[string]any{}
	home := map[string]any{}
	if p.Homepage != "" {
		home["URL"] = p.Homepage
	}
	switch p.StartupMode {
	case agentproto.StartupRestore:
		home["StartPage"] = "previous-session"
	case agentproto.StartupNewTab:
		home["StartPage"] = "none"
	case agentproto.StartupURLs:
		home["StartPage"] = "homepage"
		urls := p.StartupURLs
		if p.Homepage == "" && len(urls) > 0 {
			home["URL"], urls = urls[0], urls[1:]
		}
		var extra []any
		for _, u := range urls {
			if u != p.Homepage {
				extra = append(extra, u)
			}
		}
		if len(extra) > 0 {
			home["Additional"] = extra
		}
	}
	if len(home) > 0 {
		out["Homepage"] = home
	}
	if p.PasswordManager != nil {
		out["PasswordManagerEnabled"] = *p.PasswordManager
	}
	if len(p.FirefoxExtensions) > 0 {
		out["Extensions"] = map[string]any{"Install": toAny(p.FirefoxExtensions)}
	}
	return out
}

func toAny(v []string) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = s
	}
	return out
}
