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
	addDeleteContact(s, c)
	addSendSingle(s, c)
	addSendBulk(s, c)
	addResumeSend(s, c)
	addGetSendStatus(s, c)
	addGetSendStats(s, c)
	addListSendRecipients(s, c)
	addListSendClicks(s, c)
	addGetContactEngagement(s, c)
	addPruneList(s, c)
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

type deleteContactInput struct {
	IDOrEmail string `json:"id_or_email" jsonschema:"Contact id (c_*) or email address. Both are accepted."`
}

func addDeleteContact(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "delete_contact",
		Description: "DESTRUCTIVE. Permanently purges a contact and all their subscriptions + unsubscribe-event audit rows. Use for hard-bounce cleanup (the Mailgun webhook does this automatically; this tool is for manual / scripted purges, e.g. importing a hard-bounce list from a previous provider). For user-initiated opt-outs prefer unsubscribe_contact, which preserves the row with subscribed=0.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			IdempotentHint:  true,
			Title:           "Delete contact",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in deleteContactInput) (*mcpsdk.CallToolResult, struct{}, error) {
		return passthrough(c.Delete("/contacts/" + in.IDOrEmail))
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

type listSendRecipientsInput struct {
	SendID string `json:"send_id" jsonschema:"Send id returned by send_bulk / send_single"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Page size (default 100, max 500)"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque keyset cursor (last contact_id) from a previous page's next_cursor"`
}

func addListSendRecipients(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_send_recipients",
		Description: "Returns the per-recipient message engagement rollup for a send (one row per contact: sent/delivered timestamps, first/last open + click with counts, failure/complaint/unsubscribe state), keyset-paginated by contact_id. Requires EVENTS_ARCHIVE_ENABLED on the server. Use for 'how did each recipient engage with this send' analysis.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "List send recipients",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in listSendRecipientsInput) (*mcpsdk.CallToolResult, struct{}, error) {
		path := "/send/" + in.SendID + "/recipients"
		params := []string{}
		if in.Limit > 0 {
			params = append(params, fmt.Sprintf("limit=%d", in.Limit))
		}
		if in.Cursor != "" {
			params = append(params, "cursor="+in.Cursor)
		}
		if len(params) > 0 {
			path += "?"
			for i, p := range params {
				if i > 0 {
					path += "&"
				}
				path += p
			}
		}
		return passthrough(c.Get(path))
	})
}

type listSendClicksInput struct {
	SendID string `json:"send_id" jsonschema:"Send id returned by send_bulk / send_single"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Page size (default 100, max 500)"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque keyset cursor over (contact_id, url) from a previous page's next_cursor"`
}

func addListSendClicks(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_send_clicks",
		Description: "Returns the per-URL click rollup for a send (one row per contact + clicked link: canonical URL, first/last click, click count), keyset-paginated over (contact_id, url). Requires EVENTS_ARCHIVE_ENABLED on the server. Use to segment an audience by what they clicked.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "List send clicks",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in listSendClicksInput) (*mcpsdk.CallToolResult, struct{}, error) {
		path := "/send/" + in.SendID + "/clicks"
		params := []string{}
		if in.Limit > 0 {
			params = append(params, fmt.Sprintf("limit=%d", in.Limit))
		}
		if in.Cursor != "" {
			params = append(params, "cursor="+in.Cursor)
		}
		if len(params) > 0 {
			path += "?"
			for i, p := range params {
				if i > 0 {
					path += "&"
				}
				path += p
			}
		}
		return passthrough(c.Get(path))
	})
}

type pruneListInput struct {
	List                       string `json:"list" jsonschema:"List id or slug"`
	MinMessagesSinceEngagement int64  `json:"min_messages_since_engagement,omitempty" jsonschema:"Match contacts with messages_since_last_engagement >= N (0 disables)"`
	DormantForDays             int64  `json:"dormant_for_days,omitempty" jsonschema:"Match contacts whose last open/click is older than D days (0 disables)"`
	NoDeliveryForDays          int64  `json:"no_delivery_for_days,omitempty" jsonschema:"Match contacts with no delivered events in the last D days (0 disables)"`
	DryRun                     *bool  `json:"dry_run,omitempty" jsonschema:"When true, returns candidates without modifying any rows. DEFAULTS TO TRUE — explicitly set false to commit."`
	Limit                      int    `json:"limit,omitempty" jsonschema:"Max candidates per call (default 1000, max 10000)"`
	SampleSize                 int    `json:"sample_size,omitempty" jsonschema:"Sample rows to include in the response (default 25)"`
}

func addPruneList(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "prune_list",
		Description: "DESTRUCTIVE. Unsubscribes dormant contacts from a list based on engagement signals from the events archive. dry_run defaults to TRUE — set it false explicitly to commit. Criteria are OR'd: a contact matches when ANY enabled threshold is breached. Returns {candidates, unsubscribed, sample, reason_counts}. Requires Phase 2 (events archive) data to be populated.",
		Annotations: &mcpsdk.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			IdempotentHint:  true,
			Title:           "Prune dormant contacts from a list",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in pruneListInput) (*mcpsdk.CallToolResult, struct{}, error) {
		body := map[string]any{
			"min_messages_since_engagement": in.MinMessagesSinceEngagement,
			"dormant_for_days":              in.DormantForDays,
			"no_delivery_for_days":          in.NoDeliveryForDays,
		}
		// dry_run defaults to TRUE both on the server and on the wire.
		// When the caller omits it we still send true so the server's
		// behavior is unambiguous regardless of body coercion paths.
		if in.DryRun != nil {
			body["dry_run"] = *in.DryRun
		} else {
			body["dry_run"] = true
		}
		if in.Limit > 0 {
			body["limit"] = in.Limit
		}
		if in.SampleSize > 0 {
			body["sample_size"] = in.SampleSize
		}
		return passthrough(c.Post("/lists/"+in.List+"/prune", body))
	})
}

type contactEngagementInput struct {
	IDOrEmail string `json:"id_or_email" jsonschema:"Contact id (c_*) or email address"`
	ListID    string `json:"list_id,omitempty" jsonschema:"Optional list id or slug to narrow to one list"`
}

func addGetContactEngagement(s *mcpsdk.Server, c *client.Client) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_contact_engagement",
		Description: "Returns per-list engagement counters for a contact (total_delivered/opens/clicks, last_delivered_at_ms, last_engagement_at_ms, messages_since_last_engagement). Maintained by the events-archive pull. Useful for diagnosing dormancy before pruning and for personalization workflows that key on recent engagement.",
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint: true,
			Title:        "Get contact engagement",
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, in contactEngagementInput) (*mcpsdk.CallToolResult, struct{}, error) {
		path := "/contacts/" + in.IDOrEmail + "/engagement"
		if in.ListID != "" {
			path += "?list_id=" + in.ListID
		}
		return passthrough(c.Get(path))
	})
}
