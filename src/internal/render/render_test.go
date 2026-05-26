package render

import (
	"strings"
	"testing"
)

func TestRewriteVariables(t *testing.T) {
	in := `Hi {{first_name | "there"}}, your code is {{code}}.`
	out, vars := RewriteVariables(in)
	if !strings.Contains(out, "%recipient.first_name%") {
		t.Errorf("expected first_name rewritten, got %q", out)
	}
	if !strings.Contains(out, "%recipient.code%") {
		t.Errorf("expected code rewritten, got %q", out)
	}
	defs := map[string]string{}
	for _, v := range vars {
		defs[v.Name] = v.Default
	}
	if defs["first_name"] != "there" {
		t.Errorf("expected first_name default 'there', got %q", defs["first_name"])
	}
	if _, ok := defs["code"]; !ok {
		t.Errorf("expected code to be tracked")
	}
}

func TestMarkdownToHTML(t *testing.T) {
	out, err := MarkdownToHTML("# Hello\n\nThis is **bold**.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<h1>") || !strings.Contains(out, "<strong>") {
		t.Errorf("unexpected: %q", out)
	}
}

func TestHTMLToText(t *testing.T) {
	got := HTMLToText("<p>Hello <b>world</b></p><p>Second</p>")
	if !strings.Contains(got, "Hello world") || !strings.Contains(got, "Second") {
		t.Errorf("got %q", got)
	}
}

// Locks in the order of rewriteVariables vs markdownToHTML: a placeholder
// with a quoted default has to be rewritten before the markdown engine
// HTML-escapes the inner double-quotes, otherwise the post-render regex
// can't match `&quot;` and the placeholder ships verbatim.
func TestBuildBodyQuotedDefaultThroughMarkdown(t *testing.T) {
	md := "# Hi\n\nHow are you {{first_name | \"man\"}}?"
	html, text, _, err := BuildBody(md, "", "Weekly", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "%recipient.first_name%") {
		t.Errorf("HTML body did not have first_name rewritten:\n%s", html)
	}
	if strings.Contains(html, "{{first_name") || strings.Contains(html, "&quot;man&quot;") {
		t.Errorf("HTML body still contains an unrendered placeholder:\n%s", html)
	}
	if !strings.Contains(text, "%recipient.first_name%") {
		t.Errorf("text body did not have first_name rewritten:\n%s", text)
	}
}

func TestApplyWrapperAcceptsBothPlaceholderForms(t *testing.T) {
	cases := []struct {
		name    string
		wrapper string
	}{
		{"mustache", `<html><head><title>{{subject}}</title></head><body><span>{{preheader}}</span>{{content}}</body></html>`},
		{"html-comment", `<html><head><title><!--subject--></title></head><body><span><!--preheader--></span><!--content--></body></html>`},
		{"html-comment-spaced", `<html><head><title><!-- subject --></title></head><body><span><!-- preheader --></span><!-- content --></body></html>`},
		{"mustache-spaced", `<html><head><title>{{ subject }}</title></head><body><span>{{ preheader }}</span>{{ content }}</body></html>`},
		{"mixed", `<html><head><title>{{subject}}</title></head><body><span><!-- preheader --></span>{{content}}</body></html>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ApplyWrapper(tc.wrapper, "<p>BODY</p>", "SUBJ", "PRE")
			for _, want := range []string{"<p>BODY</p>", "SUBJ", "PRE"} {
				if !strings.Contains(out, want) {
					t.Errorf("ApplyWrapper(%s) missing %q in output:\n%s", tc.name, want, out)
				}
			}
			for _, unwanted := range []string{"{{content}}", "{{subject}}", "{{preheader}}", "<!--content-->", "<!-- content -->", "<!--subject-->", "<!--preheader-->"} {
				if strings.Contains(out, unwanted) {
					t.Errorf("ApplyWrapper(%s) left placeholder %q in output:\n%s", tc.name, unwanted, out)
				}
			}
		})
	}
}

func TestApplyWrapperPreservesDollarSignsInContent(t *testing.T) {
	wrapper := `<html><body>{{content}}</body></html>`
	content := `<p>price: $100, code: $1 vs $2</p>`
	out := ApplyWrapper(wrapper, content, "s", "p")
	if !strings.Contains(out, content) {
		t.Errorf("ApplyWrapper corrupted dollar-sign content. got:\n%s", out)
	}
}
