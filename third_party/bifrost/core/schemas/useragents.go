// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import "strings"

var CodexDesktop = UserAgentIdentifiers{"codex-desktop", "codex desktop/", "codex/"}
var RooCode = UserAgentIdentifiers{"roo"}
var ClaudeCLI = UserAgentIdentifiers{"claude-cli", "claude-code", "claude-vscode"}
var CodexCLI = UserAgentIdentifiers{"codex-cli", "codex-tui"}
var ClaudeChatWeb = UserAgentIdentifiers{"claude-chat-web", "claude-web"}
var APIClient = UserAgentIdentifiers{"fasthttp"}
var Cline = UserAgentIdentifiers{"cline"}

type UserAgentAppMatcher struct {
	App         string
	Identifiers UserAgentIdentifiers
}

var AntigravityDesktop = UserAgentIdentifiers{"antigravity-desktop", "antigravity/"}
var OpenCode = UserAgentIdentifiers{"opencode"}
var Warp = UserAgentIdentifiers{"bifrost-warp"}
var GeminiCLI = UserAgentIdentifiers{"gemini-cli", "geminicli", "gemini"}
var ClaudeCowork = UserAgentIdentifiers{"claude-cowork"}
var KiloCode = UserAgentIdentifiers{"kilo"}

type UserAgentIdentifiers []string

var Cursor = UserAgentIdentifiers{"cursor"}
var Windsurf = UserAgentIdentifiers{"windsurf"}
var ClaudeDesktop = UserAgentIdentifiers{"claude-desktop", "claude/"}
var AntigravityCLI = UserAgentIdentifiers{"antigravity-cli"}
var QwenCodeCLI = UserAgentIdentifiers{"qwen-code", "qwencode", "qwen"}
var UserAgentAppMatchers = []UserAgentAppMatcher{
	{App: "Claude Chat Web", Identifiers: ClaudeChatWeb},
	{App: "Claude Desktop", Identifiers: ClaudeDesktop},
	{App: "Claude Cowork", Identifiers: ClaudeCowork},
	{App: "Claude Code", Identifiers: ClaudeCLI},

	{App: "Warp", Identifiers: Warp},
	{App: "API", Identifiers: APIClient},
	{App: "Codex CLI", Identifiers: CodexCLI},
	{App: "Codex Desktop", Identifiers: CodexDesktop},
	{App: "Cursor", Identifiers: Cursor},
	{App: "Kilo Code", Identifiers: KiloCode},
	{App: "Roo Code", Identifiers: RooCode},
	{App: "Cline", Identifiers: Cline},
	{App: "OpenCode", Identifiers: OpenCode},
	{App: "Windsurf", Identifiers: Windsurf},
	{App: "Antigravity CLI", Identifiers: AntigravityCLI},
	{App: "Antigravity Desktop", Identifiers: AntigravityDesktop},
	{App: "Gemini CLI", Identifiers: GeminiCLI},
	{App: "Qwen Code", Identifiers: QwenCodeCLI},
}

const (
	UserAgentAppOther = "Other"
)

func (ids UserAgentIdentifiers) Matches(userAgent string) bool {
	if len(ids) == 0 || userAgent == "" {
		return false
	}
	ua := strings.ToLower(userAgent)
	for _, id := range ids {
		if id == "" {
			continue
		}
		normalizedID := strings.ToLower(id)
		if strings.HasPrefix(ua, normalizedID) || strings.Contains(ua, normalizedID) {
			return true
		}
	}
	return false
}

func (ids UserAgentIdentifiers) String() string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func DetectAppFromUserAgent(userAgent string) string {
	if strings.TrimSpace(userAgent) == "" {
		return ""
	}
	for _, matcher := range UserAgentAppMatchers {
		if matcher.Identifiers.Matches(userAgent) {
			return matcher.App
		}
	}
	return UserAgentAppOther
}
