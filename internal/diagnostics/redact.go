// Package diagnostics owns Cage's local, bounded diagnostic record. Never pass
// terminal traffic, user files, request bodies or credential objects to it.
package diagnostics

import (
	"regexp"
	"strings"
)

var patterns = []struct {
	re          *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`), ""},
	{regexp.MustCompile(`(?im)^(\s*(?:cookie|set-cookie|authorization|proxy-authorization)\s*[:=]).*$`), "${1} [redacted]"},
	{regexp.MustCompile(`(?is)-----BEGIN [^-]*(?:PRIVATE KEY|CERTIFICATE)-----.*?(?:-----END [^-]+-----|$)`), "[redacted key]"},
	{regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>"'` + "`" + `]+`), "[url]"},
	{regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+[a-z0-9._~+/=-]+`), "[redacted authorization]"},
	{regexp.MustCompile(`(?i)(["']?(?:password|passwd|pwd|token|access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|apikey|secret|client[_-]?secret|authorization|proxy-authorization|cookie|set-cookie|auth|credential|subscription[_-]?url)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)`), "${1}[redacted]"},
	{regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9_]{12,}|github_pat_[A-Za-z0-9_]+|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`), "[redacted token]"},
	{regexp.MustCompile(`\b[a-fA-F0-9]{32,}\b`), "[redacted id]"},
	{regexp.MustCompile(`(?i)[a-z]:[\\/][^\r\n"'<>:]+`), "[home path]"},
	{regexp.MustCompile(`(?i)/(?:Users|home|Volumes|mnt|media|private|tmp|var|opt|Applications|usr|etc|run)/[^\r\n"'<>:]+`), "[home path]"},
	{regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`), "[email]"},
	{regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`), "[ip]"},
}

func Redact(s string) string {
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, p.replacement)
	}
	return strings.ToValidUTF8(s, "�")
}
