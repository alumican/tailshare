#!/bin/bash
# Serving for real through the fake tailscale: what each adapter serves over HTTP, --detach, --json,
# --list, --stop, time up, Ctrl-C and TERM, and that every way of stopping leaves nothing behind.
source "$(dirname "$0")/lib.sh"

# Serve in the background; keep the JSON line in $share and the port in $port
share() {
	run "$TAILSHARE" -d -j "$@"
	assert_status 0
	share="$out"
	port="$(json_get "$share" port)"
	STARTED="$STARTED $(json_get "$share" pid)"
}

# Stop the share on $port, and check it is all gone: the process, the local server and every file
stop_and_check() {
	local server pid
	server="$(local_url "$port")"
	pid="$(json_get "$share" pid)"
	run "$TAILSHARE" --stop "$port"
	assert_status 0
	assert_contains "$err" "Stopped https://host.example.ts.net:$port/"
	gone "$pid" || fail "tailshare (pid $pid) is still running"
	assert_eq "$(http_get "$server/")" 000 "the local server after stopping"
	assert_clean
}

assert_fetch() {
	local path="$1" want="$2"
	shift 2
	assert_eq "$(fetch "$port" "$path" "$@")" "$want" "GET $path${1:+ ($*)}"
}

# MARK: - Each adapter over HTTP

test_file_with_spaces_in_its_name() {
	echo "hello" > "$FILES/my notes.txt"
	echo "secret" > "$FILES/other.txt"
	share "$FILES/my notes.txt"
	assert_eq "$(json_get "$share" url)" "https://host.example.ts.net:10443/my%20notes.txt" "URL"
	assert_eq "$(json_get "$share" adapter)" file "adapter"
	assert_fetch "/my%20notes.txt" 200
	assert_eq "$(cat "$SANDBOX/body")" "hello" "body"
	assert_fetch "/other.txt" 404
	assert_fetch "/../other.txt" 404
	stop_and_check
}

test_html_page_and_what_it_refers_to() {
	mkdir -p "$FILES/site/css" "$FILES/site/img"
	printf '<link rel="stylesheet" href="css/style.css"><img src="img/logo.png">\n' > "$FILES/site/report.html"
	printf '@import "base.css";\nbody { background: url(../img/bg.png) }\n' > "$FILES/site/css/style.css"
	touch "$FILES/site/css/base.css" "$FILES/site/img/logo.png" "$FILES/site/img/bg.png" "$FILES/site/secret.txt"
	share "$FILES/site/report.html"
	assert_eq "$(json_get "$share" url)" "https://host.example.ts.net:10443/report.html" "URL"
	assert_fetch /report.html 200
	assert_fetch /css/style.css 200
	assert_fetch /css/base.css 200
	assert_fetch /img/logo.png 200
	assert_fetch /img/bg.png 200
	assert_fetch /secret.txt 404
	stop_and_check
}

test_directory_without_hidden_entries() {
	mkdir -p "$FILES/dist/assets" "$FILES/dist/.git"
	echo "<h1>built</h1>" > "$FILES/dist/index.html"
	touch "$FILES/dist/assets/app.js" "$FILES/dist/.env" "$FILES/dist/.git/config"
	share "$FILES/dist"
	assert_eq "$(json_get "$share" url)" "https://host.example.ts.net:10443/" "URL"
	assert_fetch / 200
	assert_eq "$(cat "$SANDBOX/body")" "<h1>built</h1>" "index.html at /"
	assert_fetch /assets/app.js 200
	assert_fetch /.env 404
	assert_fetch /.git/config 404
	stop_and_check
}

test_ipa_install_page() {
	make_ipa "$FILES/Demo.ipa"
	share "$FILES/Demo.ipa"
	assert_eq "$(json_get "$share" url)" "https://host.example.ts.net:10443/" "URL"
	assert_fetch / 200
	assert_contains "$(cat "$SANDBOX/body")" "itms-services://"
	assert_fetch /manifest.plist 200
	assert_contains "$(cat "$SANDBOX/body")" "https://host.example.ts.net:10443/Demo.ipa?v="
	assert_fetch /Demo.ipa 200
	stop_and_check
}

test_adapter_failure_serves_nothing() {
	echo "not a zip" > "$FILES/Broken.ipa"
	run "$TAILSHARE" -q -t 3s "$FILES/Broken.ipa"
	assert_status 1
	assert_eq "$out" "" "stdout"
	assert_contains "$err" "adapter ipa failed"
	assert_clean
	assert_eq "$(grep -c '^serve --https=[0-9]* http' "$FAKE_TAILSCALE_DIR/calls")" 0 "calls to tailscale serve"
}

