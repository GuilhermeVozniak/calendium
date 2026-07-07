package googleapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

const gmailBase = "https://gmail.googleapis.com/gmail/v1/users/me"

// SyncMail cursor scheme (opaque to callers):
//
//	""                      initial full sync — first page
//	"full:<hist>:<pageTok>" full sync in progress; <hist> is the historyId
//	                        snapshot taken before the crawl started
//	"hist:<historyId>"      steady state — Gmail users.history incremental
//
// A 404 on users.history (expired historyId) transparently restarts a full
// sync.
const fullSyncPageSize = 25

func (c *Client) SyncMail(ctx context.Context, accessToken, cursor string) (port.MailSyncPage, error) {
	switch {
	case cursor == "":
		return c.fullMailSync(ctx, accessToken, "", "")
	case strings.HasPrefix(cursor, "full:"):
		rest := strings.TrimPrefix(cursor, "full:")
		hist, pageTok, _ := strings.Cut(rest, ":")
		return c.fullMailSync(ctx, accessToken, hist, pageTok)
	case strings.HasPrefix(cursor, "hist:"):
		page, err := c.incrementalMailSync(ctx, accessToken, strings.TrimPrefix(cursor, "hist:"))
		if errors.Is(err, domain.ErrNotFound) {
			// historyId expired upstream — restart from scratch.
			return c.fullMailSync(ctx, accessToken, "", "")
		}
		return page, err
	default:
		return port.MailSyncPage{}, fmt.Errorf("%w: unrecognized gmail sync cursor %q", domain.ErrValidation, cursor)
	}
}

