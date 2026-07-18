package msgraph

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// deltaSelect keeps delta payloads lean while still carrying everything the
// mirror and the split-inbox classifier need.
const deltaSelect = "id,conversationId,subject,bodyPreview,body,from,toRecipients,ccRecipients,bccRecipients," +
	"receivedDateTime,isRead,isDraft,flag,internetMessageId,parentFolderId,internetMessageHeaders"

// SyncMail runs a Microsoft Graph delta query over the inbox. The cursor is
// the opaque @odata.nextLink / @odata.deltaLink URL; "" starts a fresh delta
// (which also mirrors the folder list as labels).
func (c *Client) SyncMail(ctx context.Context, accessToken, cursor string) (port.MailSyncPage, error) {
	var page port.MailSyncPage

	endpoint := cursor
	if endpoint == "" {
		endpoint = graphBase + "/me/mailFolders/inbox/messages/delta?$select=" + url.QueryEscape(deltaSelect)
		labels, err := c.syncFolders(ctx, accessToken)
		if err != nil {
			return page, err
		}
		page.Labels = labels
	}

	var res struct {
		Value     []graphMessage `json:"value"`
		NextLink  string         `json:"@odata.nextLink"`
		DeltaLink string         `json:"@odata.deltaLink"`
	}
	if err := c.doJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &res); err != nil {
		var he *httpError
		if errors.As(err, &he) && he.StatusCode == http.StatusGone && cursor != "" {
			return c.SyncMail(ctx, accessToken, "") // delta token expired — full resync
		}
		return page, err
	}

	threads := map[string]*domain.Thread{}
	var order []string
	for _, gm := range res.Value {
		if gm.Removed != nil || gm.ConversationID == "" {
			continue // tombstone; Graph mail removals age out of the mirror via folder moves
		}
		im := mapGraphMessage(gm)
		page.Messages = append(page.Messages, im)

		th, ok := threads[gm.ConversationID]
		if !ok {
			th = &domain.Thread{
				ProviderThreadID: gm.ConversationID,
				Subject:          gm.Subject,
				Participants:     []domain.EmailAddress{},
				LabelIDs:         []string{},
				// This delta covers the inbox folder, so every conversation it
				// yields is inbox-resident (archived threads age out via moves).
				InInbox: true,
			}
			threads[gm.ConversationID] = th
			order = append(order, gm.ConversationID)
		}
		th.MessageCount++
		if im.Message.SentAt.After(th.LastMessageAt) {
			th.LastMessageAt = im.Message.SentAt
			th.Snippet = gm.BodyPreview
			th.Subject = gm.Subject
		}
		if !gm.IsRead {
			th.Unread = true
		}
		if gm.Flag != nil && gm.Flag.FlagStatus == "flagged" {
			th.Starred = true
		}
		if gm.ParentFolderID != "" && !contains(th.LabelIDs, gm.ParentFolderID) {
			th.LabelIDs = append(th.LabelIDs, gm.ParentFolderID)
		}
		if from := im.Message.From; from.Email != "" && !containsAddr(th.Participants, from.Email) {
			th.Participants = append(th.Participants, from)
		}
	}
	for _, id := range order {
		page.Threads = append(page.Threads, *threads[id])
	}

	if res.NextLink != "" {
		page.NextCursor = res.NextLink
		page.HasMore = true
	} else {
		page.NextCursor = res.DeltaLink
	}
	return page, nil
}

// syncFolders mirrors mail folders as labels (ProviderLabelID = folder id).
func (c *Client) syncFolders(ctx context.Context, accessToken string) ([]domain.Label, error) {
	var out []domain.Label
	endpoint := graphBase + "/me/mailFolders?$top=100"
	for endpoint != "" {
		var res struct {
			Value []struct {
				ID          string `json:"id"`
				DisplayName string `json:"displayName"`
			} `json:"value"`
			NextLink string `json:"@odata.nextLink"`
		}
		if err := c.doJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &res); err != nil {
			return nil, err
		}
		for _, f := range res.Value {
			out = append(out, domain.Label{
				ProviderLabelID: f.ID,
				Name:            f.DisplayName,
				Kind:            domain.LabelKindSystem,
			})
		}
		endpoint = res.NextLink
	}
	return out, nil
}

