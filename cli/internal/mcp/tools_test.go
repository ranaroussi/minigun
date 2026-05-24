package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ranaroussi/minigun/cli/internal/client"
)

type recordedRequest struct {
	Method string
	Path   string
	Body   string
	Auth   string
}

func newTestClient(t *testing.T, status int, response string) (*client.Client, *recordedRequest, func()) {
	t.Helper()
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.Method = r.Method
		rec.Path = r.URL.RequestURI()
		rec.Auth = r.Header.Get("Authorization")
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			rec.Body = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	return client.New(srv.URL, "tok"), rec, srv.Close
}

func textOut(res *mcpsdk.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestHealthToolForwardsAuth(t *testing.T) {
	c, rec, done := newTestClient(t, 200, `{"status":"ok","db":"ok"}`)
	defer done()

	var captured func(context.Context, *mcpsdk.CallToolRequest, emptyInput) (*mcpsdk.CallToolResult, struct{}, error)
	addHealthCustom := func() {
		captured = func(ctx context.Context, req *mcpsdk.CallToolRequest, in emptyInput) (*mcpsdk.CallToolResult, struct{}, error) {
			return passthrough(c.Get("/healthz"))
		}
	}
	addHealthCustom()

	res, _, err := captured(context.Background(), nil, emptyInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", textOut(res))
	}
	if rec.Method != "GET" || rec.Path != "/healthz" {
		t.Fatalf("got %s %s", rec.Method, rec.Path)
	}
	if rec.Auth != "Bearer tok" {
		t.Fatalf("expected bearer token, got %q", rec.Auth)
	}
}

func TestPassthroughServerError(t *testing.T) {
	c, _, done := newTestClient(t, 500, `{"error":"boom"}`)
	defer done()

	res, _, err := passthrough(c.Get("/healthz"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true")
	}
	if got := textOut(res); got == "" {
		t.Fatal("expected text content describing the error")
	}
}

func TestCreateListBodyShape(t *testing.T) {
	c, rec, done := newTestClient(t, 201, `{"id":"l_x","slug":"news","name":"News"}`)
	defer done()

	in := createListInput{Name: "News", Slug: "news"}
	res, _, err := passthrough(c.Post("/lists", in))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", textOut(res))
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(rec.Body), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body)
	}
	if body["name"] != "News" || body["slug"] != "news" {
		t.Fatalf("body: %+v", body)
	}
}

func TestResumeSendForceQuery(t *testing.T) {
	c, rec, done := newTestClient(t, 202, `{"send_id":"snd_x","status":"resumed"}`)
	defer done()

	in := resumeSendInput{SendID: "snd_x", Force: true}
	path := "/send/" + in.SendID + "/resume"
	if in.Force {
		path += "?force=1"
	}
	_, _, err := passthrough(c.Post(path, nil))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Path != "/send/snd_x/resume?force=1" {
		t.Fatalf("unexpected path: %s", rec.Path)
	}
}
