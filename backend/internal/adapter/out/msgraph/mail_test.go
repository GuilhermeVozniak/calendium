package msgraph

import (
	"bytes"
	"context"
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
// hardcoded graphBase host baked into the adapter — to the local
// httptest.Server, preserving method/path/query/headers/body. The
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

// newGraphServer starts a mock Graph API server and returns it alongside a
// *Client whose http.Client transparently redirects every request to it.
func newGraphServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, NewClient("cid", "secret", rewriteClient(t, srv.URL))
}

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

func TestClient_SyncMail_Delta(t *testing.T) {
	var deltaLink string // assigned after the server is up

	const messages = `{"value":[
		{"id":"g1","conversationId":"c1","subject":"Hi","bodyPreview":"preview one",
		 "from":{"emailAddress":{"name":"Alice","address":"alice@example.com"}},
		 "toRecipients":[{"emailAddress":{"address":"me@example.com"}}],
		 "receivedDateTime":"2024-01-01T00:00:00Z","isRead":false,"parentFolderId":"folderA",
		 "body":{"contentType":"html","content":"<p>Hi</p>"}},
		{"id":"g2","conversationId":"c1","subject":"Re: Hi","bodyPreview":"preview two",
		 "from":{"emailAddress":{"address":"bob@example.com"}},
		 "receivedDateTime":"2024-01-02T00:00:00Z","isRead":true,
		 "flag":{"flagStatus":"flagged"},"parentFolderId":"folderA"},
		{"id":"g3","@removed":{"reason":"deleted"}}
	]`

	srv, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/messages/delta"):
			io.WriteString(w, fmt.Sprintf(`%s,"@odata.deltaLink":%q}`, messages, deltaLink))
		case strings.HasSuffix(p, "/mailFolders"):
			io.WriteString(w, `{"value":[
				{"id":"folderA","displayName":"Inbox"},
				{"id":"folderB","displayName":"Archive"}
			]}`)
		default:
			t.Errorf("unexpected path %q", p)
			http.NotFound(w, r)
		}
	})
	deltaLink = srv.URL + "/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=xyz"

	page, err := c.SyncMail(context.Background(), "tok", "")
	if err != nil {
		t.Fatalf("SyncMail: %v", err)
	}

	if page.NextCursor != deltaLink {
		t.Errorf("NextCursor = %q, want deltaLink %q", page.NextCursor, deltaLink)
	}
	if page.HasMore {
		t.Errorf("HasMore = true, want false")
	}

	// Folders mirror to labels on the initial delta only.
	if len(page.Labels) != 2 || page.Labels[0].ProviderLabelID != "folderA" ||
		page.Labels[0].Kind != domain.LabelKindSystem {
		t.Fatalf("Labels = %+v, want folderA/folderB system labels", page.Labels)
	}

	// Tombstone (@removed) is dropped: only g1,g2 survive, one conversation.
	if len(page.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2 (tombstone dropped)", len(page.Messages))
	}
	if len(page.Threads) != 1 {
		t.Fatalf("Threads len = %d, want 1", len(page.Threads))
	}
	th := page.Threads[0]
	if th.ProviderThreadID != "c1" || th.Subject != "Re: Hi" { // subject follows latest
		t.Errorf("thread id/subject wrong: %+v", th)
	}
	if th.Snippet != "preview two" {
		t.Errorf("Snippet = %q, want preview two", th.Snippet)
	}
	if !th.Unread || !th.Starred || !th.InInbox {
		t.Errorf("flags unread=%v starred=%v inInbox=%v, want all true", th.Unread, th.Starred, th.InInbox)
	}
	if th.MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", th.MessageCount)
	}
	if want := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC); !th.LastMessageAt.Equal(want) {
		t.Errorf("LastMessageAt = %v, want %v", th.LastMessageAt, want)
	}
	if want := []string{"folderA"}; !reflect.DeepEqual(th.LabelIDs, want) {
		t.Errorf("LabelIDs = %v, want %v", th.LabelIDs, want)
	}
	if len(th.Participants) != 2 ||
		th.Participants[0].Email != "alice@example.com" ||
		th.Participants[1].Email != "bob@example.com" {
		t.Errorf("Participants = %+v, want [alice, bob]", th.Participants)
	}

	// g1 body: html -> BodyHTML; BodyText falls back to bodyPreview.
	m1 := page.Messages[0].Message
	if m1.ProviderMessageID != "g1" || m1.From.Email != "alice@example.com" {
		t.Errorf("m1 mapped wrong: %+v", m1)
	}
	if m1.BodyHTML != "<p>Hi</p>" || m1.BodyText != "preview one" {
		t.Errorf("m1 body wrong: html=%q text=%q", m1.BodyHTML, m1.BodyText)
	}
}

