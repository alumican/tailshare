package main

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alumican/tailshare/internal/adapter"
	"github.com/alumican/tailshare/internal/qr"
	"github.com/alumican/tailshare/internal/state"
	"github.com/alumican/tailshare/internal/tailscale"
)

// MARK: - Serve

func serve(o options, a adapter.Adapter, abs string, seconds int) {
	say := func(format string, args ...any) {
		if !o.quiet {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}

	cli, err := tailscale.Find()
	if err != nil {
		die("tailscale not found; install it, or put it on PATH")
	}
	status := cli.Status()
	switch {
	case status.State == "":
		die("tailscale is not running")
	case status.State != "Running":
		// Connected only when Running; when it is off (Stopped) or logged out (NeedsLogin), a URL would not open
		die("tailscale is not connected (state: %s); connect it and try again", status.State)
	case status.Host == "":
		die("could not find this device's tailnet name")
	}

	config, err := cli.ServeConfig()
	if err != nil {
		die("could not read what tailscale serve uses: %v", err)
	}
	port, _ := strconv.Atoi(o.port)
	if port == 0 {
		for candidate := 10443; candidate <= 65443; candidate += 1000 {
			if !config.Uses(candidate) {
				port = candidate
				break
			}
		}
		if port == 0 {
			die("no free port among 10443, 11443, ... 65443; choose one with --port")
		}
	} else if config.Uses(port) {
		die("port %d is already used by tailscale serve; choose another, or leave --port out", port)
	}
	base := fmt.Sprintf("https://%s:%d", status.Host, port)
	token := ""
	if o.secret {
		token = randomToken()
		base += "/" + token
	}

	// Everything that has to go when tailshare stops, whichever way it stops
	var (
		record  state.Record
		tmp     string
		server  *http.Server
		served  atomic.Pointer[tailscale.Serve] // set as soon as tailscale serve runs, even before it has the port
		serving bool
		started bool
	)
	stopping := make(chan struct{})
	logFile := os.Getenv("TAILSHARE_LOG")
	atExit(func() {
		close(stopping)
		if s := served.Load(); s != nil {
			s.Stop()
		}
		if server != nil {
			server.Close()
		}
		if tmp != "" {
			os.RemoveAll(tmp)
		}
		if record.File != "" {
			os.Remove(record.File)
		}
		// A detached share that never started leaves its log for the parent, which shows it
		if logFile != "" && started {
			os.Remove(logFile)
		}
		if serving {
			say("Stopped serving %s/", base)
		}
	})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-signals
		exit(0)
	}()

	// What answers requests: a server on this machine, or the site an adapter prepares
	var (
		content  http.Handler
		shareURL string
		mainPath string // what --once waits for
	)
	if o.proxy != nil {
		content = newProxy(o.proxy)
		shareURL = base + o.proxy.EscapedPath()
		if o.proxy.Path == "" {
			shareURL += "/"
		}
		if o.proxy.RawQuery != "" {
			shareURL += "?" + o.proxy.RawQuery
		}
	} else {
		// An adapter of your own fills a directory, removed on exit; adapters link to the path rather than copy it
		siteDir := ""
		if !a.Builtin() {
			tmp, err = os.MkdirTemp("", "tailshare.")
			if err != nil {
				die("cannot make a temporary directory: %v", err)
			}
			siteDir = filepath.Join(tmp, "site")
			os.Mkdir(siteDir, 0o755)
		}
		prepared, err := a.Prepare(abs, base, siteDir)
		if err != nil {
			var exitError *exec.ExitError
			switch {
			case errors.Is(err, adapter.ErrNoPath):
				die("adapter %s %v", a.Name(), err)
			case !errors.As(err, &exitError):
				fmt.Fprintf(os.Stderr, "%s: %v\n", prog, err)
			}
			die("adapter %s failed for %s", a.Name(), o.path)
		}
		content = http.FileServerFS(prepared.FS)
		shareURL = base + escapePath(prepared.Open)
		mainPath = prepared.Main
	}

	var handedOver func(string)
	if o.once {
		var once sync.Once
		handedOver = func(path string) {
			once.Do(func() {
				say("Downloaded %s completely; stopping in 10 seconds.", path)
				time.AfterFunc(10*time.Second, func() { exit(0) })
			})
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		die("cannot start the local server: %v", err)
	}
	target := "http://" + listener.Addr().String()
	handler := &shareHandler{content: content, token: token, cookie: fmt.Sprintf("tailshare_%d", port), allow: o.allow,
		main: mainPath, handedOver: handedOver, quiet: o.quiet}
	server = &http.Server{Handler: handler, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			die("the local server stopped: %v", err)
		}
	}()

	serve, err := cli.ServeForeground(port, target, serveProcess(), func(s *tailscale.Serve) { served.Store(s) })
	if err != nil {
		served.Store(nil) // it has exited
		die("tailscale serve failed: %s", serveHint(err))
	}
	serving = true
	go func() {
		<-serve.Done()
		select {
		case <-stopping: // stopped by the cleanup
		default:
			die("tailscale serve stopped: %s", serve.Output())
		}
	}()

	expires := ""
	if seconds > 0 {
		expires = time.Now().Add(time.Duration(seconds) * time.Second).Format("2006-01-02T15:04:05-0700")
	}
	record = state.Record{Port: port, URL: shareURL, Adapter: a.Name(), Path: abs, PID: os.Getpid(), Expires: expires,
		Target: target, Serve: serve.PID(), Log: logFile, Tmp: tmp}
	if err := state.Write(stateDir(), &record); err != nil {
		die("cannot record the share: %v", err)
	}

	if o.json {
		fmt.Println(jsonObject("url", shareURL, "port", port, "adapter", a.Name(), "path", abs, "pid", os.Getpid(), "expires", nullIfEmpty(expires)))
	} else {
		fmt.Println(shareURL)
	}
	started = true
	if o.qr && !o.quiet {
		showQR(shareURL)
	}
	if seconds > 0 {
		say("Serving %s as %s on your tailnet for %s. Ctrl-C to stop.", o.path, a.Name(), o.timeSpec)
		time.Sleep(time.Duration(seconds) * time.Second)
		say("Time is up (%s).", o.timeSpec)
		exit(0)
	}
	say("Serving %s as %s on your tailnet. Ctrl-C to stop.", o.path, a.Name())
	select {}
}

