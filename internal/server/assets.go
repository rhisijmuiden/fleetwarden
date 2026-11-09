package server

// Asset management: owner, location and warranty per device (typed in, or
// imported from a supplier's CSV), and software licences compared with the
// installations the agents report.

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/reports"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const (
	maxAssetText   = 200
	maxImportRows  = 5000
	maxBulkDevices = 5000
)

func (s *Server) assetRoutes(mux *http.ServeMux) {
	view := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermDevicesView, 0, h) }
	manage := func(h http.HandlerFunc) http.Handler { return s.authed(auth.PermAssetsManage, 0, h) }
	mux.Handle("GET /api/v1/assets", view(s.handleListAssets))
	mux.Handle("PUT /api/v1/assets", manage(s.handleBulkAssets))
	mux.Handle("GET /api/v1/assets/export", view(s.handleExportAssets))
	mux.Handle("POST /api/v1/assets/import", manage(s.handleImportAssets))
	mux.Handle("GET /api/v1/devices/{id}/asset", view(s.handleGetDeviceAsset))
	mux.Handle("PUT /api/v1/devices/{id}/asset", manage(s.handlePutDeviceAsset))

	mux.Handle("GET /api/v1/licenses", view(s.handleListLicenses))
	mux.Handle("POST /api/v1/licenses", manage(s.handleCreateLicense))
	mux.Handle("POST /api/v1/licenses/preview", manage(s.handlePreviewLicense))
	mux.Handle("GET /api/v1/licenses/{id}", view(s.handleGetLicense))
	mux.Handle("PUT /api/v1/licenses/{id}", manage(s.handleUpdateLicense))
	mux.Handle("DELETE /api/v1/licenses/{id}", manage(s.handleDeleteLicense))
	mux.Handle("GET /api/v1/licenses/{id}/devices", view(s.handleLicenseDevices))
	mux.Handle("POST /api/v1/licenses/{id}/key", manage(s.handleRevealLicenseKey))
}

// ---- values ----

// cleanAssetText trims a free-text field and limits its length.
func cleanAssetText(v string) string {
	v = strings.TrimSpace(strings.ToValidUTF8(v, ""))
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, v)
	if r := []rune(v); len(r) > maxAssetText {
		v = string(r[:maxAssetText])
	}
	return v
}

// parseAssetDate reads YYYY-MM-DD and, from spreadsheets, D-M-YYYY,
// D/M/YYYY and D.M.YYYY (day first). Empty stays empty.
func parseAssetDate(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", true
	}
	if len(v) > 10 && (v[10] == 'T' || v[10] == ' ') { // a date with a time from a spreadsheet
		v = v[:10]
	}
	layouts := []string{time.DateOnly, "2006/01/02", "2-1-2006", "2/1/2006", "2.1.2006"}
	for _, l := range layouts {
		if t, err := time.Parse(l, v); err == nil {
			if t.Year() < 1980 || t.Year() > 2100 {
				return "", false
			}
			return t.Format(time.DateOnly), true
		}
	}
	return "", false
}

// cleanAsset validates every field of an asset and returns an error code.
func cleanAsset(a *store.DeviceAsset) string {
	for _, p := range []*string{&a.AssetTag, &a.Owner, &a.Location, &a.Department, &a.Supplier, &a.OrderNumber} {
		*p = cleanAssetText(*p)
	}
	var ok bool
	if a.PurchaseDate, ok = parseAssetDate(a.PurchaseDate); !ok {
		return "invalid_date"
	}
	if a.WarrantyEnd, ok = parseAssetDate(a.WarrantyEnd); !ok {
		return "invalid_date"
	}
	return ""
}

func allAssetFields() store.AssetFields {
	f := store.AssetFields{}
	for _, n := range store.AssetFieldNames {
		f[n] = true
	}
	return f
}

func setAssetField(a *store.DeviceAsset, field, v string) {
	switch field {
	case "assetTag":
		a.AssetTag = v
	case "owner":
		a.Owner = v
	case "location":
		a.Location = v
	case "department":
		a.Department = v
	case "supplier":
		a.Supplier = v
	case "orderNumber":
		a.OrderNumber = v
	case "purchaseDate":
		a.PurchaseDate = v
	case "warrantyEnd":
		a.WarrantyEnd = v
	}
}

