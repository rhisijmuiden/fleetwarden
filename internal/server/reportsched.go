package server

// Scheduled reports: mailed daily, weekly or monthly as PDF and/or CSV.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/reports"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const maxReportRecipients = 20

func scheduleLocation(tz string) *time.Location {
	if tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return time.Local
}

// nextReportRun is the first send time of a schedule after a moment.
func nextReportRun(r store.ReportSchedule, after time.Time) time.Time {
	loc := scheduleLocation(r.TimeZone)
	a := after.In(loc)
	at := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, r.SendAt/60, r.SendAt%60, 0, 0, loc)
	}
	switch r.Frequency {
	case "monthly":
		t := at(a.Year(), a.Month(), r.MonthDay)
		if !t.After(after) {
			t = at(a.Year(), a.Month()+1, r.MonthDay)
		}
		return t
	case "weekly":
		for i := 0; i <= 7; i++ {
			t := at(a.Year(), a.Month(), a.Day()+i)
			if int(t.Weekday()) == r.Weekday && t.After(after) {
				return t
			}
		}
		return at(a.Year(), a.Month(), a.Day()+7)
	default: // daily
		t := at(a.Year(), a.Month(), a.Day())
		if !t.After(after) {
			t = at(a.Year(), a.Month(), a.Day()+1)
		}
		return t
	}
}

// validSchedule cleans a schedule from the web interface and returns an
// error code when it is not valid.
func (s *Server) validSchedule(ctx context.Context, r *store.ReportSchedule) string {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len([]rune(r.Name)) > 80 {
		return "invalid_name"
	}
	var kinds []string
	for _, k := range reportKinds { // fixed order
		if slices.Contains(r.Reports, k) {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return "invalid_reports"
	}
	r.Reports = kinds
	var formats []string
	for _, f := range []string{"pdf", "csv"} {
		if slices.Contains(r.Formats, f) {
			formats = append(formats, f)
		}
	}
	if len(formats) == 0 {
		return "invalid_formats"
	}
	r.Formats = formats
	if !slices.Contains([]string{"daily", "weekly", "monthly"}, r.Frequency) || r.Weekday < 0 || r.Weekday > 6 ||
		r.MonthDay < 1 || r.MonthDay > 28 || r.SendAt < 0 || r.SendAt > 1439 {
		return "invalid_schedule"
	}
	if r.TimeZone != "" {
		if _, err := time.LoadLocation(r.TimeZone); err != nil {
			return "invalid_time_zone"
		}
	}
	if r.Language != "nl" {
		r.Language = "en"
	}
	var rcpt []string
	for _, a := range r.Recipients {
		a = strings.TrimSpace(a)
		if a == "" || slices.Contains(rcpt, a) {
			continue
		}
		if !mail.ValidAddress(a) {
			return "invalid_email"
		}
		rcpt = append(rcpt, a)
	}
	if len(rcpt) == 0 || len(rcpt) > maxReportRecipients {
		return "invalid_recipients"
	}
	r.Recipients = rcpt
	if len(r.GroupIDs) > 0 {
		groups, err := s.st.ListGroups(ctx, store.Scope{All: true})
		if err != nil {
			return "internal"
		}
		for _, id := range r.GroupIDs {
			if !slices.ContainsFunc(groups, func(g store.DeviceGroup) bool { return g.ID == id }) {
				return "invalid_group"
			}
		}
	}
	return ""
}

func (s *Server) handleListReportSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListReportSchedules(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": list, "serverTimeZone": serverZoneName()})
}

func (s *Server) handleCreateReportSchedule(w http.ResponseWriter, r *http.Request) {
	var req store.ReportSchedule
	if !decode(w, r, &req) {
		return
	}
	if code := s.validSchedule(r.Context(), &req); code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	u, _ := currentUser(r)
	req.CreatedBy = u.Username
	next := nextReportRun(req, s.now())
	req.NextRunAt = &next
	created, err := s.st.CreateReportSchedule(r.Context(), req)
	if errors.Is(err, store.ErrConflict) {
		apiError(w, http.StatusConflict, "name_taken")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "report.schedule_create", created.Name, true, map[string]any{"reports": created.Reports, "frequency": created.Frequency,
		"recipients": created.Recipients})
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateReportSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	var req store.ReportSchedule
	if !decode(w, r, &req) {
		return
	}
	req.ID = id
	if code := s.validSchedule(r.Context(), &req); code != "" {
		apiError(w, http.StatusBadRequest, code)
		return
	}
	next := nextReportRun(req, s.now())
	req.NextRunAt = &next
	err := s.st.UpdateReportSchedule(r.Context(), req)
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
	s.audit(r, "report.schedule_update", req.Name, true, map[string]any{"enabled": req.Enabled, "reports": req.Reports,
		"frequency": req.Frequency, "recipients": req.Recipients})
	got, _ := s.st.ReportScheduleByID(r.Context(), id)
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleDeleteReportSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	cur, err := s.st.ReportScheduleByID(r.Context(), id)
	if err == nil {
		err = s.st.DeleteReportSchedule(r.Context(), id)
	}
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r, "report.schedule_delete", cur.Name, true, nil)
	w.WriteHeader(http.StatusNoContent)
}

