package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTP accepts one message without TLS or authentication and returns the DATA.
func fakeSMTP(t *testing.T) (port int, data <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	out := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		w := func(s string) { c.Write([]byte(s + "\r\n")) }
		w("220 fake ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				w("250-fake")
				w("250 8BITMIME")
			case cmd == "DATA":
				w("354 go ahead")
				var b strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				out <- b.String()
				w("250 queued")
			case cmd == "QUIT":
				w("221 bye")
				return
			default:
				w("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, out
}

func TestSendPlainRelay(t *testing.T) {
	port, data := fakeSMTP(t)
	cfg := Config{Enabled: true, Host: "127.0.0.1", Port: port, Security: SecurityNone, Auth: AuthNone,
		FromName: "Fleet", FromAddress: "fw@example.com"}
	msg := Render("test", "en", "admin@example.com", Brand{Product: "Fleet"}, nil)
	msg.Attachments = []Attachment{{Name: "report.csv", ContentType: "text/csv", Data: []byte("a,b\n1,2\n")}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := (&Sender{Timeout: 5 * time.Second}).Send(ctx, cfg, msg); err != nil {
		t.Fatal(err)
	}
	got := <-data
	for _, want := range []string{"Subject: Fleet test e-mail", "To: <admin@example.com>", "text/html", "text/plain",
		`filename="report.csv"`, "Content-Type: text/csv"} {
		if !strings.Contains(got, want) {
			t.Errorf("message lacks %q:\n%s", want, got)
		}
	}
}

func TestSendRequiresTLSForSTARTTLS(t *testing.T) {
	port, _ := fakeSMTP(t) // offers no STARTTLS
	cfg := Config{Enabled: true, Host: "127.0.0.1", Port: port, Security: SecuritySTARTTLS, Auth: AuthNone, FromAddress: "fw@example.com"}
	err := (&Sender{Timeout: 5 * time.Second}).Send(context.Background(), cfg, Message{To: "a@example.com", Subject: "x", Text: "x"})
	if err == nil {
		t.Fatal("sent without TLS although STARTTLS is mandatory")
	}
}