func assetField(a store.DeviceAsset, field string) string {
	switch field {
	case "assetTag":
		return a.AssetTag
	case "owner":
		return a.Owner
	case "location":
		return a.Location
	case "department":
		return a.Department
	case "supplier":
		return a.Supplier
	case "orderNumber":
		return a.OrderNumber
	case "purchaseDate":
		return a.PurchaseDate
	case "warrantyEnd":
		return a.WarrantyEnd
	}
	return ""
}

// warrantyState is expired, expiring (within days), valid or unknown.
func warrantyState(end string, today time.Time, days int) string {
	if end == "" {
		return "unknown"
	}
	t, err := time.Parse(time.DateOnly, end)
	if err != nil {
		return "unknown"
	}
	switch left := daysUntil(today, t); {
	case left < 0:
		return "expired"
	case left <= days:
		return "expiring"
	}
	return "valid"
}

// ---- asset register ----

type assetSummary struct {
	Total        int `json:"total"`
	Expired      int `json:"expired"`
	Expiring     int `json:"expiring"`
	Valid        int `json:"valid"`
	Unknown      int `json:"unknown"`
	ExpiringDays int `json:"expiringDays"`
}

type assetList struct {
	Items   []store.AssetRow `json:"items"`
	Summary assetSummary     `json:"summary"`
}

