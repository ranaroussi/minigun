package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ranaroussi/minigun/cli/internal/client"
)

func RegisterTools(s *mcpsdk.Server, c *client.Client) {
	addHealth(s, c)
	addCreateCompany(s, c)
	addGetCompany(s, c)
	addListCompanies(s, c)
	addListListsByCompany(s, c)
	addCreateList(s, c)
	addAddContact(s, c)
	addUnsubscribeContact(s, c)
	addSendSingle(s, c)
	addSendBulk(s, c)
	addResumeSend(s, c)
	addGetSendStatus(s, c)
	addGetSendStats(s, c)
}

func boolPtr(b bool) *bool { return &b }

func textResult(body []byte) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
	}
}

func errorResult(format string, args ...any) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf(format, args...)}},
		IsError: true,
	}
}

func passthrough(resp *client.Response, err error) (*mcpsdk.CallToolResult, struct{}, error) {
	if err != nil {
		return errorResult("transport error: %v", err), struct{}{}, nil
	}
	if !resp.OK() {
		return errorResult("server returned %d: %s", resp.Status, string(resp.Body)), struct{}{}, nil
	}
	return textResult(resp.Body), struct{}{}, nil
}

type emptyInput struct{}

func addHealth(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "health",
		Description: "Checks whether the MiniGun server is reachable and its database is healthy. Returns {status, db}.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "Health check",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in emptyInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Get("/healthz"))
	})
}

type createCompanyInput struct {
	Name string `json:"name" jsonschema:"Human-readable company name"`
	Slug string `json:"slug" jsonschema:"URL-safe slug: lowercase alphanumerics or hyphens 1-64 chars"`
}

func addCreateCompany(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "create_company",
		Description: "Creates a new company. Companies group mailing lists that are shown together on the /manage preferences page. Slug must be unique.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			Title:           "Create company",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in createCompanyInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Post("/companies", in))
	})
}

type companyKeyInput struct {
	Company string `json:"company" jsonschema:"Company id or slug"`
}

func addGetCompany(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_company",
		Description: "Returns a single company by id or slug.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "Get company",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in companyKeyInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Get("/companies/" + in.Company))
	})
}

func addListCompanies(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_companies",
		Description: "Lists all companies with their list_count.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "List companies",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in emptyInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Get("/companies"))
	})
}

func addListListsByCompany(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_lists_by_company",
		Description: "Returns all mailing lists that belong to a company, ordered by weight ASC then name ASC (same order as the /manage page).",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "List lists by company",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in companyKeyInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Get("/companies/" + in.Company + "/lists"))
	})
}

type createListInput struct {
	Name        string `json:"name" jsonschema:"Human-readable list name, used in unsubscribe copy"`
	Slug        string `json:"slug" jsonschema:"URL-safe slug: lowercase alphanumerics or hyphens 1-64 chars"`
	Company     string `json:"company" jsonschema:"Company id or slug the list belongs to (required)"`
	Description string `json:"description,omitempty" jsonschema:"Description shown on the /manage preferences page"`
	Weight      int    `json:"weight,omitempty" jsonschema:"Display weight on /manage (default 10, lower = higher up)"`
}

func addCreateList(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "create_list",
		Description: "Creates a new mailing list inside a company. Slug must be unique. The list inherits its /manage company-wide preferences page from its company.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			Title:           "Create list",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in createListInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Post("/lists", in))
	})
}

type addContactInput struct {
	List   string         `json:"list" jsonschema:"List id or slug"`
	Email  string         `json:"email" jsonschema:"Contact email address"`
	Params map[string]any `json:"params,omitempty" jsonschema:"Free-form contact params for template variables (e.g. first_name)"`
}

func addAddContact(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "add_contact",
		Description: "Adds a contact to a list or updates an existing contact's params. Idempotent on (list, email). Resubscribes the contact if they were previously unsubscribed from this list.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			Title:           "Add or update contact",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in addContactInput) (*mcpsdk.CallToolResult, struct{}, error) {
		body := map[string]any{"email": in.Email}
		if in.Params != nil {
			body["params"] = in.Params
		}
		return passthrough(c.Post("/lists/"+in.List+"/contacts", body))
	})
}

type unsubscribeContactInput struct {
	List  string `json:"list" jsonschema:"List id or slug"`
	Email string `json:"email" jsonschema:"Contact email address"`
}

func addUnsubscribeContact(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "unsubscribe_contact",
		Description: "DESTRUCTIVE. Marks a contact as unsubscribed on the given list. Use only for admin-driven unsubscribes; end-user unsubscribes happen via the public /u/<token> page.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			IdempotentHint:  true,
			Title:           "Unsubscribe contact",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in unsubscribeContactInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Post("/lists/"+in.List+"/unsubscribe", map[string]any{"email": in.Email}))
	})
}

