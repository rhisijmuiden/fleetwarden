package reports

import (
	"bytes"
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"
)

// Inter, the typeface of the house style (SIL Open Font License, see fonts/OFL.txt).
var (
	//go:embed fonts/Inter-Regular.ttf
	interRegular []byte
	//go:embed fonts/Inter-SemiBold.ttf
	interSemiBold []byte
)

type rgb struct{ r, g, b int }

func hexRGB(s string, fallback rgb) rgb {
	if len(s) != 7 || s[0] != '#' {
		return fallback
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return fallback
	}
	return rgb{int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)}
}

var (
	colFg      = rgb{15, 23, 41}
	colMuted   = rgb{91, 101, 120}
	colLine    = rgb{227, 230, 235}
	colSurface = rgb{246, 247, 249}
	colPrimary = rgb{47, 91, 234}
	toneRGB    = map[string]rgb{ToneSuccess: {21, 128, 61}, ToneWarning: {180, 83, 9}, ToneDanger: {185, 28, 28}, ToneMuted: colMuted}
)

const (
	pageMargin = 14.0
	rowHeight  = 6.2
)

type pdfWriter struct {
	*fpdf.Fpdf
	doc     Document
	primary rgb
	width   float64 // usable width
}

func (p *pdfWriter) text(c rgb) { p.SetTextColor(c.r, c.g, c.b) }
func (p *pdfWriter) fill(c rgb) { p.SetFillColor(c.r, c.g, c.b) }
func (p *pdfWriter) draw(c rgb) { p.SetDrawColor(c.r, c.g, c.b) }
func (p *pdfWriter) font(bold bool, size float64) {
	style := ""
	if bold {
		style = "B"
	}
	p.SetFont("Inter", style, size)
}

