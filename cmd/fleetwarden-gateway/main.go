// Command fleetwarden-gateway runs the FleetWarden secure gateway in the DMZ.
//
//	fleetwarden-gateway                 run the gateway
//	fleetwarden-gateway pairing-code    show the pairing code (or the paired server)
//	fleetwarden-gateway reset-pairing   forget the paired server and create a new code
//	fleetwarden-gateway -version        print the version
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"syscall"

	"github.com/rhisijmuiden/fleetwarden/internal/config"
	"github.com/rhisijmuiden/fleetwarden/internal/gateway"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

const serviceUser = "fleetwarden-gateway"

func main() {
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()
	if *showVersion {
		v := version.Get()
		fmt.Printf("fleetwarden-gateway %s (%s, %s channel, built %s)\n", v.Version, v.Commit, v.Channel, v.BuildDate)
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	var err error
	switch cmd := flag.Arg(0); cmd {
	case "":
		err = run(log)
	case "pairing-code", "reset-pairing":
		err = pairing(cmd)
	default:
		err = fmt.Errorf("unknown command %q (commands: pairing-code, reset-pairing)", cmd)
	}
	if err != nil {
		log.Error("fleetwarden-gateway", "err", err)
		os.Exit(1)
	}
}

func loadConfig() (gateway.Config, error) {
	if err := config.LoadEnvFile(gateway.EnvFile); err != nil {
		return gateway.Config{}, err
	}
	return gateway.FromEnv()
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	g, err := gateway.New(cfg, log, version.Version)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("FleetWarden gateway starting", "version", version.Version, "tls", cfg.TLSMode)
	return g.Run(ctx)
}

// dropToServiceUser re-runs the command as the service user when started as
// root, so files it creates keep the right owner.
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
	return true, cmd.Run()
}

func pairing(cmd string) error {
	wasRoot := os.Geteuid() == 0
	if done, err := dropToServiceUser(); done || err != nil {
		if err == nil && cmd == "reset-pairing" && wasRoot {
			// The running gateway keeps its tunnel until it restarts.
			_ = exec.Command("systemctl", "try-restart", "fleetwarden-gateway.service").Run()
		}
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	var info gateway.PairingInfo
	if cmd == "reset-pairing" {
		info, err = gateway.ResetPairing(cfg)
	} else {
		info, err = gateway.ReadPairing(cfg)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Gateway fingerprint: %s\n", info.Fingerprint)
	if info.Paired {
		fmt.Printf("Paired with the management server %s.\n", info.Server)
		fmt.Println("To pair with another server, run: sudo fleetwarden-gateway reset-pairing")
		return nil
	}
	fmt.Printf("Pairing code:        %s\n", info.Code)
	fmt.Println("Enter this code in the web interface under Admin > Secure gateway > Add gateway.")
	if cmd == "reset-pairing" && os.Geteuid() != 0 {
		fmt.Println("Restart the gateway to close the old tunnel: sudo systemctl restart fleetwarden-gateway")
	}
	return nil
}
