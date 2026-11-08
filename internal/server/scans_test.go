package server

import (
	"context"
	"testing"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

func TestScanSettingsAndGroupOverride(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.signedInAdmin()

	var got scanSettings
	if code := admin.do("GET", "/api/v1/settings/scans", nil, &got); code != 200 || got.InventoryMinutes != 360 || got.PatchScanMinutes != 360 {
		t.Fatalf("defaults: %d %+v", code, got)
	}
	for _, bad := range []scanSettings{{InventoryMinutes: 5, PatchScanMinutes: 360}, {InventoryMinutes: 60, PatchScanMinutes: 30}, {InventoryMinutes: 60, PatchScanMinutes: 20000}} {
		if code := admin.do("PUT", "/api/v1/settings/scans", bad, nil); code != 400 {
			t.Errorf("%+v accepted: %d", bad, code)
		}
	}
	if code := admin.do("PUT", "/api/v1/settings/scans", scanSettings{InventoryMinutes: 10080, PatchScanMinutes: 1440}, &got); code != 200 || got.InventoryMinutes != 10080 {
		t.Fatalf("save: %d %+v", code, got)
	}
	if code := admin.do("PUT", "/api/v1/groups/1", map[string]any{"name": "Default", "updateRing": 1, "inventoryMinutes": 10}, nil); code != 400 {
		t.Errorf("too short group override: %d", code)
	}

	// An agent online with the default gets the group override pushed, and
	// starts sending inventory at the new pace.
	_, info := e.createToken(admin, 1)
	a := newTestAgent(t, e, info.Token, "PC-SCAN", false)
	a.Minute = 10 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go a.Run(ctx) //nolint:errcheck
	d := e.onlineDevice(admin, "PC-SCAN")
	inventoryAt := func() time.Time {
		dev, err := e.srv.st.DeviceByID(ctx, d.ID)
		if err != nil || dev.InventoryAt == nil {
			return time.Time{}
		}
		return *dev.InventoryAt
	}
	waitFor(t, "first inventory", func() bool { return !inventoryAt().IsZero() })

	var g store.DeviceGroup
	if code := admin.do("PUT", "/api/v1/groups/1", map[string]any{"name": "Default", "updateRing": 1, "inventoryMinutes": 15, "patchScanMinutes": 60}, &g); code != 200 ||
		g.InventoryMinutes == nil || *g.InventoryMinutes != 15 || *g.PatchScanMinutes != 60 {
		t.Fatalf("group override: %d %+v", code, g)
	}
	e.srv.patchTick(ctx)
	first := inventoryAt()
	waitFor(t, "inventory at the new interval", func() bool { return inventoryAt().After(first) })

	// Clearing the override falls back to the default.
	admin.do("PUT", "/api/v1/groups/1", map[string]any{"name": "Default", "updateRing": 1}, &g)
	if g.InventoryMinutes != nil || g.PatchScanMinutes != nil {
		t.Errorf("override not cleared: %+v", g)
	}
	pd, _ := e.srv.st.PatchDevices(ctx, []string{d.ID})
	if c := e.srv.loadScanSettings(ctx).effective(pd[0].InventoryMinutes, pd[0].PatchScanMinutes); c.InventoryMinutes != 10080 || c.PatchScanMinutes != 1440 {
		t.Errorf("effective: %+v", c)
	}
}
