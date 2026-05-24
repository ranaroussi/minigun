package render

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
)

func MarkdownToHTML(src string) (string, error) {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func HTMLToText(s string) string {
	s = stripScriptStyle(s)
	s = blockTagRE.ReplaceAllString(s, "\n")
	s = tagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = manyNewlinesRE.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

var (
	scriptRE       = regexp.MustCompile(`(?is)<script.*?</script>`)
	styleRE        = regexp.MustCompile(`(?is)<style.*?</style>`)
	blockTagRE     = regexp.MustCompile(`(?i)</(p|div|h\d|li|tr|br|hr|blockquote)\s*>`)
	tagRE          = regexp.MustCompile(`<[^>]+>`)
	manyNewlinesRE = regexp.MustCompile(`\n{3,}`)
)

func stripScriptStyle(s string) string {
	s = scriptRE.ReplaceAllString(s, "")
	s = styleRE.ReplaceAllString(s, "")
	return s
}

var varRE = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:\|\s*"((?:[^"\\]|\\.)*)")?\s*\}\}`)

type Variable struct {
	Name    string
	Default string
}

func RewriteVariables(body string) (rewritten string, vars []Variable) {
	seen := map[string]string{}
	rewritten = varRE.ReplaceAllStringFunc(body, func(match string) string {
		m := varRE.FindStringSubmatch(match)
		name := m[1]
		def := m[2]
		if existing, ok := seen[name]; !ok || (existing == "" && def != "") {
			seen[name] = def
		}
		return "%recipient." + name + "%"
	})
	for name, def := range seen {
		vars = append(vars, Variable{Name: name, Default: def})
	}
	return rewritten, vars
}

func ApplyWrapper(wrapper, content, subject, preheader string) string {
	out := wrapper
	out = strings.ReplaceAll(out, "{{content}}", content)
	out = strings.ReplaceAll(out, "{{subject}}", html.EscapeString(subject))
	out = strings.ReplaceAll(out, "{{preheader}}", html.EscapeString(preheader))
	return out
}

const DefaultHTMLWrapper = `<!doctype html>
<html>
<head>
<meta charset="utf-8" />
<title>{{subject}}</title>
</head>
<body style="font-family: -apple-system, system-ui, Segoe UI, Roboto, sans-serif; line-height:1.5; color:#222; max-width:640px; margin:24px auto; padding:0 16px;">
<span style="display:none !important;color:#fff;height:0;width:0;overflow:hidden;">{{preheader}}</span>
{{content}}
</body>
</html>`

var unsubPlaceholderRe = regexp.MustCompile(`\{\{\s*(unsubscribe|unsub_url)\s*(?:\|\s*"[^"]*")?\s*\}\}`)

const UnsubFooterHTML = "\n\n<p>&nbsp;<br><a href=\"{{unsubscribe}}\">Unsubscribe</a></p>\n"
const UnsubFooterText = "\n\nUnsubscribe:\n{{unsubscribe}}\n"

func HasUnsubPlaceholder(s string) bool {
	return unsubPlaceholderRe.MatchString(s)
}

func EnsureUnsubFooterHTML(html string) string {
	if HasUnsubPlaceholder(html) {
		return html
	}
	return html + UnsubFooterHTML
}

func EnsureUnsubFooterText(text string) string {
	if HasUnsubPlaceholder(text) {
		return text
	}
	return text + UnsubFooterText
}

func BuildBody(markdownSrc, wrapperHTML, subject, preheader string) (htmlOut, textOut string, vars []Variable, err error) {
	operatorHasUnsub := HasUnsubPlaceholder(markdownSrc)

	rendered, err := MarkdownToHTML(markdownSrc)
	if err != nil {
		return "", "", nil, fmt.Errorf("markdown: %w", err)
	}
	if !operatorHasUnsub {
		rendered += UnsubFooterHTML
	}
	if wrapperHTML == "" {
		wrapperHTML = DefaultHTMLWrapper
	}
	wrapped := ApplyWrapper(wrapperHTML, rendered, subject, preheader)
	rewrittenHTML, htmlVars := RewriteVariables(wrapped)
	rewrittenMD, mdVars := RewriteVariables(markdownSrc)
	textOut = HTMLToText(mustHTMLRenderFromMD(rewrittenMD))
	if !operatorHasUnsub {
		footerRewritten, _ := RewriteVariables(UnsubFooterText)
		textOut += footerRewritten
	}
	vars = mergeVars(htmlVars, mdVars)
	return rewrittenHTML, textOut, vars, nil
}

func mustHTMLRenderFromMD(src string) string {
	out, err := MarkdownToHTML(src)
	if err != nil {
		return src
	}
	return out
}

func mergeVars(a, b []Variable) []Variable {
	seen := map[string]string{}
	for _, v := range a {
		if existing, ok := seen[v.Name]; !ok || (existing == "" && v.Default != "") {
			seen[v.Name] = v.Default
		}
	}
	for _, v := range b {
		if existing, ok := seen[v.Name]; !ok || (existing == "" && v.Default != "") {
			seen[v.Name] = v.Default
		}
	}
	out := make([]Variable, 0, len(seen))
	for name, def := range seen {
		out = append(out, Variable{Name: name, Default: def})
	}
	return out
}
