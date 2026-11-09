package agent

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

// TestPowerPrepareWake turns Fast Startup off and wake on magic packet on,
// and puts Fast Startup back afterwards (it needs administrator rights,
// which the CI runner has).
func TestPowerPrepareWake(t *testing.T) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, sessionPowerKey, registry.QUERY_VALUE)
	if err != nil {
		t.Skip("no power key:", err)
	}
	before, _, beforeErr := k.GetIntegerValue(hiberbootValue)
	k.Close()

	be := windowsPower{}
	if err := be.PrepareWake(true); err != nil {
		t.Fatal(err)
	}
	k, _ = registry.OpenKey(registry.LOCAL_MACHINE, sessionPowerKey, registry.QUERY_VALUE)
	v, _, err := k.GetIntegerValue(hiberbootValue)
	saved, _, serr := k.GetIntegerValue(hiberbootSaved)
	k.Close()
	if err != nil || v != 0 || serr != nil {
		t.Fatalf("fast startup: %d %v, saved %d %v", v, err, saved, serr)
	}
	// Applying it again keeps the first saved value.
	if err := be.PrepareWake(true); err != nil {
		t.Fatal(err)
	}
	if err := be.PrepareWake(false); err != nil {
		t.Fatal(err)
	}
	k, _ = registry.OpenKey(registry.LOCAL_MACHINE, sessionPowerKey, registry.QUERY_VALUE)
	defer k.Close()
	after, _, _ := k.GetIntegerValue(hiberbootValue)
	if _, _, err := k.GetIntegerValue(hiberbootSaved); err == nil {
		t.Error("saved value left behind")
	}
	want := before
	if beforeErr != nil {
		want = 1
	}
	if after != want {
		t.Errorf("fast startup restored to %d, was %d", after, want)
	}
}
