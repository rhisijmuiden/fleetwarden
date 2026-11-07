// Command fleetwarden-server runs the FleetWarden management server.
//
// Subcommands (run as root or as the fleetwarden user):
//
//	fleetwarden-server              run the server
//	fleetwarden-server bootstrap    apply migrations and create the first administrator
//	fleetwarden-server reset-admin  give the emergency administrator a new one-time password
//	fleetwarden-server backup [--full]  run a backup to all enabled locations now
//	fleetwarden-server restore-apply    apply a restore staged in the web interface (root;
//	                                    started by fleetwarden-restore.path)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/config"
	"github.com/rhisijmuiden/fleetwarden/internal/db"
	"github.com/rhisijmuiden/fleetwarden/internal/secrets"
	"github.com/rhisijmuiden/fleetwarden/internal/server"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

const envFile = "/etc/fleetwarden/server.env"

func main() {
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()
	if *showVersion {
		v := version.Get()
		fmt.Printf("fleetwarden-server %s (%s, %s channel, built %s)\n", v.Version, v.Commit, v.Channel, v.BuildDate)
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	var err error
	switch cmd := flag.Arg(0); cmd {
	case "":
		err = run(log)
	case "bootstrap", "reset-admin":
		err = admin(cmd)
	case "backup":
		err = backupNow(log, flag.Args()[1:])
	case "restore-apply":
		err = restoreApply(flag.Args()[1:])
	default:
		err = fmt.Errorf("unknown command %q (commands: bootstrap, reset-admin, backup, restore-apply)", cmd)
	}
	if err != nil {
		log.Error("fleetwarden-server", "err", err)
		os.Exit(1)
	}
}

// dropToServiceUser re-runs a command line tool as the fleetwarden user when
// started as root, so files it creates have the right owner.
func dropToServiceUser() (bool, error) {
	if os.Geteuid() != 0 {
		return false, nil
	}
	if _, err := user.Lookup("fleetwarden"); err != nil {
		return false, nil // development machine without the service user
	}
	self, err := os.Executable()
	if err != nil {
		return false, err
	}
	cmd := exec.Command("runuser", append([]string{"-u", "fleetwarden", "--", self}, os.Args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return true, cmd.Run()
}

func admin(cmd string) error {
	if done, err := dropToServiceUser(); done || err != nil {
		return err
	}
	if err := config.LoadEnvFile(envFile); err != nil {
		return err
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return errors.New("FW_DATABASE_URL is not set")
	}
	ctx := context.Background()
	if _, err := secrets.LoadOrCreate(cfg.SecretKeyPath()); err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	st := store.New(pool)
	var pw string
	if cmd == "reset-admin" {
		pw, err = server.ResetAdmin(ctx, st, cfg.InitialPasswordPath())
	} else {
		pw, err = server.EnsureAdmin(ctx, st, cfg.InitialPasswordPath())
	}
	if err != nil {
		return err
	}
	if pw != "" {
		fmt.Printf("Administrator account\n  username: %s\n  password: %s\n(saved in %s until the first login)\n",
			server.BreakGlassUsername, pw, cfg.InitialPasswordPath())
	}
	return nil
}

// loadCLI loads the configuration for a command line tool.
func loadCLI() (config.Server, error) {
	if err := config.LoadEnvFile(envFile); err != nil {
		return config.Server{}, err
	}
	cfg, err := config.FromEnv()
	if err == nil && cfg.DatabaseURL == "" {
		err = errors.New("FW_DATABASE_URL is not set")
	}
	return cfg, err
}

func backupNow(log *slog.Logger, args []string) error {
	if done, err := dropToServiceUser(); done || err != nil {
		return err
	}
	kind := "auto"
	for _, a := range args {
		switch a {
		case "--full":
			kind = "full"
		case "--incremental":
			kind = "incremental"
		default:
			return fmt.Errorf("unknown option %q (options: --full, --incremental)", a)
		}
	}
	cfg, err := loadCLI()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	box, err := secrets.LoadOrCreate(cfg.SecretKeyPath())
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	st := store.New(pool)
	app := server.New(cfg, server.Deps{DB: pool, Store: st, Secrets: box, Log: log})
	if err := app.BackupNow(ctx, kind); err != nil {
		return err
	}
	// Failure mails are queued; send them now rather than waiting for the server.
	app.ProcessMailQueue(ctx)
	fmt.Println("Backup completed.")
	return nil
}

func serviceUserExists() bool {
	_, err := user.Lookup("fleetwarden")
	return err == nil
}

func hasSystemd() bool {
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

// restoreApply applies a staged restore. As root it stops the server, runs
// the restore itself as the fleetwarden user and starts the server again;
// otherwise (development, --stage2) it restores directly.
func restoreApply(args []string) error {
	stage2 := len(args) > 0 && args[0] == "--stage2"
	cfg, err := loadCLI()
	if err != nil {
		return err
	}
	ctx := context.Background()
	asRoot := os.Geteuid() == 0 && serviceUserExists() && !stage2
	req, err := server.ReadRestoreRequest(cfg)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("No restore is waiting.")
		return nil
	}
	st := server.RestoreStatus{ID: time.Now().UTC().Format("20060102T150405Z"), State: "applying",
		Snapshot: req.Snapshot, RequestedBy: req.RequestedBy, StartedAt: time.Now().UTC()}
	report := func(state, msg string) {
		if stage2 {
			return // the root process reports
		}
		st.State, st.Message = state, msg
		if werr := server.WriteRestoreStatus(cfg.UpdateStatusDir, st); werr != nil {
			fmt.Fprintln(os.Stderr, "restore status:", werr)
		}
	}
	if err != nil {
		report("failed", err.Error())
		_ = os.Remove(filepath.Join(cfg.RestoreDir(), "request.json"))
		return err
	}
	report("applying", "")
	if !asRoot {
		msg, err := server.ApplyRestore(ctx, cfg, req)
		if err != nil {
			report("failed", err.Error())
			return err
		}
		report("completed", msg)
		fmt.Println(msg)
		return nil
	}

	systemd := hasSystemd()
	if systemd {
		if out, err := exec.Command("systemctl", "stop", "fleetwarden-server.service").CombinedOutput(); err != nil {
			report("failed", "could not stop the server: "+strings.TrimSpace(string(out)))
			_ = os.Remove(filepath.Join(cfg.RestoreDir(), "request.json"))
			return err
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	var out strings.Builder
	cmd := exec.Command("runuser", "-u", "fleetwarden", "--", self, "restore-apply", "--stage2")
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	_ = os.Remove(filepath.Join(cfg.RestoreDir(), "request.json"))
	msg := lastLine(out.String())
	if runErr != nil {
		if msg == "" {
			msg = runErr.Error()
		}
		report("failed", msg)
	} else {
		report("completed", msg)
	}
	if systemd {
		if out, err := exec.Command("systemctl", "start", "fleetwarden-server.service").CombinedOutput(); err != nil {
			fmt.Fprintln(os.Stderr, "could not start the server:", strings.TrimSpace(string(out)))
		}
	}
	if runErr != nil {
		return fmt.Errorf("restore failed: %s", msg)
	}
	fmt.Println(msg)
	return nil
}

// lastLine returns the last non-empty line, without the JSON log wrapper.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := strings.TrimSpace(lines[len(lines)-1])
	var rec struct {
		Err string `json:"err"`
	}
	if json.Unmarshal([]byte(l), &rec) == nil && rec.Err != "" {
		return rec.Err
	}
	return l
}

func run(log *slog.Logger) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	deps := server.Deps{Log: log}
	if cfg.DatabaseURL != "" {
		box, err := secrets.LoadOrCreate(cfg.SecretKeyPath())
		if err != nil {
			return err
		}
		pool, err := db.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		st := store.New(pool)
		if pw, err := server.EnsureAdmin(ctx, st, cfg.InitialPasswordPath()); err != nil {
			return err
		} else if pw != "" {
			log.Warn("created the administrator account; the one-time password is in " + cfg.InitialPasswordPath())
		}
		deps.DB, deps.Store, deps.Secrets = pool, st, box
	} else {
		log.Warn("FW_DATABASE_URL is not set; running without a database")
	}

	app := server.New(cfg, deps)
	go app.RunWorkers(ctx)
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen, "version", version.Version)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
	return nil
}