type sendSingleInput struct {
	To        string `json:"to" jsonschema:"Recipient email"`
	Subject   string `json:"subject" jsonschema:"Email subject"`
	Preheader string `json:"preheader,omitempty" jsonschema:"Short hidden snippet shown in the inbox preview line next to the subject"`
	From      string `json:"from" jsonschema:"RFC 5322 From header"`
	ReplyTo   string `json:"reply_to,omitempty"`
	Company   string `json:"company" jsonschema:"Company id or slug. Resolves the sending domain."`
	Domain    string `json:"domain,omitempty" jsonschema:"Override sending domain for this send"`
	MD        string `json:"md,omitempty" jsonschema:"Markdown body. One of md or html is required."`
	HTML      string `json:"html,omitempty" jsonschema:"HTML body. Used if md is not provided."`
	Text      string `json:"text,omitempty" jsonschema:"Plain-text body. Auto-generated from md/html if omitted."`
	Template  string `json:"template,omitempty" jsonschema:"HTML wrapper. {{content}} is replaced with the rendered body."`
	TestMode  bool   `json:"test_mode,omitempty" jsonschema:"Mailgun test mode: accepted and logged but not delivered"`
}

func addSendSingle(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "send_single",
		Description: "DESTRUCTIVE. Sends a single transactional email to one recipient via Mailgun. Returns a send_id for status/stats queries.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
			Title:           "Send single email",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in sendSingleInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Post("/send/single", in))
	})
}

type sendBulkInput struct {
	List        string `json:"list" jsonschema:"List id or slug"`
	Subject     string `json:"subject"`
	Preheader   string `json:"preheader,omitempty"`
	From        string `json:"from"`
	ReplyTo     string `json:"reply_to,omitempty"`
	MD          string `json:"md,omitempty"`
	HTML        string `json:"html,omitempty"`
	Text        string `json:"text,omitempty"`
	Template    string `json:"template,omitempty" jsonschema:"Wrapper template name (server-side)"`
	BatchSize   int    `json:"batch_size,omitempty" jsonschema:"Recipients per Mailgun batch (default 500, max 1000)"`
	ThrottleMS  int    `json:"throttle_ms,omitempty" jsonschema:"Sleep between batches in milliseconds"`
	NotifyEmail string `json:"notify_email,omitempty" jsonschema:"Email to notify on send completion or failure"`
	UnsubMode   string `json:"unsub_mode,omitempty" jsonschema:"local | redirect | external"`
	UnsubRedir  string `json:"unsub_redir,omitempty"`
	UnsubURL    string `json:"unsub_url,omitempty"`
}

func addSendBulk(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "send_bulk",
		Description: "DESTRUCTIVE. Initiates an asynchronous bulk send to all currently subscribed contacts on a list. The recipient set is frozen at send creation time. Returns a send_id; use get_send_status to monitor progress.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
			Title:           "Send bulk email",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in sendBulkInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Post("/send/bulk", in))
	})
}

type resumeSendInput struct {
	SendID string `json:"send_id" jsonschema:"Send id returned by send_bulk"`
	Force  bool   `json:"force,omitempty" jsonschema:"Resume even if batches are in_flight (may duplicate delivery)"`
}

func addResumeSend(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "resume_send",
		Description: "DESTRUCTIVE. Resumes a paused or failed bulk send from its last cursor. Refuses if any batches are still in_flight unless force=true.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(true),
			Title:           "Resume send",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in resumeSendInput) (*mcpsdk.CallToolResult, struct{}, error) {
		path := "/send/" + in.SendID + "/resume"
		if in.Force {
			path += "?force=1"
		}
		return passthrough(c.Post(path, nil))
	})
}

type sendIDInput struct {
	SendID string `json:"send_id"`
}

func addGetSendStatus(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_send_status",
		Description: "Returns current status and progress for a send (status, completed_batches, total_batches, sent, remaining, last_subscription_id).",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "Get send status",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in sendIDInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Get("/send/" + in.SendID))
	})
}

func addGetSendStats(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_send_stats",
		Description: "Returns aggregate stats for a send: sent (MiniGun), delivered/failed/opened/clicked/complained (Mailgun Metrics), unsubscribed (MiniGun). Mailgun-sourced fields may lag by 1-2 hours.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "Get send stats",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in sendIDInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Get("/send/" + in.SendID + "/stats"))
	})
}
