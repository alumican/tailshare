package plist

import (
	"os"
	"reflect"
	"testing"
)

// Both fixtures hold the same dictionary, written by Python's plistlib
var want = map[string]any{
	"CFBundleIdentifier":           "com.example.demo",
	"CFBundleDisplayName":          "Demo & Co",
	"CFBundleName":                 "Demo",
	"CFBundleShortVersionString":   "1.2.3",
	"CFBundleVersion":              int64(42),
	"UIRequiredDeviceCapabilities": []any{"arm64"},
	"LSRequiresIPhoneOS":           true,
	"Ratio":                        1.5,
	"Unicode":                      "デモ",
	"Long":                         "xxxxxxxxxxxxxxxxxxxx",
	"Data":                         []byte{0, 1},
}

func TestDecode(t *testing.T) {
	for _, name := range []string{"testdata/Info.binary.plist", "testdata/Info.xml.plist"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %#v\nwant %#v", name, got, want)
		}
	}
}

func TestDecodeRefusesBrokenInput(t *testing.T) {
	binary, err := os.ReadFile("testdata/Info.binary.plist")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"empty":             {},
		"not a plist":       []byte("hello"),
		"truncated binary":  binary[:len(binary)-10],
		"binary header":     []byte("bplist00"),
		"unclosed xml":      []byte(`<plist><dict><key>a</key>`),
		"key without value": []byte(`<plist><dict><key>a</key></dict></plist>`),
	} {
		if _, err := Decode(data); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// Every truncation and every flipped byte of a binary plist either decodes or fails, but never panics
func TestDecodeBinarySurvivesDamage(t *testing.T) {
	data, err := os.ReadFile("testdata/Info.binary.plist")
	if err != nil {
		t.Fatal(err)
	}
	for i := range data {
		Decode(data[:i])
		for _, b := range []byte{0x00, 0x0f, 0xff} {
			damaged := append([]byte(nil), data...)
			damaged[i] = b
			Decode(damaged)
		}
	}
}
