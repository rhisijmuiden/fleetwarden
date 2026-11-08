//go:build windows

package agent

// Windows Update backend: the Windows Update Agent API (WUA) through COM. It
// uses the update source Windows is configured for (Microsoft Update, or a
// WSUS server set by policy).

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows/registry"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type wuaBackend struct{}

func newPlatformPatchBackend() PatchBackend { return wuaBackend{} }

// withCOM runs fn on a locked thread with COM initialised. WUA calls block,
// so they run in their own goroutine; ctx only stops the wait.
func withCOM(ctx context.Context, fn func() error) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
			var oe *ole.OleError
			// S_FALSE: already initialised on this thread.
			if !errors.As(err, &oe) || oe.Code() != 1 {
				done <- fmt.Errorf("COM: %w", err)
				return
			}
		}
		defer ole.CoUninitialize()
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("windows update: %v", r)
			}
		}()
		done <- fn()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func createDispatch(progID string) (*ole.IDispatch, error) {
	unk, err := oleutil.CreateObject(progID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", progID, err)
	}
	defer unk.Release()
	return unk.QueryInterface(ole.IID_IDispatch)
}

func dispProp(d *ole.IDispatch, name string, args ...any) (*ole.IDispatch, error) {
	v, err := oleutil.GetProperty(d, name, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if v.VT != ole.VT_DISPATCH || v.ToIDispatch() == nil {
		return nil, fmt.Errorf("%s: no object", name)
	}
	return v.ToIDispatch(), nil
}

func valProp(d *ole.IDispatch, name string) any {
	v, err := oleutil.GetProperty(d, name)
	if err != nil {
		return nil
	}
	defer v.Clear() //nolint:errcheck
	return v.Value()
}

func strProp(d *ole.IDispatch, name string) string {
	if s, ok := valProp(d, name).(string); ok {
		return s
	}
	return ""
}

func intProp(d *ole.IDispatch, name string) int64 {
	switch v := valProp(d, name).(type) {
	case int64:
		return v
	case int32:
		return int64(v)
	case int16:
		return int64(v)
	case uint32:
		return int64(v)
	case int:
		return int64(v)
	case uint8:
		return int64(v)
	case float64:
		return int64(v)
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

func boolProp(d *ole.IDispatch, name string) bool {
	b, _ := valProp(d, name).(bool)
	return b
}

func count(d *ole.IDispatch) int { return int(intProp(d, "Count")) }

func newWUASession() (*ole.IDispatch, error) {
	s, err := createDispatch("Microsoft.Update.Session")
	if err != nil {
		return nil, err
	}
	_, _ = oleutil.PutProperty(s, "ClientApplicationID", "FleetWarden")
	return s, nil
}

// search returns the IUpdateCollection of missing software updates.
// Drivers are left out: they only come through the repository.
func search(session *ole.IDispatch) (*ole.IDispatch, error) {
	return searchFor(session, "IsInstalled=0 and IsHidden=0 and Type='Software'")
}

func searchFor(session *ole.IDispatch, criteria string) (*ole.IDispatch, error) {
	searcher, err := dispCall(session, "CreateUpdateSearcher")
	if err != nil {
		return nil, err
	}
	defer searcher.Release()
	_, _ = oleutil.PutProperty(searcher, "Online", true)
	res, err := dispCall(searcher, "Search", criteria)
	if err != nil {
		return nil, err
	}
	defer res.Release()
	return dispProp(res, "Updates")
}

func dispCall(d *ole.IDispatch, name string, args ...any) (*ole.IDispatch, error) {
	v, err := oleutil.CallMethod(d, name, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if v.VT != ole.VT_DISPATCH || v.ToIDispatch() == nil {
		return nil, fmt.Errorf("%s: no object", name)
	}
	return v.ToIDispatch(), nil
}

func describeUpdate(u *ole.IDispatch) agentproto.PatchUpdate {
	var out agentproto.PatchUpdate
	if ident, err := dispProp(u, "Identity"); err == nil {
		out.ID = strings.ToLower(strProp(ident, "UpdateID"))
		ident.Release()
	}
	out.Title = strProp(u, "Title")
	out.Severity = strProp(u, "MsrcSeverity")
	if kbs, err := dispProp(u, "KBArticleIDs"); err == nil {
		if count(kbs) > 0 {
			if v, err := oleutil.GetProperty(kbs, "Item", 0); err == nil {
				out.KB = "KB" + v.ToString()
				v.Clear() //nolint:errcheck
			}
		}
		kbs.Release()
	}
	if t, ok := valProp(u, "LastDeploymentChangeTime").(time.Time); ok && t.Year() > 2000 {
		out.ReleasedAt = t.UTC()
	}
	switch v := valProp(u, "MaxDownloadSize").(type) {
	case int64:
		out.SizeBytes = v
	case float64:
		out.SizeBytes = int64(v)
	}
	if ib, err := dispProp(u, "InstallationBehavior"); err == nil {
		out.RebootRequired = intProp(ib, "RebootBehavior") != 0
		ib.Release()
	}
	var classes []string
	if cats, err := dispProp(u, "Categories"); err == nil {
		for i := 0; i < count(cats); i++ {
			c, err := dispProp(cats, "Item", i)
			if err != nil {
				continue
			}
			if strProp(c, "Type") == "UpdateClassification" {
				classes = append(classes, strProp(c, "CategoryID"))
			}
			c.Release()
		}
		cats.Release()
	}
	out.Category = wuaCategory(classes, out.Title, intProp(u, "Type") == 2)
	return out
}

func (wuaBackend) Scan(ctx context.Context) (agentproto.PatchScanResult, error) {
	res := agentproto.PatchScanResult{Source: wuaSource(), Updates: []agentproto.PatchUpdate{}}
	err := withCOM(ctx, func() error {
		session, err := newWUASession()
		if err != nil {
			return err
		}
		defer session.Release()
		ups, err := search(session)
		if err != nil {
			return err
		}
		defer ups.Release()
		for i := 0; i < count(ups); i++ {
			u, err := dispProp(ups, "Item", i)
			if err != nil {
				continue
			}
			if d := describeUpdate(u); d.ID != "" {
				res.Updates = append(res.Updates, d)
			}
			u.Release()
		}
		return nil
	})
	res.RebootPending = wuaRebootPending(ctx)
	res.ScannedAt = time.Now().UTC()
	if err != nil {
		return res, fmt.Errorf("windows update scan: %w", err)
	}
	return res, nil
}

func (wuaBackend) Install(ctx context.Context, ids []string, progress func(string)) ([]agentproto.PatchResult, bool, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[strings.ToLower(id)] = true
	}
	var results []agentproto.PatchResult
	reboot := false
	err := withCOM(ctx, func() error {
		session, err := newWUASession()
		if err != nil {
			return err
		}
		defer session.Release()
		ups, err := search(session)
		if err != nil {
			return err
		}
		defer ups.Release()
		coll, err := createDispatch("Microsoft.Update.UpdateColl")
		if err != nil {
			return err
		}
		defer coll.Release()
		var order []string
		for i := 0; i < count(ups); i++ {
			u, err := dispProp(ups, "Item", i)
			if err != nil {
				continue
			}
			id := ""
			if ident, err := dispProp(u, "Identity"); err == nil {
				id = strings.ToLower(strProp(ident, "UpdateID"))
				ident.Release()
			}
			if want[id] {
				if !boolProp(u, "EulaAccepted") {
					_, _ = oleutil.CallMethod(u, "AcceptEula")
				}
				if _, err := oleutil.CallMethod(coll, "Add", u); err == nil {
					order = append(order, id)
					delete(want, id)
				}
			}
			u.Release()
		}
		for id := range want {
			results = append(results, agentproto.PatchResult{ID: id, OK: false, Error: "no longer offered by Windows Update"})
		}
		if len(order) == 0 {
			return nil
		}
		progress(fmt.Sprintf("downloading %d update(s)", len(order)))
		dl, err := dispCall(session, "CreateUpdateDownloader")
		if err != nil {
			return err
		}
		defer dl.Release()
		if _, err := oleutil.PutProperty(dl, "Updates", coll); err != nil {
			return err
		}
		dres, err := dispCall(dl, "Download")
		if err != nil {
			return fmt.Errorf("download: %w", err)
		}
		dres.Release()
		progress(fmt.Sprintf("installing %d update(s)", len(order)))
		inst, err := dispCall(session, "CreateUpdateInstaller")
		if err != nil {
			return err
		}
		defer inst.Release()
		_, _ = oleutil.PutProperty(inst, "ForceQuiet", true)
		if _, err := oleutil.PutProperty(inst, "Updates", coll); err != nil {
			return err
		}
		ires, err := dispCall(inst, "Install")
		if err != nil {
			return fmt.Errorf("install: %w", err)
		}
		defer ires.Release()
		reboot = boolProp(ires, "RebootRequired")
		for i, id := range order {
			r, err := dispCall(ires, "GetUpdateResult", i)
			if err != nil {
				results = append(results, agentproto.PatchResult{ID: id, Error: err.Error()})
				continue
			}
			code := int(intProp(r, "ResultCode"))
			hr := int32(intProp(r, "HResult"))
			if boolProp(r, "RebootRequired") {
				reboot = true
			}
			r.Release()
			res := agentproto.PatchResult{ID: id, OK: wuaResultOK(code)}
			if !res.OK {
				res.Error = wuaResultText(code, hr)
			}
			results = append(results, res)
		}
		return nil
	})
	if err != nil {
		return results, reboot || wuaRebootPending(ctx), fmt.Errorf("windows update: %w", err)
	}
	return results, reboot || wuaRebootPending(ctx), nil
}

func (wuaBackend) RebootPending(ctx context.Context) bool { return wuaRebootPending(ctx) }

func wuaRebootPending(ctx context.Context) bool {
	for _, k := range []string{
		`SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`,
	} {
		if key, err := registry.OpenKey(registry.LOCAL_MACHINE, k, registry.QUERY_VALUE); err == nil {
			key.Close()
			return true
		}
	}
	pending := false
	_ = withCOM(ctx, func() error {
		si, err := createDispatch("Microsoft.Update.SystemInfo")
		if err != nil {
			return err
		}
		defer si.Release()
		pending = boolProp(si, "RebootRequired")
		return nil
	})
	return pending
}

// Reboot restarts in one minute; logged-on users see the message.
func (wuaBackend) Reboot(ctx context.Context, message string) error {
	out, err := exec.CommandContext(ctx, "shutdown.exe", "/r", "/t", "60", "/d", "p:2:17", "/c", message).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

const (
	auPolicyKey  = `SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU`
	auMarker     = "FleetWardenManaged"
	auNoAutoUpd  = "NoAutoUpdate"
	wuPolicyKey  = `SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate`
	wuServerName = "WUServer"
)

// Configure turns Windows' own automatic updates off while FleetWarden takes
// over, and restores them (only what it set itself) when it no longer does.
func (wuaBackend) Configure(cfg agentproto.PatchConfig) error {
	take := cfg.Managed && cfg.TakeOverWindowsUpdate
	if take {
		k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, auPolicyKey, registry.SET_VALUE|registry.QUERY_VALUE)
		if err != nil {
			return err
		}
		defer k.Close()
		if v, _, err := k.GetIntegerValue(auNoAutoUpd); err == nil && v == 1 {
			if _, _, err := k.GetIntegerValue(auMarker); err != nil {
				return nil // set by someone else (GPO); leave it alone
			}
		}
		if err := k.SetDWordValue(auNoAutoUpd, 1); err != nil {
			return err
		}
		return k.SetDWordValue(auMarker, 1)
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, auPolicyKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	if _, _, err := k.GetIntegerValue(auMarker); err != nil {
		return nil
	}
	_ = k.DeleteValue(auNoAutoUpd)
	return k.DeleteValue(auMarker)
}

// wuaSource reports whether Windows gets updates from a WSUS server.
func wuaSource() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, auPolicyKey, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		if v, _, err := k.GetIntegerValue("UseWUServer"); err == nil && v == 1 {
			if w, err := registry.OpenKey(registry.LOCAL_MACHINE, wuPolicyKey, registry.QUERY_VALUE); err == nil {
				defer w.Close()
				if s, _, err := w.GetStringValue(wuServerName); err == nil && s != "" {
					return "wsus"
				}
			}
		}
	}
	return "windows-update"
}
