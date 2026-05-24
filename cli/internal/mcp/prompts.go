package mcp

import (
	"context"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const composeNewsletterBody = `You are drafting a newsletter for the MiniGun list ` + "`%s`" + `.

Conventions:
- Use Markdown, not HTML, unless explicitly asked otherwise.
- Personalization variables use the syntax {{var | "fallback"}}, e.g. {{first_name | "there"}}.
- Do not include an unsubscribe link manually. MiniGun injects a per-recipient unsubscribe URL via the List-Unsubscribe header and a {{unsub_url}} variable.

Process:
1. Read ` + "`minigun://lists/%s`" + ` to see subscriber count and metadata.
2. (Optional) Read ` + "`minigun://lists/%s/contacts?limit=10`" + ` to sample available contact params.
3. Propose: subject line, preheader (short pre-inbox snippet), and the Markdown body.
4. Confirm with the operator before calling ` + "`send_bulk`" + `.

%s%s`

const auditSendBody = `You are auditing a completed MiniGun send.

Steps:
1. Read ` + "`minigun://sends/%s`" + ` for send metadata, status, and progress.
2. Read ` + "`minigun://sends/%s/stats`" + ` for aggregate Mailgun + MiniGun metrics.
3. Compute and report:
   - delivery rate = delivered / sent
   - open rate = opened / delivered
   - click rate = clicked / opened (and clicked / delivered)
   - complaint rate = complained / delivered
   - unsubscribe rate = unsubscribed / delivered
4. Flag anomalies:
   - complaint rate > 0.1%%
   - unsubscribe rate > 1%%
   - open rate < 5%%
   - delivery rate < 95%%
5. Suggest 1-2 concrete follow-ups (segment further, remove repeat-complainers, A/B test subject, etc.).

Be specific and brief.`

func RegisterPrompts(s *mcpsdk.Server) {
	s.AddPrompt(&mcpsdk.Prompt{
		Name:        "compose_newsletter",
		Title:       "Compose a newsletter",
		Description: "Primes the model to draft a Markdown newsletter for a MiniGun list using MiniGun's variable conventions, then call send_bulk.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "list", Description: "List id or slug", Required: true},
			{Name: "goal", Description: "What the newsletter should accomplish", Required: false},
			{Name: "audience_notes", Description: "Anything the model should know about the audience", Required: false},
		},
	}, func(ctx context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		args := req.Params.Arguments
		list := strings.TrimSpace(args["list"])
		if list == "" {
			return nil, fmt.Errorf("`list` argument is required")
		}
		goalSection := ""
		if g := strings.TrimSpace(args["goal"]); g != "" {
			goalSection = "\nGoal:\n" + g + "\n"
		}
		audienceSection := ""
		if a := strings.TrimSpace(args["audience_notes"]); a != "" {
			audienceSection = "\nAudience notes:\n" + a + "\n"
		}
		body := fmt.Sprintf(composeNewsletterBody, list, list, list, goalSection, audienceSection)
		return &mcpsdk.GetPromptResult{
			Description: "Newsletter composition guide for list " + list,
			Messages: []*mcpsdk.PromptMessage{{
				Role:    "user",
				Content: &mcpsdk.TextContent{Text: body},
			}},
		}, nil
	})

	s.AddPrompt(&mcpsdk.Prompt{
		Name:        "audit_send",
		Title:       "Audit a finished send",
		Description: "Walks the model through producing a post-send report with delivery, open, click, complaint, and unsubscribe rates.",
		Arguments: []*mcpsdk.PromptArgument{
			{Name: "send_id", Description: "Send id returned by send_bulk", Required: true},
		},
	}, func(ctx context.Context, req *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		sendID := strings.TrimSpace(req.Params.Arguments["send_id"])
		if sendID == "" {
			return nil, fmt.Errorf("`send_id` argument is required")
		}
		body := fmt.Sprintf(auditSendBody, sendID, sendID)
		return &mcpsdk.GetPromptResult{
			Description: "Post-send audit for " + sendID,
			Messages: []*mcpsdk.PromptMessage{{
				Role:    "user",
				Content: &mcpsdk.TextContent{Text: body},
			}},
		}, nil
	})
}
