package pinky

import _ "embed"

// Skill is docs/SKILL.md, the guide agents read before they use a phone
// through Pinky. Settings › Agents installs it where an agent reads skills
// (~/.claude/skills/pinky/SKILL.md for Claude Code).
//
//go:embed docs/SKILL.md
var Skill string
