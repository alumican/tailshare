// Package tailscale runs the parts of the tailscale CLI that tailshare needs: the device's state
// and name, and tailscale serve on a port.
package tailscale

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Where the CLI is looked for when tailscale is not on PATH: the macOS app, and the Windows installer.
// Variables, so a test build can move them (go build -ldflags "-X ...").
var (
	MacApp      = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	WindowsPath = `C:\Program Files\Tailscale\tailscale.exe`
)

var ErrNotFound = errors.New("tailscale not found")

// CLI is the tailscale command.
type CLI struct{ Path string }

// Find looks for tailscale on PATH, then where the macOS app or the Windows installer puts it.
func Find() (CLI, error) {
	if path, err := exec.LookPath("tailscale"); err == nil {
		return CLI{path}, nil
	}
	fallback := map[string]string{"darwin": MacApp, "windows": WindowsPath}[runtime.GOOS]
	if fallback != "" {
		if path, err := exec.LookPath(fallback); err == nil {
			return CLI{path}, nil
		}
	}
	return CLI{}, ErrNotFound
}

// Status is what tailscale says about this device.
type Status struct {
	State string // Running when connected; Stopped when switched off, NeedsLogin, ...; empty when tailscale does not answer
	Host  string // this device's tailnet name, such as host.tailnet.ts.net
}

func (c CLI) Status() Status {
	out, _ := exec.Command(c.Path, "status", "--json").Output()
	var status struct {
		BackendState string
		Self         struct{ DNSName string }
	}
	if json.Unmarshal(out, &status) != nil {
		return Status{}
	}
	return Status{State: status.BackendState, Host: strings.TrimSuffix(status.Self.DNSName, ".")}
}

// ServeConfig is what tailscale serve does now: what runs in the background (TCP, Web), and what
// each tailscale serve running in the foreground does (Foreground, one config per session).
type ServeConfig struct {
	TCP map[string]json.RawMessage
	Web map[string]struct {
		Handlers map[string]struct{ Proxy string }
	}
	Foreground map[string]ServeConfig
}

func (c CLI) ServeConfig() (ServeConfig, error) {
	var config ServeConfig
	out, err := exec.Command(c.Path, "serve", "status", "--json").Output()
	if err != nil {
		return config, fmt.Errorf("tailscale serve status: %w", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(out, &config); err != nil {
		return config, fmt.Errorf("tailscale serve status: %w", err)
	}
	return config, nil
}

// Uses tells whether tailscale serve uses the port, for anything, in the background or in the foreground.
func (s ServeConfig) Uses(port int) bool {
	if _, ok := s.TCP[strconv.Itoa(port)]; ok {
		return true
	}
	for _, foreground := range s.Foreground {
		if foreground.Uses(port) {
			return true
		}
	}
	return false
}

// Proxy is where the port's / is proxied to in the background config, or "" when it is not.
func (s ServeConfig) Proxy(port int) string {
	for hostPort, web := range s.Web {
		if strings.HasSuffix(hostPort, ":"+strconv.Itoa(port)) {
			return web.Handlers["/"].Proxy
		}
	}
	return ""
}

// ForegroundProxy is where the port's / is proxied to by a tailscale serve running in the foreground,
// or "" when none does.
func (s ServeConfig) ForegroundProxy(port int) string {
	for _, foreground := range s.Foreground {
		if target := foreground.Proxy(port); target != "" {
			return target
		}
	}
	return ""
}

// Serve is a tailscale serve running in the foreground, as a child of tailshare. Its config lives
// only as long as it runs: tailscaled removes it when the child goes, however it goes, and it does
// not survive a restart. tailshare stops it with Stop.
type Serve struct {
	cli    CLI
	port   int
	cmd    *exec.Cmd
	output *lockedBuffer
	done   chan struct{}
}

// ServeForeground exposes target (http://127.0.0.1:<port>) on the tailnet, over HTTPS on port, and
// returns once tailscale serve has taken the port. attr sets how the child is started (its own
// process group, so a Ctrl-C reaches tailshare alone, which then stops it). started is told about
// the child as soon as it runs, so it can be stopped even if tailshare has to stop before this returns.
// The error carries what tailscale said.
func (c CLI) ServeForeground(port int, target string, attr *syscall.SysProcAttr, started func(*Serve)) (*Serve, error) {
	for attempt := 1; ; attempt++ {
		s, err := c.startServe(port, target, attr, started)
		if err != nil && strings.Contains(err.Error(), busy) && attempt < 5 {
			time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
			continue
		}
		return s, err
	}
}

func (c CLI) startServe(port int, target string, attr *syscall.SysProcAttr, started func(*Serve)) (*Serve, error) {
	s := &Serve{cli: c, port: port, output: &lockedBuffer{}, done: make(chan struct{})}
	s.cmd = exec.Command(c.Path, "serve", "--https="+strconv.Itoa(port), target)
	s.cmd.Stdout, s.cmd.Stderr = s.output, s.output
	s.cmd.SysProcAttr = attr
	if err := s.cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		s.cmd.Wait()
		close(s.done)
	}()
	started(s)

	// Running, and holding the port: its config shows in tailscale serve status
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		select {
		case <-s.done:
			if message := s.Output(); message != "" {
				return nil, errors.New(message)
			}
			return nil, errors.New("tailscale serve exited: " + s.cmd.ProcessState.String())
		case <-time.After(100 * time.Millisecond):
		}
		if config, err := c.ServeConfig(); err == nil && config.ForegroundProxy(port) == target {
			return s, nil
		}
	}
	s.Stop()
	return nil, errors.New("tailscale serve did not take the port in time")
}

