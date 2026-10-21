# Helpers for the tests, sourced by every *.test.sh: a sandbox per test, the fake tailscale, assertions.
# A test is a function named test_*; the file ends with run_tests, which runs each in its own subshell.
# Written for bash 3.2, like tailshare.

set -uo pipefail

TESTS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT="$(dirname "$TESTS")"

# Build tailshare into a directory of its own; extra arguments go to go build (such as -ldflags).
# tests/run builds once and passes it in TAILSHARE.
build_tailshare() {
	local dir="$1"
	shift
	mkdir -p "$dir"
	(cd "$ROOT" && go build "$@" -o "$dir/tailshare" ./cmd/tailshare) || return 1
	echo "$dir/tailshare"
}

# MARK: - Sandbox

# Everything a test touches lives in $SANDBOX: tailshare's records, config and temporary files,
# and the fake tailscale's serve config. Nothing outside it is read or changed.
setup() {
	SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/tailshare-test.XXXXXX")"
	SANDBOX="$(cd "$SANDBOX" && pwd -P)"
	FILES="$SANDBOX/files"
	mkdir -p "$SANDBOX/bin" "$SANDBOX/tmp" "$FILES"
	export TMPDIR="$SANDBOX/tmp"
	export XDG_CONFIG_HOME="$SANDBOX/config"
	export TAILSHARE_STATE_DIR="$SANDBOX/state"
	unset TAILSHARE_ADAPTER_PATH XDG_STATE_HOME FAKE_TAILSCALE_STATE FAKE_TAILSCALE_SERVE
	export FAKE_TAILSCALE_DIR="$SANDBOX/tailscale"
	ln -s "$TESTS/fake/tailscale" "$SANDBOX/bin/tailscale"
	export PATH="$SANDBOX/bin:$PATH"
	STARTED=""
}

# Stop whatever a test left running, even when it failed halfway, then remove the sandbox
teardown() {
	local file pid
	for file in "$TAILSHARE_STATE_DIR"/[0-9]*; do
		[[ -f "$file" ]] || continue
		pid="$(sed -n 's/^pid=//p' "$file")"
		[[ -n "$pid" ]] && STARTED="$STARTED $pid"
	done
	for pid in $STARTED; do
		kill -TERM "$pid" 2> /dev/null || true
	done
	for pid in $STARTED; do
		# Reaps the test's own children, which would otherwise linger as zombies; returns at once for others
		wait "$pid" 2> /dev/null
		wait_until 5 gone "$pid" || kill -KILL "$pid" 2> /dev/null || true
	done
	rm -rf "$SANDBOX"
}

# Use the real tailscale instead of the fake one
use_real_tailscale() {
	rm -f "$SANDBOX/bin/tailscale"
	unset FAKE_TAILSCALE_DIR
}

# MARK: - Running

# Run a command; keep its exit status in $status, its stdout in $out and its stderr in $err
run() {
	"$@" > "$SANDBOX/out" 2> "$SANDBOX/err" < /dev/null
	status=$?
	out="$(cat "$SANDBOX/out")"
	err="$(cat "$SANDBOX/err")"
}

# Run tailshare in the foreground, in the background of the test; its stdout goes to $SANDBOX/fg.out.
# Job control makes it a process group of its own, so it receives SIGINT as it would from Ctrl-C.
start_foreground() {
	set -m
	"$TAILSHARE" "$@" > "$SANDBOX/fg.out" 2> "$SANDBOX/fg.err" < /dev/null &
	FG_PID=$!
	set +m
	STARTED="$STARTED $FG_PID"
	wait_until 10 test -s "$SANDBOX/fg.out" || fail "no URL from tailshare: $(cat "$SANDBOX/fg.err")"
}

# Wait up to SECONDS for a command to succeed
wait_until() {
	local seconds="$1" i
	shift
	for ((i = 0; i < seconds * 10; i++)); do
		"$@" && return 0
		sleep 0.1
	done
	"$@"
}

gone() {
	! kill -0 "$1" 2> /dev/null
}

# A field of a JSON object
json_get() {
	python3 -c 'import json, sys; value = json.loads(sys.argv[1])[sys.argv[2]]; print("null" if value is None else value)' "$1" "$2"
}

# MARK: - HTTP

# GET a URL: print the HTTP status ("000" when nothing answers) and keep the body in $SANDBOX/body.
# Headers may follow, as "Name: value", such as the login tailscale serve adds for a tailnet user.
http_get() {
	python3 - "$SANDBOX/body" "$@" << 'EOF'
import sys, urllib.error, urllib.request
body, url = sys.argv[1:3]
request = urllib.request.Request(url, headers=dict(h.split(": ", 1) for h in sys.argv[3:]))
try:
    with urllib.request.urlopen(request, timeout=5) as response:
        status, data = response.status, response.read()
except urllib.error.HTTPError as error:
    status, data = error.code, error.read()
except OSError:
    status, data = 0, b""
with open(body, "wb") as file:
    file.write(data)
print(f"{status:03d}")
EOF
}