func TestClient_SyncMail_ContinuationCursorSkipsFolders(t *testing.T) {
	var newDeltaLink string

	srv, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/messages/delta"):
			if r.URL.Query().Get("$deltatoken") != "cont123" {
				t.Errorf("$deltatoken = %q, want cont123", r.URL.Query().Get("$deltatoken"))
			}
			fmt.Fprintf(w, `{"value":[],"@odata.deltaLink":%q}`, newDeltaLink)
		case strings.HasSuffix(p, "/mailFolders"):
			t.Errorf("folders re-fetched on a continuation cursor")
		default:
			t.Errorf("unexpected path %q", p)
			http.NotFound(w, r)
		}
	})
	cursor := srv.URL + "/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=cont123"
	newDeltaLink = srv.URL + "/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=cont456"

	page, err := c.SyncMail(context.Background(), "tok", cursor)
	if err != nil {
		t.Fatalf("SyncMail: %v", err)
	}
	if page.Labels != nil {
		t.Errorf("Labels = %+v, want nil (not re-fetched on continuation)", page.Labels)
	}
	if page.NextCursor != newDeltaLink {
		t.Errorf("NextCursor = %q, want %q", page.NextCursor, newDeltaLink)
	}
}

func TestClient_SyncMail_NextLinkPagination(t *testing.T) {
	var nextLink string

	srv, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/messages/delta"):
			fmt.Fprintf(w, `{"value":[],"@odata.nextLink":%q}`, nextLink)
		case strings.HasSuffix(p, "/mailFolders"):
			io.WriteString(w, `{"value":[]}`)
		default:
			t.Errorf("unexpected path %q", p)
			http.NotFound(w, r)
		}
	})
	nextLink = srv.URL + "/v1.0/me/mailFolders/inbox/messages/delta?$skiptoken=p2"

	page, err := c.SyncMail(context.Background(), "tok", "")
	if err != nil {
		t.Fatalf("SyncMail: %v", err)
	}
	if !page.HasMore {
		t.Errorf("HasMore = false, want true")
	}
	if page.NextCursor != nextLink {
		t.Errorf("NextCursor = %q, want %q", page.NextCursor, nextLink)
	}
}

func TestClient_SyncMail_ExpiredDeltaTokenRestarts(t *testing.T) {
	srv, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/messages/delta"):
			if r.URL.Query().Get("$deltatoken") == "stale" {
				w.WriteHeader(http.StatusGone)
				io.WriteString(w, `{"error":{"code":"ResyncRequired","message":"token expired"}}`)
				return
			}
			io.WriteString(w, `{"value":[],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/fresh"}`)
		case strings.HasSuffix(p, "/mailFolders"):
			io.WriteString(w, `{"value":[{"id":"folderA","displayName":"Inbox"}]}`)
		default:
			t.Errorf("unexpected path %q", p)
			http.NotFound(w, r)
		}
	})
	cursor := srv.URL + "/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=stale"

	page, err := c.SyncMail(context.Background(), "tok", cursor)
	if err != nil {
		t.Fatalf("SyncMail: %v", err)
	}
	if len(page.Labels) == 0 {
		t.Errorf("Labels = %+v, want non-empty (folders re-fetched after restart)", page.Labels)
	}
}

func TestClient_SyncMail_AttachmentProviderID(t *testing.T) {
	const messages = `{"value":[
		{"id":"g1","conversationId":"c1","subject":"Hi","hasAttachments":true,
		 "from":{"emailAddress":{"address":"alice@example.com"}},
		 "receivedDateTime":"2024-01-01T00:00:00Z"}
	]`

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/messages/delta"):
			fmt.Fprintf(w, `%s,"@odata.deltaLink":"https://graph.microsoft.com/v1.0/fresh"}`, messages)
		case strings.HasSuffix(p, "/mailFolders"):
			io.WriteString(w, `{"value":[]}`)
		case strings.HasSuffix(p, "/messages/g1/attachments"):
			if got := r.URL.Query().Get("$select"); got != attachmentMetaSelect {
				t.Errorf("$select = %q, want %q", got, attachmentMetaSelect)
			}
			io.WriteString(w, `{"value":[
				{"id":"att-1","name":"report.pdf","contentType":"application/pdf","size":2048}
			]}`)
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
	if att.ProviderAttachmentID != "att-1" {
		t.Errorf("ProviderAttachmentID = %q, want att-1", att.ProviderAttachmentID)
	}
}

