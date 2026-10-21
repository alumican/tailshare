#!/bin/bash
# With the real tailscale: serve over the tailnet and fetch the URL over HTTPS.
# Opens tailnet ports for a few seconds, so it runs only with TAILSHARE_LIVE=1 (tests/run --live).
# It stops only the shares it started, and checks the ports other services use are left as they were.
source "$(dirname "$0")/lib.sh"

live() {
	[[ "${TAILSHARE_LIVE:-}" == 1 ]] || skip "uses the real tailscale; run with tests/run --live"
	use_real_tailscale
	tailscale_bin="$(command -v tailscale || echo /Applications/Tailscale.app/Contents/MacOS/Tailscale)"
	[[ -x "$tailscale_bin" ]] || skip "tailscale is not installed"
	before="$(serve_ports)"
}

# The ports tailscale serve uses now, one per line
serve_ports() {
	"$tailscale_bin" serve status --json | python3 -c 'import json, sys; print("\n".join(sorted((json.load(sys.stdin) or {}).get("TCP") or {})))'
}

assert_others_untouched() {
	assert_eq "$(serve_ports)" "$before" "ports used by tailscale serve"
}

test_serve_over_the_tailnet() {
	live
	echo "hello over the tailnet" > "$FILES/hello.txt"
	run "$TAILSHARE" -d -j "$FILES/hello.txt"
	assert_status 0
	local url port pid
	url="$(json_get "$out" url)" port="$(json_get "$out" port)" pid="$(json_get "$out" pid)"
	STARTED="$STARTED $pid"
	assert_contains "$(serve_ports)" "$port"
	assert_eq "$(http_get "$url")" 200 "GET $url"
	assert_eq "$(cat "$SANDBOX/body")" "hello over the tailnet" "body"
	assert_eq "$(http_get "${url%/*}/missing")" 404 "GET a missing file"

	run "$TAILSHARE" --stop "$port"
	assert_status 0
	gone "$pid" || fail "tailshare (pid $pid) is still running"
	assert_others_untouched
	assert_clean
}

test_time_up_over_the_tailnet() {
	live
	echo hi > "$FILES/a.txt"
	run "$TAILSHARE" -d -j -t 2s "$FILES/a.txt"
	assert_status 0
	local pid
	pid="$(json_get "$out" pid)"
	STARTED="$STARTED $pid"
	wait_until 10 gone "$pid" || fail "still running after its time"
	assert_others_untouched
	assert_clean
}

run_tests
