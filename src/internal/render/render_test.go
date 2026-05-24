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