func (c *Client) fullMailSync(ctx context.Context, token, hist, pageTok string) (port.MailSyncPage, error) {
	var page port.MailSyncPage

	if hist == "" {
		// Snapshot the mailbox position first so changes made during the
		// crawl are replayed by the first incremental pass.
		var profile struct {
			HistoryID string `json:"historyId"`
		}
		if err := c.doJSON(ctx, http.MethodGet, gmailBase+"/profile", token, nil, &profile); err != nil {
			return page, err
		}
		hist = profile.HistoryID
	}

	if pageTok == "" { // labels only once, on the first page
		var lr struct {
			Labels []gmailLabel `json:"labels"`
		}
		if err := c.doJSON(ctx, http.MethodGet, gmailBase+"/labels", token, nil, &lr); err != nil {
			return page, err
		}
		for _, l := range lr.Labels {
			page.Labels = append(page.Labels, mapLabel(l))
		}
	}

	q := url.Values{"maxResults": {fmt.Sprint(fullSyncPageSize)}}
	if pageTok != "" {
		q.Set("pageToken", pageTok)
	}
	var list struct {
		Threads []struct {
			ID string `json:"id"`
		} `json:"threads"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := c.doJSON(ctx, http.MethodGet, gmailBase+"/threads?"+q.Encode(), token, nil, &list); err != nil {
		return page, err
	}

	for _, t := range list.Threads {
		if err := c.appendThread(ctx, token, t.ID, &page); err != nil {
			return page, err
		}
	}

	if list.NextPageToken != "" {
		page.NextCursor = "full:" + hist + ":" + list.NextPageToken
		page.HasMore = true
	} else {
		page.NextCursor = "hist:" + hist
	}
	return page, nil
}

func (c *Client) incrementalMailSync(ctx context.Context, token, startHistoryID string) (port.MailSyncPage, error) {
	page := port.MailSyncPage{NextCursor: "hist:" + startHistoryID}

	threadIDs := map[string]bool{}
	latest := startHistoryID
	pageTok := ""
	for {
		q := url.Values{"startHistoryId": {startHistoryID}, "maxResults": {"100"}}
		if pageTok != "" {
			q.Set("pageToken", pageTok)
		}
		var hr struct {
			History []struct {
				Messages []struct {
					ThreadID string `json:"threadId"`
				} `json:"messages"`
			} `json:"history"`
			NextPageToken string `json:"nextPageToken"`
			HistoryID     string `json:"historyId"`
		}
		if err := c.doJSON(ctx, http.MethodGet, gmailBase+"/history?"+q.Encode(), token, nil, &hr); err != nil {
			return port.MailSyncPage{}, err
		}
		if hr.HistoryID != "" {
			latest = hr.HistoryID
		}
		for _, h := range hr.History {
			for _, m := range h.Messages {
				threadIDs[m.ThreadID] = true
			}
		}
		if hr.NextPageToken == "" {
			break
		}
		pageTok = hr.NextPageToken
	}

	for id := range threadIDs {
		err := c.appendThread(ctx, token, id, &page)
		if errors.Is(err, domain.ErrNotFound) {
			continue // thread deleted upstream since the history record
		}
		if err != nil {
			return port.MailSyncPage{}, err
		}
	}
	page.NextCursor = "hist:" + latest
	return page, nil
}

func (c *Client) appendThread(ctx context.Context, token, threadID string, page *port.MailSyncPage) error {
	var gt gmailThread
	if err := c.doJSON(ctx, http.MethodGet, gmailBase+"/threads/"+url.PathEscape(threadID)+"?format=full", token, nil, &gt); err != nil {
		return err
	}
	th, msgs := mapThread(gt)
	page.Threads = append(page.Threads, th)
	page.Messages = append(page.Messages, msgs...)
	return nil
}

// Send delivers via users.messages.send with an RFC 2822 payload in
// base64url (docs: "raw" format). ThreadId keeps replies threaded.
func (c *Client) Send(ctx context.Context, accessToken string, msg port.OutgoingMessage) (port.SentMessage, error) {
	raw, err := buildRFC2822(msg)
	if err != nil {
		return port.SentMessage{}, err
	}
	body := map[string]string{"raw": base64.URLEncoding.EncodeToString(raw)}
	if msg.ProviderThreadID != "" {
		body["threadId"] = msg.ProviderThreadID
	}
	var out struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	if err := c.doJSON(ctx, http.MethodPost, gmailBase+"/messages/send", accessToken, body, &out); err != nil {
		return port.SentMessage{}, err
	}
	return port.SentMessage{ProviderMessageID: out.ID, ProviderThreadID: out.ThreadID}, nil
}

// ModifyLabels maps 1:1 onto users.threads.modify — the canonical label
// keys (INBOX, STARRED, UNREAD, TRASH, SPAM) are Gmail-native ids.
func (c *Client) ModifyLabels(ctx context.Context, accessToken, providerThreadID string, add, remove []string) error {
	body := map[string][]string{}
	if len(add) > 0 {
		body["addLabelIds"] = add
	}
	if len(remove) > 0 {
		body["removeLabelIds"] = remove
	}
	if len(body) == 0 {
		return nil
	}
	endpoint := gmailBase + "/threads/" + url.PathEscape(providerThreadID) + "/modify"
	return c.doJSON(ctx, http.MethodPost, endpoint, accessToken, body, nil)
}

// buildRFC2822 assembles a multipart/alternative MIME message.
func buildRFC2822(msg port.OutgoingMessage) ([]byte, error) {
	var b bytes.Buffer
	writeHeader := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	writeHeader("From", formatAddr(msg.From))
	writeHeader("To", formatAddrList(msg.To))
	writeHeader("Cc", formatAddrList(msg.Cc))
	writeHeader("Bcc", formatAddrList(msg.Bcc))
	writeHeader("Subject", msg.Subject)
	if msg.InReplyTo != "" {
		writeHeader("In-Reply-To", msg.InReplyTo)
		writeHeader("References", msg.InReplyTo)
	}
	writeHeader("MIME-Version", "1.0")

	text := msg.BodyText
	if text == "" {
		text = htmlToText(msg.BodyHTML)
	}

	switch {
	case msg.BodyHTML != "" && text != "":
		mw := multipart.NewWriter(&b)
		writeHeader("Content-Type", `multipart/alternative; boundary="`+mw.Boundary()+`"`)
		b.WriteString("\r\n")
		pw, err := mw.CreatePart(textHeader("text/plain"))
		if err != nil {
			return nil, err
		}
		fmt.Fprint(pw, text)
		hw, err := mw.CreatePart(textHeader("text/html"))
		if err != nil {
			return nil, err
		}
		fmt.Fprint(hw, msg.BodyHTML)
		if err := mw.Close(); err != nil {
			return nil, err
		}
	case msg.BodyHTML != "":
		writeHeader("Content-Type", `text/html; charset="UTF-8"`)
		b.WriteString("\r\n" + msg.BodyHTML)
	default:
		writeHeader("Content-Type", `text/plain; charset="UTF-8"`)
		b.WriteString("\r\n" + text)
	}
	return b.Bytes(), nil
}

func textHeader(mimeType string) map[string][]string {
	return map[string][]string{"Content-Type": {mimeType + `; charset="UTF-8"`}}
}

func formatAddr(a domain.EmailAddress) string {
	if a.Email == "" {
		return ""
	}
	if a.Name != nil && *a.Name != "" {
		return fmt.Sprintf("%q <%s>", *a.Name, a.Email)
	}
	return a.Email
}

func formatAddrList(list []domain.EmailAddress) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		if s := formatAddr(a); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

// htmlToText is a minimal tag stripper used only for the plain-text
// alternative part of outgoing mail.
func htmlToText(html string) string {
	var out strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	return strings.TrimSpace(out.String())
}
