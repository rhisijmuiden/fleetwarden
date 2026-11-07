package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"time"
)

// The tray protocol: one JSON object per line over a local connection (a
// named pipe on Windows).
//
//	tray  -> agent  {"type":"hello","lang":"nl-NL"}
//	agent -> tray   {"type":"prompt","prompt":{...Prompt}}
//	tray  -> agent  {"type":"answer","id":"12-1","postpone":true}
//	agent -> tray   {"type":"close","id":"12-1"}
//
// Every connected tray shows the prompt; the first answer counts. A tray
// that connects while a prompt is open gets it too.
type trayMsg struct {
	Type     string  `json:"type"`
	Prompt   *Prompt `json:"prompt,omitempty"`
	ID       string  `json:"id,omitempty"`
	Postpone bool    `json:"postpone,omitempty"`
	Lang     string  `json:"lang,omitempty"`
}

// trayHub implements Notifier for tray apps that connect to the agent.
type trayHub struct {
	log *slog.Logger
	// sessions lists the sessions with a logged-on user; launch starts the
	// tray app in one. Both are optional.
	sessions func() []uint32
	launch   func(session uint32) error
	// session returns the session of a connected tray (0 when unknown).
	session func(c net.Conn) uint32
	// joinWait is how long Ask waits for a launched tray to connect.
	joinWait time.Duration
	// grace is the extra time a tray gets after the countdown to answer.
	grace time.Duration

	mu       sync.Mutex
	clients  map[*trayClient]struct{}
	joined   chan struct{} // closed when a tray says hello
	current  *Prompt
	answers  chan trayMsg
	launched map[uint32]time.Time
}

type trayClient struct {
	conn    net.Conn
	session uint32
	wmu     sync.Mutex
}

func newTrayHub(log *slog.Logger) *trayHub {
	return &trayHub{
		log: log, joinWait: 30 * time.Second, grace: 15 * time.Second,
		clients: map[*trayClient]struct{}{}, joined: make(chan struct{}),
		answers: make(chan trayMsg, 16), launched: map[uint32]time.Time{},
	}
}

func (c *trayClient) send(m trayMsg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = c.conn.Write(append(b, '\n'))
	return err
}

// Serve accepts tray connections until ctx ends or the listener fails.
func (h *trayHub) Serve(ctx context.Context, l net.Listener) {
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() == nil && h.log != nil {
				h.log.Warn("tray listener stopped", "err", err)
			}
			return
		}
		go h.handle(conn)
	}
}

func (h *trayHub) handle(conn net.Conn) {
	defer conn.Close()
	c := &trayClient{conn: conn}
	if h.session != nil {
		c.session = h.session(conn)
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 4096), 64<<10)
	registered := false
	defer func() {
		if registered {
			h.mu.Lock()
			delete(h.clients, c)
			h.mu.Unlock()
		}
	}()
	for sc.Scan() {
		var m trayMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Type {
		case "hello":
			if registered {
				continue
			}
			registered = true
			h.mu.Lock()
			h.clients[c] = struct{}{}
			delete(h.launched, c.session)
			cur := h.current
			close(h.joined)
			h.joined = make(chan struct{})
			h.mu.Unlock()
			if cur != nil {
				_ = c.send(trayMsg{Type: "prompt", Prompt: cur})
			}
		case "answer":
			if registered {
				select {
				case h.answers <- m:
				default:
				}
			}
		}
	}
}

func (h *trayHub) snapshot() ([]*trayClient, chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*trayClient, 0, len(h.clients))
	for c := range h.clients {
		out = append(out, c)
	}
	return out, h.joined
}

// launchMissing starts the tray app in sessions with a user but no tray. A
// session gets a new attempt at most once a minute.
func (h *trayHub) launchMissing() (users int) {
	if h.sessions == nil {
		return 0
	}
	sessions := h.sessions()
	clients, _ := h.snapshot()
	have := map[uint32]bool{}
	for _, c := range clients {
		have[c.session] = true
	}
	for _, s := range sessions {
		if have[s] || h.launch == nil {
			continue
		}
		h.mu.Lock()
		recent := time.Since(h.launched[s]) < time.Minute
		if !recent {
			h.launched[s] = time.Now()
		}
		h.mu.Unlock()
		if recent {
			continue
		}
		if err := h.launch(s); err != nil && h.log != nil {
			h.log.Warn("start tray app", "session", s, "err", err)
		}
	}
	return len(sessions)
}

// Ask shows p on every tray and waits for the first answer. Without an
// answer before the countdown (plus a grace period) ends, the job goes
// ahead.
func (h *trayHub) Ask(ctx context.Context, p Prompt) (Answer, error) {
	users := h.launchMissing()
	clients, joined := h.snapshot()
	if len(clients) == 0 {
		if h.sessions != nil && users == 0 {
			return Answer{}, ErrNoUser
		}
		t := time.NewTimer(h.joinWait)
		select {
		case <-joined:
			t.Stop()
		case <-t.C:
			return Answer{}, ErrNoUser
		case <-ctx.Done():
			t.Stop()
			return Answer{}, ctx.Err()
		}
	}

	h.mu.Lock()
	h.current = &p
drain: // stale answers of an earlier prompt
	for {
		select {
		case <-h.answers:
		default:
			break drain
		}
	}
	h.mu.Unlock()
	defer h.closePrompt(p.ID)
	clients, _ = h.snapshot()
	for _, c := range clients {
		if err := c.send(trayMsg{Type: "prompt", Prompt: &p}); err != nil {
			_ = c.conn.Close()
		}
	}

	t := time.NewTimer(time.Duration(p.CountdownSeconds)*time.Second + h.grace)
	defer t.Stop()
	for {
		select {
		case m := <-h.answers:
			if m.ID == p.ID {
				return Answer{Postpone: m.Postpone && p.CanPostpone}, nil
			}
		case <-t.C:
			return Answer{}, nil
		case <-ctx.Done():
			return Answer{}, ctx.Err()
		}
	}
}

func (h *trayHub) closePrompt(id string) {
	h.mu.Lock()
	h.current = nil
	h.mu.Unlock()
	clients, _ := h.snapshot()
	for _, c := range clients {
		_ = c.send(trayMsg{Type: "close", ID: id})
	}
}
