// Command fleetwarden-distribution runs a FleetWarden distribution server:
// a cache in a branch office that serves approved software, drivers and
// agent updates to the agents near it.
//
//	fleetwarden-distribution                                  run the distribution server
//	fleetwarden-distribution enroll --server URL --token TOKEN  register with the management server
//	fleetwarden-distribution status                           show the enrollment
//	fleetwarden-distribution -version                         print the version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"syscall"

	"github.com/rhisijmuiden/fleetwarden/internal/config"
	"github.com/rhisijmuiden/fleetwarden/internal/distribution"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

const serviceUser = "fleetwarden-distribution"

func main() {
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()
	if *showVersion {
		v := version.Get()
		fmt.Printf("fleetwarden-distribution %s (%s, %s channel, built %s)\n", v.Version, v.Commit, v.Channel, v.BuildDate)
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	var err error
	switch cmd := flag.Arg(0); cmd {
	case "":
		err = run(log)
	case "enroll":
		err = enroll(flag.Args()[1:])
	case "status":
		err = status()
	default:
		err = fmt.Errorf("unknown command %q (commands: enroll, status)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fleetwarden-distribution:", err)
		os.Exit(1)
	}
}

func loadConfig() (distribution.Config, error) {
	if err := config.LoadEnvFile(distribution.EnvFile); err != nil {
		return distribution.Config{}, err
	}
	return distribution.FromEnv()
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	s, err := distribution.New(cfg, log, version.Version)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("FleetWarden distribution server starting", "version", version.Version, "listen", cfg.Listen)
	return s.Run(ctx)
}

// dropToServiceUser re-runs the command as the service user when started as
// root, so the files it creates keep the right owner.
func dropToServiceUser() (bool, error) {
	if os.Geteuid() != 0 {
		return false, nil
	}
	if _, err := user.Lookup(serviceUser); err != nil {
		return false, nil
	}
	self, err := os.Executable()
	if err != nil {
		return false, err
	}
	cmd := exec.Command("runuser", append([]string{"-u", serviceUser, "--", self}, os.Args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	return true, err
}

func enroll(args []string) error {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	server := fs.String("server", "", "address of the management server, as agents use it")
	token := fs.String("token", "", "one-time token from Admin > Distribution servers")
	_ = fs.Parse(args)
	if *server == "" || *token == "" {
		return errors.New("usage: fleetwarden-distribution enroll --server URL --token TOKEN")
	}
	wasRoot := os.Geteuid() == 0
	if done, err := dropToServiceUser(); done || err != nil {
		if err == nil && wasRoot {
			_ = exec.Command("systemctl", "try-restart", "fleetwarden-distribution.service").Run()
		}
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	id, err := distribution.Enroll(context.Background(), cfg, *server, *token, version.Version)
	if err != nil {
		return err
	}
	fmt.Printf("Enrolled as %q with %s.\n", id.Name, id.Server)
	return nil
}

func status() error {
	if done, err := dropToServiceUser(); done || err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	id, ok, err := distribution.LoadIdentity(cfg)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("Not enrolled. Create the server under Admin > Distribution servers and run the install command shown there.")
		return nil
	}
	fmt.Printf("Enrolled as %q (%s) with %s since %s.\n", id.Name, id.ID, id.Server, id.EnrolledAt.Format("2006-01-02 15:04"))
	return nil
}
