package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLocalTarget(t *testing.T) {
	for raw, ok := range map[string]bool{
		"http://127.0.0.1:8765":          true,
		"http://127.0.0.1:8765/studio?a": true,
		"http://localhost:3000":          true,
		"http://[::1]:8080/":             true,
		"http://127.1.2.3":               true,
		"https://127.0.0.1:8765":         false, // tailshare adds the HTTPS; a local https server is not passed on to
		"http://192.168.1.10:8765":       false,
		"http://example.com":             false,
		"http://localhost.example.com":   false,
		"http://":                        false,
		"http://0.0.0.0:8765":            false,
	} {
		if _, err := localTarget(raw); (err == nil) != ok {
			t.Errorf("%s: %v", raw, err)
		}
	}
	target, _ := localTarget("http://user:pass@127.0.0.1:8765/a#frag")
	if target.String() != "http://127.0.0.1:8765/a" {
		t.Errorf("credentials or fragment kept: %s", target)
	}
}

// An app behind a share: it tells what it was sent, takes uploads, streams, and upgrades connections
func backend(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"host": r.Host, "uri": r.URL.RequestURI(), "forwardedHost": r.Header.Get("X-Forwarded-Host"),
			"forwardedProto": r.Header.Get("X-Forwarded-Proto"), "login": r.Header.Get("Tailscale-User-Login"),
		})
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, n)
	})
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(700 * time.Millisecond)
		fmt.Fprint(w, "data: second\n\n")
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "echo" {
			http.Error(w, "upgrade required", http.StatusUpgradeRequired)
			return
		}
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		line, _ := rw.ReadString('\n')
		rw.WriteString("echo: " + line)
		rw.Flush()
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func proxyShare(t *testing.T, app *httptest.Server, token string) (*httptest.Server, *http.Client) {
	target, err := localTarget(app.URL)
	if err != nil {
		t.Fatal(err)
	}
	share := httptest.NewServer(&shareHandler{content: newProxy(target), token: token, cookie: "tailshare_10443", quiet: true})
	t.Cleanup(share.Close)
	return share, &http.Client{}
}

func TestProxyPassesRequestsOn(t *testing.T) {
	app := backend(t)
	share, client := proxyShare(t, app, "")
	request, _ := http.NewRequest(http.MethodGet, share.URL+"/echo?x=1", nil)
	request.Header.Set("Tailscale-User-Login", "alice@example.com")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var seen map[string]string
	json.NewDecoder(response.Body).Decode(&seen)
	shareHost := strings.TrimPrefix(share.URL, "http://")
	want := map[string]string{
		"host": strings.TrimPrefix(app.URL, "http://"), "uri": "/echo?x=1", "forwardedHost": shareHost,
		"forwardedProto": "https", "login": "alice@example.com",
	}
	for key, value := range want {
		if seen[key] != value {
			t.Errorf("%s: %q, want %q", key, seen[key], value)
		}
	}
}

func TestProxyStreamsBothWays(t *testing.T) {
	app := backend(t)
	share, client := proxyShare(t, app, "")

	// A large upload, as the app's frames are
	body := bytes.Repeat([]byte("x"), 20<<20)
	response, err := client.Post(share.URL+"/upload", "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(got) != fmt.Sprint(len(body)) {
		t.Errorf("upload: the app got %s bytes of %d", got, len(body))
	}

	// Server-sent events arrive as they are sent, not when the response ends
	start := time.Now()
	response, err = client.Get(share.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	first, _ := reader.ReadString('\n')
	if elapsed := time.Since(start); first != "data: first\n" || elapsed > 500*time.Millisecond {
		t.Errorf("first event %q after %v", first, elapsed)
	}
	rest, _ := io.ReadAll(reader)
	response.Body.Close()
	if !strings.Contains(string(rest), "data: second") {
		t.Errorf("rest of the stream: %q", rest)
	}
}

func TestProxyUpgradesConnections(t *testing.T) {
	app := backend(t)
	share, _ := proxyShare(t, app, "")
	conn, err := net.Dial("tcp", strings.TrimPrefix(share.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "GET /ws HTTP/1.1\r\nHost: x\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n")
	reader := bufio.NewReader(conn)
	status, _ := reader.ReadString('\n')
	if !strings.HasPrefix(status, "HTTP/1.1 101") {
		t.Fatalf("status: %q", status)
	}
	for line, _ := reader.ReadString('\n'); line != "\r\n" && line != ""; line, _ = reader.ReadString('\n') {
	}
	fmt.Fprint(conn, "ping\n")
	if line, _ := reader.ReadString('\n'); line != "echo: ping\n" {
		t.Errorf("over the upgraded connection: %q", line)
	}
}

// With --secret, opening the URL sets a cookie, so the app's own absolute links work. The cookie is
// Secure: a browser gets it over the HTTPS of tailscale serve; this test reaches the share over plain
// HTTP, so it sends the cookie back by hand rather than through a cookie jar.
func TestProxyWithSecret(t *testing.T) {
	app := backend(t)
	share, client := proxyShare(t, app, "tok")
	get := func(path, cookie string) *http.Response {
		request, _ := http.NewRequest(http.MethodGet, share.URL+path, nil)
		if cookie != "" {
			request.Header.Set("Cookie", cookie)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response
	}
	if code := get("/echo", "").StatusCode; code != http.StatusNotFound {
		t.Errorf("before opening the URL: %d", code)
	}
	opened := get("/tok/echo", "")
	cookies := opened.Cookies()
	if opened.StatusCode != http.StatusOK || len(cookies) != 1 {
		t.Fatalf("the URL: %d, cookies %v", opened.StatusCode, cookies)
	}
	cookie := cookies[0]
	if cookie.Name != "tailshare_10443" || cookie.Value != "tok" || cookie.Path != "/" || !cookie.HttpOnly || !cookie.Secure ||
		cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie: %+v", cookie)
	}
	for value, want := range map[string]int{
		"tailshare_10443=tok": http.StatusOK,       // an absolute link, with the cookie
		"tailshare_10443=tak": http.StatusNotFound, // a wrong token
		"tailshare_11443=tok": http.StatusNotFound, // the cookie of another share
	} {
		if code := get("/echo", value).StatusCode; code != want {
			t.Errorf("Cookie: %s: %d, want %d", value, code, want)
		}
	}
}

func TestProxyWhenTheAppIsDown(t *testing.T) {
	app := backend(t)
	share, client := proxyShare(t, app, "")
	app.Close()
	response, err := client.Get(share.URL + "/echo")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Errorf("status: %d", response.StatusCode)
	}
}