func TestClient_FetchAttachment_RawBodyAndContentType(t *testing.T) {
	const wantToken = "tok-attach"
	original := []byte("raw pdf bytes \x00\x01done")

	var gotPath string
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			t.Errorf("Authorization = %q, want Bearer %s", got, wantToken)
		}
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(original)
	})

	data, mimeType, err := c.FetchAttachment(context.Background(), wantToken, "m1", "att1")
	if err != nil {
		t.Fatalf("FetchAttachment: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/messages/m1/attachments/att1/$value") {
		t.Errorf("path = %q, want suffix .../messages/m1/attachments/att1/$value", gotPath)
	}
	if !bytes.Equal(data, original) {
		t.Errorf("data = %q, want %q", data, original)
	}
	if mimeType != "application/pdf" {
		t.Errorf("mimeType = %q, want application/pdf", mimeType)
	}
}

// TestClient_FetchAttachment_OversizedBodyErrors pins the M2.5 review fix
// (RECOMMENDED 3): doRaw's io.LimitReader(maxAttachmentBytes) used to just
// truncate an oversized body and report success with the truncated bytes —
// a silent data-corruption bug (the caller believes it has the whole
// attachment). It must now read one byte past the cap and fail loudly
// instead.
func TestClient_FetchAttachment_OversizedBodyErrors(t *testing.T) {
	oversized := bytes.Repeat([]byte("x"), maxAttachmentBytes+1)
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(oversized)
	})

	_, _, err := c.FetchAttachment(context.Background(), "tok", "m1", "att1")
	if err == nil {
		t.Fatal("FetchAttachment: want an error for a body exceeding maxAttachmentBytes, got nil")
	}
}

