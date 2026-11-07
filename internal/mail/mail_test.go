package mail

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	ok := Config{Enabled: true, Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS, Auth: AuthPlain,
		Username: "u", FromAddress: "fw@example.com"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Security = SecurityNone
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "security") {
		t.Errorf("password over plain text accepted: %v", err)
	}
	bad = ok
	bad.FromAddress = "nope"
	if bad.Validate() == nil {
		t.Error("bad from address accepted")
	}
}

func TestRender(t *testing.T) {
	m := Render("test", "nl", "a@example.com", Brand{Product: "Acme <EM>", Color: "red"}, nil)
	if m.Subject != "Testmail van Acme <EM>" {
		t.Errorf("subject %q", m.Subject)
	}
	if strings.Contains(m.HTML, "<EM>") || !strings.Contains(m.HTML, "#2f5bea") {
		t.Error("product name not escaped or bad colour used")
	}
	en := Render("update_failed", "xx", "a@example.com", Brand{}, map[string]string{"version": "1.2.3", "detail": "boom"})
	if !strings.Contains(en.Text, "1.2.3") || !strings.Contains(en.Subject, "FleetWarden") {
		t.Errorf("fallback render: %+v", en)
	}
}