// What tailscale said, with a hint at the likely cause
func serveHint(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(strings.ToLower(message), "access denied"), strings.Contains(strings.ToLower(message), "permission"):
		return message + " (on Linux, allow yourself once with: sudo tailscale set --operator=$USER)"
	default:
		return message + " (are HTTPS certificates enabled on the tailnet?)"
	}
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// URL-encode a path, keeping its slashes
func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// 128 random bits, as 26 lower-case letters and digits, to put in a URL that cannot be guessed
func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		die("cannot make a secret: %v", err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

func showQR(text string) {
	code, err := qr.Encode(text)
	if err != nil {
		warn("the URL is too long for a QR code")
		return
	}
	fmt.Fprint(os.Stderr, code.Terminal(2))
}

func init() {
	// What iOS expects for an install, whatever this system's MIME table says
	mime.AddExtensionType(".ipa", "application/octet-stream")
	mime.AddExtensionType(".plist", "text/xml; charset=utf-8")
}

// MARK: - Requests

// shareHandler serves the site, or passes requests on to a server, for whoever may open it; logs
// each request; and tells when the main file has been handed over (--once).
type shareHandler struct {
	content    http.Handler
	token      string   // the first path segment of the URL (--secret), or empty
	cookie     string   // the cookie that carries the token once the URL has been opened
	allow      []string // logins or globs that may open it (--allow); anyone on the tailnet when empty
	main       string   // the path whose complete download hands the thing over
	handedOver func(path string)
	quiet      bool
}

func (h *shareHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	recorder := &recorder{ResponseWriter: w}
	login := userLogin(r)
	defer func() {
		if h.quiet {
			return
		}
		who := ""
		if login != "" {
			who = " (" + login + ")"
		}
		fmt.Fprintf(os.Stderr, "%s %s %s %d%s\n", time.Now().Format("15:04:05"), r.Method, r.URL.RequestURI(), recorder.Status(), who)
	}()

	if len(h.allow) > 0 && !allowed(login, h.allow) {
		http.Error(recorder, "Forbidden", http.StatusForbidden)
		return
	}
	path := r.URL.Path
	if h.token != "" {
		// The token comes in the URL, or, once the URL has been opened, in a cookie: a page's own
		// links that start with / (a built site's /assets/..., an app's /api/...) have no token
		prefix := "/" + h.token
		switch {
		case path == prefix:
			http.Redirect(recorder, r, prefix+"/", http.StatusMovedPermanently)
			return
		case strings.HasPrefix(path, prefix+"/"):
			http.SetCookie(recorder, &http.Cookie{Name: h.cookie, Value: h.token, Path: "/", HttpOnly: true, Secure: true,
				SameSite: http.SameSiteLaxMode})
			// From here on, and in the log, the path is the one inside the site, without the token
			path = strings.TrimPrefix(path, prefix)
			stripped := r.Clone(r.Context())
			stripped.URL.Path = path
			stripped.URL.RawPath = ""
			r = stripped
		case !h.hasCookie(r):
			http.NotFound(recorder, r)
			return
		}
	}
	h.content.ServeHTTP(recorder, r)

	if h.handedOver != nil && r.Method == http.MethodGet && isMain(path, h.main) && recorder.Complete() {
		h.handedOver(h.main)
	}
}

func (h *shareHandler) hasCookie(r *http.Request) bool {
	cookie, err := r.Cookie(h.cookie)
	return err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(h.token)) == 1
}

