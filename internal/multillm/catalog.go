package multillm

import "os/exec"

// CLIEntry describes a supported integration without granting execution
// permission. Detection and execution are deliberately separate operations.
type CLIEntry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Section     string   `json:"section"`
	Visibility  string   `json:"visibility"`
	Mode        string   `json:"mode"`
	Executables []string `json:"executables,omitempty"`
}

type CLIStatus struct {
	CLIEntry
	Installed  bool   `json:"installed"`
	Executable string `json:"executable,omitempty"`
}

// DetectCLIs reports executable names only. It never executes a discovered
// program and does not expose its absolute filesystem path.
func DetectCLIs() []CLIStatus {
	items := BuiltInCLICatalog()
	result := make([]CLIStatus, 0, len(items))
	for _, item := range items {
		status := CLIStatus{CLIEntry: item}
		for _, candidate := range item.Executables {
			if _, err := exec.LookPath(candidate); err == nil {
				status.Installed = true
				status.Executable = candidate
				break
			}
		}
		result = append(result, status)
	}
	return result
}

// BuiltInCLICatalog returns a fresh copy of the curated DZ23 catalog.
func BuiltInCLICatalog() []CLIEntry {
	items := []CLIEntry{
		// Code: native and visible.
		{"aider", "Aider", "code", "native", "executor", []string{"aider"}},
		{"claude-code", "Claude Code", "code", "native", "executor", []string{"claude"}},
		{"cline", "Cline", "code", "native", "client", nil},
		{"codewhale", "CodeWhale", "code", "native", "client", nil},
		{"continue", "Continue", "code", "native", "client", []string{"cn"}},
		{"crush", "Crush", "code", "native", "executor", []string{"crush"}},
		{"cursor-agent", "Cursor Agent CLI", "code", "native", "executor", []string{"cursor-agent"}},
		{"custom-cli", "Custom CLI", "code", "native", "custom", nil},
		{"deepseek-tui", "DeepSeek TUI", "code", "native", "client", nil},
		{"factory-droid", "Factory Droid", "code", "native", "executor", []string{"droid"}},
		{"forgecode", "ForgeCode", "code", "native", "executor", []string{"forge"}},
		{"github-copilot", "GitHub Copilot", "code", "native", "executor", []string{"copilot", "gh"}},
		{"grok-build", "Grok Build", "code", "native", "client", nil},
		{"jcode", "jcode", "code", "native", "executor", []string{"jcode"}},
		{"kilo-code", "Kilo Code", "code", "native", "client", nil},
		{"openai-codex", "OpenAI Codex CLI", "code", "native", "executor", []string{"codex"}},
		{"opencode", "OpenCode", "code", "native", "executor", []string{"opencode"}},
		{"pi", "Pi", "code", "native", "executor", []string{"pi"}},
		{"qwen-code", "Qwen Code", "code", "native", "executor", []string{"qwen"}},
		{"roo-code", "Roo Code", "code", "native", "client", nil},
		{"smelt", "Smelt", "code", "native", "executor", []string{"smelt"}},

		// Code: compatible integrations not shown by default.
		{"antigravity", "Antigravity", "code", "integrable", "client", nil},
		{"cursor", "Cursor", "code", "integrable", "client", []string{"cursor"}},
		{"hermes", "Hermes", "code", "integrable", "executor", []string{"hermes"}},
		{"kiro-ai", "Kiro AI", "code", "integrable", "client", []string{"kiro-cli"}},
		{"zcode", "ZCode", "code", "integrable", "executor", []string{"zcode"}},

		// Agent.
		{"5dive", "5dive", "agent", "native", "orchestrator", []string{"5dive"}},
		{"agent-deck", "Agent Deck", "agent", "native", "orchestrator", []string{"agent-deck"}},
		{"goose", "Goose", "agent", "native", "orchestrator", []string{"goose"}},
		{"hermes-agent", "Hermes Agent", "agent", "native", "orchestrator", []string{"hermes"}},
		{"letta", "Letta CLI", "agent", "native", "orchestrator", []string{"letta"}},
		{"oh-my-pi", "Oh My Pi", "agent", "native", "orchestrator", []string{"omp"}},
		{"open-claw", "Open Claw", "agent", "native", "orchestrator", []string{"openclaw"}},
		{"open-interpreter", "Open Interpreter", "agent", "native", "orchestrator", []string{"interpreter"}},
		{"prime-agent", "Prime Agent", "agent", "native", "orchestrator", []string{"prime"}},
		{"warp-ai", "Warp AI", "agent", "native", "client", nil},

		// External OpenAI-compatible clients.
		{"amazon-q", "Amazon Q", "external", "compatible", "client", []string{"q"}},
		{"sourcegraph-amp", "Sourcegraph Amp", "external", "compatible", "client", []string{"amp"}},
		{"openhands", "OpenHands", "external", "compatible", "orchestrator", []string{"openhands"}},
		{"plandex", "Plandex", "external", "compatible", "client", []string{"plandex"}},
		{"windsurf", "Windsurf / Codeium", "external", "compatible", "client", []string{"windsurf"}},
		{"aichat", "aichat", "external", "compatible", "client", []string{"aichat"}},
		{"shell-gpt", "shell-gpt (sgpt)", "external", "compatible", "client", []string{"sgpt"}},
		{"mods", "mods", "external", "compatible", "client", []string{"mods"}},
		{"llm", "llm", "external", "compatible", "client", []string{"llm"}},
		{"fabric", "fabric", "external", "compatible", "client", []string{"fabric"}},
	}
	return append([]CLIEntry(nil), items...)
}
