#!/bin/bash
# The command line: help, version, and wrong arguments. Nothing is served.
source "$(dirname "$0")/lib.sh"

test_no_arguments_prints_usage() {
	run "$TAILSHARE"
	assert_status 2
	assert_eq "$out" "" "stdout"
	assert_contains "$err" "Usage:"
}

test_help_goes_to_stdout() {
	run "$TAILSHARE" --help
	assert_status 0
	assert_contains "$out" "USAGE"
	assert_contains "$out" "EXIT STATUS"
	assert_eq "$err" "" "stderr"
}

test_version() {
	run "$TAILSHARE" -V
	assert_status 0
	[[ "$out" =~ ^tailshare\ [0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "version: $out"
}

test_wrong_arguments_exit_2() {
	echo hi > "$FILES/a.txt"
	local args
	while IFS= read -r args; do
		# Word splitting is wanted here: each line is one command line
		run "$TAILSHARE" $args
		[[ "$status" == 2 ]] || fail "tailshare $args: expected exit status 2, got $status ($err)"
		assert_eq "$out" "" "stdout of tailshare $args"
		assert_contains "$err" "Try 'tailshare --help'."
	done << EOF
--bogus $FILES/a.txt
$FILES/a.txt $FILES/a.txt
-p
-p 0 $FILES/a.txt
-p 65536 $FILES/a.txt
-p http $FILES/a.txt
-t 5x $FILES/a.txt
-t -1 $FILES/a.txt
--stop
--stop everything
-dx $FILES/a.txt
EOF
}

test_missing_path() {
	run "$TAILSHARE" -p 10443
	assert_status 2
	assert_contains "$err" "no path given"
}

test_no_such_file_exits_1() {
	run "$TAILSHARE" -t 3s "$FILES/missing.txt"
	assert_status 1
	assert_contains "$err" "no such file or directory"
	assert_eq "$(cat "$FAKE_TAILSCALE_DIR/calls" 2> /dev/null)" "" "calls to tailscale"
}

test_options_with_equals_and_after_double_dash() {
	echo hi > "$FILES/-odd.txt"
	run "$TAILSHARE" --as=file --which -- "$FILES/-odd.txt"
	assert_status 0
	assert_eq "$(cut -f 1 <<< "$out")" "file" "adapter"
}

test_skill_for_each_agent() {
	local agent
	for agent in claude codex; do
		run "$TAILSHARE" --skill "$agent"
		assert_status 0
		assert_eq "$out" "$(cat "$ROOT/skills/$agent/tailshare/SKILL.md")" "the skill for $agent, as in the repository"
		assert_eq "$(sed -n 2p <<< "$out")" "name: tailshare" "name in the skill for $agent"
	done
	run "$TAILSHARE" --skill
	assert_status 2
	run "$TAILSHARE" --skill gemini
	assert_status 2
	assert_contains "$err" "--skill takes claude or codex (got 'gemini')"
}

# The skills differ only in their front matter: what follows it is one text
test_skills_say_the_same() {
	local claude codex
	claude="$(sed '1,/^---$/d' "$ROOT/skills/claude/tailshare/SKILL.md")"
	codex="$(sed '1,/^---$/d' "$ROOT/skills/codex/tailshare/SKILL.md")"
	[[ -n "$claude" ]] || fail "no body in the skill for claude"
	assert_eq "$claude" "$codex" "the skills' bodies"
	grep -qx 'allowed-tools: Bash(tailshare \*)' "$ROOT/skills/claude/tailshare/SKILL.md" \
		|| fail "the skill for claude does not allow tailshare without asking"
}

# Every option in --help is in the README; those an agent needs to share something are in the skill
test_options_are_documented() {
	local option
	run "$TAILSHARE" --help
	for option in $(grep -oE -- '--[a-z]+' <<< "$out" | sort -u); do
		grep -qF -- "$option" "$ROOT/README.md" || fail "$option is in --help, not in README.md"
	done
	for option in -d -q -j -t -p --once --secret --allow --qr --list --stop --which; do
		grep -qF -- "$option" "$ROOT/skills/codex/tailshare/SKILL.md" || fail "$option is not in the skill"
	done
}

run_tests
