// Package adapter decides what is served for a path.
//
// The bundled adapters (ipa, html, dir, file) are built in. An adapter of your own is a directory
// with a match file (patterns) and an executable prepare (see README.md); it is tried in name order
// together with the built-in ones, and replaces a built-in one of the same name.
package adapter

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/alumican/tailshare/internal/site"
)

// Adapter is one way of serving a path.
type Adapter struct {
	ID          string   // the whole name, which sets the order: "10-ipa"
	Dir         string   // where an adapter of your own lives; empty for a built-in one
	Patterns    []string // shell globs on the name of the path, or @dir for any directory
	Description string
	Usable      bool // false when an adapter of your own has no executable prepare

	prepare func(input, base string) (fs.FS, string, error)
}

// Name is the ID without its ordering number: 10-ipa -> ipa.
func (a Adapter) Name() string {
	if number, name, ok := strings.Cut(a.ID, "-"); ok && number != "" && strings.Trim(number, "0123456789") == "" {
		return name
	}
	return a.ID
}

func (a Adapter) Builtin() bool { return a.Dir == "" }

// Matches tells whether the adapter's patterns accept the path.
func (a Adapter) Matches(input string, isDir bool) bool {
	name := strings.ToLower(filepath.Base(input))
	for _, pattern := range a.Patterns {
		if pattern == "@dir" {
			if isDir {
				return true
			}
		} else if !isDir {
			if ok, _ := path.Match(strings.ToLower(pattern), name); ok {
				return true
			}
		}
	}
	return false
}

// Prepare decides what to serve for the path: the site, and the path to open in it (such as / or
// /report.html, not URL-encoded). base is https://<host>:<port>. An adapter of your own fills
// siteDir, an empty directory that must stay until the share stops.
func (a Adapter) Prepare(input, base, siteDir string) (fs.FS, string, error) {
	if a.prepare != nil {
		return a.prepare(input, base)
	}
	return a.runPrepare(input, base, siteDir)
}

// MARK: - Finding adapters

// List returns every adapter in the order they are tried. searchPath lists the directories that hold
// adapters of your own; the first one found for a name wins, and the built-in ones come last.
func List(searchPath []string) []Adapter {
	seen := map[string]bool{}
	var list []Adapter
	for _, dir := range searchPath {
		entries, err := os.ReadDir(dir)
		if dir == "" || err != nil {
			continue
		}
		for _, entry := range entries {
			full := filepath.Join(dir, entry.Name())
			if seen[entry.Name()] || !isDir(full) {
				continue
			}
			seen[entry.Name()] = true
			list = append(list, own(entry.Name(), full))
		}
	}
	for _, builtin := range builtins {
		if !seen[builtin.ID] {
			list = append(list, builtin)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// Find returns the first usable adapter whose patterns accept the path, or the one named wanted
// (by its name or its whole ID) whatever the path is. warn is told about adapters that are skipped.
func Find(list []Adapter, input string, wanted string, warn func(string)) (Adapter, bool) {
	dir := isDir(input)
	for _, a := range list {
		if wanted != "" {
			if a.ID != wanted && a.Name() != wanted {
				continue
			}
		} else if !a.Matches(input, dir) {
			continue
		}
		if !a.Usable {
			warn(fmt.Sprintf("skipping adapter %s: %s is missing or not executable", a.ID, filepath.Join(a.Dir, "prepare")))
			if wanted != "" {
				return Adapter{}, false
			}
			continue
		}
		return a, true
	}
	return Adapter{}, false
}

func isDir(name string) bool {
	info, err := os.Stat(name)
	return err == nil && info.IsDir()
}

// MARK: - Adapters of your own

func own(id, dir string) Adapter {
	a := Adapter{ID: id, Dir: dir}
	if data, err := os.ReadFile(filepath.Join(dir, "match")); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := scanner.Text()
			if a.Description == "" && strings.HasPrefix(line, "#") {
				a.Description = strings.TrimSpace(strings.TrimPrefix(line, "#"))
			}
			line, _, _ = strings.Cut(line, "#")
			a.Patterns = append(a.Patterns, strings.Fields(line)...)
		}
	}
	a.Usable = prepareProgram(dir) != ""
	return a
}

// The adapter's executable prepare, or "" when there is none
func prepareProgram(dir string) string {
	names := []string{"prepare"}
	if runtime.GOOS == "windows" {
		names = []string{"prepare.exe", "prepare.cmd", "prepare.bat"}
	}
	for _, name := range names {
		full := filepath.Join(dir, name)
		info, err := os.Stat(full)
		if err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0o111 != 0) {
			return full
		}
	}
	return ""
}

// Run the adapter's prepare: it fills siteDir and prints the path to open as its last line.
// What it says on stderr goes to ours.
func (a Adapter) runPrepare(input, base, siteDir string) (fs.FS, string, error) {
	cmd := exec.Command(prepareProgram(a.Dir), input, siteDir, base)
	cmd.Env = append(os.Environ(), "TAILSHARE_ADAPTER_DIR="+a.Dir)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, "", err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	open := strings.TrimRight(lines[len(lines)-1], "\r")
	if !strings.HasPrefix(open, "/") {
		if open == "" {
			open = "nothing"
		}
		return nil, "", fmt.Errorf("%w (got: %s)", ErrNoPath, open)
	}
	return os.DirFS(siteDir), open, nil
}

// ErrNoPath is returned when an adapter of your own does not print the path to open.
var ErrNoPath = errors.New("printed no path to open")

// ErrNotAnApp is returned by the ipa adapter for a file that is not an iOS app archive.
var ErrNotAnApp = errors.New("not an iOS app archive")

// MARK: - Built in

var builtins = []Adapter{
	{ID: "10-ipa", Patterns: []string{"*.ipa"}, Description: "Install page for an iOS app (.ipa), with its manifest", Usable: true, prepare: ipa},
	{ID: "50-html", Patterns: []string{"*.html", "*.htm"}, Description: "The page and the local files it refers to, nothing else", Usable: true, prepare: htmlPage},
	{ID: "80-dir", Patterns: []string{"@dir"}, Description: "The directory at /: its index.html or a listing, without hidden entries", Usable: true, prepare: directory},
	{ID: "99-file", Patterns: []string{"*"}, Description: "That one file (anything no other adapter takes)", Usable: true, prepare: file},
}

// That one file, at /<its name>
func file(input, _ string) (fs.FS, string, error) {
	name := filepath.Base(input)
	return site.Files{name: {Path: input}}, "/" + name, nil
}

// The directory at /, so a built site finds its files where it expects them: its index.html, or a listing
func directory(input, _ string) (fs.FS, string, error) {
	return site.Dir(input), "/", nil
}
