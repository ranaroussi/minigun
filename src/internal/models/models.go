package models

import "time"

type Company struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type List struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Weight      int       `json:"weight"`
	CompanyID   string    `json:"company_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Contact struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Params    string    `json:"params"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Subscription struct {
	ID             int64      `json:"id"`
	ListID         string     `json:"list_id"`
	ContactID      string     `json:"contact_id"`
	Subscribed     bool       `json:"subscribed"`
	SubscribedAt   *time.Time `json:"subscribed_at,omitempty"`
	UnsubscribedAt *time.Time `json:"unsubscribed_at,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type SendType string

const (
	SendTypeBulk   SendType = "bulk"
	SendTypeSingle SendType = "single"
)

type SendStatus string

const (
	SendStatusQueued    SendStatus = "queued"
	SendStatusRunning   SendStatus = "running"
	SendStatusCompleted SendStatus = "completed"
	SendStatusFailed    SendStatus = "failed"
	SendStatusCancelled SendStatus = "cancelled"
)

type UnsubscribeMode string

const (
	UnsubModeLocal    UnsubscribeMode = "local"
	UnsubModeRedirect UnsubscribeMode = "redirect"
	UnsubModeExternal UnsubscribeMode = "external"
)

type Send struct {
	ID                     string          `json:"id"`
	Type                   SendType        `json:"type"`
	ListID                 *string         `json:"list_id,omitempty"`
	RecipientEmail         *string         `json:"recipient_email,omitempty"`
	Subject                string          `json:"subject"`
	FromHeader             string          `json:"from_header"`
	ReplyTo                *string         `json:"reply_to,omitempty"`
	TemplateName           *string         `json:"template_name,omitempty"`
	BodyMD                 *string         `json:"body_md,omitempty"`
	BodyHTML               *string         `json:"body_html,omitempty"`
	BodyText               *string         `json:"body_text,omitempty"`
	Status                 SendStatus      `json:"status"`
	BatchSize              int             `json:"batch_size"`
	ThrottleMS             int             `json:"throttle_ms"`
	LastSubscriptionID     int64           `json:"last_subscription_id"`
	MaxSubscriptionID      *int64          `json:"max_subscription_id,omitempty"`
	TotalRecipients        int             `json:"total_recipients"`
	UnsubscribeMode        UnsubscribeMode `json:"unsubscribe_mode"`
	UnsubscribeRedirectURL *string         `json:"unsubscribe_redirect_url,omitempty"`
	UnsubscribeExternalURL *string         `json:"unsubscribe_external_url,omitempty"`
	NotifyEmail            *string         `json:"notify_email,omitempty"`
	LastError              *string         `json:"last_error,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	CompletedAt            *time.Time      `json:"completed_at,omitempty"`
}

type BatchStatus string

const (
	BatchStatusPending   BatchStatus = "pending"
	BatchStatusInFlight  BatchStatus = "in_flight"
	BatchStatusSucceeded BatchStatus = "succeeded"
	BatchStatusFailed    BatchStatus = "failed"
)

type SendBatch struct {
	ID                  string      `json:"id"`
	SendID              string      `json:"send_id"`
	BatchIndex          int         `json:"batch_index"`
	StartSubscriptionID int64       `json:"start_subscription_id"`
	EndSubscriptionID   int64       `json:"end_subscription_id"`
	RecipientCount      int         `json:"recipient_count"`
	Status              BatchStatus `json:"status"`
	MailgunResponse     *string     `json:"mailgun_response,omitempty"`
	CreatedAt           time.Time   `json:"created_at"`
	UpdatedAt           time.Time   `json:"updated_at"`
}

type UnsubscribeEvent struct {
	ID             string    `json:"id"`
	SendID         *string   `json:"send_id,omitempty"`
	SubscriptionID int64     `json:"subscription_id"`
	ListID         string    `json:"list_id"`
	ContactID      string    `json:"contact_id"`
	Email          string    `json:"email"`
	CreatedAt      time.Time `json:"created_at"`
}

type Recipient struct {
	SubscriptionID int64
	ContactID      string
	Email          string
	Params         string
}
