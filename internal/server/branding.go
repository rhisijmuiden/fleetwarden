package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// brandingSettings is the white-label configuration. Empty colours mean the
// default theme from branding/.
type brandingSettings struct {
	ProductName      string `json:"productName"`
	PrimaryColor     string `json:"primaryColor"`
	PrimaryColorDark string `json:"primaryColorDark"`
	SidebarColor     string `json:"sidebarColor"`
}

type brandingView struct {
	brandingSettings
	HasLogo    bool  `json:"hasLogo"`
	HasFavicon bool  `json:"hasFavicon"`
	Revision   int64 `json:"revision"`
}

const defaultProduct = "FleetWarden"

var colorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s *Server) loadBranding(ctx context.Context) brandingSettings {
	var b brandingSettings
	_ = s.st.GetSetting(ctx, keyBranding, &b)
	if b.ProductName == "" {
		b.ProductName = defaultProduct
	}
	return b
}

func (s *Server) brandingView(ctx context.Context) brandingView {
	v := brandingView{brandingSettings: s.loadBranding(ctx)}
	for _, name := range []string{"logo", "favicon"} {
		if a, err := s.st.GetAsset(ctx, name); err == nil {
			if name == "logo" {
				v.HasLogo = true
			} else {
				v.HasFavicon = true
			}
			if t := a.UpdatedAt.Unix(); t > v.Revision {
				v.Revision = t
			}
		}
	}
	return v
}

func (s *Server) handleBranding(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.brandingView(r.Context()))
}

func validAssetName(n string) bool { return n == "logo" || n == "favicon" }

func (s *Server) handleBrandingAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("asset")
	if !validAssetName(name) {
		http.NotFound(w, r)
		return
	}
	a, err := s.st.GetAsset(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.ContentType)
	// Uploaded SVGs could contain scripts; sandbox them when opened directly.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Cache-Control", "public, max-age=300")
	h.Set("Content-Length", strconv.Itoa(len(a.Data)))
	_, _ = w.Write(a.Data)
}

func (s *Server) handlePutBranding(w http.ResponseWriter, r *http.Request) {
	var req brandingSettings
	if !decode(w, r, &req) {
		return
	}
	req.ProductName = strings.TrimSpace(req.ProductName)
	if utf8.RuneCountInString(req.ProductName) > 40 {
		apiError(w, http.StatusBadRequest, "name_too_long")
		return
	}
	for _, c := range []string{req.PrimaryColor, req.PrimaryColorDark, req.SidebarColor} {
		if c != "" && !colorRE.MatchString(c) {
			apiError(w, http.StatusBadRequest, "invalid_color")
			return
		}
	}
	if err := s.st.PutSetting(r.Context(), keyBranding, req); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "settings.branding", "branding", true, map[string]any{"productName": req.ProductName,
		"primaryColor": req.PrimaryColor, "primaryColorDark": req.PrimaryColorDark, "sidebarColor": req.SidebarColor})
	writeJSON(w, http.StatusOK, s.brandingView(r.Context()))
}

const maxAssetSize = 512 << 10

// sniffImage returns the content type of a supported image, or "".
func sniffImage(data []byte) string {
	switch ct := http.DetectContentType(data); ct {
	case "image/png", "image/jpeg", "image/webp", "image/x-icon", "image/vnd.microsoft.icon":
		return ct
	}
	head := bytes.TrimSpace(data[:min(len(data), 512)])
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	if bytes.HasPrefix(head, []byte("<svg")) || (bytes.HasPrefix(head, []byte("<?xml")) && bytes.Contains(data, []byte("<svg"))) {
		return "image/svg+xml"
	}
	return ""
}

func (s *Server) handleUploadAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("asset")
	if !validAssetName(name) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAssetSize+1))
	if err != nil || len(data) > maxAssetSize {
		apiError(w, http.StatusRequestEntityTooLarge, "file_too_large")
		return
	}
	ct := sniffImage(data)
	if ct == "" {
		apiError(w, http.StatusBadRequest, "unsupported_image")
		return
	}
	if err := s.st.PutAsset(r.Context(), name, ct, data); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "settings.branding.upload", name, true, map[string]any{"contentType": ct, "bytes": len(data)})
	writeJSON(w, http.StatusOK, s.brandingView(r.Context()))
}

func (s *Server) handleDeleteAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("asset")
	if !validAssetName(name) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := s.st.DeleteAsset(r.Context(), name); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "settings.branding.delete", name, true, nil)
	writeJSON(w, http.StatusOK, s.brandingView(r.Context()))
}
