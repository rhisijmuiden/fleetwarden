package agent

import (
	"slices"
	"testing"
)

func TestCandidates(t *testing.T) {
	c := Config{Server: "https://gw.example.com", Endpoints: []string{"https://fleet.lan", "https://gw.example.com"}}
	if got := c.Candidates(); !slices.Equal(got, []string{"https://fleet.lan", "https://gw.example.com"}) {
		t.Errorf("candidates: %v", got)
	}
	if got := (Config{Server: "https://fleet.lan"}).Candidates(); !slices.Equal(got, []string{"https://fleet.lan"}) {
		t.Errorf("no endpoints: %v", got)
	}
	got := cleanEndpoints([]string{"https://a.example.com/", "fleet.lan", "ftp://x", "https://u@b.example.com", "http://10.0.0.1:8080"})
	if !slices.Equal(got, []string{"https://a.example.com", "http://10.0.0.1:8080"}) {
		t.Errorf("clean: %v", got)
	}
}
