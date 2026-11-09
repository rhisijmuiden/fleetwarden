package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/mail"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

const mailMaxAttempts = 6

func mailBackoff(attempt int) time.Duration {
	return time.Duration(attempt*attempt) * time.Minute // 1, 4, 9, 16, 25 minutes
}

// RunWorkers runs the background jobs until ctx ends: the mail queue,
// session and mail log clean-up, update and restore result reporting and
// scheduled backups. It waits for a running backup to stop before returning.
func (s *Server) RunWorkers(ctx context.Context) {
	if s.st == nil {
		return
	}
	s.workersStarted = s.now()
	stop := context.AfterFunc(ctx, s.bgCancel)
	defer stop()
	defer s.bgWG.Wait()
	s.reportUpdateOutcome(ctx)
	s.reportRestoreOutcome(ctx)
	if err := s.st.FailInterruptedBackupRuns(ctx); err != nil {
		s.log.Warn("backup history", "err", err)
	}
	if err := s.st.EndInterruptedRemoteSessions(ctx); err != nil {
		s.log.Warn("remote session log", "err", err)
	}
	s.reconcileGateways(ctx)
	if err := s.st.ResetInterruptedDriverDownloads(ctx); err != nil {
		s.log.Warn("driver repository", "err", err)
	}
	s.checkRepository(ctx)
	s.vendorTick()
	s.bgWG.Add(1)
	go s.agentReleaseLoop(s.bgCtx)
	mailTick := time.NewTicker(15 * time.Second)
	houseTick := time.NewTicker(time.Hour)
	updTick := time.NewTicker(time.Minute)
	defer mailTick.Stop()
	defer houseTick.Stop()
	defer updTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-mailTick.C:
			s.ProcessMailQueue(ctx)
		case <-updTick.C:
			s.reportUpdateOutcome(ctx)
			s.backupTick(ctx)
			s.agentUpdateTick(ctx)
			s.patchTick(ctx)
			s.driversTick(ctx)
			s.softwareTick(ctx)
			s.configTick(ctx)
			s.reportTick(ctx)
			s.alertTick(ctx)
			s.vulnTick(ctx)
		case <-houseTick.C:
			s.checkRepository(ctx)
			s.vendorTick()
			if err := s.st.PurgeSessions(ctx, sessionIdle); err != nil {
				s.log.Warn("purge sessions", "err", err)
			}
			if err := s.st.PurgeRemoteSessions(ctx, remoteLogDays*24*time.Hour); err != nil {
				s.log.Warn("purge remote session log", "err", err)
			}
			if err := s.st.PurgeMail(ctx, 90*24*time.Hour); err != nil {
				s.log.Warn("purge mail log", "err", err)
			}
			if err := s.st.PurgeBackupRuns(ctx, 400*24*time.Hour); err != nil {
				s.log.Warn("purge backup history", "err", err)
			}
			if err := s.st.PurgePatchRuns(ctx, 400*24*time.Hour); err != nil {
				s.log.Warn("purge patch history", "err", err)
			}
			if err := s.st.PurgeSoftwareJobs(ctx, 400*24*time.Hour); err != nil {
				s.log.Warn("purge software history", "err", err)
			}
			if err := s.st.PurgeConfigurationRuns(ctx, configRunDays*24*time.Hour); err != nil {
				s.log.Warn("purge configuration script runs", "err", err)
			}
			s.pruneSoftwareFiles(ctx)
			if err := s.st.PurgeVulnExceptions(ctx); err != nil {
				s.log.Warn("purge accepted vulnerability risks", "err", err)
			}
		}
	}
}

// ProcessMailQueue sends due messages once.
func (s *Server) ProcessMailQueue(ctx context.Context) {
	cfg, err := s.loadMail(ctx)
	if err != nil || !cfg.Enabled {
		return // messages wait until mail is configured
	}
	for {
		n, err := s.st.ProcessMail(ctx, 10, mailMaxAttempts, mailBackoff, func(m store.Mail) error {
			sctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			msg := mail.Message{To: m.To, Subject: m.Subject, Text: m.BodyText, HTML: m.BodyHTML}
			if len(m.Attachments) > 2 {
				if err := json.Unmarshal(m.Attachments, &msg.Attachments); err != nil {
					return fmt.Errorf("attachments: %w", err)
				}
			}
			return s.mailer.Send(sctx, cfg, msg)
		})
		if err != nil {
			s.log.Error("mail queue", "err", err)
			return
		}
		if n < 10 {
			return
		}
	}
}