func (s *Server) handleListAssets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days := s.loadReportSettings(r.Context()).ExpiringDays
	now := s.now()
	aq := store.AssetQuery{Scope: s.scope(r), Search: strings.TrimSpace(q.Get("search")), WarrantyDays: days, Today: now.Format(time.DateOnly)}
	aq.GroupID, _ = strconv.ParseInt(q.Get("group"), 10, 64)
	all, err := s.st.ListAssets(r.Context(), aq)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	want := q.Get("warranty")
	out := assetList{Items: []store.AssetRow{}, Summary: assetSummary{ExpiringDays: days}}
	for _, a := range all {
		st := warrantyState(a.WarrantyEnd, now, days)
		out.Summary.Total++
		switch st {
		case "expired":
			out.Summary.Expired++
		case "expiring":
			out.Summary.Expiring++
		case "valid":
			out.Summary.Valid++
		default:
			out.Summary.Unknown++
		}
		if want != "" && want != st {
			continue
		}
		a.Online = s.hub.online(a.ID)
		out.Items = append(out.Items, a)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetDeviceAsset(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	a, err := s.st.DeviceAssetOf(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handlePutDeviceAsset(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceInScope(w, r)
	if !ok {
		return
	}
	var a store.DeviceAsset
	if !decode(w, r, &a) {
		return
	}
	if code := cleanAsset(&a); code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	u, _ := currentUser(r)
	if _, err := s.st.SetDeviceAssets(r.Context(), map[string]store.DeviceAsset{d.ID: a}, map[string]store.AssetFields{d.ID: allAssetFields()}, u.Username); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "asset.update", d.Hostname, true, map[string]any{"owner": a.Owner, "location": a.Location, "warrantyEnd": a.WarrantyEnd})
	got, err := s.st.DeviceAssetOf(r.Context(), d.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

type bulkAssetReq struct {
	IDs    []string          `json:"ids"`
	Fields map[string]string `json:"fields"` // only these fields change
}

func (s *Server) handleBulkAssets(w http.ResponseWriter, r *http.Request) {
	var req bulkAssetReq
	if !decode(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxBulkDevices || len(req.Fields) == 0 {
		apiError(w, http.StatusBadRequest, "bad_request")
		return
	}
	var a store.DeviceAsset
	f := store.AssetFields{}
	for k, v := range req.Fields {
		if !slices.Contains(store.AssetFieldNames, k) {
			apiError(w, http.StatusBadRequest, "bad_request")
			return
		}
		setAssetField(&a, k, v)
		f[k] = true
	}
	if code := cleanAsset(&a); code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	// Only devices within the user's scope.
	rows, err := s.st.ListAssets(r.Context(), store.AssetQuery{Scope: s.scope(r), IDs: req.IDs})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(rows) != len(uniqueStrings(req.IDs)) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	updates, fields := map[string]store.DeviceAsset{}, map[string]store.AssetFields{}
	for _, row := range rows {
		updates[row.ID], fields[row.ID] = a, f
	}
	u, _ := currentUser(r)
	n, err := s.st.SetDeviceAssets(r.Context(), updates, fields, u.Username)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	changed := map[string]any{}
	for k := range f {
		changed[k] = assetField(a, k)
	}
	s.audit(r, "asset.bulk_update", fmt.Sprintf("%d devices", n), true, changed)
	writeJSON(w, http.StatusOK, map[string]int{"updated": n})
}

func uniqueStrings(v []string) []string {
	out := []string{}
	for _, x := range v {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// Columns of the asset CSV. The export can be imported again.
var assetCSVColumns = []string{"hostname", "serial", "manufacturer", "model", "group", "assetTag", "owner", "location", "department",
	"supplier", "orderNumber", "purchaseDate", "warrantyEnd"}

func (s *Server) handleExportAssets(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListAssets(r.Context(), store.AssetQuery{Scope: s.scope(r)})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	u, _ := currentUser(r)
	lang := reports.Lang(u.Language)
	if l := r.URL.Query().Get("lang"); l == "en" || l == "nl" {
		lang = l
	}
	rows := make([][]string, 0, len(list))
	for _, a := range list {
		rows = append(rows, []string{a.Hostname, a.Serial, a.Manufacturer, a.Model, a.GroupName, a.AssetTag, a.Owner, a.Location,
			a.Department, a.Supplier, a.OrderNumber, a.PurchaseDate, a.WarrantyEnd})
	}
	body := reports.CSV(lang, assetCSVColumns, rows)
	s.audit(r, "asset.export", "", true, map[string]any{"devices": len(rows)})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="fleetwarden-assets-%s.csv"`, s.now().Format(time.DateOnly)))
	_, _ = w.Write(body)
}

// ---- CSV import ----

// importAliases maps a normalised CSV heading to a field. Headings in
// English and Dutch, and the usual spellings of supplier exports, work.
var importAliases = map[string]string{
	"hostname": "hostname", "host": "hostname", "computer": "hostname", "computername": "hostname", "device": "hostname",
	"devicename": "hostname", "apparaat": "hostname", "apparaatnaam": "hostname", "hostnaam": "hostname", "naam": "hostname", "name": "hostname",
	"serial": "serial", "serialnumber": "serial", "serienummer": "serial", "servicetag": "serial", "sn": "serial", "serial#": "serial",
	"assettag": "assetTag", "asset": "assetTag", "assetnumber": "assetTag", "inventarisnummer": "assetTag", "assetnummer": "assetTag",
	"owner": "owner", "user": "owner", "eigenaar": "owner", "gebruiker": "owner", "assignedto": "owner",
	"location": "location", "locatie": "location", "site": "location", "room": "location", "ruimte": "location", "vestiging": "location",
	"department": "department", "afdeling": "department", "costcenter": "department", "kostenplaats": "department",
	"supplier": "supplier", "vendor": "supplier", "leverancier": "supplier", "reseller": "supplier",
	"ordernumber": "orderNumber", "order": "orderNumber", "ordernummer": "orderNumber", "po": "orderNumber", "ponumber": "orderNumber",
	"invoice": "orderNumber", "factuurnummer": "orderNumber", "bestelnummer": "orderNumber",
	"purchasedate": "purchaseDate", "purchased": "purchaseDate", "shipdate": "purchaseDate", "aankoopdatum": "purchaseDate",
	"gekochtop": "purchaseDate", "leverdatum": "purchaseDate", "orderdate": "purchaseDate",
	"warrantyend": "warrantyEnd", "warranty": "warrantyEnd", "warrantyenddate": "warrantyEnd", "warrantyexpires": "warrantyEnd",
	"warrantyexpiration": "warrantyEnd", "enddate": "warrantyEnd", "garantie": "warrantyEnd", "garantietot": "warrantyEnd",
	"einddatumgarantie": "warrantyEnd", "garantieeinde": "warrantyEnd", "garantieeind": "warrantyEnd",
}

func normaliseHeading(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimPrefix(h, "\ufeff")) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '#' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type importReq struct {
	CSV   string `json:"csv"`
	Apply bool   `json:"apply"` // false only checks the file
}

type importRow struct {
	Line     int               `json:"line"`
	Key      string            `json:"key"` // the host name or serial in the file
	DeviceID string            `json:"deviceId,omitempty"`
	Hostname string            `json:"hostname,omitempty"`
	Status   string            `json:"status"` // update, unchanged, not_found, ambiguous, invalid
	Error    string            `json:"error,omitempty"`
	Changes  map[string]string `json:"changes,omitempty"`
}

type importResult struct {
	Columns []string    `json:"columns"` // recognised fields, in file order
	Ignored []string    `json:"ignored"` // headings that were not recognised
	Rows    []importRow `json:"rows"`
	Updated int         `json:"updated"`
	Applied bool        `json:"applied"`
}

// readCSV parses a file with a comma or semicolon (Dutch spreadsheets)
// between the values.
func readCSV(text string) ([][]string, error) {
	text = strings.TrimPrefix(text, "\ufeff")
	first, _, _ := strings.Cut(text, "\n")
	cr := csv.NewReader(strings.NewReader(text))
	if strings.Count(first, ";") > strings.Count(first, ",") {
		cr.Comma = ';'
	} else if strings.Count(first, "\t") > strings.Count(first, ",") {
		cr.Comma = '\t'
	}
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	var out [][]string
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func (s *Server) handleImportAssets(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var req importReq
	if !decode4(w, r, &req) {
		return
	}
	recs, err := readCSV(req.CSV)
	if err != nil || len(recs) < 2 {
		apiError(w, http.StatusBadRequest, "invalid_csv")
		return
	}
	if len(recs) > maxImportRows+1 {
		apiError(w, http.StatusBadRequest, "too_many_rows")
		return
	}
	res := importResult{Columns: []string{}, Ignored: []string{}, Rows: []importRow{}}
	col := map[string]int{}
	for i, h := range recs[0] {
		f, ok := importAliases[normaliseHeading(h)]
		if !ok {
			if strings.TrimSpace(h) != "" {
				res.Ignored = append(res.Ignored, strings.TrimSpace(h))
			}
			continue
		}
		if _, dup := col[f]; !dup {
			col[f] = i
			res.Columns = append(res.Columns, f)
		}
	}
	_, hasHost := col["hostname"]
	_, hasSerial := col["serial"]
	fieldCount := 0
	for _, f := range store.AssetFieldNames {
		if _, ok := col[f]; ok {
			fieldCount++
		}
	}
	if (!hasHost && !hasSerial) || fieldCount == 0 {
		apiError(w, http.StatusBadRequest, "csv_columns")
		return
	}

	// Match within the user's scope: by serial first, then by host name.
	list, err := s.st.ListAssets(r.Context(), store.AssetQuery{Scope: s.scope(r)})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	bySerial, byHost := map[string][]int{}, map[string][]int{}
	for i, a := range list {
		if sn := strings.ToLower(strings.TrimSpace(a.Serial)); sn != "" && !placeholderSerial(sn) {
			bySerial[sn] = append(bySerial[sn], i)
		}
		h := strings.ToLower(a.Hostname)
		byHost[h] = append(byHost[h], i)
		if short, _, ok := strings.Cut(h, "."); ok {
			byHost[short] = append(byHost[short], i)
		}
	}
	cell := func(rec []string, f string) (string, bool) {
		i, ok := col[f]
		if !ok || i >= len(rec) {
			return "", false
		}
		return strings.TrimSpace(rec[i]), true
	}
	updates, fields := map[string]store.DeviceAsset{}, map[string]store.AssetFields{}
	for n, rec := range recs[1:] {
		line := n + 2
		host, _ := cell(rec, "hostname")
		serial, _ := cell(rec, "serial")
		if host == "" && serial == "" {
			if strings.TrimSpace(strings.Join(rec, "")) != "" {
				res.Rows = append(res.Rows, importRow{Line: line, Status: "invalid", Error: "no_key"})
			}
			continue
		}
		row := importRow{Line: line, Key: host}
		if row.Key == "" {
			row.Key = serial
		}
		var match []int
		if serial != "" {
			match = bySerial[strings.ToLower(serial)]
		}
		if len(match) == 0 && host != "" {
			match = byHost[strings.ToLower(host)]
		}
		if len(match) != 1 {
			row.Status = "not_found"
			if len(match) > 1 {
				row.Status = "ambiguous"
			}
			res.Rows = append(res.Rows, row)
			continue
		}
		dev := list[match[0]]
		row.DeviceID, row.Hostname = dev.ID, dev.Hostname
		if _, seen := updates[dev.ID]; seen {
			row.Status, row.Error = "invalid", "duplicate"
			res.Rows = append(res.Rows, row)
			continue
		}
		next := dev.DeviceAsset
		f := store.AssetFields{}
		bad := ""
		for _, name := range store.AssetFieldNames {
			v, ok := cell(rec, name)
			if !ok || v == "" {
				continue // empty cells keep the current value
			}
			if v == "-" {
				v = "" // a dash clears it
			} else if name == "purchaseDate" || name == "warrantyEnd" {
				d, ok := parseAssetDate(v)
				if !ok {
					bad = name
					break
				}
				v = d
			} else {
				v = cleanAssetText(v)
			}
			if v != assetField(dev.DeviceAsset, name) {
				setAssetField(&next, name, v)
				f[name] = true
			}
		}
		switch {
		case bad != "":
			row.Status, row.Error = "invalid", "invalid_date:"+bad
		case len(f) == 0:
			row.Status = "unchanged"
		default:
			row.Status = "update"
			row.Changes = map[string]string{}
			for k := range f {
				row.Changes[k] = assetField(next, k)
			}
			updates[dev.ID], fields[dev.ID] = next, f
		}
		res.Rows = append(res.Rows, row)
	}
	res.Updated = len(updates)
	if req.Apply && len(updates) > 0 {
		u, _ := currentUser(r)
		if _, err := s.st.SetDeviceAssets(r.Context(), updates, fields, u.Username); err != nil {
			s.internalError(w, r, err)
			return
		}
		res.Applied = true
		s.audit(r, "asset.import", fmt.Sprintf("%d devices", len(updates)), true, map[string]any{"rows": len(recs) - 1, "columns": res.Columns})
	}
	writeJSON(w, http.StatusOK, res)
}

// decode4 is decode for bodies up to the reader's own limit.
func decode4(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		apiError(w, http.StatusBadRequest, "bad_request")
		return false
	}
	return true
}

// placeholderSerial: serial numbers that many machines report.
func placeholderSerial(sn string) bool {
	switch sn {
	case "0", "none", "default string", "to be filled by o.e.m.", "system serial number", "not specified", "n/a", "123456789", "0123456789":
		return true
	}
	return false
}

// ---- licences ----

type licenseReq struct {
	store.SoftwareLicense
	// Key replaces the stored key when not nil; "" removes it.
	Key *string `json:"key"`
}

func cleanLicense(l *store.SoftwareLicense) string {
	l.Name = cleanAssetText(l.Name)
	l.Vendor = cleanAssetText(l.Vendor)
	l.MatchName = cleanAssetText(l.MatchName)
	l.MatchPublisher = cleanAssetText(l.MatchPublisher)
	l.Notes = strings.TrimSpace(strings.ToValidUTF8(l.Notes, ""))
	if l.Name == "" || len([]rune(l.Name)) > 120 {
		return "invalid_name"
	}
	if strings.Trim(l.MatchName, "* ") == "" {
		return "invalid_match"
	}
	if l.Seats < 0 || l.Seats > 1_000_000 {
		return "invalid_seats"
	}
	if l.Cost != nil && (*l.Cost < 0 || *l.Cost > 1e9 || math.IsNaN(*l.Cost)) {
		return "invalid_cost"
	}
	if len(l.Notes) > 4000 {
		return "invalid_notes"
	}
	var ok bool
	if l.ExpiresOn, ok = parseAssetDate(l.ExpiresOn); !ok {
		return "invalid_date"
	}
	return ""
}

func (s *Server) handleListLicenses(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListLicenses(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"licenses": list, "expiringDays": s.loadReportSettings(r.Context()).ExpiringDays})
}

func (s *Server) licenseFromPath(w http.ResponseWriter, r *http.Request) (store.SoftwareLicense, bool) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return store.SoftwareLicense{}, false
	}
	l, err := s.st.LicenseByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return l, false
	}
	if err != nil {
		s.internalError(w, r, err)
		return l, false
	}
	return l, true
}

func (s *Server) handleGetLicense(w http.ResponseWriter, r *http.Request) {
	if l, ok := s.licenseFromPath(w, r); ok {
		writeJSON(w, http.StatusOK, l)
	}
}

func (s *Server) sealKey(w http.ResponseWriter, r *http.Request, key string) (string, bool) {
	key = strings.TrimSpace(key)
	if len(key) > 4000 {
		apiError(w, http.StatusBadRequest, "invalid_key")
		return "", false
	}
	if key == "" {
		return "", true
	}
	if s.box == nil {
		s.internalError(w, r, errNoSecrets)
		return "", false
	}
	enc, err := s.box.Seal(key)
	if err != nil {
		s.internalError(w, r, err)
		return "", false
	}
	return enc, true
}

func (s *Server) handleCreateLicense(w http.ResponseWriter, r *http.Request) {
	var req licenseReq
	if !decode(w, r, &req) {
		return
	}
	l := req.SoftwareLicense
	if code := cleanLicense(&l); code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	if req.Key != nil {
		enc, ok := s.sealKey(w, r, *req.Key)
		if !ok {
			return
		}
		l.KeyEnc = enc
	}
	u, _ := currentUser(r)
	l.CreatedBy = u.Username
	created, err := s.st.CreateLicense(r.Context(), l)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "name_taken")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "license.create", created.Name, true, map[string]any{"seats": created.Seats, "match": created.MatchName})
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateLicense(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.licenseFromPath(w, r)
	if !ok {
		return
	}
	var req licenseReq
	if !decode(w, r, &req) {
		return
	}
	l := req.SoftwareLicense
	l.ID = cur.ID
	if code := cleanLicense(&l); code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	if req.Key != nil {
		enc, ok := s.sealKey(w, r, *req.Key)
		if !ok {
			return
		}
		l.KeyEnc = enc
	}
	err := s.st.UpdateLicense(r.Context(), l, req.Key != nil)
	switch {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, http.StatusNotFound, "not_found")
		return
	case errors.Is(err, store.ErrConflict):
		apiError(w, http.StatusConflict, "name_taken")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "license.update", l.Name, true, map[string]any{"seats": l.Seats, "match": l.MatchName, "keyChanged": req.Key != nil})
	got, _ := s.st.LicenseByID(r.Context(), l.ID)
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleDeleteLicense(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.licenseFromPath(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteLicense(r.Context(), cur.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "license.delete", cur.Name, true, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLicenseDevices(w http.ResponseWriter, r *http.Request) {
	l, ok := s.licenseFromPath(w, r)
	if !ok {
		return
	}
	list, err := s.st.LicenseInstalls(r.Context(), l.ID, s.scope(r))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": list})
}

func (s *Server) handleRevealLicenseKey(w http.ResponseWriter, r *http.Request) {
	l, ok := s.licenseFromPath(w, r)
	if !ok {
		return
	}
	if s.box == nil {
		s.internalError(w, r, errNoSecrets)
		return
	}
	key, err := s.box.Open(l.KeyEnc)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "license.key_view", l.Name, true, nil)
	writeJSON(w, http.StatusOK, map[string]string{"key": key})
}

func (s *Server) handlePreviewLicense(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MatchName      string `json:"matchName"`
		MatchPublisher string `json:"matchPublisher"`
	}
	if !decode(w, r, &req) {
		return
	}
	if strings.Trim(req.MatchName, "* ") == "" {
		apiError(w, http.StatusBadRequest, "invalid_match")
		return
	}
	n, names, err := s.st.LicensePreview(r.Context(), cleanAssetText(req.MatchName), cleanAssetText(req.MatchPublisher))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": n, "programs": names})
}

// licenseState is over (more installations than seats), expired, expiring
// or ok.
func licenseState(l store.SoftwareLicense, today time.Time, days int) string {
	if l.InUse > l.Seats {
		return "over"
	}
	if l.ExpiresOn != "" {
		switch warrantyState(l.ExpiresOn, today, days) {
		case "expired":
			return "expired"
		case "expiring":
			return "expiring"
		}
	}
	return "ok"
}

// ---- report ----

type assetLicenseRow struct {
	store.SoftwareLicense
	Free  int    `json:"free"`
	State string `json:"state"`
}

type assetDeviceRow struct {
	store.AssetRow
	State    string `json:"state"`
	DaysLeft *int   `json:"daysLeft"`
}

type assetsReport struct {
	Generated    time.Time         `json:"generated"`
	Scope        string            `json:"scope"`
	ExpiringDays int               `json:"expiringDays"`
	Devices      int               `json:"devices"`
	Expired      int               `json:"expired"`
	Expiring     int               `json:"expiring"`
	Unknown      int               `json:"unknown"`
	NoOwner      int               `json:"noOwner"`
	Licenses     []assetLicenseRow `json:"licenses"`
	LicensesOver int               `json:"licensesOver"`
	LicensesEnd  int               `json:"licensesEnding"` // expired or expiring
	Seats        int               `json:"seats"`
	InUse        int               `json:"inUse"`
	Warranty     []assetDeviceRow  `json:"warranty"` // ended or ending soon, soonest first
	All          []assetDeviceRow  `json:"-"`        // for the CSV
}

func (s *Server) assetsReport(ctx context.Context, in reportInput) (assetsReport, error) {
	days := in.settings.ExpiringDays
	rep := assetsReport{Generated: in.now, Scope: in.scope, ExpiringDays: days, Licenses: []assetLicenseRow{}, Warranty: []assetDeviceRow{}}
	rows, err := s.st.ListAssets(ctx, store.AssetQuery{Scope: store.Scope{All: true}, IDs: deviceIDs(in.devices)})
	if err != nil {
		return rep, err
	}
	for _, a := range rows {
		a.Online = s.hub.online(a.ID)
		row := assetDeviceRow{AssetRow: a, State: warrantyState(a.WarrantyEnd, in.now, days)}
		if t, err := time.Parse(time.DateOnly, a.WarrantyEnd); err == nil {
			left := daysUntil(in.now, t)
			row.DaysLeft = &left
		}
		rep.Devices++
		if a.Owner == "" {
			rep.NoOwner++
		}
		switch row.State {
		case "expired":
			rep.Expired++
		case "expiring":
			rep.Expiring++
		case "unknown":
			rep.Unknown++
		}
		if row.State == "expired" || row.State == "expiring" {
			rep.Warranty = append(rep.Warranty, row)
		}
		rep.All = append(rep.All, row)
	}
	sort.SliceStable(rep.Warranty, func(i, j int) bool { return rep.Warranty[i].WarrantyEnd < rep.Warranty[j].WarrantyEnd })
	lics, err := s.st.ListLicenses(ctx)
	if err != nil {
		return rep, err
	}
	for _, l := range lics {
		row := assetLicenseRow{SoftwareLicense: l, Free: l.Seats - l.InUse, State: licenseState(l, in.now, days)}
		rep.Seats += l.Seats
		rep.InUse += l.InUse
		switch row.State {
		case "over":
			rep.LicensesOver++
		case "expired", "expiring":
			rep.LicensesEnd++
		}
		rep.Licenses = append(rep.Licenses, row)
	}
	return rep, nil
}

func (r assetsReport) output(lang string) reportOutput {
	o := reportOutput{Kind: reportAssets, Title: reports.T(lang, "assets.title")}
	sum := reports.Section{Title: reports.T(lang, "assets.summary"), Text: reports.T(lang, "assets.note", r.ExpiringDays), Stats: []reports.Stat{
		{Label: reports.T(lang, "assets.expired"), Value: strconv.Itoa(r.Expired), Tone: countToneDanger(r.Expired),
			Sub: reports.T(lang, "compliance.ofDevices", r.Expired, r.Devices)},
		{Label: reports.T(lang, "assets.expiring"), Value: strconv.Itoa(r.Expiring), Tone: countTone(r.Expiring),
			Sub: reports.T(lang, "assets.withinDays", r.ExpiringDays)},
		{Label: reports.T(lang, "assets.unknown"), Value: strconv.Itoa(r.Unknown), Tone: reports.ToneMuted},
		{Label: reports.T(lang, "assets.licensesOver"), Value: strconv.Itoa(r.LicensesOver), Tone: countToneDanger(r.LicensesOver),
			Sub: reports.T(lang, "assets.seatsUsed", r.InUse, r.Seats)},
	}}
	lt := reports.Table{Title: reports.T(lang, "assets.licenses"), Empty: reports.T(lang, "assets.noLicenses"), Columns: []reports.Column{
		{Label: reports.T(lang, "col.license"), Width: 3}, {Label: reports.T(lang, "col.vendor"), Width: 1.8},
		{Label: reports.T(lang, "col.seats"), Width: 1, Right: true}, {Label: reports.T(lang, "col.inUse"), Width: 1, Right: true},
		{Label: reports.T(lang, "col.free"), Width: 1, Right: true}, {Label: reports.T(lang, "col.expires"), Width: 1.6, Right: true}}}
	for _, l := range r.Licenses {
		free := reports.Cell{Text: strconv.Itoa(l.Free)}
		if l.Free < 0 {
			free.Tone = reports.ToneDanger
		}
		exp := reports.Cell{Text: "-"}
		if t, err := time.Parse(time.DateOnly, l.ExpiresOn); err == nil {
			exp.Text = reports.FormatDate(lang, t)
			switch l.State {
			case "expired":
				exp.Tone = reports.ToneDanger
			case "expiring":
				exp.Tone = reports.ToneWarning
			}
		}
		lt.Rows = append(lt.Rows, []reports.Cell{{Text: l.Name}, {Text: l.Vendor, Tone: reports.ToneMuted}, {Text: strconv.Itoa(l.Seats)},
			{Text: strconv.Itoa(l.InUse)}, free, exp})
	}
	wt := reports.Table{Title: reports.T(lang, "assets.warranty"), Empty: reports.T(lang, "assets.noWarranty"), Columns: []reports.Column{
		{Label: reports.T(lang, "col.device"), Width: 1.8}, {Label: reports.T(lang, "col.model"), Width: 2.4},
		{Label: reports.T(lang, "col.serial"), Width: 1.5}, {Label: reports.T(lang, "col.owner"), Width: 1.6},
		{Label: reports.T(lang, "col.location"), Width: 1.4}, {Label: reports.T(lang, "col.warrantyEnd"), Width: 1.6, Right: true}}}
	for _, d := range r.Warranty {
		end := reports.Cell{Text: d.WarrantyEnd, Tone: reports.ToneWarning}
		if t, err := time.Parse(time.DateOnly, d.WarrantyEnd); err == nil {
			end.Text = reports.FormatDate(lang, t)
		}
		if d.State == "expired" {
			end.Tone = reports.ToneDanger
		}
		wt.Rows = append(wt.Rows, []reports.Cell{{Text: d.Hostname}, {Text: strings.TrimSpace(d.Manufacturer + " " + d.Model)},
			{Text: d.Serial, Tone: reports.ToneMuted}, {Text: d.Owner}, {Text: d.Location}, end})
	}
	o.Sections = []reports.Section{sum, {Tables: []reports.Table{lt, wt}}}
	o.CSVHeader = []string{reports.T(lang, "col.device"), reports.T(lang, "col.group"), reports.T(lang, "col.manufacturer"),
		reports.T(lang, "col.model"), reports.T(lang, "col.serial"), reports.T(lang, "col.assetTag"), reports.T(lang, "col.owner"),
		reports.T(lang, "col.location"), reports.T(lang, "col.department"), reports.T(lang, "col.supplier"),
		reports.T(lang, "col.purchaseDate"), reports.T(lang, "col.warrantyEnd"), reports.T(lang, "col.warrantyState")}
	for _, d := range r.All {
		o.CSVRows = append(o.CSVRows, []string{d.Hostname, d.GroupName, d.Manufacturer, d.Model, d.Serial, d.AssetTag, d.Owner, d.Location,
			d.Department, d.Supplier, d.PurchaseDate, d.WarrantyEnd, reports.T(lang, "warranty."+d.State)})
	}
	o.Facts = []mail.Fact{
		{Label: reports.T(lang, "assets.expired"), Value: strconv.Itoa(r.Expired), Tone: countToneDanger(r.Expired)},
		{Label: reports.T(lang, "assets.expiring"), Value: strconv.Itoa(r.Expiring), Tone: countTone(r.Expiring)},
		{Label: reports.T(lang, "assets.licensesOver"), Value: strconv.Itoa(r.LicensesOver), Tone: countToneDanger(r.LicensesOver)},
	}
	return o
}