// PID is the child's process ID, recorded so a share that is gone can have it stopped.
func (s *Serve) PID() int { return s.cmd.Process.Pid }

// Done is closed when the child exits.
func (s *Serve) Done() <-chan struct{} { return s.done }

// Output is what the child said.
func (s *Serve) Output() string { return strings.TrimSpace(s.output.String()) }

// Stop asks the child to stop, kills it if it does not, and waits until its port is free again.
func (s *Serve) Stop() {
	Terminate(s.cmd.Process)
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		s.cmd.Process.Kill()
		<-s.done
	}
	s.cli.WaitFree(s.port, 3*time.Second)
}

// WaitFree waits until tailscale serve no longer uses the port, for up to timeout.
func (c CLI) WaitFree(port int, timeout time.Duration) {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if config, err := c.ServeConfig(); err != nil || !config.Uses(port) {
			return
		}
	}
}

// Terminate asks a process to stop: SIGTERM, or on Windows, where that cannot be asked, Kill.
func Terminate(p *os.Process) {
	if runtime.GOOS == "windows" {
		p.Kill()
		return
	}
	p.Signal(syscall.SIGTERM)
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Serve exposes target on port in the background (tailscale serve --bg), until ServeOff.
// tailshare serves in the foreground; this is kept for what older versions left.
func (c CLI) Serve(port int, target string) error {
	return c.serve("--bg", "--https="+strconv.Itoa(port), target)
}

func (c CLI) ServeOff(port int) error {
	return c.serve("--https="+strconv.Itoa(port), "off")
}

// What tailscale serve says when another client changed the serve config at the same moment
// (such as another share starting); the change is safe to try again
const busy = "Another client is changing the serve config"

func (c CLI) serve(args ...string) error {
	for attempt := 1; ; attempt++ {
		out, err := exec.Command(c.Path, append([]string{"serve"}, args...)...).CombinedOutput()
		if err == nil {
			return nil
		}
		if strings.Contains(string(out), busy) && attempt < 5 {
			time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
			continue
		}
		if message := strings.TrimSpace(string(out)); message != "" {
			return errors.New(message)
		}
		return err
	}
}
