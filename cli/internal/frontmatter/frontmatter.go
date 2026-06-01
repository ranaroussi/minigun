// Package frontmatter parses an optional YAML-style "---" fenced metadata
// block at the top of a Markdown document. It is intentionally tiny (no YAML
// dependency): it reads only the handful of per-send header keys MiniGun
// cares about and ignores everything else. This is a CLI/MCP authoring
// convenience — the block is always stripped from the body before the send
// is dispatched, so it never renders into the email and the server-facing
// API stays a clean structured-field contract.
package frontmatter

import "strings"

// Meta holds the recognized frontmatter fields. Unrecognized keys are ignored.
type Meta struct {
	Subject   string
	Preheader string
	From      string
	ReplyTo   string
}

// Parse extracts a leading frontmatter block from md. It returns the body with
// the block stripped and the parsed fields. When md does not begin with a
// frontmatter fence (the first non-empty line is "---", closed by a later
// "---"), md is returned unchanged and meta is the zero value.
func Parse(md string) (body string, meta Meta) {
	src := strings.TrimPrefix(md, "\ufeff") // tolerate a UTF-8 BOM
	lines := strings.Split(src, "\n")

	// The first non-empty line must be the opening fence.
	open := 0
	for open < len(lines) && strings.TrimSpace(lines[open]) == "" {
		open++
	}
	if open >= len(lines) || !isFence(lines[open]) {
		return md, Meta{}
	}

	// Find the closing fence.
	closing := -1
	for j := open + 1; j < len(lines); j++ {
		if isFence(lines[j]) {
			closing = j
			break
		}
	}
	if closing < 0 {
		// Unterminated block: treat the whole document as body, not metadata.
		return md, Meta{}
	}

	meta = parseBlock(lines[open+1 : closing])

	bodyLines := lines[closing+1:]
	for len(bodyLines) > 0 && strings.TrimSpace(bodyLines[0]) == "" {
		bodyLines = bodyLines[1:]
	}
	return strings.Join(bodyLines, "\n"), meta
}

func parseBlock(lines []string) Meta {
	var m Meta
	for _, ln := range lines {
		ln = strings.TrimRight(ln, "\r")
		c := strings.IndexByte(ln, ':')
		if c < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(ln[:c]))
		val := unquote(strings.TrimSpace(ln[c+1:]))
		switch key {
		case "subject":
			m.Subject = val
		case "preheader":
			m.Preheader = val
		case "from":
			m.From = val
		case "reply_to", "reply-to":
			m.ReplyTo = val
		}
	}
	return m
}

// isFence reports whether a line is a frontmatter delimiter: three or more
// dashes and nothing else (tolerating surrounding whitespace). Accepting more
// than three is friendlier to authors who type "-----".
func isFence(line string) bool {
	s := strings.TrimSpace(line)
	if len(s) < 3 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '-' {
			return false
		}
	}
	return true
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
