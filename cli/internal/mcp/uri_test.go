package mcp

import "testing"

func TestParseURIHappy(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want parsedURI
	}{
		{"lists index", "minigun://lists", parsedURI{kind: kindListIndex}},
		{"list item", "minigun://lists/newsletter", parsedURI{kind: kindListItem, slug: "newsletter"}},
		{"list contacts", "minigun://lists/newsletter/contacts", parsedURI{kind: kindListContacts, slug: "newsletter"}},
		{"list contacts paginated", "minigun://lists/newsletter/contacts?cursor=abc&limit=10", parsedURI{kind: kindListContacts, slug: "newsletter", cursor: "abc", limit: "10"}},
		{"sends index", "minigun://sends", parsedURI{kind: kindSendIndex}},
		{"sends paginated", "minigun://sends?cursor=eyJ0IjoiMjAyNiJ9", parsedURI{kind: kindSendIndex, cursor: "eyJ0IjoiMjAyNiJ9"}},
		{"send item", "minigun://sends/snd_abc", parsedURI{kind: kindSendItem, sendID: "snd_abc"}},
		{"send stats", "minigun://sends/snd_abc/stats", parsedURI{kind: kindSendStats, sendID: "snd_abc"}},
		{"companies index", "minigun://companies", parsedURI{kind: kindCompanyIndex}},
		{"company item", "minigun://companies/acme", parsedURI{kind: kindCompanyItem, company: "acme"}},
		{"company lists", "minigun://companies/acme/lists", parsedURI{kind: kindCompanyLists, company: "acme"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseURI(tt.uri)
			if err != nil {
				t.Fatalf("parseURI: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseURIErrors(t *testing.T) {
	tests := []string{
		"http://lists",
		"minigun://other",
		"minigun://lists/newsletter/foo",
		"minigun://lists/newsletter/contacts/extra",
		"minigun://sends/snd_abc/foo",
		"minigun://sends/snd_abc/stats/extra",
		"minigun://companies/acme/foo",
		"minigun://companies/acme/lists/extra",
	}
	for _, uri := range tests {
		t.Run(uri, func(t *testing.T) {
			if _, err := parseURI(uri); err == nil {
				t.Fatalf("expected error for %q", uri)
			}
		})
	}
}

func TestBuildQuery(t *testing.T) {
	if q := buildQuery("", ""); q != "" {
		t.Fatalf("expected empty, got %q", q)
	}
	if q := buildQuery("abc", ""); q != "?cursor=abc" {
		t.Fatalf("got %q", q)
	}
	if q := buildQuery("", "20"); q != "?limit=20" {
		t.Fatalf("got %q", q)
	}
	if q := buildQuery("abc", "20"); q != "?cursor=abc&limit=20" {
		t.Fatalf("got %q", q)
	}
}