// fit shortens s with an ellipsis so it fits in w millimetres.
func (p *pdfWriter) fit(s string, w float64) string {
	if p.GetStringWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && p.GetStringWidth(string(r)+"…") > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// PDF renders the document on landscape A4 pages.
func (d Document) PDF() ([]byte, error) {
	f := fpdf.New("L", "mm", "A4", "")
	f.AddUTF8FontFromBytes("Inter", "", interRegular)
	f.AddUTF8FontFromBytes("Inter", "B", interSemiBold)
	f.SetMargins(pageMargin, 24, pageMargin)
	f.SetAutoPageBreak(true, 16)
	f.AliasNbPages("{nb}")
	f.SetTitle(d.Title, true)
	f.SetAuthor(d.Product, true)
	f.SetCreator(d.Product, true)
	f.SetCreationDate(d.Generated)
	f.SetModificationDate(d.Generated)
	pw, _ := f.GetPageSize()
	p := &pdfWriter{Fpdf: f, doc: d, primary: hexRGB(d.Color, colPrimary), width: pw - 2*pageMargin}

	logo := ""
	var logoW float64
	if isPNG(d.Logo) || isJPEG(d.Logo) {
		typ := "PNG"
		if isJPEG(d.Logo) {
			typ = "JPG"
		}
		if info := f.RegisterImageOptionsReader("logo", fpdf.ImageOptions{ImageType: typ}, bytes.NewReader(d.Logo)); info != nil && f.Ok() && info.Height() > 0 {
			logo, logoW = "logo", 8*info.Width()/info.Height()
			logoW = min(logoW, 40)
		}
		if !f.Ok() { // an unreadable logo is left out, not fatal
			f.ClearError()
			logo = ""
		}
	}

	f.SetHeaderFunc(func() {
		p.fill(p.primary)
		f.Rect(0, 0, pw, 3, "F")
		x := pageMargin
		if logo != "" {
			f.ImageOptions(logo, x, 8, logoW, 8, false, fpdf.ImageOptions{}, 0, "")
			x += logoW + 3
		}
		p.font(true, 11)
		p.text(colFg)
		f.SetXY(x, 8)
		f.CellFormat(p.width/2, 8, d.Product, "", 0, "LM", false, 0, "")
		p.font(false, 9)
		p.text(colMuted)
		f.SetXY(pageMargin+p.width/2, 8)
		f.CellFormat(p.width/2, 8, d.Title, "", 0, "RM", false, 0, "")
		p.draw(colLine)
		f.Line(pageMargin, 19, pw-pageMargin, 19)
		f.SetY(24)
	})
	f.SetFooterFunc(func() {
		f.SetY(-11)
		p.font(false, 8)
		p.text(colMuted)
		f.CellFormat(p.width/2, 5, fmt.Sprintf(tr(d.Lang, "pdf.generated"), formatDateTime(d.Lang, d.Generated)), "", 0, "L", false, 0, "")
		f.CellFormat(p.width/2, 5, fmt.Sprintf(tr(d.Lang, "pdf.page"), f.PageNo(), "{nb}"), "", 0, "R", false, 0, "")
	})

	f.AddPage()
	p.font(true, 20)
	p.text(colFg)
	f.CellFormat(p.width, 10, d.Title, "", 1, "L", false, 0, "")
	if d.Subtitle != "" {
		p.font(false, 10)
		p.text(colMuted)
		f.MultiCell(p.width, 5, d.Subtitle, "", "L", false)
	}
	f.Ln(3)
	for i, s := range d.Sections {
		if s.Heading {
			if i > 0 {
				f.AddPage()
			}
			p.font(true, 16)
			p.text(p.primary)
			f.CellFormat(p.width, 10, s.Title, "", 1, "L", false, 0, "")
			continue
		}
		if i > 0 && !d.Sections[i-1].Heading {
			f.Ln(4)
		}
		p.section(s)
	}
	var out bytes.Buffer
	if err := f.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (p *pdfWriter) section(s Section) {
	f := p.Fpdf
	_, ph := f.GetPageSize()
	if f.GetY() > ph-50 {
		f.AddPage()
	}
	if s.Title != "" {
		p.font(true, 13)
		p.text(colFg)
		f.CellFormat(p.width, 8, s.Title, "", 1, "L", false, 0, "")
	}
	if s.Text != "" {
		p.font(false, 9.5)
		p.text(colMuted)
		f.MultiCell(p.width, 4.8, s.Text, "", "L", false)
		f.Ln(1)
	}
	if len(s.Stats) > 0 {
		p.stats(s.Stats)
	}
	for _, t := range s.Tables {
		p.table(t)
	}
}

func (p *pdfWriter) stats(stats []Stat) {
	f := p.Fpdf
	const gap, h = 4.0, 21.0
	perRow := 4
	if len(stats) == 5 {
		perRow = 5
	}
	w := (p.width - gap*float64(perRow-1)) / float64(perRow)
	_, ph := f.GetPageSize()
	for i := 0; i < len(stats); i += perRow {
		if f.GetY()+h > ph-18 {
			f.AddPage()
		}
		y := f.GetY()
		for j := i; j < min(i+perRow, len(stats)); j++ {
			st := stats[j]
			x := pageMargin + float64(j-i)*(w+gap)
			p.draw(colLine)
			p.fill(rgb{255, 255, 255})
			f.RoundedRect(x, y, w, h, 2, "1234", "D")
			p.font(false, 8.5)
			p.text(colMuted)
			f.SetXY(x+4, y+2.5)
			f.CellFormat(w-8, 4.5, p.fit(st.Label, w-8), "", 0, "L", false, 0, "")
			p.font(true, 16)
			c := colFg
			if tc, ok := toneRGB[st.Tone]; ok {
				c = tc
			}
			p.text(c)
			f.SetXY(x+4, y+7.5)
			f.CellFormat(w-8, 7, p.fit(st.Value, w-8), "", 0, "L", false, 0, "")
			if st.Sub != "" {
				p.font(false, 7.5)
				p.text(colMuted)
				f.SetXY(x+4, y+15)
				f.CellFormat(w-8, 4, p.fit(st.Sub, w-8), "", 0, "L", false, 0, "")
			}
		}
		f.SetXY(pageMargin, y+h+gap)
	}
}

func (p *pdfWriter) table(t Table) {
	f := p.Fpdf
	_, ph := f.GetPageSize()
	if f.GetY() > ph-40 {
		f.AddPage()
	}
	if t.Title != "" {
		p.font(true, 10.5)
		p.text(colFg)
		f.CellFormat(p.width, 7, t.Title, "", 1, "L", false, 0, "")
	}
	total := 0.0
	for _, c := range t.Columns {
		total += max(c.Width, 0.5)
	}
	widths := make([]float64, len(t.Columns))
	for i, c := range t.Columns {
		widths[i] = p.width * max(c.Width, 0.5) / total
	}
	header := func() {
		p.font(true, 8)
		p.text(colMuted)
		p.fill(colSurface)
		p.draw(colLine)
		for i, c := range t.Columns {
			align := "LM"
			if c.Right {
				align = "RM"
			}
			f.CellFormat(widths[i], rowHeight+0.6, " "+p.fit(strings.ToUpper(c.Label), widths[i]-2)+" ", "B", 0, align, true, 0, "")
		}
		f.Ln(-1)
	}
	header()
	if len(t.Rows) == 0 {
		p.font(false, 9)
		p.text(colMuted)
		msg := t.Empty
		if msg == "" {
			msg = tr(p.doc.Lang, "pdf.noRows")
		}
		f.CellFormat(p.width, rowHeight+1, " "+msg, "", 1, "L", false, 0, "")
	}
	for n, row := range t.Rows {
		if f.GetY()+rowHeight > ph-16 {
			f.AddPage()
			header()
		}
		zebra := n%2 == 1
		if zebra {
			p.fill(rgb{250, 251, 252})
		}
		for i := range t.Columns {
			var c Cell
			if i < len(row) {
				c = row[i]
			}
			p.font(c.Tone == ToneDanger || c.Tone == ToneWarning || c.Tone == ToneSuccess, 8)
			col := colFg
			if tc, ok := toneRGB[c.Tone]; ok {
				col = tc
			}
			p.text(col)
			align := "LM"
			if t.Columns[i].Right {
				align = "RM"
			}
			f.CellFormat(widths[i], rowHeight, " "+p.fit(c.Text, widths[i]-2)+" ", "", 0, align, zebra, 0, "")
		}
		f.Ln(-1)
		p.draw(colLine)
		f.Line(pageMargin, f.GetY(), pageMargin+p.width, f.GetY())
	}
	if t.Note != "" {
		f.Ln(1.5)
		p.font(false, 8)
		p.text(colMuted)
		f.MultiCell(p.width, 4.2, t.Note, "", "L", false)
	}
	f.Ln(4)
}

func isPNG(b []byte) bool  { return len(b) > 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n" }
func isJPEG(b []byte) bool { return len(b) > 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff }
