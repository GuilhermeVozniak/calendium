// Package smtp is the outbound transactional-email adapter: a port.Mailer
// over the Go standard library only (net/smtp, crypto/tls, mime/*,
// net/mail). It sends from the instance's own SMTP_FROM address and never
// touches a user's connected mailbox.
package smtp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"net/textproto"
	"strings"
	"time"

	"calendium/backend/internal/port"
)

// message is a fully rendered RFC 5322 message plus its SMTP envelope.
type message struct {
	from  string   // envelope MAIL FROM (bare addr-spec)
	rcpts []string // envelope RCPT TO (bare addr-specs)
	raw   []byte   // headers + body, CRLF line endings; dot-stuffing is the transport's job
}

// errHeaderNewline rejects CR/LF in any header value so a caller-controlled
// string can never inject extra headers.
var errHeaderNewline = errors.New("smtp: header values must not contain CR or LF")

// buildMessage renders msg into wire form. from is the parsed SMTP_FROM;
// now stamps Date; msgID is the full "<id@domain>" Message-ID.
func buildMessage(msg port.Email, from *netmail.Address, now time.Time, msgID string) (message, error) {
	if len(msg.To) == 0 {
		return message{}, errors.New("smtp: at least one recipient is required")
	}
	if strings.TrimSpace(msg.Text) == "" {
		return message{}, errors.New("smtp: a text body is required")
	}
	for _, v := range append([]string{msg.Subject, msg.ReplyTo}, msg.To...) {
		if strings.ContainsAny(v, "\r\n") {
			return message{}, errHeaderNewline
		}
	}
	out := message{from: from.Address}
	display := make([]string, 0, len(msg.To))
	for _, t := range msg.To {
		a, err := parseAddress(t)
		if err != nil {
			return message{}, fmt.Errorf("smtp: invalid recipient %q: %w", t, err)
		}
		out.rcpts = append(out.rcpts, a.Address)
		display = append(display, a.String())
	}

	var b strings.Builder
	header := func(k, v string) {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	header("From", from.String())
	header("To", strings.Join(display, ", "))
	if msg.ReplyTo != "" {
		a, err := parseAddress(msg.ReplyTo)
		if err != nil {
			return message{}, fmt.Errorf("smtp: invalid Reply-To %q: %w", msg.ReplyTo, err)
		}
		header("Reply-To", a.String())
	}
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", now.UTC().Format(time.RFC1123Z))
	header("Message-ID", msgID)
	header("MIME-Version", "1.0")

	if msg.HTML == "" {
		header("Content-Type", `text/plain; charset="utf-8"`)
		header("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		if err := writeQuotedPrintable(&b, msg.Text); err != nil {
			return message{}, err
		}
		out.raw = []byte(b.String())
		return out, nil
	}

	mw := multipart.NewWriter(&b)
	header("Content-Type", `multipart/alternative; boundary="`+mw.Boundary()+`"`)
	b.WriteString("\r\n")
	parts := []struct{ ctype, body string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}}
	for _, p := range parts {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {p.ctype + `; charset="utf-8"`},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return message{}, err
		}
		if err := writeQuotedPrintable(pw, p.body); err != nil {
			return message{}, err
		}
	}
	if err := mw.Close(); err != nil {
		return message{}, err
	}
	out.raw = []byte(b.String())
	return out, nil
}

// parseAddress accepts an RFC 5322 mailbox plus the common human form
// `Bob, Jr. <bob@example.test>` whose unquoted display name contains
// specials (a comma, a period). net/mail rejects that, but it is unambiguous
// once the trailing angle-addr is split off; Address.String() re-quotes the
// name on output, so the rendered header is still one valid mailbox.
func parseAddress(s string) (*netmail.Address, error) {
	a, err := netmail.ParseAddress(s)
	if err == nil {
		return a, nil
	}
	s = strings.TrimSpace(s)
	i := strings.LastIndex(s, "<")
	if i <= 0 || !strings.HasSuffix(s, ">") {
		return nil, err
	}
	spec, perr := netmail.ParseAddress(s[i:])
	if perr != nil {
		return nil, err
	}
	spec.Name = strings.Trim(strings.TrimSpace(s[:i]), `"`)
	return spec, nil
}

func writeQuotedPrintable(w io.Writer, s string) error {
	qw := quotedprintable.NewWriter(w)
	if _, err := io.WriteString(qw, s); err != nil {
		return err
	}
	return qw.Close()
}

// newMessageID returns "<32 hex chars@domain>" where domain is the part of
// fromAddr after '@' (falling back to "calendium").
func newMessageID(fromAddr string) (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("smtp: message id: %w", err)
	}
	domain := "calendium"
	if i := strings.LastIndex(fromAddr, "@"); i >= 0 && i+1 < len(fromAddr) {
		domain = fromAddr[i+1:]
	}
	return "<" + hex.EncodeToString(buf[:]) + "@" + domain + ">", nil
}
