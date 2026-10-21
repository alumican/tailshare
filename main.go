// tailshare: serve a file or a directory to your tailnet over HTTPS, and print its URL.
// What is served for a path is decided by adapters (see README.md). Requires tailscale.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/alumican/tailshare/internal/adapter"
)

// Set by a release build: go build -ldflags "-X main.version=..."
var version = "1.1.0"

var prog = filepath.Base(os.Args[0])

// The agent skill for Claude Code and Codex, printed by --skill
//
//go:embed skills/tailshare/SKILL.md
var skill string

func usage() string {
	return fmt.Sprintf(`Usage: %[1]s [options] <path>      serve a path
       %[1]s --list                show running shares
       %[1]s --stop <port|all>     stop running shares
       %[1]s --adapters            list the adapters
       %[1]s --which <path>        show which adapter would serve a path
       %[1]s --skill               print the skill for AI agents (SKILL.md)

Try '%[1]s --help' for more.
`, prog)
}

func help() string {
	return fmt.Sprintf(`%[1]s %[2]s - serve a file or a directory to your tailnet over HTTPS

USAGE
  %[1]s [options] <path>
  %[1]s --list | --stop <port|all> | --adapters | --which <path> | --skill

DESCRIPTION
  Serves <path> over HTTPS to the devices on your tailnet only, and prints its URL.
  Stops after --time or on Ctrl-C, and leaves nothing behind: the tailscale serve
  config, the local server and every temporary file are removed. The path itself is
  never copied or changed.

  What is served is decided by the first adapter whose patterns match the path:
    *.ipa           an install page for iPhone (with its manifest)
    *.html, *.htm   the page and the local files it refers to, nothing else
    a directory     the directory at /, without hidden entries (.git, .env, ...)
    anything else   that one file
  See them with --adapters; add your own in %[3]s (README.md).

OPTIONS
  -p, --port PORT    tailnet HTTPS port. Default: the first of 10443, 11443, ...
                     that tailscale serve does not use. A port in use is refused.
  -t, --time TIME    stop after TIME: 90 (seconds), 30s, 10m, 2h, or 0 for never.
                     Default: 1h.
  -a, --as NAME      use this adapter whatever the path is: ipa, html, dir, file, ...
      --once         stop once the thing has been downloaded completely (the file,
                     the IPA, the page), ten seconds later so a page can load the rest.
      --secret       put an unguessable token in the URL, so only those who get the
                     URL can open it.
      --allow LOGIN  only let these tailnet users open it: their login, such as
                     alice@example.com, or a glob such as *@example.com. Repeat it,
                     or separate with commas. Tagged devices have no login.
      --qr           also show the URL as a QR code on stderr, for a phone camera.
  -d, --detach       serve in the background: print the URL and return at once.
                     Stop it with --stop, or let --time end it.
  -j, --json         print one JSON object per line instead of plain text
                     (for serving, --list, --adapters and --which).
  -q, --quiet        print nothing to stderr (no messages, no request log).
      --list         show running shares: port, URL, adapter, path, until when.
      --stop TARGET  stop the share on port TARGET, or all of them with "all".
      --adapters     list the adapters in the order they are tried.
      --which PATH   show which adapter would serve PATH; serve nothing.
      --skill        print the skill that teaches AI agents (Claude Code, Codex) to
                     use tailshare: save it as <skills>/tailshare/SKILL.md.
  -h, --help         show this help.  -V, --version  show the version.

OUTPUT
  stdout carries only the result: the URL when serving (one line), or the listing.
  With --json, serving prints:
    {"url": "...", "port": 10443, "adapter": "html", "path": "...", "pid": 123,
     "expires": "2026-01-01T12:00:00+0900"}        (expires is null with --time 0)
  stderr carries messages and the request log (with who opened it), unless --quiet.

EXIT STATUS
  0  served and stopped normally (time up, Ctrl-C, --stop), or the query succeeded
  1  error: no such path, no adapter, port in use, tailscale not running, the home
     directory or the root of a disk given to serve, ...
  2  wrong arguments

ENVIRONMENT
  TAILSHARE_ADAPTER_PATH  where your own adapters are looked for, separated by "%[4]c"
                          (now: %[5]s)
  TAILSHARE_STATE_DIR     where running shares are recorded (now: %[6]s)

EXAMPLES
  %[1]s report.html                 share a page for an hour
  %[1]s -t 0 ./dist                 share a built site until Ctrl-C
  %[1]s -t 10m App.ipa              an iPhone install page for ten minutes
  %[1]s --once --secret --qr a.zip  one download, by whoever scans the code
  url=$(%[1]s -d -q notes.pdf)      share in the background, keep the URL
  %[1]s -d -j build/ | jq -r .url   the same, as JSON
  %[1]s --list                      what is being shared now
  %[1]s --stop all                  stop everything shared

REQUIRES
  tailscale, with HTTPS certificates enabled on the tailnet
`, prog, version, filepath.Join(configHome(), "tailshare", "adapters"), os.PathListSeparator,
		strings.Join(adapterPath(), string(os.PathListSeparator)), stateDir())
}

