package googleapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- shared test helpers (also used by calendar_test.go in this package) ---

// rewriteRoundTripper redirects every outbound request — regardless of the
// hardcoded gmailBase/calendarBase host baked into the adapter — to the
// local httptest.Server, preserving method/path/query/headers/body. The
// *http.Client passed to NewClient is the injection seam; production
// source is unchanged.
type rewriteRoundTripper struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	req.Host = rt.target.Host
	return rt.base.RoundTrip(req)
}

func rewriteClient(t *testing.T, serverURL string) *http.Client {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	return &http.Client{Transport: rewriteRoundTripper{target: u, base: http.DefaultTransport}}
}

// newGoogleServer starts a mock Google API server and returns it alongside a
// *Client whose http.Client transparently redirects every request — Gmail
// or Calendar, whichever host is hardcoded in the adapter — to the server.
func newGoogleServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, NewClient("cid", "secret", rewriteClient(t, srv.URL))
}

// b64url encodes a body the way Gmail returns message part data.
func b64url(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

// decodeBody reads and JSON-decodes a request body into a generic map.
// Uses t.Errorf (not Fatalf) since it runs on the httptest server's
// goroutine, not the test's own.
func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read body: %v", err)
		return nil
	}
	var m map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Errorf("unmarshal body %s: %v", raw, err)
		}
	}
	return m
}

