package googleapi

import (
	"encoding/base64"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// --- Gmail wire types (users.messages / users.threads / users.labels) ---

type gmailLabel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"` // "system" | "user"
	Color *struct {
		BackgroundColor string `json:"backgroundColor"`
	} `json:"color"`
}

type gmailBody struct {
	AttachmentID string `json:"attachmentId"`
	Size         int64  `json:"size"`
	Data         string `json:"data"` // base64url
}

type gmailPart struct {
	PartID   string `json:"partId"`
	MimeType string `json:"mimeType"`
	Filename string `json:"filename"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body  gmailBody   `json:"body"`
	Parts []gmailPart `json:"parts"`
}

type gmailMessage struct {
	ID           string    `json:"id"`
	ThreadID     string    `json:"threadId"`
	LabelIDs     []string  `json:"labelIds"`
	Snippet      string    `json:"snippet"`
	InternalDate string    `json:"internalDate"` // epoch millis as string
	Payload      gmailPart `json:"payload"`
}

type gmailThread struct {
	ID       string         `json:"id"`
	Messages []gmailMessage `json:"messages"`
}

// --- mapping to domain ---

func mapLabel(l gmailLabel) domain.Label {
	kind := domain.LabelKindUser
	if l.Type == "system" {
		kind = domain.LabelKindSystem
	}
	out := domain.Label{ProviderLabelID: l.ID, Name: l.Name, Kind: kind}
	if l.Color != nil && l.Color.BackgroundColor != "" {
		c := l.Color.BackgroundColor
		out.Color = &c
	}
	return out
}

// mapThread flattens a full-format Gmail thread into the sync-page shape:
// one domain.Thread (ProviderThreadID set, ID/AccountID empty) plus one
// IncomingMessage per message with the raw headers the classifier needs.
func mapThread(t gmailThread) (domain.Thread, []port.IncomingMessage) {
	th := domain.Thread{
		ProviderThreadID: t.ID,
		Participants:     []domain.EmailAddress{},
		LabelIDs:         []string{},
		MessageCount:     len(t.Messages),
	}
	labelSet := map[string]bool{}
	seenFrom := map[string]bool{}
	msgs := make([]port.IncomingMessage, 0, len(t.Messages))

	for i, gm := range t.Messages {
		im := mapMessage(gm)
		msgs = append(msgs, im)
		m := im.Message

		if i == 0 {
			th.Subject = m.Subject
		}
		if m.SentAt.After(th.LastMessageAt) {
			th.LastMessageAt = m.SentAt
			th.Snippet = gm.Snippet
		}
		if !seenFrom[strings.ToLower(m.From.Email)] && m.From.Email != "" {
			seenFrom[strings.ToLower(m.From.Email)] = true
			th.Participants = append(th.Participants, m.From)
		}
		for _, id := range gm.LabelIDs {
			switch id {
			case "UNREAD":
				th.Unread = true
			case "STARRED":
				th.Starred = true
			case "INBOX":
				th.InInbox = true // provider inbox membership drives the local mirror
			}
			if !labelSet[id] {
				labelSet[id] = true
				th.LabelIDs = append(th.LabelIDs, id)
			}
		}
	}
	return th, msgs
}

func mapMessage(gm gmailMessage) port.IncomingMessage {
	headers := map[string]string{}
	collectHeaders(gm.Payload, headers)

	m := domain.Message{
		ThreadID:          gm.ThreadID, // provider thread id at this boundary
		ProviderMessageID: gm.ID,
		From:              firstAddr(parseAddrList(headers["From"])),
		To:                parseAddrList(headers["To"]),
		Cc:                parseAddrList(headers["Cc"]),
		Bcc:               parseAddrList(headers["Bcc"]),
		Subject:           headers["Subject"],
		Attachments:       []domain.Attachment{},
		SentAt:            internalDate(gm.InternalDate),
	}
	for _, id := range gm.LabelIDs {
		if id == "DRAFT" {
			m.IsDraft = true
		}
	}
	extractParts(gm.Payload, &m)
	return port.IncomingMessage{Message: m, Headers: headers}
}

// collectHeaders records top-level payload headers under canonical MIME keys.
func collectHeaders(p gmailPart, into map[string]string) {
	for _, h := range p.Headers {
		key := textproto.CanonicalMIMEHeaderKey(h.Name)
		if _, dup := into[key]; !dup {
			into[key] = h.Value
		}
	}
}

// extractParts walks the MIME tree filling bodies and attachment metadata.
func extractParts(p gmailPart, m *domain.Message) {
	if p.Filename != "" {
		m.Attachments = append(m.Attachments, domain.Attachment{
			ID:        p.Body.AttachmentID,
			Filename:  p.Filename,
			MimeType:  p.MimeType,
			SizeBytes: p.Body.Size,
		})
	} else if p.Body.Data != "" {
		switch {
		case strings.HasPrefix(p.MimeType, "text/html") && m.BodyHTML == "":
			m.BodyHTML = decodeBase64URL(p.Body.Data)
		case strings.HasPrefix(p.MimeType, "text/plain") && m.BodyText == "":
			m.BodyText = decodeBase64URL(p.Body.Data)
		}
	}
	for _, child := range p.Parts {
		extractParts(child, m)
	}
}

func decodeBase64URL(s string) string {
	if b, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(strings.TrimRight(s, "=")); err == nil {
		return string(b)
	}
	return ""
}

func internalDate(ms string) time.Time {
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(n).UTC()
}

func parseAddrList(s string) []domain.EmailAddress {
	if strings.TrimSpace(s) == "" {
		return []domain.EmailAddress{}
	}
	parsed, err := mail.ParseAddressList(s)
	if err != nil {
		return []domain.EmailAddress{{Email: strings.TrimSpace(s)}}
	}
	out := make([]domain.EmailAddress, 0, len(parsed))
	for _, a := range parsed {
		addr := domain.EmailAddress{Email: a.Address}
		if a.Name != "" {
			name := a.Name
			addr.Name = &name
		}
		out = append(out, addr)
	}
	return out
}

func firstAddr(list []domain.EmailAddress) domain.EmailAddress {
	if len(list) == 0 {
		return domain.EmailAddress{}
	}
	return list[0]
}