// Send creates a draft then submits it — unlike /me/sendMail this yields the
// provider message + conversation ids the mirror needs.
func (c *Client) Send(ctx context.Context, accessToken string, msg port.OutgoingMessage) (port.SentMessage, error) {
	bodyContent, contentType := msg.BodyHTML, "HTML"
	if bodyContent == "" {
		bodyContent, contentType = msg.BodyText, "Text"
	}
	draft := map[string]any{
		"subject":       msg.Subject,
		"body":          map[string]string{"contentType": contentType, "content": bodyContent},
		"toRecipients":  graphRecipients(msg.To),
		"ccRecipients":  graphRecipients(msg.Cc),
		"bccRecipients": graphRecipients(msg.Bcc),
	}

	var created struct {
		ID             string `json:"id"`
		ConversationID string `json:"conversationId"`
	}
	if err := c.doJSON(ctx, http.MethodPost, graphBase+"/me/messages", accessToken, draft, &created); err != nil {
		return port.SentMessage{}, err
	}
	if err := c.doJSON(ctx, http.MethodPost, graphBase+"/me/messages/"+url.PathEscape(created.ID)+"/send", accessToken, nil, nil); err != nil {
		return port.SentMessage{}, err
	}
	return port.SentMessage{
		ProviderMessageID: created.ID,
		ProviderThreadID:  firstNonEmpty(msg.ProviderThreadID, created.ConversationID),
		SentAt:            time.Now().UTC(),
	}, nil
}

// FetchAttachment satisfies the widened port.MailProvider interface so the
// backend compiles once ports/domain land (this task). The real Graph
// attachment download is implemented in M2.5 Task 6
// (docs/superpowers/plans/2026-07-17-m2-5-compose-contact.md).
func (c *Client) FetchAttachment(ctx context.Context, accessToken, providerMessageID, providerAttachmentID string) ([]byte, string, error) {
	return nil, "", errors.New("not implemented")
}

