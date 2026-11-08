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