func TestClient_SyncMail_InitialFullSync(t *testing.T) {
	const wantToken = "access-tok-1"

	threadJSON := fmt.Sprintf(`{
		"id":"t1",
		"messages":[
			{"id":"m1","threadId":"t1","labelIds":["INBOX","UNREAD"],
			 "snippet":"first snippet","internalDate":"1704067200000",
			 "payload":{"mimeType":"text/plain",
			   "headers":[{"name":"From","value":"Alice <alice@example.com>"},
			              {"name":"To","value":"me@example.com"},
			              {"name":"Subject","value":"Hello thread"}],
			   "body":{"data":%q}}},
			{"id":"m2","threadId":"t1","labelIds":["INBOX","STARRED"],
			 "snippet":"second snippet","internalDate":"1704153600000",
			 "payload":{"mimeType":"text/html",
			   "headers":[{"name":"From","value":"Bob <bob@example.com>"},
			              {"name":"Subject","value":"Re: Hello thread"}],
			   "body":{"data":%q}}}
		]}`, b64url("Hi there"), b64url("<p>Reply</p>"))

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			t.Errorf("Authorization = %q, want Bearer %s", got, wantToken)
		}
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/profile"):
			io.WriteString(w, `{"historyId":"98765"}`)
		case strings.HasSuffix(p, "/labels"):
			io.WriteString(w, `{"labels":[
				{"id":"INBOX","name":"INBOX","type":"system"},
				{"id":"Label_1","name":"Work","type":"user","color":{"backgroundColor":"#ff0000"}}
			]}`)
		case strings.HasSuffix(p, "/threads"): // thread list (no nextPageToken => single page)
			io.WriteString(w, `{"threads":[{"id":"t1"}]}`)
		case strings.Contains(p, "/threads/"): // one full thread
			io.WriteString(w, threadJSON)
		default:
			t.Errorf("unexpected path %q", p)
			http.NotFound(w, r)
		}
	})

	page, err := c.SyncMail(context.Background(), wantToken, "")
	if err != nil {
		t.Fatalf("SyncMail: %v", err)
	}

	// Steady-state history cursor built from the profile snapshot.
	if page.NextCursor != "hist:98765" {
		t.Errorf("NextCursor = %q, want hist:98765", page.NextCursor)
	}
	if page.HasMore {
		t.Errorf("HasMore = true, want false")
	}

	// Labels: system has no color; user label carries its color.
	if len(page.Labels) != 2 {
		t.Fatalf("Labels len = %d, want 2", len(page.Labels))
	}
	if page.Labels[0].ProviderLabelID != "INBOX" ||
		page.Labels[0].Kind != domain.LabelKindSystem || page.Labels[0].Color != nil {
		t.Errorf("system label mapped wrong: %+v", page.Labels[0])
	}
	if page.Labels[1].Kind != domain.LabelKindUser ||
		page.Labels[1].Color == nil || *page.Labels[1].Color != "#ff0000" {
		t.Errorf("user label mapped wrong: %+v", page.Labels[1])
	}

	// Thread aggregation across both messages.
	if len(page.Threads) != 1 {
		t.Fatalf("Threads len = %d, want 1", len(page.Threads))
	}
	th := page.Threads[0]
	if th.ProviderThreadID != "t1" || th.Subject != "Hello thread" {
		t.Errorf("thread id/subject wrong: %+v", th)
	}
	if th.Snippet != "second snippet" { // snippet follows the latest message
		t.Errorf("Snippet = %q, want %q", th.Snippet, "second snippet")
	}
	if !th.Unread || !th.Starred || !th.InInbox {
		t.Errorf("flags unread=%v starred=%v inInbox=%v, want all true", th.Unread, th.Starred, th.InInbox)
	}
	if th.MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", th.MessageCount)
	}
	if want := time.UnixMilli(1704153600000).UTC(); !th.LastMessageAt.Equal(want) {
		t.Errorf("LastMessageAt = %v, want %v", th.LastMessageAt, want)
	}
	if want := []string{"INBOX", "UNREAD", "STARRED"}; !reflect.DeepEqual(th.LabelIDs, want) {
		t.Errorf("LabelIDs = %v, want %v", th.LabelIDs, want)
	}
	if len(th.Participants) != 2 ||
		th.Participants[0].Email != "alice@example.com" ||
		th.Participants[1].Email != "bob@example.com" {
		t.Errorf("Participants = %+v, want [alice, bob]", th.Participants)
	}

	// Messages: bodies decoded per MIME type, raw headers preserved.
	if len(page.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(page.Messages))
	}
	m1 := page.Messages[0].Message
	if m1.ProviderMessageID != "m1" || m1.From.Email != "alice@example.com" || m1.Subject != "Hello thread" {
		t.Errorf("m1 mapped wrong: %+v", m1)
	}
	if m1.BodyText != "Hi there" {
		t.Errorf("m1.BodyText = %q, want %q", m1.BodyText, "Hi there")
	}
	if !m1.SentAt.Equal(time.UnixMilli(1704067200000).UTC()) {
		t.Errorf("m1.SentAt = %v", m1.SentAt)
	}
	if page.Messages[0].Headers["From"] != "Alice <alice@example.com>" {
		t.Errorf("raw From header not preserved: %v", page.Messages[0].Headers)
	}
	if m2 := page.Messages[1].Message; m2.BodyHTML != "<p>Reply</p>" {
		t.Errorf("m2.BodyHTML = %q, want %q", m2.BodyHTML, "<p>Reply</p>")
	}
}

func TestClient_SyncMail_Incremental(t *testing.T) {
	threadJSON := `{"id":"t9","messages":[{"id":"m1","threadId":"t9","labelIds":["INBOX"],
		"snippet":"s","internalDate":"1704067200000",
		"payload":{"mimeType":"text/plain","headers":[{"name":"From","value":"a@x.com"}],"body":{"data":""}}}]}`

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/history"):
			if got := r.URL.Query().Get("startHistoryId"); got != "55" {
				t.Errorf("startHistoryId = %q, want 55", got)
			}
			io.WriteString(w, `{"history":[{"messages":[{"threadId":"t9"}]}],"historyId":"77"}`)
		case strings.HasSuffix(p, "/threads/t9"):
			io.WriteString(w, threadJSON)
		default:
			t.Errorf("unexpected path %q", p)
			http.NotFound(w, r)
		}
	})

	page, err := c.SyncMail(context.Background(), "tok", "hist:55")
	if err != nil {
		t.Fatalf("SyncMail: %v", err)
	}
	if len(page.Threads) != 1 || page.Threads[0].ProviderThreadID != "t9" {
		t.Fatalf("Threads = %+v, want [t9]", page.Threads)
	}
	if page.NextCursor != "hist:77" {
		t.Errorf("NextCursor = %q, want hist:77", page.NextCursor)
	}
}

