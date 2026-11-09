package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func TestValidateDrivesAndPrinters(t *testing.T) {
	check := func(typ, settings, want string) store.Configuration {
		t.Helper()
		c := store.Configuration{Name: "x", Platform: "windows", Type: typ, Settings: json.RawMessage(settings)}
		if got := validateConfig(&c); got != want {
			t.Errorf("%s %s: %q, want %q", typ, settings, got, want)
		}
		return c
	}
	c := check("drives", `{"drives":[{"letter":"h:","path":" //srv/home/%USERNAME%/ ","label":"Home"}]}`, "")
	if !strings.Contains(string(c.Settings), `"letter":"H"`) || !strings.Contains(string(c.Settings), `"path":"\\\\srv\\home\\%USERNAME%"`) {
		t.Errorf("normalised: %s", c.Settings)
	}
	check("drives", `{"drives":[]}`, "nothing_configured")
	check("drives", `{"drives":[{"letter":"C","path":"\\\\srv\\x"}]}`, "invalid_drive_letter")
	check("drives", `{"drives":[{"letter":"H","path":"\\\\srv"}]}`, "invalid_drive_path")
	check("drives", `{"drives":[{"letter":"H","path":"C:\\data"}]}`, "invalid_drive_path")
	check("drives", `{"drives":[{"letter":"H","path":"\\\\srv\\a"},{"letter":"h","path":"\\\\srv\\b"}]}`, "duplicate_drive")
	check("drives", `{"drives":[{"letter":"H","path":"\\\\srv\\a"}]}`, "")

	c = check("printers", `{"printers":[{"kind":"ip","name":"Floor 2","address":"10.0.0.20","driver":"Microsoft IPP Class Driver","default":true},
		{"kind":"shared","path":"\\\\srv\\Reception"}]}`, "")
	if !strings.Contains(string(c.Settings), `"port":9100`) || !strings.Contains(string(c.Settings), `"scope":"device"`) {
		t.Errorf("printer defaults: %s", c.Settings)
	}
	check("printers", `{"printers":[{"kind":"ip","name":"P","address":"10.0.0.20"}]}`, "driver_required")
	check("printers", `{"printers":[{"kind":"ip","name":"P","address":"10.0.0.20 ; rm","driver":"d"}]}`, "invalid_printer_address")
	check("printers", `{"printers":[{"kind":"ip","name":"P","address":"h","driver":"d","protocol":"lpr"}]}`, "invalid_printer_queue")
	check("printers", `{"printers":[{"kind":"ip","name":"P","address":"h","driver":"d","driverInf":"\\\\srv\\drv\\x.exe"}]}`, "invalid_driver_inf")
	check("printers", `{"printers":[{"kind":"shared","path":"\\\\srv\\a,b"}]}`, "invalid_printer_path")
	check("printers", `{"printers":[{"kind":"shared","path":"\\\\srv\\a","default":true},{"kind":"shared","path":"\\\\srv\\b","default":true}]}`, "multiple_default_printers")
	check("printers", `{"printers":[{"kind":"shared","path":"\\\\srv\\a"},{"kind":"shared","path":"\\\\SRV\\A"}]}`, "duplicate_printer")
	c = store.Configuration{Name: "x", Platform: "linux", Type: "drives", Settings: json.RawMessage(`{"drives":[{"letter":"H","path":"\\\\srv\\a"}]}`)}
	if got := validateConfig(&c); got != "invalid_config_type" {
		t.Errorf("drives on Linux: %q", got)
	}
}

func TestValidateWindowsSettings(t *testing.T) {
	check := func(typ, settings, want string) store.Configuration {
		t.Helper()
		c := store.Configuration{Name: "x", Platform: "windows", Type: typ, Settings: json.RawMessage(settings)}
		if got := validateConfig(&c); got != want {
			t.Errorf("%s %s: %q, want %q", typ, settings, got, want)
		}
		return c
	}
	c := check("desktop", `{"wallpaper":"//srv/img/bg.jpg","lockMinutes":10}`, "")
	if !strings.Contains(string(c.Settings), `"wallpaperStyle":"fill"`) {
		t.Errorf("default style: %s", c.Settings)
	}
	check("desktop", `{}`, "nothing_configured")
	check("desktop", `{"wallpaper":"C:\\img\\bg.exe"}`, "invalid_wallpaper")
	check("desktop", `{"wallpaper":"bg.jpg"}`, "invalid_wallpaper")
	check("desktop", `{"wallpaper":"C:\\img\\bg.png","wallpaperStyle":"zoom"}`, "invalid_wallpaper_style")
	check("desktop", `{"lockMinutes":100000}`, "invalid_lock_minutes")

	check("power", `{"monitorAc":10,"sleepAc":0}`, "")
	check("power", `{}`, "nothing_configured")
	check("power", `{"sleepDc":-1}`, "invalid_power_minutes")

	c = check("admins", `{"members":[" CONTOSO/IT Support ","AzureAD\\anna@contoso.com","S-1-5-21-1-2-3-500",""]}`, "")
	if !strings.Contains(string(c.Settings), `"CONTOSO\\IT Support"`) {
		t.Errorf("admins: %s", c.Settings)
	}
	check("admins", `{"members":[]}`, "nothing_configured")
	check("admins", `{"members":["a","A"]}`, "duplicate_member")
	check("admins", `{"members":["bad\"name"]}`, "invalid_member")

	c = check("firewall", `{"rules":[{"name":"Web","direction":"in","action":"allow","protocol":"tcp","localPorts":"80, 443,8000-8080",
		"remoteAddresses":"192.168.1.0/24, localsubnet,10.0.0.1-10.0.0.9","profiles":["public","private","domain"]}]}`, "")
	if s := string(c.Settings); !strings.Contains(s, `"localPorts":"80,443,8000-8080"`) || !strings.Contains(s, `LocalSubnet`) || strings.Contains(s, "profiles") {
		t.Errorf("firewall: %s", s)
	}
	check("firewall", `{"rules":[{"name":"x","direction":"up","action":"allow"}]}`, "invalid_direction")
	check("firewall", `{"rules":[{"name":"x","direction":"in","action":"allow","localPorts":"80"}]}`, "ports_need_tcp_udp")
	check("firewall", `{"rules":[{"name":"x","direction":"in","action":"allow","protocol":"tcp","localPorts":"70000"}]}`, "invalid_ports")
	check("firewall", `{"rules":[{"name":"x","direction":"in","action":"allow","remoteAddresses":"10.0.0.300"}]}`, "invalid_addresses")
	check("firewall", `{"rules":[{"name":"x","direction":"in","action":"allow","program":"notepad"}]}`, "invalid_program")
	check("firewall", `{"rules":[{"name":"x","direction":"in","action":"block","profiles":["home"]}]}`, "invalid_profile")
	check("firewall", `{"rules":[{"name":"x","direction":"in","action":"block"},{"name":"X","direction":"out","action":"block"}]}`, "duplicate_rule")
}
