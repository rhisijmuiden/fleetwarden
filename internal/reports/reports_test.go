package reports

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func sampleDoc(lang string) Document {
	rows := [][]Cell{}
	for i := 0; i < 70; i++ {
		rows = append(rows, []Cell{{Text: "PC-ÉÈ-" + strings.Repeat("x", i%9)}, {Text: "Werkplekken"}, {Text: "Mozilla Firefox (x64 nl) with a very long name that will not fit in this column"}, {Text: "158.0", Tone: ToneWarning}, {Text: "159.0"}})
	}
	return Document{
		Title: T(lang, "software.title"), Subtitle: T(lang, "scope.all"), Product: "FleetWarden", Color: "#2f5bea", Lang: lang,
		Generated: time.Date(2026, 11, 8, 14, 30, 0, 0, time.UTC),
		Sections: []Section{{Title: T(lang, "software.summary"), Text: T(lang, "software.note"),
			Stats: []Stat{{Label: T(lang, "software.devices"), Value: "12", Tone: ToneWarning}, {Label: "B", Value: "93%", Sub: "12 of 13", Tone: ToneSuccess}, {Label: "C", Value: "0"}, {Label: "D", Value: "1"}, {Label: "E", Value: "5", Tone: ToneDanger}},
			Tables: []Table{{Title: T(lang, "software.byDevice"), Columns: []Column{{Label: T(lang, "col.device"), Width: 2}, {Label: T(lang, "col.group"), Width: 1.5}, {Label: T(lang, "col.program"), Width: 3}, {Label: T(lang, "col.installed"), Width: 1, Right: true}, {Label: T(lang, "col.latest"), Width: 1, Right: true}}, Rows: rows},
				{Title: "Empty", Columns: []Column{{Label: "A"}}, Empty: "Nothing here"}}}},
	}
}

func TestPDF(t *testing.T) {
	for _, lang := range []string{"en", "nl"} {
		b, err := sampleDoc(lang).PDF()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(b, []byte("%PDF-")) || len(b) < 5000 {
			t.Fatalf("not a PDF: %d bytes", len(b))
		}
		if p := os.Getenv("FW_TEST_PDF_OUT"); p != "" {
			os.WriteFile(p+"-"+lang+".pdf", b, 0o644)
		}
	}
}

func TestPDFWithLogo(t *testing.T) {
	d := sampleDoc("en")
	d.Logo = []byte("\x89PNG\r\n\x1a\nnot really a png")
	if _, err := d.PDF(); err != nil {
		t.Fatalf("a broken logo must not fail the report: %v", err)
	}
}

func TestCSV(t *testing.T) {
	got := string(CSV("nl", []string{"Apparaat", "Programma"}, [][]string{{"pc1", "=cmd|' /C calc'!A0"}, {"pc2", "-5"}, {"pc;3", "7-Zip"}}))
	want := "\ufeffApparaat;Programma\r\npc1;'=cmd|' /C calc'!A0\r\npc2;-5\r\n\"pc;3\";7-Zip\r\n"
	if got != want {
		t.Errorf("CSV:\n%q\nwant\n%q", got, want)
	}
	if !strings.HasPrefix(string(CSV("en", []string{"a", "b"}, nil)), "\ufeffa,b") {
		t.Error("English CSV uses commas")
	}
}
