package drivercat

import (
	"os"
	"testing"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestKey(t *testing.T) {
	for _, c := range []struct {
		p           Platform
		vendor, key string
	}{
		{Platform{Manufacturer: "Dell Inc.", SKU: "0a3e"}, Dell, "0A3E"},
		{Platform{Manufacturer: "Dell Inc.", SKU: "Not specified"}, Dell, ""},
		{Platform{Manufacturer: "HP", Baseboard: "8723"}, HP, "8723"},
		{Platform{Manufacturer: "Hewlett-Packard", Baseboard: "22DA"}, HP, "22DA"},
		{Platform{Manufacturer: "LENOVO", Model: "21AHCTO1WW"}, Lenovo, "21AH"},
		{Platform{Manufacturer: "Microsoft Corporation", Model: "Virtual Machine"}, "", ""},
	} {
		v, k := Key(c.p)
		if v != c.vendor || k != c.key {
			t.Errorf("%+v: %q %q, want %q %q", c.p, v, k, c.vendor, c.key)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.21.0", "1.23.0", -1}, {"2.10.1", "2.9.0", 1}, {"01.25.00", "01.25.00", 0}, {"A10", "A09", 1}, {"1.2", "1.2.0", 0},
	} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("%s vs %s: %d", c.a, c.b, got)
		}
	}
}

func TestDell(t *testing.T) {
	idx, err := ParseDellIndex(read(t, "dell-index.xml"))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := idx["075D"]
	if !ok || m.Path == "" || len(m.SHA256) != 64 {
		t.Fatalf("075D: %+v (%d models)", m, len(idx))
	}
	items, err := ParseDellModel(read(t, "dell-model.xml"), "075D")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("%d BIOS items: %+v", len(items), items)
	}
	for _, it := range items {
		if it.URL[:len(DellBase)] != DellBase || len(it.SHA256) != 64 || it.Size == 0 || it.Name == "" || it.Version == "" {
			t.Errorf("incomplete: %+v", it)
		}
	}
	best, ok := Best(Dell, "1.0.3", items)
	if !ok || best.Version != "2.2.0" {
		t.Errorf("best for 1.0.3: %+v %v", best, ok)
	}
	if _, ok := Best(Dell, "2.2.0", items); ok {
		t.Error("an update for the newest BIOS")
	}
	if _, ok := Best(Dell, "", items); ok {
		t.Error("an update without a BIOS version")
	}
}

func TestHP(t *testing.T) {
	items, err := ParseHPRef(read(t, "hp-ref.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("%d items: %+v", len(items), items)
	}
	it := items[0]
	if it.ID != "sp174024" || it.Version != "01.25.00" || it.URL != "https://ftp.hp.com/pub/softpaq/sp174001-174500/sp174024.exe" ||
		len(it.SHA256) != 64 || it.Command != "HpFirmwareUpdRec.exe -b -s" || len(it.Families) != 2 {
		t.Errorf("item: %+v", it)
	}
	if !it.Applies(HP, "S70 Ver. 01.23.00") || it.Applies(HP, "S70 Ver. 01.25.00") || it.Applies(HP, "T76 Ver. 01.10.00") {
		t.Error("applies")
	}
}

func TestLenovo(t *testing.T) {
	refs, err := ParseLenovoCatalog(read(t, "lenovo-catalog.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Location != "https://download.lenovo.com/pccbbs/mobiles/n3buj24w_2_.xml" || len(refs[0].SHA256) != 64 {
		t.Fatalf("refs: %+v", refs)
	}
	it, err := ParseLenovoPackage(read(t, "lenovo-package.xml"), refs[0].Location)
	if err != nil {
		t.Fatal(err)
	}
	if it.ID != "n3buj24w" || it.URL != "https://download.lenovo.com/pccbbs/mobiles/n3buj24w.exe" || it.Size != 22561432 ||
		it.Command != `%PACKAGEPATH%\winuptp.exe -s` || len(it.ExitCodes) != 2 {
		t.Errorf("item: %+v", it)
	}
	for bios, want := range map[string]bool{
		"N3BET65W (1.45 )": true, "N3BET70W (1.50 )": false, "N3MET20W (1.20 )": true, "N3MET29W (1.29 )": false, "R1EET60W (1.60 )": false,
	} {
		if got := it.Applies(Lenovo, bios); got != want {
			t.Errorf("%s: %v", bios, got)
		}
	}
}

func TestHPOSVersions(t *testing.T) {
	v := HPOSVersions(Platform{OSName: "Microsoft Windows 10 Pro", OSVersion: "22H2"})
	if v[0] != "10.0.22h2" {
		t.Errorf("%v", v)
	}
	if v := HPOSVersions(Platform{OSName: "Microsoft Windows 11 Pro", OSVersion: "24H2"}); v[0] != "11.0.24h2" || len(v) != 4 {
		t.Errorf("%v", v)
	}
}