test_adapter_of_your_own() {
	local own="$XDG_CONFIG_HOME/tailshare/adapters/30-apk"
	mkdir -p "$own"
	printf '# Install page for an Android app\n*.apk\n' > "$own/match"
	echo '<a href="%s">Install</a>' > "$own/page.html"
	cat > "$own/prepare" << 'EOF'
#!/bin/bash
# The contract: prepare <path> <site> <base-url>, TAILSHARE_ADAPTER_DIR, the path to open on the last line
set -euo pipefail
input="$1" site="$2" base="$3"
ln -s "$input" "$site/$(basename "$input")"
printf "$(cat "$TAILSHARE_ADAPTER_DIR/page.html")\n" "$base/$(basename "$input")" > "$site/index.html"
echo "said on stderr" >&2
echo "something else first"
echo "/"
EOF
	chmod +x "$own/prepare"
	echo "apk" > "$FILES/My App.apk"

	run "$TAILSHARE" -d -j "$FILES/My App.apk"
	assert_status 0
	share="$out" port="$(json_get "$out" port)"
	STARTED="$STARTED $(json_get "$share" pid)"
	assert_eq "$(json_get "$share" adapter)" apk "adapter"
	assert_eq "$(json_get "$share" url)" "https://host.example.ts.net:10443/" "URL"
	assert_fetch / 200
	assert_eq "$(cat "$SANDBOX/body")" '<a href="https://host.example.ts.net:10443/My App.apk">Install</a>' "index.html"
	assert_fetch "/My%20App.apk" 200
	assert_eq "$(cat "$SANDBOX/body")" "apk" "the APK, through the link"
	stop_and_check
}

test_adapter_of_your_own_that_fails() {
	local own="$XDG_CONFIG_HOME/tailshare/adapters/30-broken"
	mkdir -p "$own"
	echo "*.txt" > "$own/match"
	printf '#!/bin/bash\necho "cannot do this" >&2\nexit 3\n' > "$own/prepare"
	chmod +x "$own/prepare"
	echo hi > "$FILES/a.txt"
	run "$TAILSHARE" -t 3s "$FILES/a.txt"
	assert_status 1
	assert_contains "$err" "cannot do this"
	assert_contains "$err" "adapter broken failed for $FILES/a.txt"
	assert_clean

	printf '#!/bin/bash\necho "not a path"\n' > "$own/prepare"
	run "$TAILSHARE" -t 3s "$FILES/a.txt"
	assert_status 1
	assert_contains "$err" "adapter broken printed no path to open (got: not a path)"
	assert_clean
}

# MARK: - Options

test_json_and_time() {
	echo hi > "$FILES/a.txt"
	share -t 10m "$FILES/a.txt"
	python3 - "$share" "$FILES/a.txt" << 'EOF' || fail "JSON: $share"
import json, sys, time
from datetime import datetime
share = json.loads(sys.argv[1])
assert sorted(share) == ["adapter", "expires", "path", "pid", "port", "url"], share
assert share["path"] == sys.argv[2] and isinstance(share["port"], int) and isinstance(share["pid"], int), share
left = datetime.strptime(share["expires"], "%Y-%m-%dT%H:%M:%S%z").timestamp() - time.time()
assert 590 <= left <= 600, left
EOF
	stop_and_check
}

test_time_zero_never_expires() {
	echo hi > "$FILES/a.txt"
	share -t 0 "$FILES/a.txt"
	assert_eq "$(json_get "$share" expires)" null "expires"
	stop_and_check
}

test_chosen_port() {
	echo hi > "$FILES/a.txt"
	share -p 20443 "$FILES/a.txt"
	assert_eq "$port" 20443 "port"
	assert_eq "$(json_get "$share" url)" "https://host.example.ts.net:20443/a.txt" "URL"
	stop_and_check
}

test_detach_quiet_prints_only_the_url() {
	echo hi > "$FILES/a.txt"
	run "$TAILSHARE" -dq "$FILES/a.txt"
	assert_status 0
	assert_eq "$out" "https://host.example.ts.net:10443/a.txt" "stdout"
	assert_eq "$err" "" "stderr"
	run "$TAILSHARE" --stop 10443
	assert_status 0
	assert_clean
}

test_detach_says_how_to_stop() {
	echo hi > "$FILES/a.txt"
	run "$TAILSHARE" -d "$FILES/a.txt"
	assert_status 0
	assert_eq "$out" "https://host.example.ts.net:10443/a.txt" "stdout"
	assert_contains "$err" "Stop it with: tailshare --stop 10443"
	run "$TAILSHARE" --stop 10443
	assert_clean
}