func TestClient_SyncMail_IncrementalDeletedThreadSkipped(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/history"):
			io.WriteString(w, `{"history":[{"messages":[{"threadId":"t9"}]}],"historyId":"77"}`)
		case strings.HasSuffix(p, "/threads/t9"):
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":{"code":404,"message":"not found"}}`)
		default:
			t.Errorf("unexpected path %q", p)
			http.NotFound(w, r)
		}
	})

	page, err := c.SyncMail(context.Background(), "tok", "hist:55")
	if err != nil {
		t.Fatalf("SyncMail: %v", err)
	}
	if len(page.Threads) != 0 {
		t.Errorf("Threads = %+v, want none (deleted thread skipped)", page.Threads)
	}
	if page.NextCursor != "hist:77" {
		t.Errorf("NextCursor = %q, want hist:77", page.NextCursor)
	}
}

func TestClient_SyncMail_ExpiredHistoryIDRestartsFullSync(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/history"):
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":{"code":404,"message":"history id expired"}}`)
		case strings.HasSuffix(p, "/profile"):
			io.WriteString(w, `{"historyId":"555"}`)
		case strings.HasSuffix(p, "/labels"):
			io.WriteString(w, `{"labels":[]}`)
		case strings.HasSuffix(p, "/threads"):
			io.WriteString(w, `{"threads":[]}`)
		default:
			t.Errorf("unexpected path %q", p)
			http.NotFound(w, r)
		}
	})

	page, err := c.SyncMail(context.Background(), "tok", "hist:oldid")
	if err != nil {
		t.Fatalf("SyncMail: %v", err)
	}
	if page.NextCursor != "hist:555" {
		t.Errorf("NextCursor = %q, want hist:555", page.NextCursor)
	}
}

func TestClient_SyncMail_BogusCursor(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call for bogus cursor: %s", r.URL)
	})

	page, err := c.SyncMail(context.Background(), "tok", "bogus")
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	if !reflect.DeepEqual(page, port.MailSyncPage{}) {
		t.Errorf("page = %+v, want zero value", page)
	}
}

func TestClient_SyncMail_TransportErrorUnauthorized(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/profile"):
			io.WriteString(w, `{"historyId":"1"}`)
		case strings.HasSuffix(p, "/labels"):
			io.WriteString(w, `{"labels":[]}`)
		case strings.HasSuffix(p, "/threads"):
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error":{"code":403,"message":"forbidden"}}`)
		default:
			t.Errorf("unexpected path %q", p)
			http.NotFound(w, r)
		}
	})

	_, err := c.SyncMail(context.Background(), "tok", "")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestClient_Send_TextOnly(t *testing.T) {
	var gotBody map[string]any

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/messages/send") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{"id":"s1","threadId":"th1"}`)
	})

	msg := port.OutgoingMessage{
		From:     domain.EmailAddress{Email: "me@x.com"},
		To:       []domain.EmailAddress{{Email: "you@x.com"}},
		Subject:  "Hi",
		BodyText: "Body",
	}
	sent, err := c.Send(context.Background(), "tok", msg)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	rawMsg, err := base64.URLEncoding.DecodeString(gotBody["raw"].(string))
	if err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	body := string(rawMsg)
	for _, want := range []string{"To: you@x.com", "Subject: Hi", "Content-Type: text/plain", "Body"} {
		if !strings.Contains(body, want) {
			t.Errorf("raw body missing %q:\n%s", want, body)
		}
	}
	if sent.ProviderMessageID != "s1" || sent.ProviderThreadID != "th1" {
		t.Errorf("sent = %+v, want {s1 th1}", sent)
	}
}

