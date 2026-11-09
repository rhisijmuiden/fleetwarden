package agent

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

const fwupdUpdatesJSON = `{
  "Devices" : [
    {
      "Name" : "System Firmware",
      "DeviceId" : "a45df35ac0e948ee180fe216a5f703f32dda163f",
      "Guid" : [ "230c8b18-8d9b-53ec-838b-6cfc0383493a" ],
      "Plugin" : "uefi_capsule",
      "Flags" : [ "internal", "updatable", "require-ac", "supported", "registered", "needs-reboot-after" ],
      "Vendor" : "LENOVO",
      "Version" : "0.1.42",
      "Releases" : [
        {
          "AppstreamId" : "com.lenovo.ThinkPadN2HET.firmware",
          "Name" : "ThinkPad T14s System Update",
          "Summary" : "Lenovo ThinkPad T14s System Firmware",
          "Description" : "<p>Lenovo ThinkPad T14s System Firmware</p><ul><li>Fixed a sleep issue.</li></ul>",
          "Version" : "0.1.45",
          "Filename" : "2b5d9cb3e3b8d1e0b2b0b39b6d6f8a3c-Lenovo-N2HET45W.cab",
          "Checksum" : [ "1d8e1b5c5bd3c1e5a4f4e3d2c1b0a9f8e7d6c5b4", "7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069" ],
          "Size" : 8650752,
          "Created" : 1730000000,
          "Locations" : [ "https://fwupd.org/downloads/2b5d9cb3e3b8d1e0b2b0b39b6d6f8a3c-Lenovo-N2HET45W.cab" ],
          "Vendor" : "Lenovo Ltd.",
          "Flags" : [ "is-upgrade" ]
        },
        { "Version" : "0.1.44", "Uri" : "https://fwupd.org/downloads/old.cab" }
      ]
    },
    { "Name" : "No releases", "DeviceId" : "b1", "Releases" : [] },
    {
      "Name" : "USB Receiver",
      "DeviceId" : "c2",
      "Plugin" : "logitech_hidpp",
      "Version" : "RQR12.07",
      "Releases" : [ { "Version" : "RQR12.10", "Uri" : "https://fwupd.org/downloads/receiver.cab", "Checksum" : [ "0123456789012345678901234567890123456789" ] } ]
    }
  ]
}`

func TestParseFwupdUpdates(t *testing.T) {
	list, err := parseFwupdUpdates([]byte(fwupdUpdatesJSON))
	if err != nil || len(list) != 2 {
		t.Fatalf("parse: %v %+v", err, list)
	}
	fw := list[0]
	if fw.Source != "fwupd" || fw.Kind != "firmware" || fw.SourceID != "7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069" ||
		fw.Version != "0.1.45" || fw.CurrentVersion != "0.1.42" || fw.DeviceID != "a45df35ac0e948ee180fe216a5f703f32dda163f" ||
		fw.Vendor != "Lenovo Ltd." || !fw.RebootRequired || fw.ReleasedAt.Year() != 2024 || fw.HardwareID == "" {
		t.Errorf("firmware: %+v", fw)
	}
	if fw.Title != "ThinkPad T14s System Update 0.1.45" || !strings.Contains(fw.Description, "- Fixed a sleep issue.") {
		t.Errorf("title/description: %q %q", fw.Title, fw.Description)
	}
	f := fw.Files[0]
	if f.Name != "2b5d9cb3e3b8d1e0b2b0b39b6d6f8a3c-Lenovo-N2HET45W.cab" || f.Size != 8650752 || f.SHA1 == "" || f.SHA256 == "" ||
		!strings.HasPrefix(f.URL, "https://fwupd.org/downloads/") {
		t.Errorf("file: %+v", f)
	}
	rx := list[1]
	if rx.SourceID != "0123456789012345678901234567890123456789" || rx.Files[0].Name != "receiver.cab" || rx.RebootRequired ||
		rx.Title != "USB Receiver RQR12.10" {
		t.Errorf("receiver: %+v", rx)
	}
}

func TestFwupdBackend(t *testing.T) {
	var calls [][]string
	needsReboot := false
	b := fwupdBackend{Run: func(_ context.Context, args ...string) ([]byte, int, error) {
		calls = append(calls, args)
		switch args[0] {
		case "refresh":
			return []byte("Metadata is up to date; use --force to refresh again."), 2, nil
		case "get-updates":
			return []byte("WARNING: some plugin warning\n" + fwupdUpdatesJSON), 0, nil
		case "install":
			needsReboot = true
			return []byte("Decompressing…\nScheduling…\nSuccessfully installed firmware\n"), 0, nil
		case "get-devices":
			if needsReboot {
				return []byte(`{"Devices":[{"Name":"System Firmware","Flags":["needs-reboot"]}]}`), 0, nil
			}
			return []byte(`{"Devices":[]}`), 0, nil
		}
		return nil, 1, nil
	}}
	list, err := b.Scan(context.Background())
	if err != nil || len(list) != 2 {
		t.Fatalf("scan: %v %d", err, len(list))
	}
	reboot, note, err := b.Install(context.Background(), agentproto.DriverInstall{DeviceID: "a45d"}, []string{"/tmp/x/fw.cab"})
	if err != nil || !reboot || note != "Successfully installed firmware" {
		t.Errorf("install: %v %v %q", reboot, note, err)
	}
	if !slices.Equal(calls[2], []string{"install", "--assume-yes", "--no-reboot-check", "/tmp/x/fw.cab", "a45d"}) {
		t.Errorf("install args: %v", calls[2])
	}

	// Nothing to update: fwupd answers with exit code 2 and no JSON.
	b.Run = func(_ context.Context, args ...string) ([]byte, int, error) {
		if args[0] == "get-updates" {
			return []byte("No updatable devices"), 2, nil
		}
		return nil, 0, nil
	}
	if list, err := b.Scan(context.Background()); err != nil || len(list) != 0 || list == nil {
		t.Errorf("no updates: %v %v", list, err)
	}
	// Without fwupd there is nothing to report, and no error.
	b.Run = func(context.Context, ...string) ([]byte, int, error) { return nil, -1, errFwupdMissing }
	if list, err := b.Scan(context.Background()); err != nil || len(list) != 0 {
		t.Errorf("no fwupd: %v %v", list, err)
	}
	// A failed install reports fwupd's last line.
	b.Run = func(_ context.Context, args ...string) ([]byte, int, error) {
		return []byte("Decompressing…\nfirmware version not supported\n"), 1, nil
	}
	if _, _, err := b.Install(context.Background(), agentproto.DriverInstall{}, []string{"a.cab"}); err == nil ||
		err.Error() != "fwupd: firmware version not supported" {
		t.Errorf("failed install: %v", err)
	}
}

