// Package skills holds the skills that teach AI agents to use tailshare, one per agent, each in the
// Agent Skills format (<name>/SKILL.md). tailshare --skill prints them.
package skills

import _ "embed"

var (
	//go:embed claude/tailshare/SKILL.md
	claude string
	//go:embed codex/tailshare/SKILL.md
	codex string
)

// For returns the skill for an agent: "claude" (Claude Code) or "codex" (Codex).
func For(agent string) (string, bool) {
	skill, ok := map[string]string{"claude": claude, "codex": codex}[agent]
	return skill, ok
}
