// Package tailscale runs the parts of the tailscale CLI that tailshare needs: the device's state
// and name, and tailscale serve on a port.
package tailscale

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
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

// ServeConfig is what tailscale serve does now.
type ServeConfig struct {
	TCP map[string]json.RawMessage
	Web map[string]struct {
		Handlers map[string]struct{ Proxy string }
	}
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

// Uses tells whether tailscale serve uses the port, for anything.
func (s ServeConfig) Uses(port int) bool {
	_, ok := s.TCP[strconv.Itoa(port)]
	return ok
}

// Proxy is where the port's / is proxied to, or "" when it is not.
func (s ServeConfig) Proxy(port int) string {
	for hostPort, web := range s.Web {
		if strings.HasSuffix(hostPort, ":"+strconv.Itoa(port)) {
			return web.Handlers["/"].Proxy
		}
	}
	return ""
}

// Serve exposes target (http://127.0.0.1:<port>) on the tailnet, over HTTPS on port, until ServeOff.
// The error carries what tailscale said.
func (c CLI) Serve(port int, target string) error {
	out, err := exec.Command(c.Path, "serve", "--bg", "--https="+strconv.Itoa(port), target).CombinedOutput()
	if err != nil {
		if message := strings.TrimSpace(string(out)); message != "" {
			return errors.New(message)
		}
		return err
	}
	return nil
}

func (c CLI) ServeOff(port int) error {
	return exec.Command(c.Path, "serve", "--https="+strconv.Itoa(port), "off").Run()
}