test_secret_puts_a_token_in_the_url() {
	make_ipa "$FILES/Demo.ipa"
	share --secret "$FILES/Demo.ipa"
	local url token
	url="$(json_get "$share" url)"
	[[ "$url" =~ ^https://host\.example\.ts\.net:10443/([a-z2-7]{26})/$ ]] || fail "URL: $url"
	token="${BASH_REMATCH[1]}"
	assert_fetch "/$token/" 200
	assert_fetch "/$token/Demo.ipa" 200
	assert_fetch / 404
	assert_fetch /Demo.ipa 404
	assert_fetch /manifest.plist 404
	# The install page and the manifest point inside the token
	assert_fetch "/$token/manifest.plist" 200
	assert_contains "$(cat "$SANDBOX/body")" "https://host.example.ts.net:10443/$token/Demo.ipa?v="
	run "$TAILSHARE" --list --json
	assert_eq "$(json_get "$out" url)" "$url" "URL in --list"
	stop_and_check
}

test_allow_lets_only_these_users_in() {
	echo hi > "$FILES/a.txt"
	share --allow alice@example.com --allow '*@team.example' "$FILES/a.txt"
	assert_fetch /a.txt 403
	assert_fetch /a.txt 403 "Tailscale-User-Login: bob@example.com"
	assert_fetch /a.txt 200 "Tailscale-User-Login: alice@example.com"
	assert_fetch /a.txt 200 "Tailscale-User-Login: carol@team.example"
	# The request log says who asked
	assert_contains "$(cat "$TAILSHARE_STATE_DIR"/log.*)" "GET /a.txt 200 (alice@example.com)"
	assert_contains "$(cat "$TAILSHARE_STATE_DIR"/log.*)" "GET /a.txt 403 (bob@example.com)"
	stop_and_check
}

test_allow_needs_a_login() {
	echo hi > "$FILES/a.txt"
	run "$TAILSHARE" --allow '' "$FILES/a.txt"
	assert_status 2
	run "$TAILSHARE" --allow '[bad' "$FILES/a.txt"
	assert_status 2
	assert_contains "$err" "--allow takes logins such as alice@example.com"
}

test_once_stops_after_the_whole_file() {
	echo "the file" > "$FILES/a.txt"
	share --once -t 0 "$FILES/a.txt"
	local pid
	pid="$(json_get "$share" pid)"
	assert_fetch / 200
	assert_fetch /a.txt 200
	wait_until 15 gone "$pid" || fail "still running after the download"
	assert_clean
}

test_qr_code_on_stderr() {
	echo hi > "$FILES/a.txt"
	run "$TAILSHARE" -d --qr "$FILES/a.txt"
	assert_status 0
	assert_eq "$out" "https://host.example.ts.net:10443/a.txt" "stdout"
	assert_contains "$err" $'\e[30;47m'
	assert_contains "$err" "▀"
	run "$TAILSHARE" --stop 10443
	run "$TAILSHARE" -d -q --qr "$FILES/a.txt"
	assert_eq "$err" "" "stderr with --quiet"
	run "$TAILSHARE" --stop 10443
	assert_clean
}

test_home_and_the_root_are_refused() {
	export HOME="$SANDBOX/home"
	mkdir -p "$HOME/site"
	echo hi > "$HOME/site/index.html"
	run "$TAILSHARE" -t 3s "$HOME"
	assert_status 1
	assert_contains "$err" "refusing to serve $HOME: it is your home directory"
	run "$TAILSHARE" -t 3s "$SANDBOX"
	assert_status 1
	assert_contains "$err" "it holds your home directory"
	run "$TAILSHARE" -t 3s -d /
	assert_status 1
	assert_contains "$err" "refusing to serve /: it is the root of a disk"
	assert_eq "$(cat "$FAKE_TAILSCALE_DIR/calls" 2> /dev/null)" "" "calls to tailscale"
	share "$HOME/site"
	assert_fetch / 200
	stop_and_check
}

test_a_share_stops_when_tailscale_serve_does() {
	echo hi > "$FILES/a.txt"
	start_foreground -t 0 "$FILES/a.txt"
	local serve_pid
	serve_pid="$(sed -n 's/^serve_pid=//p' "$TAILSHARE_STATE_DIR/10443")"
	[[ -n "$serve_pid" ]] || fail "no serve_pid in the record"
	kill -TERM "$serve_pid"
	wait "$FG_PID"
	status=$?
	out="$(cat "$SANDBOX/fg.out")" err="$(cat "$SANDBOX/fg.err")"
	assert_status 1
	assert_contains "$err" "tailscale serve stopped"
	assert_clean
}

# MARK: - Running shares

test_list_and_stop_one_of_two() {
	echo hi > "$FILES/a.txt"
	mkdir "$FILES/dist"
	share "$FILES/a.txt"
	local first="$share" first_port="$port"
	share -t 0 "$FILES/dist"
	assert_eq "$port" 11443 "second port"

	run "$TAILSHARE" --list
	assert_status 0
	assert_contains "$out" "10443 file   until "
	assert_contains "$out" "11443 dir    until stopped"
	assert_contains "$out" "$FILES/dist"
	run "$TAILSHARE" --list --json
	assert_eq "$(wc -l <<< "$out" | tr -d ' ')" 2 "lines of --list --json"
	assert_eq "$(head -n 1 <<< "$out")" "$first" "the first share as listed"

	run "$TAILSHARE" --stop "$first_port"
	assert_status 0
	run "$TAILSHARE" --list --json
	assert_eq "$(json_get "$out" port)" 11443 "the share left"
	assert_fetch / 200
	stop_and_check
}

test_list_when_nothing_is_shared() {
	run "$TAILSHARE" --list
	assert_status 0
	assert_eq "$out" "" "stdout"
	assert_eq "$err" "Nothing is being shared." "stderr"
	run "$TAILSHARE" --list --json
	assert_status 0
	assert_eq "$out$err" "" "output"
}

test_stop_unknown_port() {
	run "$TAILSHARE" --stop 10443
	assert_status 1
	assert_contains "$err" "nothing is shared on port 10443"
}

test_stop_all() {
	echo hi > "$FILES/a.txt"
	share "$FILES/a.txt"
	share "$FILES/a.txt"
	run "$TAILSHARE" --stop all
	assert_status 0
	assert_eq "$(grep -c Stopped <<< "$err")" 2 "shares stopped"
	assert_clean
}

test_a_killed_share_is_cleaned_up_after() {
	echo hi > "$FILES/a.txt"
	share "$FILES/a.txt"
	local pid server
	pid="$(json_get "$share" pid)"
	server="$(local_url "$port")"
	kill -KILL "$pid"
	wait_until 5 gone "$pid"
	assert_eq "$(http_get "$server/")" 000 "the local server of a killed share"
	# It could not clean up after itself; --list finds it gone and does
	run "$TAILSHARE" --list --json
	assert_status 0
	assert_eq "$out" "" "running shares"
	assert_clean
}

test_a_killed_share_leaves_a_port_taken_since_alone() {
	echo hi > "$FILES/a.txt"
	share "$FILES/a.txt"
	kill -KILL "$(json_get "$share" pid)"
	wait_until 5 gone "$(json_get "$share" pid)"
	# Something else took the port since
	echo "http://127.0.0.1:1" > "$FAKE_TAILSCALE_DIR/serve/$port"
	run "$TAILSHARE" --list
	assert_eq "$(cat "$FAKE_TAILSCALE_DIR/serve/$port")" "http://127.0.0.1:1" "the other service's config"
	assert_empty_dir "$TAILSHARE_STATE_DIR"
}

# MARK: - Every way of stopping leaves nothing behind

test_time_up_when_detached() {
	echo hi > "$FILES/a.txt"
	share -t 2s "$FILES/a.txt"
	local server pid
	server="$(local_url "$port")"
	pid="$(json_get "$share" pid)"
	assert_fetch /a.txt 200
	wait_until 10 gone "$pid" || fail "still running after its time"
	assert_eq "$(http_get "$server/")" 000 "the local server after time up"
	assert_clean
}

test_time_up_in_the_foreground() {
	echo hi > "$FILES/a.txt"
	run "$TAILSHARE" -t 1s "$FILES/a.txt"
	assert_status 0
	assert_eq "$out" "https://host.example.ts.net:10443/a.txt" "stdout"
	assert_contains "$err" "Time is up (1s)."
	assert_contains "$err" "Stopped serving https://host.example.ts.net:10443/"
	assert_clean
}

test_ctrl_c() {
	echo hi > "$FILES/a.txt"
	start_foreground -t 0 "$FILES/a.txt"
	local server
	server="$(local_url 10443)"
	assert_eq "$(http_get "$server/a.txt")" 200 "GET /a.txt"
	kill -INT "$FG_PID"
	wait "$FG_PID"
	status=$?
	out="$(cat "$SANDBOX/fg.out")" err="$(cat "$SANDBOX/fg.err")"
	assert_status 0
	assert_contains "$err" "GET /a.txt 200"
	assert_eq "$(http_get "$server/")" 000 "the local server after Ctrl-C"
	assert_clean
}

test_term() {
	echo hi > "$FILES/a.txt"
	start_foreground -q -t 0 "$FILES/a.txt"
	kill -TERM "$FG_PID"
	wait "$FG_PID"
	status=$?
	out="$(cat "$SANDBOX/fg.out")" err="$(cat "$SANDBOX/fg.err")"
	assert_status 0
	assert_eq "$err" "" "stderr with --quiet"
	assert_clean
}

run_tests
