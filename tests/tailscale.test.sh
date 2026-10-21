#!/bin/bash
# When tailscale is missing, not running, off or logged out, or its port is taken: tailshare refuses and leaves nothing.
# Uses the fake tailscale only.
source "$(dirname "$0")/lib.sh"

# A copy of tailshare that looks for the Tailscale app in the sandbox instead of /Applications,
# run with a PATH that has no tailscale on it
without_tailscale_on_path() {
	rm "$SANDBOX/bin/tailscale"
	PATH="$SANDBOX/bin:/usr/bin:/bin" command -v tailscale > /dev/null && skip "tailscale is in /usr/bin or /bin"
	sed "s|/Applications/Tailscale.app|$SANDBOX/Applications/Tailscale.app|g" "$TAILSHARE" > "$SANDBOX/bin/tailshare"
	chmod +x "$SANDBOX/bin/tailshare"
	export TAILSHARE_ADAPTER_PATH="$ROOT/adapters"
	export PATH="$SANDBOX/bin:/usr/bin:/bin"
}

test_tailscale_not_installed() {
	echo hi > "$FILES/a.txt"
	without_tailscale_on_path
	run tailshare -t 3s "$FILES/a.txt"
	assert_status 1
	assert_eq "$err" "tailshare: tailscale not found; install it, or put it on PATH" "stderr"
	assert_clean
}

test_tailscale_app_when_not_on_path() {
	echo hi > "$FILES/a.txt"
	without_tailscale_on_path
	mkdir -p "$SANDBOX/Applications/Tailscale.app/Contents/MacOS"
	ln -s "$TESTS/fake/tailscale" "$SANDBOX/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	run tailshare -d -q "$FILES/a.txt"
	assert_status 0
	assert_eq "$out" "https://host.example.ts.net:10443/a.txt" "URL"
	run tailshare --stop 10443
	assert_status 0
	assert_clean
}

test_tailscale_not_running() {
	echo hi > "$FILES/a.txt"
	export FAKE_TAILSCALE_STATE=down
	run "$TAILSHARE" -t 3s "$FILES/a.txt"
	assert_status 1
	assert_eq "$out" "" "stdout"
	assert_eq "$err" "tailshare: tailscale is not running" "stderr"
	assert_clean
}

test_tailscale_off_or_logged_out() {
	echo hi > "$FILES/a.txt"
	local state
	for state in Stopped NeedsLogin NeedsMachineAuth Starting; do
		export FAKE_TAILSCALE_STATE=$state
		run "$TAILSHARE" -t 3s "$FILES/a.txt"
		[[ "$status" == 1 ]] || fail "$state: expected exit status 1, got $status ($out)"
		assert_eq "$out" "" "stdout when $state"
		assert_contains "$err" "tailscale is not connected (state: $state)"
		assert_clean
	done
}

test_tailscale_off_when_detached() {
	echo hi > "$FILES/a.txt"
	export FAKE_TAILSCALE_STATE=Stopped
	run "$TAILSHARE" -d -q "$FILES/a.txt"
	assert_status 1
	assert_eq "$out" "" "stdout"
	assert_contains "$err" "tailscale is not connected (state: Stopped)"
	assert_contains "$err" "failed to start in the background"
	assert_clean
}

test_queries_work_without_tailscale() {
	echo hi > "$FILES/a.txt"
	export FAKE_TAILSCALE_STATE=down
	run "$TAILSHARE" --list
	assert_status 0
	run "$TAILSHARE" --stop all
	assert_status 0
	run "$TAILSHARE" --adapters
	assert_status 0
	run "$TAILSHARE" --which "$FILES/a.txt"
	assert_status 0
	assert_eq "$(cat "$FAKE_TAILSCALE_DIR/calls")" "" "calls to tailscale"
}

test_serve_failure_leaves_nothing() {
	echo hi > "$FILES/a.txt"
	export FAKE_TAILSCALE_SERVE=fail
	run "$TAILSHARE" -t 3s "$FILES/a.txt"
	assert_status 1
	assert_eq "$out" "" "stdout"
	assert_contains "$err" "tailscale serve failed"
	assert_clean
	# The local server was started before tailscale serve; it is stopped too
	pgrep -f "http.server .* --directory $TMPDIR/" > /dev/null && fail "the local server is still running"
	return 0
}

test_port_in_use_is_refused() {
	echo hi > "$FILES/a.txt"
	mkdir -p "$FAKE_TAILSCALE_DIR/serve"
	echo "http://127.0.0.1:1" > "$FAKE_TAILSCALE_DIR/serve/10443"
	run "$TAILSHARE" -t 3s -p 10443 "$FILES/a.txt"
	assert_status 1
	assert_contains "$err" "port 10443 is already used by tailscale serve"
	assert_eq "$(cat "$FAKE_TAILSCALE_DIR/serve/10443")" "http://127.0.0.1:1" "the other service's config"
	assert_eq "$(grep -c -- 'off' "$FAKE_TAILSCALE_DIR/calls")" 0 "calls that turn a port off"
}

test_default_port_skips_ports_in_use() {
	echo hi > "$FILES/a.txt"
	mkdir -p "$FAKE_TAILSCALE_DIR/serve"
	echo "http://127.0.0.1:1" > "$FAKE_TAILSCALE_DIR/serve/10443"
	echo "http://127.0.0.1:2" > "$FAKE_TAILSCALE_DIR/serve/11443"
	run "$TAILSHARE" -d -j "$FILES/a.txt"
	assert_status 0
	assert_eq "$(json_get "$out" port)" 12443 "port"
	run "$TAILSHARE" --stop 12443
	assert_status 0
	assert_eq "$(cat "$FAKE_TAILSCALE_DIR/serve/10443")" "http://127.0.0.1:1" "the other service's config"
	assert_eq "$(cat "$FAKE_TAILSCALE_DIR/serve/11443")" "http://127.0.0.1:2" "the other service's config"
	assert_eq "$(ls "$FAKE_TAILSCALE_DIR/serve")" "10443
11443" "ports served"
}

run_tests