// handleSendReportSchedule mails a schedule's reports now, without moving
// its next run.
func (s *Server) handleSendReportSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	sch, err := s.st.ReportScheduleByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if cfg, err := s.loadMail(r.Context()); err != nil || !cfg.Enabled {
		apiError(w, http.StatusConflict, "mail_not_configured")
		return
	}
	err = s.sendScheduledReport(r.Context(), sch)
	_ = s.st.FinishReportRun(context.WithoutCancel(r.Context()), sch.ID, s.now(), errText(err))
	if err != nil {
		s.audit(r, "report.send", sch.Name, false, map[string]any{"error": err.Error()})
		apiErrorDetail(w, http.StatusInternalServerError, "report_failed", err.Error())
		return
	}
	s.audit(r, "report.send", sch.Name, true, map[string]any{"recipients": sch.Recipients})
	writeJSON(w, http.StatusOK, map[string]int{"queued": len(sch.Recipients)})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// reportTick sends the schedules that are due. Each one is claimed first,
// so a restart or a second server does not send it twice.
func (s *Server) reportTick(ctx context.Context) {
	now := s.now()
	due, err := s.st.DueReportSchedules(ctx, now)
	if err != nil {
		s.log.Warn("report schedules", "err", err)
		return
	}
	for _, sch := range due {
		next := nextReportRun(sch, now)
		ok, err := s.st.ClaimReportRun(ctx, sch.ID, *sch.NextRunAt, next)
		if err != nil || !ok {
			continue
		}
		s.bgWG.Add(1)
		go func(sch store.ReportSchedule) {
			defer s.bgWG.Done()
			rctx, cancel := context.WithTimeout(s.bgCtx, 10*time.Minute)
			defer cancel()
			err := s.sendScheduledReport(rctx, sch)
			if err != nil {
				s.log.Error("scheduled report", "schedule", sch.Name, "err", err)
			} else {
				s.log.Info("scheduled report queued", "schedule", sch.Name, "recipients", len(sch.Recipients))
			}
			_ = s.st.FinishReportRun(context.WithoutCancel(rctx), sch.ID, s.now(), errText(err))
		}(sch)
	}
}

// sendScheduledReport builds the reports of a schedule and queues one
// message per recipient with the PDF and/or CSV files attached.
func (s *Server) sendScheduledReport(ctx context.Context, sch store.ReportSchedule) error {
	lang := reports.Lang(sch.Language)
	in, err := s.reportInput(ctx, store.Scope{All: true}, sch.GroupIDs, lang)
	if err != nil {
		return err
	}
	var outs []reportOutput
	var facts []mail.Fact
	for _, kind := range sch.Reports {
		_, out, err := s.buildReport(ctx, kind, in, lang)
		if err != nil {
			return fmt.Errorf("%s report: %w", kind, err)
		}
		outs = append(outs, out)
		facts = append(facts, out.Facts...)
	}
	var files []mail.Attachment
	if slices.Contains(sch.Formats, "pdf") {
		title := sch.Name
		if len(outs) == 1 {
			title = outs[0].Title
		}
		pdf, err := s.document(ctx, title, in.scope, lang, outs).PDF()
		if err != nil {
			return fmt.Errorf("pdf: %w", err)
		}
		name := reportFileName("report", in.now, "pdf")
		if len(outs) == 1 {
			name = reportFileName(outs[0].Kind, in.now, "pdf")
		}
		files = append(files, mail.Attachment{Name: name, ContentType: "application/pdf", Data: pdf})
	}
	if slices.Contains(sch.Formats, "csv") {
		for _, o := range outs {
			files = append(files, mail.Attachment{Name: reportFileName(o.Kind, in.now, "csv"), ContentType: "text/csv",
				Data: reports.CSV(lang, o.CSVHeader, o.CSVRows)})
		}
	}
	raw, err := json.Marshal(files)
	if err != nil {
		return err
	}
	b := s.loadBranding(ctx)
	var g generalSettings
	_ = s.st.GetSetting(ctx, keyGeneral, &g)
	vars := map[string]string{"name": sch.Name, "scope": in.scope, "date": reports.FormatDateTime(lang, in.now.In(scheduleLocation(sch.TimeZone)))}
	for _, to := range sch.Recipients {
		msg := mail.RenderFacts("report", lang, to, mailBrandOf(b, g), vars, facts)
		if _, err := s.st.EnqueueMail(ctx, store.Mail{Kind: "report", To: to, Subject: msg.Subject, BodyText: msg.Text,
			BodyHTML: msg.HTML, Attachments: raw}); err != nil {
			return err
		}
	}
	return nil
}
