// Command fleetwarden-agent is the FleetWarden endpoint agent for Windows and Linux.
//
// Phase 0 only provides the binary skeleton and version reporting. Enrollment,
// inventory and the service wrapper follow in phase 3.
package main

import (
	"flag"
	"fmt"

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
	fmt.Println("fleetwarden-agent: enrollment and service mode arrive in phase 3. Use -version for build info.")
}