func TestPnputilOutcome(t *testing.T) {
	for _, c := range []struct {
		code   int
		out    string
		reboot bool
		ok     bool
	}{
		{0, "Driver package added successfully.\nDriver package installed on matching devices.", false, true},
		{0, "Driver package installed on device.\nSystem reboot is needed to complete install operations!", true, true},
		{3010, "", true, true},
		{259, "Driver package is up-to-date on device.", false, true},
		{1, "Failed to add driver package: The hash for the file is not present in the specified catalog file.", false, false},
	} {
		reboot, _, err := pnputilOutcome(c.code, c.out)
		if reboot != c.reboot || (err == nil) != c.ok {
			t.Errorf("code %d: reboot %v err %v", c.code, reboot, err)
		}
	}
}

func TestVersionInTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Intel - Display - 31.0.101.5590":                        "31.0.101.5590",
		"Realtek Semiconductor Corp. - Extension - 10.0.22000.1": "10.0.22000.1",
		"Lenovo - Firmware - 1.48.0.0":                           "1.48.0.0",
		"Dell, Inc. - Firmware - 1.21":                           "1.21",
		"Microsoft - Other hardware - Surface Driver":            "",
	} {
		if got := versionInTitle(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestValidDriverJob(t *testing.T) {
	ok := agentproto.DriverInstall{ID: "driver:12", Files: []agentproto.RepoFile{{Name: "x.cab", SHA256: strings.Repeat("a", 64), Size: 3}}}
	if !validDriverJob(ok) {
		t.Error("valid job rejected")
	}
	for _, bad := range []agentproto.DriverInstall{
		{ID: "driver:12"},
		{ID: "driver:12", Files: []agentproto.RepoFile{{Name: "../x.cab", SHA256: strings.Repeat("a", 64)}}},
		{ID: "driver:12", Files: []agentproto.RepoFile{{Name: "x.cab", SHA256: "abc"}}},
		{ID: "../x", Files: ok.Files},
	} {
		if validDriverJob(bad) {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestPackageCommand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	exe, args, err := packageCommand(dir, `"HpFirmwareUpdRec.exe" -b -s`)
	if err != nil || exe != filepath.Join(dir, "HpFirmwareUpdRec.exe") || len(args) != 2 || args[1] != "-s" {
		t.Errorf("hp: %s %v %v", exe, args, err)
	}
	exe, args, err = packageCommand(dir, `%PACKAGEPATH%`+string(filepath.Separator)+`winuptp.exe -s`)
	if err != nil || exe != filepath.Join(dir, "winuptp.exe") || len(args) != 1 {
		t.Errorf("lenovo: %s %v %v", exe, args, err)
	}
	for _, bad := range []string{"", `..` + string(filepath.Separator) + `evil.exe`, filepath.Join(t.TempDir(), "other.exe")} {
		if _, _, err := packageCommand(dir, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestInstallerOutcomes(t *testing.T) {
	if r, _, err := dellOutcome(2); !r || err != nil {
		t.Error("dell 2")
	}
	if r, _, err := dellOutcome(0); r || err != nil {
		t.Error("dell 0")
	}
	if _, _, err := dellOutcome(7); err == nil || !strings.Contains(err.Error(), "password") {
		t.Errorf("dell 7: %v", err)
	}
	if _, _, err := exitOutcome(1, []int{0, 1}); err != nil {
		t.Error("lenovo 1")
	}
	if _, _, err := exitOutcome(1, nil); err == nil {
		t.Error("exe 1")
	}
	if r, _, err := exitOutcome(3010, []int{0}); !r || err != nil {
		t.Error("3010")
	}
}

func TestValidDriverJobInstaller(t *testing.T) {
	f := []agentproto.RepoFile{{Name: "a.exe", SHA256: strings.Repeat("a", 64), Size: 1}}
	if !validDriverJob(agentproto.DriverInstall{ID: "driver:1", Files: f, Installer: agentproto.InstallerDell}) {
		t.Error("dell job refused")
	}
	if validDriverJob(agentproto.DriverInstall{ID: "driver:1", Files: f, Installer: "shell"}) {
		t.Error("unknown installer accepted")
	}
	if validDriverJob(agentproto.DriverInstall{ID: "driver:1", Files: append(f, f...), Installer: agentproto.InstallerHP}) {
		t.Error("two files for an HP SoftPaq accepted")
	}
}