// MARK: - Exiting

var (
	exitMu   sync.Mutex
	cleanups []func()
)

// atExit runs f when tailshare exits through exit or die, last registered first.
func atExit(f func()) {
	exitMu.Lock()
	defer exitMu.Unlock()
	cleanups = append(cleanups, f)
}

// exit runs the cleanups once and exits. A second caller (a signal during cleanup) waits for the first.
func exit(code int) {
	exitMu.Lock()
	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}
	cleanups = nil
	os.Exit(code)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", prog, fmt.Sprintf(format, args...))
	exit(1)
}

func usageError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s: %s\nTry '%s --help'.\n", prog, fmt.Sprintf(format, args...), prog)
	exit(2)
}

func warn(message string) {
	fmt.Fprintf(os.Stderr, "%s: warning: %s\n", prog, message)
}

// MARK: - Places

func configHome() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	if runtime.GOOS == "windows" {
		return os.Getenv("AppData")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

// Where adapters of your own are looked for, first wins for the same name
func adapterPath() []string {
	if path := os.Getenv("TAILSHARE_ADAPTER_PATH"); path != "" {
		return filepath.SplitList(path)
	}
	return []string{filepath.Join(configHome(), "tailshare", "adapters")}
}

// Where running shares are recorded, one file per port, so --list and --stop can find them
func stateDir() string {
	if dir := os.Getenv("TAILSHARE_STATE_DIR"); dir != "" {
		return dir
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "tailshare")
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LocalAppData"), "tailshare", "state")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "tailshare")
}

// MARK: - Output

// One JSON object from key/value pairs, in their order, as {"key": value, ...}
func jsonObject(pairs ...any) string {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(jsonValue(pairs[i]) + ": " + jsonValue(pairs[i+1]))
	}
	b.WriteString("}")
	return b.String()
}

func jsonValue(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.Encode(value)
	return strings.TrimSuffix(buffer.String(), "\n")
}

// MARK: - Arguments

type options struct {
	port     string
	timeSpec string
	as       string
	detach   bool
	json     bool
	quiet    bool
	once     bool
	secret   bool
	allow    []string
	qr       bool
	mode     string // serve, list, stop, adapters, which, skill
	stop     string
	path     string
}

func parse(args []string) options {
	o := options{timeSpec: "1h", mode: "serve"}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage())
		exit(2)
	}
	setPath := func(path string) {
		if o.path != "" {
			usageError("only one path can be served (got '%s' and '%s')", o.path, path)
		}
		o.path = path
	}
	value := func(i int) string {
		if i+1 >= len(args) {
			usageError("%s needs a value", args[i])
		}
		return args[i+1]
	}
	addAllow := func(list string) {
		for _, login := range strings.Split(list, ",") {
			login = strings.TrimSpace(login)
			if _, err := path.Match(login, ""); login == "" || err != nil {
				usageError("--allow takes logins such as alice@example.com or *@example.com (got '%s')", list)
			}
			o.allow = append(o.allow, login)
		}
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, inline, hasInline := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "--") || !hasInline {
			name, inline = arg, ""
		}
		switch {
		case arg == "-p" || arg == "--port":
			o.port = value(i)
			i++
		case name == "--port" && hasInline:
			o.port = inline
		case arg == "-t" || arg == "--time":
			o.timeSpec = value(i)
			i++
		case name == "--time" && hasInline:
			o.timeSpec = inline
		case arg == "-a" || arg == "--as":
			o.as = value(i)
			i++
		case name == "--as" && hasInline:
			o.as = inline
		case arg == "--allow":
			addAllow(value(i))
			i++
		case name == "--allow" && hasInline:
			addAllow(inline)
		case arg == "--once":
			o.once = true
		case arg == "--secret":
			o.secret = true
		case arg == "--qr":
			o.qr = true
		case arg == "--skill":
			o.mode = "skill"
		case arg == "-d" || arg == "--detach":
			o.detach = true
		case arg == "-j" || arg == "--json":
			o.json = true
		case arg == "-q" || arg == "--quiet":
			o.quiet = true
		case arg == "--list":
			o.mode = "list"
		case arg == "--stop":
			o.mode, o.stop = "stop", value(i)
			i++
		case name == "--stop" && hasInline:
			o.mode, o.stop = "stop", inline
		case arg == "--adapters":
			o.mode = "adapters"
		case arg == "--which":
			o.mode = "which"
		case arg == "-h" || arg == "--help":
			fmt.Print(help())
			exit(0)
		case arg == "-V" || arg == "--version":
			fmt.Println(prog, version)
			exit(0)
		case arg == "--":
			rest := args[i+1:]
			if len(rest) > 0 {
				if o.path != "" || len(rest) > 1 {
					usageError("only one path can be served")
				}
				o.path = rest[0]
			}
			return o
		case bundled.MatchString(arg):
			// Short options without values, such as -dq
			for _, flag := range arg[1:] {
				switch flag {
				case 'd':
					o.detach = true
				case 'j':
					o.json = true
				case 'q':
					o.quiet = true
				default:
					usageError("-%c cannot be combined with other options; give it on its own", flag)
				}
			}
		case strings.HasPrefix(arg, "-"):
			usageError("unknown option: %s", arg)
		default:
			setPath(arg)
		}
	}
	return o
}

