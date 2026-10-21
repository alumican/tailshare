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

# The ports tailscale serve uses now, one per line, in the background or in the foreground
serve_ports() {
	"$tailscale_bin" serve status --json | python3 -c '
import json, sys
config = json.load(sys.stdin) or {}
ports = set(config.get("TCP") or {})
for foreground in (config.get("Foreground") or {}).values():
    ports |= set(foreground.get("TCP") or {})
print("\n".join(sorted(ports)))'
}

serving_on() {
	grep -qx "$1" <<< "$(serve_ports)"
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

test_secret_and_allow_over_the_tailnet() {
	live
	echo "for the owner" > "$FILES/a.txt"
	run "$TAILSHARE" -d -j --secret --allow nobody@example.invalid "$FILES/a.txt"
	assert_status 0
	local url port pid
	url="$(json_get "$out" url)" port="$(json_get "$out" port)" pid="$(json_get "$out" pid)"
	STARTED="$STARTED $pid"
	# This machine is on the tailnet as its owner, who is not allowed
	assert_eq "$(http_get "$url")" 403 "GET $url as someone not allowed"
	run "$TAILSHARE" --stop "$port"

	run "$TAILSHARE" -d -j --secret --allow '*' "$FILES/a.txt"
	assert_status 0
	url="$(json_get "$out" url)" port="$(json_get "$out" port)" pid="$(json_get "$out" pid)"
	STARTED="$STARTED $pid"
	assert_eq "$(http_get "$url")" 200 "GET $url"
	assert_eq "$(cat "$SANDBOX/body")" "for the owner" "body"
	assert_eq "$(http_get "${url%%:${port}/*}:$port/a.txt")" 404 "GET without the token"
	# tailscale serve told who asked, and the log says so, without the token
	grep -q "GET /a.txt 200 (.*@.*)" "$TAILSHARE_STATE_DIR"/log.* || fail "no login in the log: $(cat "$TAILSHARE_STATE_DIR"/log.*)"
	run "$TAILSHARE" --stop "$port"
	assert_status 0
	assert_others_untouched
	assert_clean
}

test_a_port_held_in_the_foreground_is_refused() {
	live
	# A port nothing uses, held by a tailscale serve in the foreground, as another tool would
	local port=19443
	while grep -qx "$port" <<< "$before"; do
		port=$((port + 1000))
	done
	"$tailscale_bin" serve --https="$port" http://127.0.0.1:9 > /dev/null 2>&1 < /dev/null &
	local holder=$!
	STARTED="$STARTED $holder"
	wait_until 10 serving_on "$port" || fail "the foreground serve did not start"
	echo hi > "$FILES/a.txt"
	run "$TAILSHARE" -t 3s -p "$port" "$FILES/a.txt"
	assert_status 1
	assert_contains "$err" "port $port is already used by tailscale serve"
	# A foreground serve's config goes with it
	kill -TERM "$holder"
	wait "$holder" 2> /dev/null
	wait_until 10 eval '! serving_on "$port"' || fail "the foreground serve left port $port behind"
	assert_others_untouched
	assert_clean
}

run_tests
