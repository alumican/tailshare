#!/bin/bash
# Adapters: which one is chosen, and what each bundled one puts in the site. Nothing is served.
source "$(dirname "$0")/lib.sh"

# Run a bundled adapter's prepare on a path, into $SANDBOX/site
prepare() {
	local adapter="$1" input="$2"
	rm -rf "$SANDBOX/site"
	mkdir "$SANDBOX/site"
	run env TAILSHARE_ADAPTER_DIR="$ROOT/adapters/$adapter" "$ROOT/adapters/$adapter/prepare" \
		"$input" "$SANDBOX/site" "https://host.example.ts.net:10443"
}

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
	mkdir -p "$SANDBOX/elsewhere/50-any"
	printf '# Anything\n*\n' > "$SANDBOX/elsewhere/50-any/match"
	printf '#!/bin/bash\necho /\n' > "$SANDBOX/elsewhere/50-any/prepare"
	chmod +x "$SANDBOX/elsewhere/50-any/prepare"
	touch "$FILES/App.ipa"
	export TAILSHARE_ADAPTER_PATH="$SANDBOX/elsewhere"
	assert_eq "$(which_adapter "$FILES/App.ipa")" any "the only adapter on the path"
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

# MARK: - What each puts in the site

test_file_prepare() {
	echo hi > "$FILES/my notes.txt"
	prepare 99-file "$FILES/my notes.txt"
	assert_status 0
	assert_eq "$out" "/my notes.txt" "path to open"
	assert_eq "$(tree "$SANDBOX/site")" "my notes.txt" "site"
	assert_eq "$(readlink "$SANDBOX/site/my notes.txt")" "$FILES/my notes.txt" "link"
}

test_dir_prepare_leaves_out_hidden_entries() {
	mkdir -p "$FILES/dist/assets" "$FILES/dist/.git"
	touch "$FILES/dist/index.html" "$FILES/dist/assets/app.js" "$FILES/dist/.env" "$FILES/dist/.git/config"
	prepare 80-dir "$FILES/dist"
	assert_status 0
	assert_eq "$out" "/" "path to open"
	assert_eq "$(tree "$SANDBOX/site")" "assets
index.html" "site"
}

test_dir_prepare_empty_directory() {
	mkdir "$FILES/empty"
	prepare 80-dir "$FILES/empty"
	assert_status 0
	assert_eq "$(tree "$SANDBOX/site")" "" "site"
}

test_html_prepare_follows_references_and_nothing_else() {
	local page="$FILES/site"
	mkdir -p "$page/css" "$page/img" "$page/js"
	cat > "$page/page.html" << 'EOF'
<!doctype html>
<link rel="stylesheet" href="css/style.css">
<style>@import "css/extra.css"; .hero { background: url('img/hero.png') }</style>
<img src="img/a%20b.png" srcset="img/1x.png 1x, img/2x.png 2x">
<video poster="img/poster.png"></video>
<div style="background-image: url(img/inline.png)"></div>
<script src="/js/app.js"></script>
<a href="https://example.com/">elsewhere</a>
<a href="#top">anchor</a>
<img src="../outside.png">
<img src="img/missing.png">
EOF
	printf '@import "base.css";\nbody { background: url(../img/bg.png) }\n' > "$page/css/style.css"
	echo "p { color: red }" > "$page/css/base.css"
	echo "" > "$page/css/extra.css"
	touch "$page/img/hero.png" "$page/img/a b.png" "$page/img/1x.png" "$page/img/2x.png" "$page/img/poster.png" \
		"$page/img/inline.png" "$page/img/bg.png" "$page/js/app.js" "$page/secret.txt" "$FILES/outside.png"

	prepare 50-html "$page/page.html"
	assert_status 0
	assert_eq "$out" "/page.html" "path to open"
	assert_eq "$(tree "$SANDBOX/site")" "css/base.css
css/extra.css
css/style.css
img/1x.png
img/2x.png
img/a b.png
img/bg.png
img/hero.png
img/inline.png
img/poster.png
js/app.js
page.html" "site"
}

test_ipa_prepare_makes_install_page_and_manifest() {
	make_ipa "$FILES/Demo App.ipa"
	prepare 10-ipa "$FILES/Demo App.ipa"
	assert_status 0
	assert_eq "$out" "/" "path to open"
	assert_eq "$(tree "$SANDBOX/site")" "Demo App.ipa
index.html
manifest.plist" "site"

	local manifest page
	manifest="$(cat "$SANDBOX/site/manifest.plist")"
	assert_contains "$manifest" "<string>https://host.example.ts.net:10443/Demo%20App.ipa?v="
	assert_contains "$manifest" "<string>com.example.demo</string>"
	assert_contains "$manifest" "<string>1.2.3</string>"
	assert_contains "$manifest" "<string>Demo &amp; Co</string>"
	python3 -c 'import plistlib, sys; plistlib.load(open(sys.argv[1], "rb"))' "$SANDBOX/site/manifest.plist" \
		|| fail "manifest.plist is not a valid plist"
	page="$(cat "$SANDBOX/site/index.html")"
	assert_contains "$page" "itms-services://?action=download-manifest&amp;url=https://host.example.ts.net:10443/manifest.plist"
	assert_contains "$page" "Demo &amp; Co"
}

test_ipa_prepare_refuses_what_is_not_an_ipa() {
	echo "not a zip" > "$FILES/Broken.ipa"
	prepare 10-ipa "$FILES/Broken.ipa"
	assert_status 1
	assert_contains "$err" "not an iOS app archive"
}

run_tests
