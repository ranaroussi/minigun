package tmpl

import (
	"embed"
	"html/template"

	"github.com/ranaroussi/minigun/internal/store"
)

//go:embed *.html
var FS embed.FS

var Unsubscribe = template.Must(template.ParseFS(FS, "unsubscribe.html"))
var Manage = template.Must(template.ParseFS(FS, "manage.html"))

type UnsubscribeData struct {
	Token            string
	Email            string
	ListName         string
	TurnstileSiteKey string
	Done             bool
	Error            string
}

type ManageData struct {
	Token               string
	Email               string
	CompanyName         string
	Lists               []store.ManageListState
	Deltas              []store.SubscriptionDelta
	Done                bool
	AlreadyUnsubscribed bool
	Error               string
}
