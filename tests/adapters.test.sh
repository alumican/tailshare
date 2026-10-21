#!/bin/bash
# Adapters: --adapters, --which and --as, and adapters of your own. Nothing is served.
# What the built-in adapters serve is tested in Go (internal/adapter), and over HTTP in serve.test.sh.
source "$(dirname "$0")/lib.sh"

which_adapter() {
	run "$TAILSHARE" --which "$1"
	assert_status 0
	cut -f 1 <<< "$out"
}

# MARK: - Choosing

test_bundled_adapters_in_order() {
	run "$TAILSHARE" --adapters --json
	assert_status 0
	assert_eq "$(python3 -c 'import json, sys; print(" ".join(json.loads(line)["id"] for line in sys.stdin))' <<< "$out")" \
		"10-ipa 50-html 80-dir 99-file" "adapters"
}

test_which_by_name_and_kind() {
	mkdir "$FILES/dist" "$FILES/site.html"
	touch "$FILES/App.IPA" "$FILES/page.htm" "$FILES/notes.pdf" "$FILES/no-extension"
	assert_eq "$(which_adapter "$FILES/App.IPA")" ipa "App.IPA (case-insensitive)"
	assert_eq "$(which_adapter "$FILES/page.htm")" html "page.htm"
	assert_eq "$(which_adapter "$FILES/notes.pdf")" file "notes.pdf"
	assert_eq "$(which_adapter "$FILES/no-extension")" file "no-extension"
	assert_eq "$(which_adapter "$FILES/dist")" dir "a directory"
	assert_eq "$(which_adapter "$FILES/site.html")" dir "a directory named like a page"
}

test_which_json() {
	touch "$FILES/report.html"
	run "$TAILSHARE" --which --json "$FILES/report.html"
	assert_status 0
	assert_eq "$(json_get "$out" adapter)" html "adapter"
	assert_eq "$(json_get "$out" id)" 50-html "id"
	assert_eq "$(json_get "$out" path)" "$FILES/report.html" "path"
	assert_eq "$(json_get "$out" dir)" null "dir of a built-in adapter"
	run "$TAILSHARE" --which "$FILES/report.html"
	assert_eq "$out" "html	(built in)" "plain"
}

test_as_overrides_the_match() {
	touch "$FILES/report.html"
	run "$TAILSHARE" --as file --which "$FILES/report.html"
	assert_eq "$(cut -f 1 <<< "$out")" file "short name"
	run "$TAILSHARE" --as 99-file --which "$FILES/report.html"
	assert_eq "$(cut -f 1 <<< "$out")" file "whole name"
	run "$TAILSHARE" --as nothing --which "$FILES/report.html"
	assert_status 1
	assert_contains "$err" "no usable adapter named nothing"
}

test_own_adapters_win_and_join_the_order() {
	local own="$XDG_CONFIG_HOME/tailshare/adapters"
	mkdir -p "$own/99-file" "$own/30-apk"
	printf '# Mine\n*\n' > "$own/99-file/match"
	printf '#!/bin/bash\necho /\n' > "$own/99-file/prepare"
	printf '# Install page for an Android app\n*.apk\n' > "$own/30-apk/match"
	printf '#!/bin/bash\necho /\n' > "$own/30-apk/prepare"
	chmod +x "$own/99-file/prepare" "$own/30-apk/prepare"
	touch "$FILES/app.apk" "$FILES/notes.txt"

	run "$TAILSHARE" --which "$FILES/notes.txt"
	assert_eq "$out" "file	$own/99-file" "the own file adapter"
	run "$TAILSHARE" --which "$FILES/app.apk"
	assert_eq "$out" "apk	$own/30-apk" "the own apk adapter"
	run "$TAILSHARE" --adapters --json
	assert_eq "$(python3 -c 'import json, sys; print(" ".join(json.loads(line)["id"] for line in sys.stdin))' <<< "$out")" \
		"10-ipa 30-apk 50-html 80-dir 99-file" "adapters"
}

test_adapter_path_from_the_environment() {
	local adapter
	for adapter in "$SANDBOX/first/50-any" "$SANDBOX/second/50-any" "$SANDBOX/second/60-more"; do
		mkdir -p "$adapter"
		printf '# Anything\n*\n' > "$adapter/match"
		printf '#!/bin/bash\necho /\n' > "$adapter/prepare"
		chmod +x "$adapter/prepare"
	done
	touch "$FILES/App.ipa" "$FILES/notes.txt"
	export TAILSHARE_ADAPTER_PATH="$SANDBOX/first:$SANDBOX/missing:$SANDBOX/second"
	run "$TAILSHARE" --which "$FILES/notes.txt"
	assert_eq "$out" "any	$SANDBOX/first/50-any" "the first on the path wins"
	run "$TAILSHARE" --as more --which "$FILES/notes.txt"
	assert_eq "$out" "more	$SANDBOX/second/60-more" "an adapter further along the path"
	# The built-in ones are still there, in their place in the order
	assert_eq "$(which_adapter "$FILES/App.ipa")" ipa "App.ipa"
}

test_adapter_without_executable_prepare_is_skipped() {
	local own="$XDG_CONFIG_HOME/tailshare/adapters"
	mkdir -p "$own/20-text"
	printf '# Text\n*.txt\n' > "$own/20-text/match"
	printf '#!/bin/bash\necho /\n' > "$own/20-text/prepare"
	touch "$FILES/notes.txt"

	run "$TAILSHARE" --which "$FILES/notes.txt"
	assert_status 0
	assert_eq "$(cut -f 1 <<< "$out")" file "adapter"
	assert_contains "$err" "warning: skipping adapter 20-text"
	run "$TAILSHARE" --adapters --json
	assert_contains "$out" '"id": "20-text"'
	assert_contains "$(grep 20-text <<< "$out")" '"usable": false'
}

run_tests
