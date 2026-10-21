package site

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func write(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	files := Files{
		"index.html":     {Data: []byte("<h1>hi</h1>")},
		"css/style.css":  {Path: write(t, filepath.Join(dir, "style.css"), "body {}")},
		"img/a/logo.png": {Path: write(t, filepath.Join(dir, "logo.png"), "png")},
		"my notes.txt":   {Path: write(t, filepath.Join(dir, "my notes.txt"), "notes")},
	}
	if err := fstest.TestFS(files, "index.html", "css/style.css", "img/a/logo.png", "my notes.txt"); err != nil {
		t.Fatal(err)
	}
	if data, _ := fs.ReadFile(files, "css/style.css"); string(data) != "body {}" {
		t.Errorf("css/style.css: %q", data)
	}
	for _, name := range []string{"missing", "css/missing.css", "style.css", "../style.css"} {
		if _, err := files.Open(name); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
}

func TestDirLeavesOutHiddenEntriesAtTheTop(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "index.html"), "<h1>built</h1>")
	write(t, filepath.Join(dir, "assets", "app.js"), "app")
	write(t, filepath.Join(dir, "assets", ".keep"), "")
	write(t, filepath.Join(dir, ".env"), "SECRET=1")
	write(t, filepath.Join(dir, ".git", "config"), "")

	site := Dir(dir)
	if err := fstest.TestFS(site, "index.html", "assets/app.js", "assets/.keep"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".env", ".git", ".git/config"} {
		if _, err := site.Open(name); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	entries, err := fs.ReadDir(site, ".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 2 || names[0] != "assets" || names[1] != "index.html" {
		t.Errorf("top: %v", names)
	}
}
