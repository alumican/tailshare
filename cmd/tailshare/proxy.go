package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

// A share of a server running on this machine, such as a dev server, instead of a path:
// tailshare http://127.0.0.1:8765. Requests are passed on to it by tailshare's own handler, so
// --secret, --allow and the request log apply as for files.

// isURL tells whether what was given to share is a URL rather than a path.
func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// localTarget checks that a URL is a server on this machine, the only kind tailshare passes requests on to.
func localTarget(raw string) (*url.URL, error) {
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" {
		return nil, fmt.Errorf("not a URL: %s", raw)
	}
	if target.Scheme != "http" {
		return nil, fmt.Errorf("only http:// servers can be shared (got %s); tailshare adds the HTTPS", raw)
	}
	host := target.Hostname()
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("refusing to share %s: only servers on this machine (127.0.0.1, ::1, localhost) can be shared", raw)
	}
	target.User = nil
	target.Fragment = ""
	return target, nil
}

// answers tells whether something listens at the target, so a share is not started in front of nothing.
func answers(target *url.URL) bool {
	port := target.Port()
	if port == "" {
		port = "80"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(target.Hostname(), port), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// newProxy passes requests on to the server at target (its scheme and host; the whole server is
// shared, whatever path target has), streaming both ways: request bodies of any size, responses
// flushed as they come (server-sent events), and upgraded connections (WebSocket).
// The app sees itself addressed as target (dev servers often check the Host header), with the
// address it was really opened at in X-Forwarded-Host, and who asked in Tailscale-User-Login.
func newProxy(target *url.URL) http.Handler {
	server := &url.URL{Scheme: target.Scheme, Host: target.Host}
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(server)
			r.SetXForwarded()
			r.Out.Header.Set("X-Forwarded-Proto", "https")
			// The address it was opened at, as tailscale serve passed it on
			r.Out.Header.Set("X-Forwarded-Host", r.In.Host)
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(r.Context().Err(), err) {
				return // the client went away
			}
			fmt.Fprintf(os.Stderr, "%s: cannot reach %s: %v\n", prog, target.Redacted(), err)
			http.Error(w, "tailshare: the app behind this share does not answer", http.StatusBadGateway)
		},
	}
}
