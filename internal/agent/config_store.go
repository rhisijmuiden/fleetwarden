package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// canonical returns a value as JSON with sorted keys and float numbers, so
// values decoded from a file compare equal to the ones the server sent.
func canonical(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var x any
	if json.Unmarshal(raw, &x) != nil {
		return string(raw)
	}
	raw, _ = json.Marshal(x)
	return string(raw)
}

// jsonPolicyFile is one JSON policy file. Wrapped files keep the policies
// under a "policies" member (Firefox); others hold them at the top
// (Chrome and Edge, in a file of their own).
type jsonPolicyFile struct {
	path    string
	wrapped bool
	doc     map[string]any // the whole file
	pol     map[string]any // the policies in it
	dirty   bool
	existed bool
}

func openJSONPolicyFile(path string, wrapped bool) (*jsonPolicyFile, error) {
	f := &jsonPolicyFile{path: path, wrapped: wrapped, doc: map[string]any{}}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		f.existed = true
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &f.doc); err != nil {
				return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
			}
		}
	}
	if f.doc == nil {
		f.doc = map[string]any{}
	}
	f.pol = f.doc
	if wrapped {
		p, _ := f.doc["policies"].(map[string]any)
		if p == nil {
			p = map[string]any{}
		}
		f.pol = p
	}
	return f, nil
}

func (f *jsonPolicyFile) close() error {
	if !f.dirty {
		return nil
	}
	if f.wrapped {
		if len(f.pol) == 0 {
			delete(f.doc, "policies")
		} else {
			f.doc["policies"] = f.pol
		}
	}
	if len(f.doc) == 0 {
		if err := os.Remove(f.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	raw, err := json.MarshalIndent(f.doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	tmp := f.path + ".fleetwarden-tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

// jsonPolicyStore keeps the same policies in one or more files (Chrome and
// Chromium read different folders).
type jsonPolicyStore struct{ files []*jsonPolicyFile }

func newJSONPolicyStore(wrapped bool, paths ...string) (*jsonPolicyStore, error) {
	s := &jsonPolicyStore{}
	for _, p := range paths {
		f, err := openJSONPolicyFile(p, wrapped)
		if err != nil {
			return nil, err
		}
		s.files = append(s.files, f)
	}
	return s, nil
}

func (s *jsonPolicyStore) Matches(name string, v any) (bool, bool, error) {
	want := canonical(v)
	match, present := true, false
	for _, f := range s.files {
		cur, ok := f.pol[name]
		present = present || ok
		if !ok || canonical(cur) != want {
			match = false
		}
	}
	return match, present, nil
}

func (s *jsonPolicyStore) Set(name string, v any) error {
	var x any
	if err := json.Unmarshal([]byte(canonical(v)), &x); err != nil {
		return err
	}
	for _, f := range s.files {
		f.pol[name] = x
		f.dirty = true
	}
	return nil
}

func (s *jsonPolicyStore) Delete(name string) error {
	for _, f := range s.files {
		if _, ok := f.pol[name]; ok {
			delete(f.pol, name)
			f.dirty = true
		}
	}
	return nil
}

func (s *jsonPolicyStore) Close() error {
	var errs []error
	for _, f := range s.files {
		errs = append(errs, f.close())
	}
	return errors.Join(errs...)
}

// ---- registry form of policies (Windows) ----

// regEntry is one registry value below a policy root key.
type regEntry struct {
	Path string // subkey below the root, "" for the root itself
	Name string
	Kind string // "sz" or "dword"
	S    string
	N    uint64
}

func (e regEntry) String() string {
	if e.Kind == "dword" {
		return fmt.Sprintf("%s|%s|dword|%d", strings.ToLower(e.Path), strings.ToLower(e.Name), e.N)
	}
	return fmt.Sprintf("%s|%s|sz|%s", strings.ToLower(e.Path), strings.ToLower(e.Name), e.S)
}

// regEntries sorts entries into a comparable form.
func regEntries(es []regEntry) string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.String()
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// flattenPolicy turns a policy value into registry values, the way the
// browsers' administrative templates lay them out: strings are REG_SZ,
// booleans and whole numbers REG_DWORD, and lists a subkey with the values
// "1", "2", ... Chrome and Edge take objects (and lists of objects) as a
// JSON string; Firefox (nested) takes them as subkeys.
func flattenPolicy(name string, v any, nested bool) []regEntry {
	var out []regEntry
	var walk func(path, name string, v any)
	walk = func(path, name string, v any) {
		sub := name
		if path != "" {
			sub = path + `\` + name
		}
		switch x := v.(type) {
		case nil:
		case bool:
			n := uint64(0)
			if x {
				n = 1
			}
			out = append(out, regEntry{Path: path, Name: name, Kind: "dword", N: n})
		case float64:
			if x >= 0 && x == float64(uint64(x)) && x <= 0xffffffff {
				out = append(out, regEntry{Path: path, Name: name, Kind: "dword", N: uint64(x)})
			} else {
				out = append(out, regEntry{Path: path, Name: name, Kind: "sz", S: strconv.FormatFloat(x, 'f', -1, 64)})
			}
		case string:
			out = append(out, regEntry{Path: path, Name: name, Kind: "sz", S: x})
		case []any:
			if !nested && !allScalar(x) {
				out = append(out, regEntry{Path: path, Name: name, Kind: "sz", S: canonical(x)})
				return
			}
			for i, e := range x {
				walk(sub, strconv.Itoa(i+1), e)
			}
		case map[string]any:
			if !nested {
				out = append(out, regEntry{Path: path, Name: name, Kind: "sz", S: canonical(x)})
				return
			}
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(sub, k, x[k])
			}
		default:
			out = append(out, regEntry{Path: path, Name: name, Kind: "sz", S: canonical(x)})
		}
	}
	var x any
	_ = json.Unmarshal([]byte(canonical(v)), &x)
	walk("", name, x)
	return out
}

func allScalar(list []any) bool {
	for _, e := range list {
		switch e.(type) {
		case map[string]any, []any:
			return false
		}
	}
	return true
}
