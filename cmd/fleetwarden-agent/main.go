// Command fleetwarden-agent is the FleetWarden endpoint agent for Windows and Linux.
//
//	fleetwarden-agent run                      run the agent (the service does this)
//	fleetwarden-agent configure --server URL --token TOKEN [--group NAME]
//	                                           store the server and enrollment token
//	fleetwarden-agent configure --from-file enroll.json
//	fleetwarden-agent enroll                   enroll now (normally done by run)
//	fleetwarden-agent once                     enroll, send one inventory and exit
//	fleetwarden-agent status                   show the enrollment
//	fleetwarden-agent inventory                print the inventory as JSON
//	fleetwarden-agent test-connection --server URL
//	                                           check a server address (installer wizard)
//	fleetwarden-agent forget                   remove the enrollment (uninstall)
//	fleetwarden-agent update-helper --plan F   install a self-update (started by the agent)
//	fleetwarden-agent -version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agent"
	"github.com/rhisijmuiden/fleetwarden/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print version information and exit")
	flag.Parse()
	v := version.Get()
	if *showVersion {
		fmt.Printf("fleetwarden-agent %s (%s, %s channel, %s)\n", v.Version, v.Commit, v.Channel, v.Platform)
		return
	}
	// The update helper is started by the service but is not one itself.
	if flag.Arg(0) == "update-helper" {
		if err := updateHelper(flag.Args()[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "fleetwarden-agent:", err)
			os.Exit(1)
		}
		return
	}
	if isWindowsService() {
		runService()
		return
	}
	cmd, args := flag.Arg(0), flag.Args()
	if len(args) > 0 {
		args = args[1:]
	}
	var err error
	switch cmd {
	case "run", "":
		err = runForeground(false)
	case "once":
		err = runForeground(true)
	case "configure":
		err = configure(args)
	case "enroll":
		a := newAgent(false)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err = a.Enroll(ctx)
	case "status":
		err = status()
	case "inventory":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		inv, ierr := agent.CollectInventory(ctx)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(inv)
		err = ierr
	case "patch-scan":
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		res, serr := agent.ScanPatches(ctx)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
		err = serr
	case "test-connection":
		err = testConnection(args)
	case "forget":
		err = forget()
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fleetwarden-agent:", err)
		os.Exit(1)
	}
}

func newAgent(once bool) *agent.Agent {
	return &agent.Agent{ConfigPath: agent.ConfigPath(), Log: slog.New(slog.NewTextHandler(os.Stderr, nil)), Once: once}
}

func runForeground(once bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := newAgent(once).Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	if errors.Is(err, agent.ErrRemoved) {
		fmt.Fprintln(os.Stderr, "fleetwarden-agent:", err)
		return nil // a clean exit, so systemd does not restart the agent
	}
	return err
}

type enrollFile struct {
	Server string `json:"server"`
	Token  string `json:"token"`
	Group  string `json:"group"`
}

func configure(args []string) error {
	fs := flag.NewFlagSet("configure", flag.ContinueOnError)
	server := fs.String("server", "", "management server address, e.g. https://em.example.com")
	token := fs.String("token", "", "enrollment token")
	group := fs.String("group", "", "device group name (optional)")
	fromFile := fs.String("from-file", "", "read server and token from a JSON file")
	force := fs.Bool("force", false, "replace an existing enrollment")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *fromFile != "" {
		raw, err := os.ReadFile(*fromFile)
		if err != nil {
			return err
		}
		var f enrollFile
		if err := json.Unmarshal(raw, &f); err != nil {
			return fmt.Errorf("%s: %w", *fromFile, err)
		}
		if *server == "" {
			*server = f.Server
		}
		if *token == "" {
			*token = f.Token
		}
		if *group == "" {
			*group = f.Group
		}
	}
	path := agent.ConfigPath()
	cfg, err := agent.LoadConfig(path)
	if err != nil {
		return err
	}
	if cfg.Enrolled() && !*force {
		fmt.Println("Already enrolled as device", cfg.DeviceID, "with", cfg.Server, "(use --force to enroll again).")
		return nil
	}
	srv, err := agent.NormalizeServer(*server)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*token) == "" {
		return errors.New("--token is required")
	}
	cfg = agent.Config{Server: srv, Token: strings.TrimSpace(*token), Group: strings.TrimSpace(*group)}
	if err := agent.SaveConfig(path, cfg); err != nil {
		return err
	}
	fmt.Println("Configured for", srv, "- the agent enrolls when the service starts.")
	return nil
}

func status() error {
	cfg, err := agent.LoadConfig(agent.ConfigPath())
	if err != nil {
		return err
	}
	switch {
	case cfg.Enrolled():
		fmt.Printf("Enrolled as device %s with %s\n", cfg.DeviceID, cfg.Server)
	case cfg.Server != "":
		fmt.Printf("Waiting to enroll with %s\n", cfg.Server)
	default:
		fmt.Println("Not configured. Run: fleetwarden-agent configure --server URL --token TOKEN")
	}
	return nil
}

func testConnection(args []string) error {
	fs := flag.NewFlagSet("test-connection", flag.ContinueOnError)
	server := fs.String("server", "", "management server address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res := agent.TestConnection(ctx, *server)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
	if !res.OK {
		return errors.New(res.Error)
	}
	return nil
}

func forget() error {
	path := agent.ConfigPath()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// updateHelper installs a self-update; it logs next to the plan.
func updateHelper(args []string) error {
	fs := flag.NewFlagSet("update-helper", flag.ContinueOnError)
	plan := fs.String("plan", "", "update plan written by the agent")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *plan == "" {
		return errors.New("--plan is required")
	}
	logf, err := os.OpenFile(filepath.Join(filepath.Dir(*plan), "helper.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	return agent.RunUpdateHelper(ctx, *plan, slog.New(slog.NewTextHandler(logf, nil)))
}