func TestClient_FetchAttachment_NotFound(t *testing.T) {
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":{"code":"ItemNotFound","message":"not found"}}`)
	})

	_, _, err := c.FetchAttachment(context.Background(), "tok", "m1", "att1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestClient_FetchAttachment_Unauthorized(t *testing.T) {
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"code":"InvalidAuthenticationToken","message":"bad token"}}`)
	})

	_, _, err := c.FetchAttachment(context.Background(), "tok", "m1", "att1")
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestClient_Send_HTMLBody(t *testing.T) {
	var draftBody map[string]any
	var sawSend bool

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/me/messages") && r.Method == http.MethodPost:
			draftBody = decodeBody(t, r)
			io.WriteString(w, `{"id":"d1","conversationId":"conv1"}`)
		case strings.HasSuffix(r.URL.Path, "/me/messages/d1/send"):
			sawSend = true
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	msg := port.OutgoingMessage{
		To:       []domain.EmailAddress{{Email: "you@x.com"}},
		Subject:  "Hi",
		BodyHTML: "<p>Hello</p>",
	}
	sent, err := c.Send(context.Background(), "tok", msg)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !sawSend {
		t.Error("expected a POST to .../messages/d1/send")
	}
	if draftBody["subject"] != "Hi" {
		t.Errorf("subject = %v, want Hi", draftBody["subject"])
	}
	bodyMap, ok := draftBody["body"].(map[string]any)
	if !ok || bodyMap["contentType"] != "HTML" || bodyMap["content"] != "<p>Hello</p>" {
		t.Errorf("body = %v, want HTML/<p>Hello</p>", draftBody["body"])
	}
	toRecipients, ok := draftBody["toRecipients"].([]any)
	if !ok || len(toRecipients) != 1 {
		t.Fatalf("toRecipients = %v, want 1 entry", draftBody["toRecipients"])
	}
	toEntry, ok := toRecipients[0].(map[string]any)
	if !ok {
		t.Fatalf("toRecipients[0] = %v, want object", toRecipients[0])
	}
	toEmail, ok := toEntry["emailAddress"].(map[string]any)
	if !ok || toEmail["address"] != "you@x.com" {
		t.Errorf("toRecipients[0].emailAddress = %v, want you@x.com", toEntry["emailAddress"])
	}
	if sent.ProviderMessageID != "d1" || sent.ProviderThreadID != "conv1" {
		t.Errorf("sent = %+v, want {d1 conv1}", sent)
	}
	if sent.SentAt.IsZero() {
		t.Error("SentAt is zero, want non-zero (time.Now().UTC() stamped by Send)")
	}
}

func TestClient_Send_TextBody(t *testing.T) {
	var draftBody map[string]any

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/me/messages") && r.Method == http.MethodPost {
			draftBody = decodeBody(t, r)
			io.WriteString(w, `{"id":"d2","conversationId":"conv2"}`)
			return
		}
		// .../messages/d2/send
	})

	msg := port.OutgoingMessage{
		To:       []domain.EmailAddress{{Email: "you@x.com"}},
		Subject:  "Hi",
		BodyText: "Plain hello",
	}
	if _, err := c.Send(context.Background(), "tok", msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	bodyMap, ok := draftBody["body"].(map[string]any)
	if !ok || bodyMap["contentType"] != "Text" || bodyMap["content"] != "Plain hello" {
		t.Errorf("body = %v, want Text/Plain hello", draftBody["body"])
	}
}

func TestClient_Send_ExistingThreadPrefersCallerID(t *testing.T) {
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/me/messages") && r.Method == http.MethodPost {
			io.WriteString(w, `{"id":"d3","conversationId":"conv-from-server"}`)
			return
		}
	})

	msg := port.OutgoingMessage{
		To:               []domain.EmailAddress{{Email: "you@x.com"}},
		Subject:          "Re: Hi",
		BodyText:         "Reply",
		ProviderThreadID: "conv-orig",
	}
	sent, err := c.Send(context.Background(), "tok", msg)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if sent.ProviderThreadID != "conv-orig" {
		t.Errorf("ProviderThreadID = %q, want conv-orig (caller id preferred)", sent.ProviderThreadID)
	}
}

func TestClient_ModifyLabels_StarredAndUnread(t *testing.T) {
	var patchBody map[string]any

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/me/messages") && r.Method == http.MethodGet:
			if got := r.URL.Query().Get("$filter"); got != "conversationId eq 'c1'" {
				t.Errorf("$filter = %q, want conversationId eq 'c1'", got)
			}
			io.WriteString(w, `{"value":[{"id":"m1","categories":[]}]}`)
		case strings.HasSuffix(r.URL.Path, "/me/messages/m1") && r.Method == http.MethodPatch:
			patchBody = decodeBody(t, r)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	err := c.ModifyLabels(context.Background(), "tok", "c1", []string{port.LabelKeyStarred}, []string{port.LabelKeyUnread})
	if err != nil {
		t.Fatalf("ModifyLabels: %v", err)
	}
	flag, ok := patchBody["flag"].(map[string]any)
	if !ok || flag["flagStatus"] != "flagged" {
		t.Errorf("flag = %v, want flagged", patchBody["flag"])
	}
	if patchBody["isRead"] != true {
		t.Errorf("isRead = %v, want true", patchBody["isRead"])
	}
}

func TestClient_ModifyLabels_TrashMoves(t *testing.T) {
	var moveBody map[string]any

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/me/messages") && r.Method == http.MethodGet:
			io.WriteString(w, `{"value":[{"id":"m1","categories":[]}]}`)
		case strings.HasSuffix(r.URL.Path, "/me/messages/m1/move"):
			moveBody = decodeBody(t, r)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	err := c.ModifyLabels(context.Background(), "tok", "c1", []string{port.LabelKeyTrash}, nil)
	if err != nil {
		t.Fatalf("ModifyLabels: %v", err)
	}
	if moveBody["destinationId"] != "deleteditems" {
		t.Errorf("destinationId = %v, want deleteditems", moveBody["destinationId"])
	}
}

func TestClient_ModifyLabels_RemoveInboxArchives(t *testing.T) {
	var moveBody map[string]any

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/me/messages") && r.Method == http.MethodGet:
			io.WriteString(w, `{"value":[{"id":"m1","categories":[]}]}`)
		case strings.HasSuffix(r.URL.Path, "/me/messages/m1/move"):
			moveBody = decodeBody(t, r)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	err := c.ModifyLabels(context.Background(), "tok", "c1", nil, []string{port.LabelKeyInbox})
	if err != nil {
		t.Fatalf("ModifyLabels: %v", err)
	}
	if moveBody["destinationId"] != "archive" {
		t.Errorf("destinationId = %v, want archive", moveBody["destinationId"])
	}
}

func TestClient_ModifyLabels_CustomCategory(t *testing.T) {
	var patchBody map[string]any

	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/me/messages") && r.Method == http.MethodGet:
			io.WriteString(w, `{"value":[{"id":"m1","categories":[]}]}`)
		case strings.HasSuffix(r.URL.Path, "/me/messages/m1") && r.Method == http.MethodPatch:
			patchBody = decodeBody(t, r)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	err := c.ModifyLabels(context.Background(), "tok", "c1", []string{"Project"}, nil)
	if err != nil {
		t.Fatalf("ModifyLabels: %v", err)
	}
	cats, ok := patchBody["categories"].([]any)
	if !ok || len(cats) != 1 || cats[0] != "Project" {
		t.Errorf("categories = %v, want [Project]", patchBody["categories"])
	}
}

func TestClient_ModifyLabels_NoMessagesNotFound(t *testing.T) {
	_, c := newGraphServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"value":[]}`)
	})

	err := c.ModifyLabels(context.Background(), "tok", "c1", []string{port.LabelKeyStarred}, nil)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
