// Command fleetwarden-server runs the FleetWarden management server.
//
// Subcommands (run as root or as the fleetwarden user):
//
//	fleetwarden-server              run the server
//	fleetwarden-server bootstrap    apply migrations and create the first administrator
//	fleetwarden-server reset-admin  give the emergency administrator a new one-time password
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
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
	default:
		err = fmt.Errorf("unknown command %q (commands: bootstrap, reset-admin)", cmd)
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