// ModifyLabels applies canonical label keys to every message of the
// conversation: folder moves for INBOX/TRASH/SPAM, isRead for UNREAD, the
// follow-up flag for STARRED; anything else becomes an Outlook category.
func (c *Client) ModifyLabels(ctx context.Context, accessToken, providerThreadID string, add, remove []string) error {
	msgs, err := c.conversationMessages(ctx, accessToken, providerThreadID)
	if err != nil {
		return err
	}

	patch := map[string]any{}
	moveTo := ""
	var addCats, removeCats []string

	for _, key := range add {
		switch key {
		case port.LabelKeyTrash:
			moveTo = "deleteditems"
		case port.LabelKeySpam:
			moveTo = "junkemail"
		case port.LabelKeyInbox:
			moveTo = "inbox"
		case port.LabelKeyUnread:
			patch["isRead"] = false
		case port.LabelKeyStarred:
			patch["flag"] = map[string]string{"flagStatus": "flagged"}
		default:
			addCats = append(addCats, key)
		}
	}
	for _, key := range remove {
		switch key {
		case port.LabelKeyInbox:
			if moveTo == "" {
				moveTo = "archive" // archiving = leaving the inbox
			}
		case port.LabelKeyUnread:
			patch["isRead"] = true
		case port.LabelKeyStarred:
			patch["flag"] = map[string]string{"flagStatus": "notFlagged"}
		case port.LabelKeyTrash, port.LabelKeySpam:
			if moveTo == "" {
				moveTo = "inbox"
			}
		default:
			removeCats = append(removeCats, key)
		}
	}

	for _, m := range msgs {
		msgPatch := patch
		if len(addCats) > 0 || len(removeCats) > 0 {
			msgPatch = map[string]any{}
			for k, v := range patch {
				msgPatch[k] = v
			}
			msgPatch["categories"] = mergeCategories(m.Categories, addCats, removeCats)
		}
		if len(msgPatch) > 0 {
			if err := c.doJSON(ctx, http.MethodPatch, graphBase+"/me/messages/"+url.PathEscape(m.ID), accessToken, msgPatch, nil); err != nil {
				return err
			}
		}
		if moveTo != "" {
			body := map[string]string{"destinationId": moveTo}
			if err := c.doJSON(ctx, http.MethodPost, graphBase+"/me/messages/"+url.PathEscape(m.ID)+"/move", accessToken, body, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

type conversationMessage struct {
	ID         string   `json:"id"`
	Categories []string `json:"categories"`
}

func (c *Client) conversationMessages(ctx context.Context, accessToken, conversationID string) ([]conversationMessage, error) {
	filter := fmt.Sprintf("conversationId eq '%s'", strings.ReplaceAll(conversationID, "'", "''"))
	endpoint := graphBase + "/me/messages?$select=id,categories&$filter=" + url.QueryEscape(filter)
	var out []conversationMessage
	for endpoint != "" {
		var res struct {
			Value    []conversationMessage `json:"value"`
			NextLink string                `json:"@odata.nextLink"`
		}
		if err := c.doJSON(ctx, http.MethodGet, endpoint, accessToken, nil, &res); err != nil {
			return nil, err
		}
		out = append(out, res.Value...)
		endpoint = res.NextLink
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: conversation %s has no messages", domain.ErrNotFound, conversationID)
	}
	return out, nil
}

// --- wire types + mapping ---

type graphAddress struct {
	EmailAddress struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"emailAddress"`
}

type graphMessage struct {
	ID                string         `json:"id"`
	ConversationID    string         `json:"conversationId"`
	Subject           string         `json:"subject"`
	BodyPreview       string         `json:"bodyPreview"`
	InternetMessageID string         `json:"internetMessageId"`
	ParentFolderID    string         `json:"parentFolderId"`
	From              *graphAddress  `json:"from"`
	ToRecipients      []graphAddress `json:"toRecipients"`
	CcRecipients      []graphAddress `json:"ccRecipients"`
	BccRecipients     []graphAddress `json:"bccRecipients"`
	ReceivedDateTime  time.Time      `json:"receivedDateTime"`
	IsRead            bool           `json:"isRead"`
	IsDraft           bool           `json:"isDraft"`
	Body              *struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
	Flag *struct {
		FlagStatus string `json:"flagStatus"`
	} `json:"flag"`
	InternetMessageHeaders []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"internetMessageHeaders"`
	Removed *struct {
		Reason string `json:"reason"`
	} `json:"@removed"`
}

func mapGraphMessage(gm graphMessage) port.IncomingMessage {
	m := domain.Message{
		ThreadID:          gm.ConversationID, // provider thread id at this boundary
		ProviderMessageID: gm.ID,
		To:                mapAddresses(gm.ToRecipients),
		Cc:                mapAddresses(gm.CcRecipients),
		Bcc:               mapAddresses(gm.BccRecipients),
		Subject:           gm.Subject,
		Attachments:       []domain.Attachment{},
		SentAt:            gm.ReceivedDateTime.UTC(),
		IsDraft:           gm.IsDraft,
	}
	if gm.From != nil {
		m.From = mapAddress(*gm.From)
	}
	if gm.Body != nil {
		if strings.EqualFold(gm.Body.ContentType, "html") {
			m.BodyHTML = gm.Body.Content
		} else {
			m.BodyText = gm.Body.Content
		}
	}
	if m.BodyText == "" {
		m.BodyText = gm.BodyPreview
	}

	headers := map[string]string{}
	for _, h := range gm.InternetMessageHeaders {
		key := textproto.CanonicalMIMEHeaderKey(h.Name)
		if _, dup := headers[key]; !dup {
			headers[key] = h.Value
		}
	}
	if gm.InternetMessageID != "" {
		headers["Message-Id"] = gm.InternetMessageID
	}
	return port.IncomingMessage{Message: m, Headers: headers}
}

func mapAddress(a graphAddress) domain.EmailAddress {
	out := domain.EmailAddress{Email: a.EmailAddress.Address}
	if a.EmailAddress.Name != "" {
		n := a.EmailAddress.Name
		out.Name = &n
	}
	return out
}

func mapAddresses(list []graphAddress) []domain.EmailAddress {
	out := make([]domain.EmailAddress, 0, len(list))
	for _, a := range list {
		out = append(out, mapAddress(a))
	}
	return out
}

func graphRecipients(list []domain.EmailAddress) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		email := map[string]string{"address": a.Email}
		if a.Name != nil && *a.Name != "" {
			email["name"] = *a.Name
		}
		out = append(out, map[string]any{"emailAddress": email})
	}
	return out
}

func mergeCategories(current, add, remove []string) []string {
	out := make([]string, 0, len(current)+len(add))
	for _, c := range current {
		if !contains(remove, c) {
			out = append(out, c)
		}
	}
	for _, a := range add {
		if !contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func containsAddr(list []domain.EmailAddress, email string) bool {
	for _, a := range list {
		if strings.EqualFold(a.Email, email) {
			return true
		}
	}
	return false
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
