package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ranaroussi/minigun/cli/internal/client"
)

func RegisterResources(s *mcpsdk.Server, c *client.Client) {
	handler := func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		return handleResource(ctx, c, req)
	}

	s.AddResource(&mcpsdk.Resource{
		URI:         "minigun://companies",
		Name:        "companies",
		Title:       "Companies",
		Description: "All companies with their list_count.",
		MIMEType:    "application/json",
	}, handler)

	s.AddResource(&mcpsdk.Resource{
		URI:         "minigun://lists",
		Name:        "lists",
		Title:       "Mailing lists",
		Description: "All mailing lists with subscribed_count, ordered by weight ASC then name ASC.",
		MIMEType:    "application/json",
	}, handler)

	s.AddResource(&mcpsdk.Resource{
		URI:         "minigun://sends",
		Name:        "sends",
		Title:       "Sends",
		Description: "All sends ordered by created_at DESC. Paginated; pass ?cursor= and ?limit= in the URI to advance.",
		MIMEType:    "application/json",
	}, handler)

	s.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "minigun://companies/{company}",
		Name:        "company",
		Title:       "Company detail",
		Description: "A single company by id or slug.",
		MIMEType:    "application/json",
	}, handler)

	s.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "minigun://companies/{company}/lists",
		Name:        "company_lists",
		Title:       "Lists for a company",
		Description: "All mailing lists in a company, ordered by weight then name (same order as the /manage page).",
		MIMEType:    "application/json",
	}, handler)

	s.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "minigun://lists/{slug}",
		Name:        "list",
		Title:       "Mailing list detail",
		Description: "A single mailing list: id, slug, name, description, weight, company_id, subscribed_count, total_count, last_send_at.",
		MIMEType:    "application/json",
	}, handler)

	s.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "minigun://lists/{slug}/contacts",
		Name:        "list_contacts",
		Title:       "Contacts in list",
		Description: "Paginated contacts in a mailing list. Append ?cursor= and ?limit= to the URI to advance.",
		MIMEType:    "application/json",
	}, handler)

	s.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "minigun://sends/{id}",
		Name:        "send",
		Title:       "Send detail",
		Description: "Status + progress for a single send.",
		MIMEType:    "application/json",
	}, handler)

	s.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "minigun://sends/{id}/stats",
		Name:        "send_stats",
		Title:       "Send stats",
		Description: "Aggregate Mailgun + MiniGun stats for a send.",
		MIMEType:    "application/json",
	}, handler)
}

func handleResource(ctx context.Context, c *client.Client, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	uri := req.Params.URI
	p, err := parseURI(uri)
	if err != nil {
		return nil, mcpsdk.ResourceNotFoundError(uri)
	}

	var path string
	switch p.kind {
	case kindListIndex:
		path = "/lists"
	case kindListItem:
		path = "/lists/" + p.slug
	case kindListContacts:
		path = "/lists/" + p.slug + "/contacts" + buildQuery(p.cursor, p.limit)
	case kindSendIndex:
		path = "/sends" + buildQuery(p.cursor, p.limit)
	case kindSendItem:
		path = "/send/" + p.sendID
	case kindSendStats:
		path = "/send/" + p.sendID + "/stats"
	case kindCompanyIndex:
		path = "/companies"
	case kindCompanyItem:
		path = "/companies/" + p.company
	case kindCompanyLists:
		path = "/companies/" + p.company + "/lists"
	default:
		return nil, mcpsdk.ResourceNotFoundError(uri)
	}

	resp, err := c.Get(path)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", path, err)
	}
	if resp.Status == 404 {
		return nil, mcpsdk.ResourceNotFoundError(uri)
	}
	if !resp.OK() {
		return nil, fmt.Errorf("server returned %d for %s: %s", resp.Status, path, string(resp.Body))
	}

	return &mcpsdk.ReadResourceResult{
		Contents: []*mcpsdk.ResourceContents{{
			URI:      uri,
			MIMEType: "application/json",
			Text:     string(resp.Body),
		}},
	}, nil
}
