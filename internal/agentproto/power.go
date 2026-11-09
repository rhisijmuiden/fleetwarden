package agentproto

import (
	"errors"
	"net"
	"time"
)

// Power management messages.
//
// Wake-on-LAN: a switched-off computer cannot be reached over the internet,
// so the server asks an online agent in the same network to send the magic
// packet for it (a broadcast does not cross routers). Restart and shut down:
// the agent asks the logged-on user first when the action says so, with a
// countdown and postpones like a patch restart.
const (
	// server -> agent
	TypePowerWake   = "power.wake"   // send magic packets for other devices
	TypePowerAction = "power.action" // restart or shut down this device
	TypePowerConfig = "power.config" // settings the agent applies itself
	// agent -> server
	TypePowerStatus = "power.status"
)

// Power actions.
const (
	PowerWake     = "wake"
	PowerRestart  = "restart"
	PowerShutdown = "shutdown"
)

// ValidPowerAction reports whether a is a known action.
func ValidPowerAction(a string) bool {
	return a == PowerWake || a == PowerRestart || a == PowerShutdown
}

// WakePort is the usual Wake-on-LAN port (discard); 7 (echo) also works.
const WakePort = 9

// WakeTarget is one network adapter to wake: its MAC address and the
// broadcast addresses of its network.
type WakeTarget struct {
	MAC        string   `json:"mac"`
	Broadcasts []string `json:"broadcasts"`
}

// PowerWakeJob asks the agent to send magic packets for other devices.
type PowerWakeJob struct {
	ID      int64        `json:"id"`
	Targets []WakeTarget `json:"targets"`
	Port    int          `json:"port"`
}

// PowerActionJob asks the agent to restart or shut down its device.
type PowerActionJob struct {
	ID     int64  `json:"id"`
	Action string `json:"action"` // restart, shutdown
	// Notify shows the logged-on user a countdown first, with postpones.
	Notify           bool `json:"notify"`
	MaxPostpones     int  `json:"maxPostpones"`
	PostponeMinutes  int  `json:"postponeMinutes"`
	CountdownSeconds int  `json:"countdownSeconds"`
	// SkipIfUserLoggedOn leaves the device alone while someone works on it.
	SkipIfUserLoggedOn bool `json:"skipIfUserLoggedOn,omitempty"`
	// OnlyIfRebootPending restarts only when updates wait for a restart.
	OnlyIfRebootPending bool `json:"onlyIfRebootPending,omitempty"`
}

// PowerConfig carries the power settings the agent applies itself.
type PowerConfig struct {
	// PrepareWake turns on "wake on magic packet" on the wired network
	// adapters and, on Windows, turns off Fast Startup (a computer shut
	// down with Fast Startup often does not wake from the network).
	PrepareWake bool `json:"prepareWake"`
}

// Power job states. The server sets sent and, for a wake, completed when the
// device comes online and failed when it does not.
const (
	PowerSent        = "sent"
	PowerWaitingUser = "waiting-user"
	PowerPostponed   = "postponed"
	PowerExecuting   = "executing" // final from the agent: the restart or shutdown was started
	PowerSkipped     = "skipped"
	PowerCompleted   = "completed"
	PowerFailed      = "failed"
)

// PowerFinished reports whether a power job state is final.
func PowerFinished(state string) bool {
	return state == PowerExecuting || state == PowerSkipped || state == PowerCompleted || state == PowerFailed
}

// PowerStatus reports the progress of a power job. For a wake job it
// reports that the packets were sent (Sent) or failed.
type PowerStatus struct {
	ID             int64     `json:"id"`
	Wake           bool      `json:"wake,omitempty"` // the status of a PowerWakeJob
	State          string    `json:"state"`
	Message        string    `json:"message,omitempty"`
	Sent           int       `json:"sent,omitempty"` // magic packets sent
	Postpones      int       `json:"postpones,omitempty"`
	PostponedUntil time.Time `json:"postponedUntil,omitzero"`
}

// MagicPacket builds the Wake-on-LAN packet for a MAC address: six 0xFF
// bytes and the address sixteen times.
func MagicPacket(mac string) ([]byte, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return nil, err
	}
	if len(hw) != 6 {
		return nil, errors.New("not an Ethernet MAC address")
	}
	p := make([]byte, 0, 102)
	for range 6 {
		p = append(p, 0xff)
	}
	for range 16 {
		p = append(p, hw...)
	}
	return p, nil
}

// WakeableMAC reports whether a MAC address can be woken: a unicast,
// non-zero Ethernet address.
func WakeableMAC(mac string) bool {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 || hw[0]&1 == 1 {
		return false
	}
	for _, b := range hw {
		if b != 0 {
			return true
		}
	}
	return false
}