func TestClient_Send_ThreadedReply(t *testing.T) {
	var gotBody map[string]any

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{"id":"s2","threadId":"th1"}`)
	})

	msg := port.OutgoingMessage{
		From:             domain.EmailAddress{Email: "me@x.com"},
		To:               []domain.EmailAddress{{Email: "you@x.com"}},
		Subject:          "Re: Hi",
		BodyText:         "Body",
		ProviderThreadID: "th1",
	}
	if _, err := c.Send(context.Background(), "tok", msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotBody["threadId"] != "th1" {
		t.Errorf("threadId = %v, want th1", gotBody["threadId"])
	}
}

func TestClient_Send_Multipart(t *testing.T) {
	var gotBody map[string]any

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{"id":"s3","threadId":"th3"}`)
	})

	msg := port.OutgoingMessage{
		From:     domain.EmailAddress{Email: "me@x.com"},
		To:       []domain.EmailAddress{{Email: "you@x.com"}},
		Subject:  "Hi",
		BodyText: "Plain body",
		BodyHTML: "<p>HTML body</p>",
	}
	if _, err := c.Send(context.Background(), "tok", msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	rawMsg, err := base64.URLEncoding.DecodeString(gotBody["raw"].(string))
	if err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	body := string(rawMsg)
	if !strings.Contains(body, "multipart/alternative; boundary=") {
		t.Errorf("missing multipart header:\n%s", body)
	}
	if !strings.Contains(body, "Content-Type: text/plain") || !strings.Contains(body, "Plain body") {
		t.Errorf("missing text/plain part:\n%s", body)
	}
	if !strings.Contains(body, "Content-Type: text/html") || !strings.Contains(body, "<p>HTML body</p>") {
		t.Errorf("missing text/html part:\n%s", body)
	}
}

func TestClient_ModifyLabels_AddRemove(t *testing.T) {
	var gotBody map[string]any
	var gotPath string

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = decodeBody(t, r)
	})

	err := c.ModifyLabels(context.Background(), "tok", "t1", []string{"STARRED"}, []string{"UNREAD"})
	if err != nil {
		t.Fatalf("ModifyLabels: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/threads/t1/modify") {
		t.Errorf("path = %q, want suffix /threads/t1/modify", gotPath)
	}
	want := map[string]any{"addLabelIds": []any{"STARRED"}, "removeLabelIds": []any{"UNREAD"}}
	if !reflect.DeepEqual(gotBody, want) {
		t.Errorf("body = %v, want %v", gotBody, want)
	}
}

func TestClient_ModifyLabels_NoOpMakesNoRequest(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call: %s", r.URL)
	})

	if err := c.ModifyLabels(context.Background(), "tok", "t1", nil, nil); err != nil {
		t.Fatalf("ModifyLabels: %v", err)
	}
}

func TestHtmlToText(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string
	}{
		{"plain text passthrough", "hello world", "hello world"},
		{"strips simple tags", "<p>Hello</p>", "Hello"},
		{"strips nested tags", "<div><b>Bold</b> text</div>", "Bold text"},
		{"strips tag attributes", `<a href="https://example.com">Link</a>`, "Link"},
		{"does not decode entities", "Tom &amp; Jerry", "Tom &amp; Jerry"},
		{"trims only leading/trailing whitespace", "  <p>Hi</p>  ", "Hi"},
		{"preserves internal newlines between stripped tags", "<p>Line one</p>\n<p>Line two</p>", "Line one\nLine two"},
		{"self-closing tag leaves no gap", "Before<br/>After", "BeforeAfter"},
		{"unclosed trailing tag consumes rest of string", "Hello <div", "Hello"},
		{"unicode passes through untouched", "<p>café</p>", "café"},
		{"empty input", "", ""},
		{"only tags yields empty string", "<div></div>", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := htmlToText(tc.html); got != tc.want {
				t.Errorf("htmlToText(%q) = %q, want %q", tc.html, got, tc.want)
			}
		})
	}
}