# The local server behind a tailnet port, as the fake tailscale was told: http://127.0.0.1:<port>
local_url() {
	cat "$FAKE_TAILSCALE_DIR/foreground/$1" 2> /dev/null || cat "$FAKE_TAILSCALE_DIR/serve/$1"
}

# GET a path from what is shared on a tailnet port, through the fake tailscale's record
fetch() {
	local port="$1" path="$2"
	shift 2
	http_get "$(local_url "$port")$path" "$@"
}

# MARK: - Fixtures

# Start tests/fake/app, an app on this machine to share by URL; its port is in $APP_PORT
start_app() {
	python3 "$TESTS/fake/app" "$SANDBOX/app.port" > "$SANDBOX/app.log" 2>&1 &
	STARTED="$STARTED $!"
	wait_until 30 test -s "$SANDBOX/app.port" || fail "the app did not start: $(cat "$SANDBOX/app.log")"
	APP_PORT="$(cat "$SANDBOX/app.port")"
}

# An IPA with only an Info.plist (binary, as Xcode writes it), which is all the ipa adapter reads
make_ipa() {
	python3 - "$1" << 'EOF'
import plistlib, sys, zipfile
info = {"CFBundleIdentifier": "com.example.demo", "CFBundleDisplayName": "Demo & Co", "CFBundleShortVersionString": "1.2.3"}
with zipfile.ZipFile(sys.argv[1], "w") as archive:
    archive.writestr("Payload/Demo.app/Info.plist", plistlib.dumps(info, fmt=plistlib.FMT_BINARY))
    archive.writestr("Payload/Demo.app/Demo", b"binary")
EOF
}

# The files and links under a directory, one relative path per line, sorted
tree() {
	(cd "$1" && find . \( -type f -o -type l \) | sed 's|^\./||' | LC_ALL=C sort)
}

# MARK: - Assertions

fail() {
	echo "$*" >&2
	exit 1
}

skip() {
	echo "$*" >&2
	exit 77
}

assert_status() {
	[[ "$status" == "$1" ]] || fail "exit status: expected $1, got $status
stdout: $out
stderr: $err"
}

assert_eq() {
	[[ "$1" == "$2" ]] || fail "${3:-value}: expected [$2], got [$1]"
}

assert_contains() {
	[[ "$1" == *"$2"* ]] || fail "expected to contain [$2]: [$1]"
}

assert_not_contains() {
	[[ "$1" != *"$2"* ]] || fail "expected not to contain [$2]: [$1]"
}

assert_empty_dir() {
	local leftovers=""
	[[ -d "$1" ]] && leftovers="$(ls -A "$1")"
	[[ -z "$leftovers" ]] || fail "${2:-$1} is not empty: $leftovers"
}

# Nothing left behind: no serve config, no record of the share, no temporary files
assert_clean() {
	if [[ -n "${FAKE_TAILSCALE_DIR:-}" ]]; then
		assert_empty_dir "$FAKE_TAILSCALE_DIR/serve" "the tailscale serve config"
		assert_empty_dir "$FAKE_TAILSCALE_DIR/foreground" "the tailscale serve config in the foreground"
	fi
	assert_empty_dir "$TAILSHARE_STATE_DIR" "the state directory"
	assert_empty_dir "$TMPDIR" "the temporary directory"
}

# MARK: - Runner

# Run each test_* function of the calling file, in the order they are written, each in a fresh sandbox.
# TEST_FILTER keeps only the tests whose name contains it.
run_tests() {
	local file="$0" name result passed=0 failed=0 skipped=0 log
	BUILD="$(mktemp -d "${TMPDIR:-/tmp}/tailshare-test-build.XXXXXX")"
	BUILD="$(cd "$BUILD" && pwd -P)"
	if [[ -z "${TAILSHARE:-}" ]]; then
		TAILSHARE="$(build_tailshare "$BUILD")" || { echo "FAIL    go build"; rm -rf "$BUILD"; return 1; }
	fi
	log="$BUILD/log"
	for name in $(grep -oE '^test_[A-Za-z0-9_]+' "$file"); do
		[[ -z "${TEST_FILTER:-}" || "$name" == *"$TEST_FILTER"* ]] || continue
		(
			setup
			trap 'result=$?; teardown; exit $result' EXIT
			"$name"
		) > "$log" 2>&1
		result=$?
		case "$result" in
			0) passed=$((passed + 1)); echo "ok      $name" ;;
			77) skipped=$((skipped + 1)); echo "skip    $name: $(tail -n 1 "$log")" ;;
			*) failed=$((failed + 1)); echo "FAIL    $name"; sed 's/^/        /' "$log" ;;
		esac
	done
	rm -rf "$BUILD"
	echo "# $(basename "$file"): $passed passed, $failed failed, $skipped skipped"
	((failed == 0))
}