// Who asked, as tailscale serve says: the login of a tailnet user, or empty for a tagged device.
// tailscale serve removes any such header a client sends, so it cannot be forged over the tailnet.
func userLogin(r *http.Request) string {
	login := r.Header.Get("Tailscale-User-Login")
	if decoded, err := new(mime.WordDecoder).DecodeHeader(login); err == nil {
		login = decoded
	}
	return login
}

func allowed(login string, patterns []string) bool {
	if login == "" {
		return false
	}
	for _, pattern := range patterns {
		if ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(login)); ok {
			return true
		}
	}
	return false
}

// Whether a request path is the main file; a page called index.html is also served at its directory
func isMain(path, main string) bool {
	return path == main || strings.HasSuffix(main, "/index.html") && path == strings.TrimSuffix(main, "index.html")
}

// recorder remembers what a response sent, for the request log and to tell a complete download
type recorder struct {
	http.ResponseWriter
	status  int
	written int64
	failed  bool
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	if err != nil {
		r.failed = true
	}
	return n, err
}

// Flush and Hijack pass through, so streamed responses and upgraded connections (WebSocket) of a
// server behind a share reach the client; Unwrap lets http.ResponseController find them too
func (r *recorder) Flush() {
	http.NewResponseController(r.ResponseWriter).Flush()
}

func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(r.ResponseWriter).Hijack()
	if err == nil && r.status == 0 {
		r.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}

func (r *recorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *recorder) Status() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

// Complete tells whether the response sent a whole file: all of it, or the last part of it when
// the client fetched it in ranges, as iOS does with a large IPA
func (r *recorder) Complete() bool {
	if r.failed {
		return false
	}
	switch r.Status() {
	case http.StatusOK:
		length := r.Header().Get("Content-Length")
		return length == "" || length == strconv.FormatInt(r.written, 10)
	case http.StatusPartialContent:
		var first, last, total int64
		if _, err := fmt.Sscanf(r.Header().Get("Content-Range"), "bytes %d-%d/%d", &first, &last, &total); err != nil {
			return false
		}
		return last == total-1 && r.written == last-first+1
	}
	return false
}

// MARK: - Detach

