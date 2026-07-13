package permission

import (
	"path/filepath"
	"regexp"
	"strings"
)

type CommandAnalysis struct {
	Program string
	Risk    RiskLevel
	Reasons []string
	Paths   []string
}

type CommandAnalyzer struct{}

func NewCommandAnalyzer() *CommandAnalyzer { return &CommandAnalyzer{} }

var tokenPattern = regexp.MustCompile(`"(?:\\.|[^"])*"|'[^']*'|[^\s]+`)

// Analyze conservatively classifies common POSIX and PowerShell commands.
// It is intentionally not a replacement for process isolation.
func (a *CommandAnalyzer) Analyze(command, workingDirectory string) CommandAnalysis {
	trimmed := strings.TrimSpace(command)
	result := CommandAnalysis{Risk: Safe}
	if trimmed == "" {
		result.Reasons = []string{"empty shell command"}
		return result
	}
	tokens := tokenPattern.FindAllString(trimmed, -1)
	if len(tokens) == 0 {
		result.Reasons = []string{"unable to parse shell command"}
		return result
	}
	programIndex := 0
	if strings.EqualFold(unquote(tokens[0]), "sudo") {
		result.Program = "sudo"
		return critical(result, "privilege escalation with sudo")
	}
	if programIndex >= len(tokens) {
		return result
	}
	program := strings.ToLower(filepath.Base(unquote(tokens[programIndex])))
	result.Program = program
	lower := strings.ToLower(trimmed)
	result.Paths = extractCommandPaths(program, tokens[programIndex+1:], trimmed)

	if containsCommandSubstitution(trimmed) {
		result.Risk = MaxRisk(result.Risk, High)
		result.Reasons = append(result.Reasons, "command substitution")
	}
	if isCurlPipeShell(lower) {
		return critical(result, "remote content piped to a shell")
	}
	if isCriticalProgram(program) {
		return critical(result, "system-destructive command: "+program)
	}
	if (program == "find" || program == "find.exe") && strings.Contains(lower, "-delete") && hasRootArgument(tokens[programIndex+1:]) {
		return critical(result, "recursive deletion from filesystem root")
	}
	if program == "rm" || program == "rm.exe" || program == "remove-item" || program == "del" || program == "erase" || program == "rmdir" {
		result.Risk = High
		result.Reasons = append(result.Reasons, "file deletion")
		if hasRootArgument(tokens[programIndex+1:]) || targetsHome(tokens[programIndex+1:]) {
			return critical(result, "deletion targets root or home directory")
		}
		return result
	}
	if program == "git" && (strings.Contains(lower, " reset ") && strings.Contains(lower, "--hard") || strings.Contains(lower, " clean ")) {
		result.Risk = High
		result.Reasons = append(result.Reasons, "destructive git operation")
		return result
	}
	if hasOverwriteRedirect(trimmed) {
		result.Risk = High
		result.Reasons = append(result.Reasons, "file overwrite redirection")
		return result
	}
	if hasAppendRedirect(trimmed) {
		result.Risk = MaxRisk(result.Risk, Low)
		result.Reasons = append(result.Reasons, "append redirection within workspace")
		return result
	}
	if strings.ContainsAny(trimmed, "|;") || strings.Contains(trimmed, "&&") || strings.Contains(trimmed, "||") {
		result.Risk = High
		result.Reasons = append(result.Reasons, "compound shell command")
		return result
	}
	if isReadOnlyProgram(program, lower) {
		result.Risk = MaxRisk(result.Risk, Safe)
		result.Reasons = append(result.Reasons, "read-only command")
		return result
	}
	if isWorkspaceWriteProgram(program) {
		result.Risk = MaxRisk(result.Risk, Low)
		result.Reasons = append(result.Reasons, "workspace write or build command")
		return result
	}
	result.Risk = High
	result.Reasons = append(result.Reasons, "unrecognized shell command")
	return result
}

func critical(result CommandAnalysis, reason string) CommandAnalysis {
	result.Risk = Critical
	result.Reasons = append(result.Reasons, reason)
	return result
}

func unquote(s string) string { return strings.Trim(s, `"'`) }

func isCriticalProgram(p string) bool {
	switch p {
	case "mkfs", "mkfs.ext4", "dd", "shutdown", "reboot", "halt", "poweroff", "format", "format.com", "format-volume", "clear-disk", "stop-computer", "restart-computer":
		return true
	}
	return false
}

func isReadOnlyProgram(p, command string) bool {
	switch p {
	case "pwd", "ls", "dir", "cat", "type", "grep", "rg", "findstr", "head", "tail", "wc", "stat", "get-content", "get-childitem", "get-item", "test-path":
		return true
	case "git":
		return strings.Contains(command, " status") || strings.Contains(command, " diff") || strings.Contains(command, " log") || strings.Contains(command, " show")
	}
	return false
}

func isWorkspaceWriteProgram(p string) bool {
	switch p {
	case "touch", "mkdir", "new-item", "set-content", "add-content", "go", "npm", "pnpm", "yarn", "cargo", "make", "cmake":
		return true
	}
	return false
}

func hasRootArgument(tokens []string) bool {
	for _, raw := range tokens {
		t := unquote(strings.TrimSpace(raw))
		if strings.HasPrefix(t, "-") || strings.HasPrefix(t, "/q") || strings.HasPrefix(t, "/s") {
			continue
		}
		clean := filepath.Clean(t)
		if clean == string(filepath.Separator) || clean == "/" || regexp.MustCompile(`(?i)^[a-z]:\\?$`).MatchString(clean) {
			return true
		}
	}
	return false
}

func targetsHome(tokens []string) bool {
	for _, raw := range tokens {
		t := strings.ToLower(unquote(raw))
		if t == "~" || t == "$home" || t == "%userprofile%" || strings.HasPrefix(t, "~/") || strings.HasPrefix(t, `~\`) {
			return true
		}
	}
	return false
}

func containsCommandSubstitution(s string) bool {
	return strings.Contains(s, "$(") || strings.Contains(s, "`")
}

func isCurlPipeShell(s string) bool {
	return (strings.Contains(s, "curl ") || strings.Contains(s, "wget ") || strings.Contains(s, "invoke-webrequest")) && strings.Contains(s, "|") &&
		(strings.Contains(s, " bash") || strings.Contains(s, " sh") || strings.Contains(s, "powershell") || strings.Contains(s, " pwsh"))
}

func hasOverwriteRedirect(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '>' {
			if i+1 < len(s) && s[i+1] == '>' {
				i++
				continue
			}
			return true
		}
	}
	return false
}

func hasAppendRedirect(s string) bool { return strings.Contains(s, ">>") }

func extractCommandPaths(program string, args []string, command string) []string {
	pathCommand := false
	switch program {
	case "rm", "rm.exe", "remove-item", "del", "erase", "rmdir", "touch", "mkdir", "new-item", "cat", "type", "get-content", "set-content", "add-content", "find":
		pathCommand = true
	}
	var paths []string
	if pathCommand {
		for _, raw := range args {
			arg := unquote(strings.TrimSpace(raw))
			if arg == "" || strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "/q") || strings.HasPrefix(arg, "/s") {
				continue
			}
			if strings.ContainsAny(arg, "|;&") || arg == ">" || arg == ">>" {
				break
			}
			paths = append(paths, arg)
		}
	}
	redirect := regexp.MustCompile(`>{1,2}\s*("[^"]+"|'[^']+'|[^\s|;&]+)`).FindAllStringSubmatch(command, -1)
	for _, match := range redirect {
		if len(match) > 1 {
			paths = append(paths, unquote(match[1]))
		}
	}
	return paths
}
