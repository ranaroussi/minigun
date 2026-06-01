package frontmatter

import "testing"

func TestParseExtractsAndStrips(t *testing.T) {
	md := "---\n" +
		"subject: \"Tuesday digest — what's new\"\n" +
		"preheader: A quick look at this week's releases\n" +
		"from: Ran <ran@example.com>\n" +
		"reply_to: support@example.com\n" +
		"---\n" +
		"\n" +
		"Hi {{first_name | \"there\"}}, body here.\n"

	body, meta := Parse(md)

	if meta.Subject != "Tuesday digest — what's new" {
		t.Errorf("subject = %q", meta.Subject)
	}
	if meta.Preheader != "A quick look at this week's releases" {
		t.Errorf("preheader = %q", meta.Preheader)
	}
	if meta.From != "Ran <ran@example.com>" {
		t.Errorf("from = %q", meta.From)
	}
	if meta.ReplyTo != "support@example.com" {
		t.Errorf("reply_to = %q", meta.ReplyTo)
	}
	if want := "Hi {{first_name | \"there\"}}, body here.\n"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

func TestParseNoFrontmatterReturnsUnchanged(t *testing.T) {
	md := "Hello there.\n\nNo frontmatter here.\n"
	body, meta := Parse(md)
	if body != md {
		t.Errorf("body mutated: %q", body)
	}
	if meta != (Meta{}) {
		t.Errorf("meta should be zero, got %+v", meta)
	}
}

// A horizontal rule mid-document is not frontmatter.
func TestParseHorizontalRuleNotFrontmatter(t *testing.T) {
	md := "Intro paragraph.\n\n---\n\nAfter the rule.\n"
	body, meta := Parse(md)
	if body != md {
		t.Errorf("body should be unchanged, got %q", body)
	}
	if meta != (Meta{}) {
		t.Errorf("meta should be zero, got %+v", meta)
	}
}

func TestParseUnterminatedIsNotFrontmatter(t *testing.T) {
	md := "---\nsubject: Oops\nbody with no closing fence\n"
	body, meta := Parse(md)
	if body != md {
		t.Errorf("unterminated block should be left as body, got %q", body)
	}
	if meta != (Meta{}) {
		t.Errorf("meta should be zero, got %+v", meta)
	}
}

func TestParseIgnoresUnknownKeysAndCRLF(t *testing.T) {
	md := "---\r\nsubject: Hi\r\nauthor: someone\r\ndate: 2026-06-01\r\n---\r\nBody.\r\n"
	body, meta := Parse(md)
	if meta.Subject != "Hi" {
		t.Errorf("subject = %q", meta.Subject)
	}
	if meta.From != "" || meta.Preheader != "" || meta.ReplyTo != "" {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if body != "Body.\r\n" {
		t.Errorf("body = %q", body)
	}
}

func TestParseAcceptsReplyToHyphen(t *testing.T) {
	md := "---\nreply-to: r@x.com\n---\nBody.\n"
	_, meta := Parse(md)
	if meta.ReplyTo != "r@x.com" {
		t.Errorf("reply-to = %q", meta.ReplyTo)
	}
}

// Authors who type more than three dashes still get a valid fence.
func TestParseAcceptsLongerFence(t *testing.T) {
	md := "-----\nsubject: Hi\nfrom: Ran <r@x.com>\n-----\nBody here.\n"
	body, meta := Parse(md)
	if meta.Subject != "Hi" || meta.From != "Ran <r@x.com>" {
		t.Errorf("meta = %+v", meta)
	}
	if body != "Body here.\n" {
		t.Errorf("body = %q", body)
	}
}
