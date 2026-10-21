package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alumican/tailshare/internal/site"
)

func newHandler(t *testing.T, token string, allow []string, main string) (*shareHandler, *[]string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "App.ipa"), []byte(strings.Repeat("x", 1000)), 0o644); err != nil {
		t.Fatal(err)
	}
	files := site.Files{
		"index.html": {Data: []byte("<a href=itms-services://...>Install</a>")},
		"App.ipa":    {Path: filepath.Join(dir, "App.ipa")},
	}
	var handed []string
	h := &shareHandler{content: http.FileServerFS(files), token: token, cookie: "tailshare_10443", allow: allow, main: main, quiet: true,
		handedOver: func(path string) { handed = append(handed, path) }}
	return h, &handed
}

func get(h http.Handler, path string, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestToken(t *testing.T) {
	h, _ := newHandler(t, "abc123", nil, "/App.ipa")
	for path, want := range map[string]int{
		"/abc123/":          200,
		"/abc123/App.ipa":   200,
		"/abc123/missing":   404,
		"/abc123":           301,
		"/":                 404,
		"/App.ipa":          404,
		"/abc12/App.ipa":    404,
		"/abc1234/App.ipa":  404,
		"/x/abc123/App.ipa": 404,
	} {
		if got := get(h, path).Code; got != want {
			t.Errorf("GET %s: %d, want %d", path, got, want)
		}
	}
	if location := get(h, "/abc123").Header().Get("Location"); location != "/abc123/" {
		t.Errorf("redirect to %q", location)
	}
}

func TestAllow(t *testing.T) {
	h, _ := newHandler(t, "", []string{"alice@example.com", "*@team.example"}, "/App.ipa")
	for login, want := range map[string]int{
		"":                                403, // a tagged device, or someone on this machine itself
		"alice@example.com":               200,
		"ALICE@example.com":               200,
		"bob@example.com":                 403,
		"carol@team.example":              200,
		"carol@team.example.evil":         403,
		"=?utf-8?q?alice=40example.com?=": 200, // as tailscale encodes a header that is not ASCII
	} {
		var header []string
		if login != "" {
			header = []string{"Tailscale-User-Login", login}
		}
		if got := get(h, "/", header...).Code; got != want {
			t.Errorf("%q: %d, want %d", login, got, want)
		}
	}
	open, _ := newHandler(t, "", nil, "/App.ipa")
	if got := get(open, "/").Code; got != 200 {
		t.Errorf("without --allow, anyone: %d", got)
	}
}

func TestOnceWaitsForTheWholeMainFile(t *testing.T) {
	h, handed := newHandler(t, "", nil, "/App.ipa")
	get(h, "/")
	get(h, "/App.ipa", "Range", "bytes=0-99")
	get(h, "/App.ipa", "Range", "bytes=100-499")
	if len(*handed) != 0 {
		t.Fatalf("handed over after the page and part of the IPA: %v", *handed)
	}
	head := httptest.NewRequest(http.MethodHead, "/App.ipa", nil)
	h.ServeHTTP(httptest.NewRecorder(), head)
	if len(*handed) != 0 {
		t.Fatalf("handed over after a HEAD request: %v", *handed)
	}
	// The last part of it, fetched in ranges as iOS does
	if w := get(h, "/App.ipa", "Range", "bytes=500-999"); w.Code != http.StatusPartialContent {
		t.Fatalf("range: %d", w.Code)
	}
	if len(*handed) != 1 || (*handed)[0] != "/App.ipa" {
		t.Fatalf("handed over: %v", *handed)
	}

	whole, handed := newHandler(t, "tok", nil, "/App.ipa")
	if w := get(whole, "/tok/App.ipa"); w.Code != 200 || w.Body.Len() != 1000 {
		t.Fatalf("whole: %d, %d bytes", w.Code, w.Body.Len())
	}
	if len(*handed) != 1 {
		t.Fatalf("handed over after the whole IPA: %v", *handed)
	}

	refused, handed := newHandler(t, "", []string{"alice@example.com"}, "/App.ipa")
	get(refused, "/App.ipa")
	if len(*handed) != 0 {
		t.Fatalf("handed over to someone refused: %v", *handed)
	}
}

func TestIsMain(t *testing.T) {
	for _, c := range []struct {
		path, main string
		want       bool
	}{
		{"/a.txt", "/a.txt", true},
		{"/b.txt", "/a.txt", false},
		{"/", "/index.html", true},
		{"/docs/", "/docs/index.html", true},
		{"/", "/report.html", false},
	} {
		if got := isMain(c.path, c.main); got != c.want {
			t.Errorf("isMain(%q, %q) = %v", c.path, c.main, got)
		}
	}
}

func TestTooBroad(t *testing.T) {
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	t.Setenv("HOME", home)
	os.Mkdir(filepath.Join(home, "site"), 0o755)
	for dir, want := range map[string]string{
		"/":                         "it is the root of a disk",
		home:                        "it is your home directory",
		filepath.Dir(home):          "it holds your home directory",
		filepath.Join(home, "site"): "",
		filepath.Join(home + "2"):   "",
	} {
		if got := tooBroad(dir); got != want {
			t.Errorf("%s: %q, want %q", dir, got, want)
		}
	}
}

func TestRandomToken(t *testing.T) {
	a, b := randomToken(), randomToken()
	if len(a) != 26 || a == b || strings.Trim(a, "abcdefghijklmnopqrstuvwxyz234567") != "" {
		t.Errorf("tokens %q and %q", a, b)
	}
}