// Run this same command in the background, and hand back what it prints once it is serving
func detach(o options, a adapter.Adapter, abs string) {
	say := func(format string, args ...any) {
		if !o.quiet {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		die("cannot make %s: %v", dir, err)
	}
	log, err := os.CreateTemp(dir, "log.")
	if err != nil {
		die("cannot make a log in %s: %v", dir, err)
	}
	defer log.Close()
	self, err := os.Executable()
	if err != nil {
		die("cannot find myself: %v", err)
	}

	args := []string{"--time", o.timeSpec}
	if o.proxy == nil {
		args = append(args, "--as", a.ID)
	}
	if o.port != "" {
		args = append(args, "--port", o.port)
	}
	if o.json {
		args = append(args, "--json")
	}
	if o.once {
		args = append(args, "--once")
	}
	if o.secret {
		args = append(args, "--secret")
	}
	for _, login := range o.allow {
		args = append(args, "--allow", login)
	}
	cmd := exec.Command(self, append(args, "--", abs)...)
	cmd.Env = append(os.Environ(), "TAILSHARE_LOG="+log.Name())
	cmd.Stderr = log
	cmd.SysProcAttr = detachedProcess()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		die("cannot start in the background: %v", err)
	}
	if err := cmd.Start(); err != nil {
		die("cannot start in the background: %v", err)
	}

	line := make(chan string, 1)
	go func() {
		first, _ := bufio.NewReader(stdout).ReadString('\n')
		line <- strings.TrimRight(first, "\n")
	}()
	var out string
	select {
	case out = <-line:
	case <-time.After(30 * time.Second):
	}
	if out == "" {
		cmd.Process.Kill()
		if text, err := os.ReadFile(log.Name()); err == nil {
			os.Stderr.Write(text)
		}
		os.Remove(log.Name())
		die("failed to start in the background")
	}
	fmt.Println(out)
	shareURL := out
	if o.json {
		var share struct{ URL string }
		json.Unmarshal([]byte(out), &share)
		shareURL = share.URL
	}
	port := ""
	if match := portInURL.FindStringSubmatch(shareURL); match != nil {
		port = match[1]
	}
	if o.qr && !o.quiet {
		showQR(shareURL)
	}
	say("Serving in the background (pid %d). Stop it with: %s --stop %s", cmd.Process.Pid, prog, port)
	exit(0)
}

var portInURL = regexp.MustCompile(`:([0-9]+)/`)

// MARK: - Running shares

// The records of the shares still running. A share that is gone without cleaning up after itself
// (killed with -9, or a crash) is cleaned up here.
func runningShares() []state.Record {
	var running []state.Record
	for _, r := range state.All(stateDir()) {
		if r.PID > 0 && alive(r.PID) {
			running = append(running, r)
		} else {
			cleanUpAfter(r)
		}
	}
	return running
}

// Remove what a share that is gone left behind. Its tailscale serve is stopped only if the port still
// proxies to the share's own local server, so a port something else took since is not touched: the
// tailscale serve it ran in the foreground (which outlives a share killed with -9), or the config
// left in the background by tailshare 1.0.
func cleanUpAfter(r state.Record) {
	if r.Target != "" {
		if cli, err := tailscale.Find(); err == nil {
			if config, err := cli.ServeConfig(); err == nil {
				if r.Serve > 0 && config.ForegroundProxy(r.Port) == r.Target {
					// While its config is there, the process is still that tailscale serve
					if process, err := os.FindProcess(r.Serve); err == nil {
						tailscale.Terminate(process)
						cli.WaitFree(r.Port, 5*time.Second)
					}
				}
				if config.Proxy(r.Port) == r.Target {
					cli.ServeOff(r.Port)
				}
			}
		}
	}
	r.Remove()
}

func showShares(o options) {
	shares := runningShares()
	for _, r := range shares {
		if o.json {
			fmt.Println(jsonObject("url", r.URL, "port", r.Port, "adapter", r.Adapter, "path", r.Path, "pid", r.PID, "expires", nullIfEmpty(r.Expires)))
			continue
		}
		until := "until stopped"
		if len(r.Expires) >= 16 {
			until = "until " + r.Expires[11:16]
		}
		fmt.Printf("%-5d %-6s %-12s %s\n", r.Port, r.Adapter, until, r.URL)
		fmt.Printf("%-5s %-6s %-12s %s\n", "", "", "", r.Path)
	}
	if !o.json && len(shares) == 0 {
		fmt.Fprintln(os.Stderr, "Nothing is being shared.")
	}
}

func stopShares(target string) {
	stopped := 0
	for _, r := range runningShares() {
		if target != "all" && strconv.Itoa(r.Port) != target {
			continue
		}
		terminate(r.PID)
		// Its own cleanup removes the record once the share is down
		for i := 0; i < 50 && exists(r.File); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if exists(r.File) {
			// It did not stop in time (or cannot be asked to, on Windows): stop it and clean up for it
			if process, err := os.FindProcess(r.PID); err == nil {
				process.Kill()
			}
			cleanUpAfter(r)
		}
		fmt.Fprintf(os.Stderr, "Stopped %s\n", r.URL)
		stopped++
	}
	if stopped == 0 && target != "all" {
		die("nothing is shared on port %s (see %s --list)", target, prog)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
