package tailscale

import (
	"encoding/json"
	"testing"
)

// The shape of `tailscale serve status --json` (ipn.ServeConfig), trimmed
const serveStatus = `{
	"TCP": {"443": {"HTTPS": true}, "10443": {"HTTPS": true}},
	"Web": {
		"host.example.ts.net:443": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:3000"}}},
		"host.example.ts.net:10443": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:52000"}}}
	},
	"Foreground": {
		"session-1": {
			"TCP": {"11443": {"HTTPS": true}},
			"Web": {"host.example.ts.net:11443": {"Handlers": {"/": {"Proxy": "http://127.0.0.1:8000"}}}}
		}
	}
}`

func TestServeConfig(t *testing.T) {
	var config ServeConfig
	if err := json.Unmarshal([]byte(serveStatus), &config); err != nil {
		t.Fatal(err)
	}
	for port, used := range map[int]bool{443: true, 10443: true, 11443: true, 12443: false, 8443: false} {
		if config.Uses(port) != used {
			t.Errorf("Uses(%d) = %v", port, !used)
		}
	}
	if got := config.Proxy(10443); got != "http://127.0.0.1:52000" {
		t.Errorf("Proxy(10443) = %q", got)
	}
	// A foreground serve belongs to another process; it is never cleaned up as a share's own
	if got := config.Proxy(11443); got != "" {
		t.Errorf("Proxy(11443) = %q", got)
	}
	var empty ServeConfig
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil || empty.Uses(10443) {
		t.Errorf("an empty config: %v, %v", err, empty.Uses(10443))
	}
}