func TestClient_FetchAttachment_DecodesBase64URL(t *testing.T) {
	const wantToken = "tok-attach"
	original := []byte("hello attachment bytes \x00\x01\xffdone")

	var gotPath string
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			t.Errorf("Authorization = %q, want Bearer %s", got, wantToken)
		}
		gotPath = r.URL.Path
		fmt.Fprintf(w, `{"size":%d,"data":%q}`, len(original), b64url(string(original)))
	})

	data, mimeType, err := c.FetchAttachment(context.Background(), wantToken, "m1", "att1")
	if err != nil {
		t.Fatalf("FetchAttachment: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/messages/m1/attachments/att1") {
		t.Errorf("path = %q, want suffix /messages/m1/attachments/att1", gotPath)
	}
	if !bytes.Equal(data, original) {
		t.Errorf("data = %q, want %q", data, original)
	}
	if mimeType != "" {
		t.Errorf("mimeType = %q, want empty (caller falls back to mirrored mime type)", mimeType)
	}
}

func TestClient_FetchAttachment_NotFound(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":{"code":404,"message":"not found"}}`)
	})

	_, _, err := c.FetchAttachment(context.Background(), "tok", "m1", "att1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestClient_FetchAttachment_BadDataErrors(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"size":3,"data":"not valid base64!!"}`)
	})

	_, _, err := c.FetchAttachment(context.Background(), "tok", "m1", "att1")
	if err == nil {
		t.Fatal("expected a decode error, got nil")
	}
}

func TestClient_SyncMail_AttachmentProviderID(t *testing.T) {
	threadJSON := `{"id":"t5","messages":[{"id":"m1","threadId":"t5","labelIds":["INBOX"],
		"snippet":"s","internalDate":"1704067200000",
		"payload":{"mimeType":"multipart/mixed",
			"headers":[{"name":"From","value":"a@x.com"}],
			"parts":[
				{"mimeType":"text/plain","body":{"data":""}},
				{"mimeType":"application/pdf","filename":"report.pdf",
				 "body":{"attachmentId":"att-xyz","size":2048}}
			]}}]}`

	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/profile"):
			io.WriteString(w, `{"historyId":"1"}`)
		case strings.HasSuffix(p, "/labels"):
			io.WriteString(w, `{"labels":[]}`)
		case strings.HasSuffix(p, "/threads"):
			io.WriteString(w, `{"threads":[{"id":"t5"}]}`)
		case strings.Contains(p, "/threads/"):
			io.WriteString(w, threadJSON)
		default:
			t.Errorf("unexpected path %q", p)
			http.NotFound(w, r)
		}
	})

	page, err := c.SyncMail(context.Background(), "tok", "")
	if err != nil {
		t.Fatalf("SyncMail: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(page.Messages))
	}
	atts := page.Messages[0].Message.Attachments
	if len(atts) != 1 {
		t.Fatalf("Attachments = %+v, want 1", atts)
	}
	att := atts[0]
	if att.Filename != "report.pdf" || att.MimeType != "application/pdf" || att.SizeBytes != 2048 {
		t.Errorf("attachment metadata wrong: %+v", att)
	}
	if att.ProviderAttachmentID != "att-xyz" {
		t.Errorf("ProviderAttachmentID = %q, want att-xyz", att.ProviderAttachmentID)
	}
}

func TestClient_ModifyLabels_Unauthorized(t *testing.T) {
	_, c := newGoogleServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"code":401,"message":"invalid token"}}`)
	})

	err := c.ModifyLabels(context.Background(), "tok", "t1", []string{"STARRED"}, nil)
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}
