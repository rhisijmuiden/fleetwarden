// Package reports renders FleetWarden reports as PDF (in the house style or
// the white-label branding) and CSV.
package reports

import (
	"bytes"
	"encoding/csv"
	"time"
)

// Tones colour a value or cell; empty is neutral.
const (
	ToneSuccess = "success"
	ToneWarning = "warning"
	ToneDanger  = "danger"
	ToneMuted   = "muted"
)

// Stat is a key figure, shown as a tile.
type Stat struct {
	Label string
	Value string
	Sub   string
	Tone  string
}

// Column is a table column. Width is relative to the other columns.
type Column struct {
	Label string
	Width float64
	Right bool // align right (numbers)
}

// Cell is one table value.
type Cell struct {
	Text string
	Tone string
}

// Table is a titled table.
type Table struct {
	Title   string
	Columns []Column
	Rows    [][]Cell
	Empty   string // shown when there are no rows
	Note    string // shown under the table
}

// Section is a part of a report: key figures and tables.
type Section struct {
	// Heading starts a new report inside a document with several reports.
	Heading bool
	Title   string
	Text    string
	Stats   []Stat
	Tables  []Table
}

// Document is a complete report.
type Document struct {
	Title     string
	Subtitle  string
	Product   string
	Color     string // brand colour, #rrggbb
	Logo      []byte // PNG or JPEG; other formats are left out
	Lang      string
	Generated time.Time
	Sections  []Section
}

// CSV writes rows as CSV for spreadsheets: UTF-8 with a byte order mark,
// separated by semicolons for Dutch (as Dutch Excel expects) and commas
// otherwise.
func CSV(lang string, header []string, rows [][]string) []byte {
	var b bytes.Buffer
	b.WriteString("\ufeff")
	w := csv.NewWriter(&b)
	if lang == "nl" {
		w.Comma = ';'
	}
	w.UseCRLF = true
	_ = w.Write(header)
	for _, r := range rows {
		_ = w.Write(sanitizeCSV(r))
	}
	w.Flush()
	return b.Bytes()
}

// sanitizeCSV stops spreadsheet formulas in values that come from devices
// (a program named "=HYPERLINK(...)").
func sanitizeCSV(r []string) []string {
	out := make([]string, len(r))
	for i, v := range r {
		if v != "" && (v[0] == '=' || v[0] == '+' || v[0] == '@' || v[0] == '\t' || v[0] == '\r' ||
			(v[0] == '-' && len(v) > 1 && (v[1] < '0' || v[1] > '9'))) {
			v = "'" + v
		}
		out[i] = v
	}
	return out
}
