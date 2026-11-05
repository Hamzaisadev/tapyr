package sim

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/kentrow/tapyr/internal/mapping"
)

// FuzzAnonymizer anonymizes arbitrary JSON documents with the default
// rules. Whatever a recorded response holds, the anonymizer never panics,
// gives the same pseudonyms for the same values (fixtures stay consistent),
// keeps the structure of the document, never leaves the value of a field it
// recognizes, and leaves only documentation addresses in free text.
func FuzzAnonymizer(f *testing.F) {
	f.Add([]byte(recorded))
	files, _ := filepath.Glob("../../providers/*/sim/fixtures/*.json")
	nested, _ := filepath.Glob("../../providers/*/sim/fixtures/*/*.json")
	for _, file := range append(files, nested...) {
		if b, err := os.ReadFile(file); err == nil && len(b) < 64<<10 {
			f.Add(b)
		}
	}
	for _, s := range []string{
		`{"notes": "10.0.0.1.2.3.4 and a@b.co and AA:BB:CC:DD:EE:FF and fe80::1"}`,
		`{"serial": "", "mac": "x", "email": 3, "ip": null, "lat": "north", "hostname": ["a", {"b": 1}]}`,
		`[{"firmware_version": "10.1.2.3", "name": "a@b.co"}, "192.168.1.1"]`,
		`{"a": {"b": {"c": [{"d": "user-x@example.com 203.0.113.9 02:00:00:00:00:01"}]}}}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := mapping.DecodeJSON(data)
		if err != nil {
			return
		}
		a, err := NewAnonymizer(AnonymizeRules{})
		if err != nil {
			t.Fatal(err)
		}
		out := a.Document(doc)
		if again := a.Document(doc); !reflect.DeepEqual(out, again) {
			t.Fatalf("two anonymizations differ:\n%v\n%v", out, again)
		}
		compareAnonymized(t, doc, out, "")
	})
}

// compareAnonymized checks an anonymized value against its original.
func compareAnonymized(t *testing.T, in, out any, key string) {
	t.Helper()
	switch x := in.(type) {
	case map[string]any:
		o, ok := out.(map[string]any)
		if !ok || len(o) != len(x) {
			t.Fatalf("%q: the object became %v", key, out)
		}
		for k, v := range x {
			ov, ok := o[k]
			if !ok {
				t.Fatalf("%q: key %q disappeared", key, k)
			}
			compareAnonymized(t, v, ov, k)
		}
		return
	case []any:
		o, ok := out.([]any)
		if !ok || len(o) != len(x) {
			t.Fatalf("%q: the list became %v", key, out)
		}
		for i := range x {
			compareAnonymized(t, x[i], o[i], key)
		}
		return
	case nil, bool:
		if out != in {
			t.Errorf("%q: %v became %v", key, in, out)
		}
		return
	}
	s, isString := in.(string)
	if !isString {
		return
	}
	if _, recognized := keyRule(key); recognized {
		// A value without letters or digits (" ", "-") carries nothing to
		// hide, and pseudonyms keep the character classes.
		if strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 && out == in {
			t.Errorf("%q: the value %q was kept", key, s)
		}
		return
	}
	if versionKey(key) {
		return
	}
	text, _ := out.(string)
	for _, m := range ipv4Pattern.FindAllString(text, -1) {
		if ip := net.ParseIP(m); ip != nil && !documentationIPv4(ip) {
			t.Errorf("%q: the address %s of %q is left in %q", key, m, s, text)
		}
	}
	for _, m := range emailPattern.FindAllString(text, -1) {
		if !strings.HasSuffix(m, "@example.com") {
			t.Errorf("%q: the email %s of %q is left in %q", key, m, s, text)
		}
	}
	for _, m := range macPattern.FindAllString(text, -1) {
		if !strings.HasPrefix(m, "02") {
			t.Errorf("%q: the MAC %s of %q is left in %q", key, m, s, text)
		}
	}
}

func documentationIPv4(ip net.IP) bool {
	for _, cidr := range []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24"} {
		if _, n, _ := net.ParseCIDR(cidr); n.Contains(ip) {
			return true
		}
	}
	return false
}
