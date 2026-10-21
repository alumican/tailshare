package adapter

import (
	"archive/zip"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/alumican/tailshare/internal/plist"
)

const base = "https://host.example.ts.net:10443"

func touch(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	touch(t, path)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The temporary directory with symlinks resolved, as the adapters see it (on macOS /var is /private/var)
func tempDir(t *testing.T) string {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Every file a site serves, sorted
func tree(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var files []string
	err := fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

func builtin(t *testing.T, id string) Adapter {
	for _, a := range builtins {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("no built-in adapter %s", id)
	return Adapter{}
}

// MARK: - Choosing

func ids(list []Adapter) []string {
	var ids []string
	for _, a := range list {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestChoosing(t *testing.T) {
	dir := tempDir(t)
	touch(t, filepath.Join(dir, "App.IPA"), filepath.Join(dir, "page.htm"), filepath.Join(dir, "notes.pdf"), filepath.Join(dir, "no-extension"))
	os.MkdirAll(filepath.Join(dir, "dist"), 0o755)
	os.MkdirAll(filepath.Join(dir, "site.html"), 0o755)

	list := List(nil)
	if got := ids(list); !reflect.DeepEqual(got, []string{"10-ipa", "50-html", "80-dir", "99-file"}) {
		t.Fatalf("built-in adapters: %v", got)
	}
	for name, want := range map[string]string{"App.IPA": "ipa", "page.htm": "html", "notes.pdf": "file", "no-extension": "file", "dist": "dir", "site.html": "dir"} {
		a, ok := Find(list, filepath.Join(dir, name), "", nil)
		if !ok || a.Name() != want {
			t.Errorf("%s: got %s, want %s", name, a.Name(), want)
		}
	}
	if a, ok := Find(list, filepath.Join(dir, "page.htm"), "99-file", nil); !ok || a.Name() != "file" {
		t.Errorf("wanted by its whole name: %v", a.ID)
	}
}

func TestOwnAdapters(t *testing.T) {
	own, elsewhere := tempDir(t), tempDir(t)
	write(t, filepath.Join(own, "30-apk", "match"), "# Install page for an Android app\n*.apk # and nothing else\n")
	write(t, filepath.Join(own, "30-apk", "prepare"), "#!/bin/sh\necho /\n")
	os.Chmod(filepath.Join(own, "30-apk", "prepare"), 0o755)
	write(t, filepath.Join(own, "99-file", "match"), "*\n")
	write(t, filepath.Join(own, "20-text", "match"), "*.txt\n")
	write(t, filepath.Join(own, "20-text", "prepare"), "not executable")
	write(t, filepath.Join(elsewhere, "30-apk", "match"), "*\n")

	list := List([]string{own, elsewhere, filepath.Join(own, "missing")})
	if got := ids(list); !reflect.DeepEqual(got, []string{"10-ipa", "20-text", "30-apk", "50-html", "80-dir", "99-file"}) {
		t.Fatalf("adapters: %v", got)
	}
	apk := list[2]
	if apk.Dir != filepath.Join(own, "30-apk") || apk.Description != "Install page for an Android app" ||
		!reflect.DeepEqual(apk.Patterns, []string{"*.apk"}) || !apk.Usable || apk.Builtin() {
		t.Errorf("30-apk: %+v", apk)
	}
	if file := list[5]; file.Builtin() || file.Usable {
		t.Errorf("99-file of your own, without prepare: %+v", file)
	}

	var warnings []string
	warn := func(message string) { warnings = append(warnings, message) }
	if a, ok := Find(list, "notes.txt", "", warn); ok {
		t.Errorf("notes.txt: got %s, but no usable adapter matches", a.ID)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "skipping adapter 20-text") || !strings.Contains(warnings[1], "skipping adapter 99-file") {
		t.Errorf("warnings: %q", warnings)
	}
	if _, ok := Find(list, "notes.txt", "text", func(string) {}); ok {
		t.Error("an adapter without executable prepare was used when asked for by name")
	}
}

func TestName(t *testing.T) {
	for id, name := range map[string]string{"10-ipa": "ipa", "99-file": "file", "apk": "apk", "x-y": "x-y", "10-two-words": "two-words"} {
		if got := (Adapter{ID: id}).Name(); got != name {
			t.Errorf("%s: %s", id, got)
		}
	}
}

// MARK: - What the built-in ones serve

func TestFile(t *testing.T) {
	dir := tempDir(t)
	write(t, filepath.Join(dir, "my notes.txt"), "hello")
	prepared, err := builtin(t, "99-file").Prepare(filepath.Join(dir, "my notes.txt"), base, "")
	fsys, open := prepared.FS, prepared.Open
	if err != nil || open != "/my notes.txt" {
		t.Fatalf("open %q, err %v", open, err)
	}
	if got := tree(t, fsys); !reflect.DeepEqual(got, []string{"my notes.txt"}) {
		t.Errorf("site: %v", got)
	}
}

func TestDirectory(t *testing.T) {
	dir := tempDir(t)
	touch(t, filepath.Join(dir, "index.html"), filepath.Join(dir, "assets", "app.js"), filepath.Join(dir, ".env"), filepath.Join(dir, ".git", "config"))
	prepared, err := builtin(t, "80-dir").Prepare(dir, base, "")
	fsys, open := prepared.FS, prepared.Open
	if err != nil || open != "/" {
		t.Fatalf("open %q, err %v", open, err)
	}
	if got := tree(t, fsys); !reflect.DeepEqual(got, []string{"assets/app.js", "index.html"}) {
		t.Errorf("site: %v", got)
	}
}

func TestHTMLFollowsReferencesAndNothingElse(t *testing.T) {
	dir := tempDir(t)
	page := filepath.Join(dir, "site")
	write(t, filepath.Join(page, "page.html"), `<!doctype html>
<!-- <img src="img/commented.png"> -->
<LINK REL="stylesheet" HREF="css/style.css">
<style>@import "css/extra.css"; .hero { background: url('img/hero.png') }</style>
<img src="img/a%20b.png" srcset="img/1x.png 1x, img/2x.png 2x" alt="a > b">
<video poster=img/poster.png></video>
<div style="background-image: url(img/inline.png)"></div>
<script src="/js/app.js"></script>
<script>const s = "<img src='img/scripted.png'>"</script>
<a href="https://example.com/">elsewhere</a>
<a href="#top">anchor</a>
<a href="data:text/plain,hi">data</a>
<a href="img/q.png?v=1&amp;w=2">query</a>
<img src="../outside.png">
<img src="img/missing.png">
<img src="css">
`)
	write(t, filepath.Join(page, "css", "style.css"), "@import \"base.css\";\nbody { background: url(../img/bg.png) }\n")
	write(t, filepath.Join(page, "css", "base.css"), "p { background: url(\"../img/from-base.png\") }")
	touch(t, filepath.Join(page, "css", "extra.css"))
	for _, name := range []string{"hero", "a b", "1x", "2x", "poster", "inline", "bg", "from-base", "q", "commented", "scripted"} {
		touch(t, filepath.Join(page, "img", name+".png"))
	}
	touch(t, filepath.Join(page, "js", "app.js"), filepath.Join(page, "secret.txt"), filepath.Join(dir, "outside.png"))

	prepared, err := builtin(t, "50-html").Prepare(filepath.Join(page, "page.html"), base, "")
	fsys, open := prepared.FS, prepared.Open
	if err != nil || open != "/page.html" {
		t.Fatalf("open %q, err %v", open, err)
	}
	want := []string{
		"css/base.css", "css/extra.css", "css/style.css",
		"img/1x.png", "img/2x.png", "img/a b.png", "img/bg.png", "img/from-base.png", "img/hero.png", "img/inline.png", "img/poster.png", "img/q.png",
		"js/app.js", "page.html",
	}
	if got := tree(t, fsys); !reflect.DeepEqual(got, want) {
		t.Errorf("site:\n got %v\nwant %v", got, want)
	}
}

func TestHTMLPageThroughALink(t *testing.T) {
	dir, elsewhere := tempDir(t), tempDir(t)
	write(t, filepath.Join(elsewhere, "real.html"), `<img src="logo.png">`)
	touch(t, filepath.Join(dir, "logo.png"), filepath.Join(elsewhere, "logo.png"))
	os.Symlink(filepath.Join(elsewhere, "real.html"), filepath.Join(dir, "page.html"))

	prepared, err := builtin(t, "50-html").Prepare(filepath.Join(dir, "page.html"), base, "")
	fsys, open := prepared.FS, prepared.Open
	if err != nil || open != "/page.html" {
		t.Fatalf("open %q, err %v", open, err)
	}
	// References are taken from where the link is, not from where it points
	if got := tree(t, fsys); !reflect.DeepEqual(got, []string{"logo.png", "page.html"}) {
		t.Errorf("site: %v", got)
	}
}

func makeIPA(t *testing.T, path string, info []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, data := range map[string][]byte{"Payload/Demo.app/Info.plist": info, "Payload/Demo.app/Demo": []byte("binary")} {
		w, _ := archive.Create(name)
		w.Write(data)
	}
	archive.Close()
	file.Close()
}

func TestIPA(t *testing.T) {
	dir := tempDir(t)
	info, err := os.ReadFile("../plist/testdata/Info.binary.plist")
	if err != nil {
		t.Fatal(err)
	}
	makeIPA(t, filepath.Join(dir, "Demo App.ipa"), info)

	prepared, err := builtin(t, "10-ipa").Prepare(filepath.Join(dir, "Demo App.ipa"), base, "")
	fsys, open := prepared.FS, prepared.Open
	if err != nil || open != "/" {
		t.Fatalf("open %q, err %v", open, err)
	}
	if got := tree(t, fsys); !reflect.DeepEqual(got, []string{"Demo App.ipa", "index.html", "manifest.plist"}) {
		t.Errorf("site: %v", got)
	}

	manifest, _ := fs.ReadFile(fsys, "manifest.plist")
	for _, want := range []string{
		"<string>" + base + "/Demo%20App.ipa?v=",
		"<string>com.example.demo</string>",
		"<string>1.2.3</string>",
		"<string>Demo &amp; Co</string>",
	} {
		if !strings.Contains(string(manifest), want) {
			t.Errorf("manifest.plist has no %s:\n%s", want, manifest)
		}
	}
	if _, err := plist.Decode(manifest); err != nil {
		t.Errorf("manifest.plist: %v", err)
	}
	page, _ := fs.ReadFile(fsys, "index.html")
	for _, want := range []string{
		`href="itms-services://?action=download-manifest&amp;url=` + base + `/manifest.plist"`,
		"<h1>Demo &amp; Co</h1>",
		"<p>1.2.3 / built ",
	} {
		if !strings.Contains(string(page), want) {
			t.Errorf("index.html has no %s:\n%s", want, page)
		}
	}
}

func TestIPARefusesWhatIsNotAnApp(t *testing.T) {
	dir := tempDir(t)
	write(t, filepath.Join(dir, "Broken.ipa"), "not a zip")
	makeIPA(t, filepath.Join(dir, "NoID.ipa"), []byte(`<plist><dict><key>CFBundleName</key><string>x</string></dict></plist>`))
	for _, name := range []string{"Broken.ipa", "NoID.ipa"} {
		if _, err := builtin(t, "10-ipa").Prepare(filepath.Join(dir, name), base, ""); !errors.Is(err, ErrNotAnApp) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