var bundled = regexp.MustCompile(`^-[a-zA-Z]{2,}$`)

func main() {
	o := parse(os.Args[1:])

	switch o.mode {
	case "skill":
		fmt.Print(skill)
		exit(0)
	case "adapters":
		showAdapters(o)
		exit(0)
	case "list":
		showShares(o)
		exit(0)
	case "stop":
		if _, err := strconv.Atoi(o.stop); o.stop != "all" && (err != nil || strings.TrimLeft(o.stop, "0123456789") != "") {
			usageError("--stop takes a port or 'all'")
		}
		stopShares(o.stop)
		exit(0)
	}

	if o.path == "" {
		usageError("no path given")
	}
	if port, err := strconv.Atoi(o.port); o.port != "" && (err != nil || strings.TrimLeft(o.port, "0123456789") != "" || port < 1 || port > 65535) {
		usageError("invalid port: %s", o.port)
	}
	seconds, ok := parseTime(o.timeSpec)
	if !ok {
		usageError("invalid time: %s (use 90, 30s, 10m, 2h or 0)", o.timeSpec)
	}
	info, err := os.Stat(o.path)
	if err != nil {
		die("no such file or directory: %s", o.path)
	}
	if !readable(o.path, info.IsDir()) {
		die("cannot read: %s", o.path)
	}
	abs, err := filepath.Abs(o.path)
	if err == nil {
		// Like pwd -P: the directory without symlinks, the name as given
		if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
			abs = filepath.Join(dir, filepath.Base(abs))
		}
		if info.IsDir() {
			abs, _ = filepath.EvalSymlinks(abs)
		}
	}

	a, found := adapter.Find(adapter.List(adapterPath()), abs, o.as, warn)
	if !found {
		if o.as != "" {
			die("no usable adapter named %s (see %s --adapters)", o.as, prog)
		}
		die("no adapter matches %s (see %s --adapters)", o.path, prog)
	}

	if o.mode == "which" {
		if o.json {
			fmt.Println(jsonObject("adapter", a.Name(), "id", a.ID, "dir", adapterDir(a), "path", abs))
		} else {
			fmt.Printf("%s\t%s\n", a.Name(), adapterDirText(a))
		}
		exit(0)
	}

	if info.IsDir() {
		if reason := tooBroad(abs); reason != "" {
			die("refusing to serve %s: %s; serve the folder you mean inside it", o.path, reason)
		}
	}
	if o.detach {
		detach(o, a, abs)
	}
	serve(o, a, abs, seconds)
}

// Why a directory would hand out far more than meant: the root of a disk, or your home directory
// or one above it. Empty when it is fine.
func tooBroad(dir string) string {
	if filepath.Dir(dir) == dir {
		return "it is the root of a disk"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	switch {
	case dir == home:
		return "it is your home directory"
	case strings.HasPrefix(home, dir+string(filepath.Separator)):
		return "it holds your home directory"
	}
	return ""
}

var timePattern = regexp.MustCompile(`^([0-9]+)([smh]?)$`)

func parseTime(spec string) (int, bool) {
	match := timePattern.FindStringSubmatch(spec)
	if match == nil {
		return 0, false
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return n * map[string]int{"": 1, "s": 1, "m": 60, "h": 3600}[match[2]], true
}

func readable(path string, isDir bool) bool {
	if isDir {
		_, err := os.ReadDir(path)
		return err == nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// MARK: - Adapters

func adapterDir(a adapter.Adapter) any {
	if a.Builtin() {
		return nil
	}
	return a.Dir
}

func adapterDirText(a adapter.Adapter) string {
	if a.Builtin() {
		return "(built in)"
	}
	return a.Dir
}

func showAdapters(o options) {
	for _, a := range adapter.List(adapterPath()) {
		patterns := strings.Join(a.Patterns, " ")
		if o.json {
			fmt.Println(jsonObject("name", a.Name(), "id", a.ID, "patterns", patterns, "description", a.Description,
				"dir", adapterDir(a), "usable", a.Usable))
			continue
		}
		note := ""
		if !a.Usable {
			note = "  (skipped: prepare is not executable)"
		}
		fmt.Printf("%-6s %-14s %s\n", a.Name(), patterns, a.Description)
		fmt.Printf("%-6s %-14s %s%s\n", "", "", adapterDirText(a), note)
	}
}
