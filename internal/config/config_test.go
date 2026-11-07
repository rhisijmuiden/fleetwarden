package config

import "testing"

func TestParsePrefixes(t *testing.T) {
	got, err := ParsePrefixes("127.0.0.1, 10.0.0.0/8 ,::1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1/32", "10.0.0.0/8", "::1/128"}
	if len(got) != len(want) {
		t.Fatalf("got %d prefixes, want %d", len(got), len(want))
	}
	for i, p := range got {
		if p.String() != want[i] {
			t.Errorf("prefix %d = %s, want %s", i, p, want[i])
		}
	}
	if _, err := ParsePrefixes("not-an-ip"); err == nil {
		t.Error("expected error for invalid input")
	}
}
