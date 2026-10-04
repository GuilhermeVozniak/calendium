package smtp

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	"calendium/backend/internal/port"
)

// testNow is a Sunday so the RFC 1123Z Date header is deterministic.
var testNow = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func parseFrom(t *testing.T, s string) *netmail.Address {
	t.Helper()
	a, err := netmail.ParseAddress(s)
	if err != nil {
		t.Fatalf("parse from %q: %v", s, err)
	}
	return a
}

func TestBuildMessageTextOnly(t *testing.T) {
	m, err := buildMessage(port.Email{
		To:      []string{"Ada Lovelace <ada@example.test>"},
		Subject: "Welcome",
		Text:    "Hello Ada,\nline two = with equals\n",
	}, parseFrom(t, "Calendium <noreply@calendium.test>"), testNow, "<abc@calendium.test>")
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if m.from != "noreply@calendium.test" {
		t.Fatalf("envelope from = %q, want the bare SMTP_FROM address", m.from)
	}
	if len(m.rcpts) != 1 || m.rcpts[0] != "ada@example.test" {
		t.Fatalf("envelope rcpts = %v, want [ada@example.test]", m.rcpts)
	}
	msg, err := netmail.ReadMessage(bytes.NewReader(m.raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v\n%s", err, m.raw)
	}
	for k, want := range map[string]string{
		"From":                      `"Calendium" <noreply@calendium.test>`,
		"To":                        `"Ada Lovelace" <ada@example.test>`,
		"Subject":                   "Welcome",
		"Date":                      "Sun, 04 Oct 2026 12:00:00 +0000",
		"Message-Id":                "<abc@calendium.test>",
		"Mime-Version":              "1.0",
		"Content-Type":              `text/plain; charset="utf-8"`,
		"Content-Transfer-Encoding": "quoted-printable",
	} {
		if got := msg.Header.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
	if got := msg.Header.Get("Reply-To"); got != "" {
		t.Errorf("Reply-To = %q, want absent", got)
	}
	if !bytes.Contains(m.raw, []byte("=3D")) {
		t.Fatalf("body is not quoted-printable encoded:\n%s", m.raw)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if string(body) != "Hello Ada,\r\nline two = with equals\r\n" {
		t.Fatalf("decoded body = %q", body)
	}
}

func TestBuildMessageMultipartWithReplyToAndEncodedSubject(t *testing.T) {
	const subject = "Convite: equipe “Vendas” — café"
	m, err := buildMessage(port.Email{
		To:      []string{"a@example.test", "Bob, Jr. <bob@example.test>"},
		ReplyTo: "Olive Owner <owner@acme.test>",
		Subject: subject,
		Text:    "Plain body\n",
		HTML:    "<p>HTML body &amp; more</p>\n",
	}, parseFrom(t, "noreply@calendium.test"), testNow, "<id@calendium.test>")
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if len(m.rcpts) != 2 || m.rcpts[0] != "a@example.test" || m.rcpts[1] != "bob@example.test" {
		t.Fatalf("envelope rcpts = %v", m.rcpts)
	}
	msg, err := netmail.ReadMessage(bytes.NewReader(m.raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v\n%s", err, m.raw)
	}
	// Subject is RFC 2047 Q-encoded on the wire and decodes back verbatim.
	rawSubject := msg.Header.Get("Subject")
	if !strings.HasPrefix(rawSubject, "=?utf-8?q?") {
		t.Fatalf("Subject = %q, want an RFC 2047 encoded word", rawSubject)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(rawSubject)
	if err != nil || decoded != subject {
		t.Fatalf("decoded subject = %q (err %v), want %q", decoded, err, subject)
	}
	if got := msg.Header.Get("Reply-To"); got != `"Olive Owner" <owner@acme.test>` {
		t.Fatalf("Reply-To = %q", got)
	}
	// A display name with a comma is quoted so the header still parses as two addresses.
	if got := msg.Header.Get("To"); got != `<a@example.test>, "Bob, Jr." <bob@example.test>` {
		t.Fatalf("To = %q", got)
	}
	if tos, err := msg.Header.AddressList("To"); err != nil || len(tos) != 2 || tos[1].Name != "Bob, Jr." {
		t.Fatalf("To does not parse back to two addresses: %v (err %v)", tos, err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" || params["boundary"] == "" {
		t.Fatalf("Content-Type = %q (err %v), want multipart/alternative with a boundary", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var types, bodies []string
	for {
		p, err := mr.NextRawPart() // raw: keep the CTE header and decode ourselves
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextRawPart: %v", err)
		}
		if cte := p.Header.Get("Content-Transfer-Encoding"); cte != "quoted-printable" {
			t.Fatalf("part CTE = %q, want quoted-printable", cte)
		}
		types = append(types, p.Header.Get("Content-Type"))
		b, err := io.ReadAll(quotedprintable.NewReader(p))
		if err != nil {
			t.Fatalf("decode part: %v", err)
		}
		bodies = append(bodies, string(b))
	}
	if len(types) != 2 || types[0] != `text/plain; charset="utf-8"` || types[1] != `text/html; charset="utf-8"` {
		t.Fatalf("part types = %v, want text/plain then text/html", types)
	}
	if bodies[0] != "Plain body\r\n" || bodies[1] != "<p>HTML body &amp; more</p>\r\n" {
		t.Fatalf("part bodies = %q", bodies)
	}
}

func TestBuildMessageRejectsBadInput(t *testing.T) {
	from := parseFrom(t, "noreply@calendium.test")
	tests := []struct {
		name string
		msg  port.Email
		want string
	}{
		{"no recipients", port.Email{Subject: "s", Text: "t"}, "at least one recipient"},
		{"blank text", port.Email{To: []string{"a@example.test"}, Subject: "s", Text: " \n"}, "text body is required"},
		{"CRLF in subject", port.Email{To: []string{"a@example.test"}, Subject: "x\r\nBcc: evil@example.test", Text: "t"}, errHeaderNewline.Error()},
		{"LF in reply-to", port.Email{To: []string{"a@example.test"}, ReplyTo: "a@b.test\nX-Injected: 1", Subject: "s", Text: "t"}, errHeaderNewline.Error()},
		{"CR in recipient", port.Email{To: []string{"a@example.test\rBcc: x@y.test"}, Subject: "s", Text: "t"}, errHeaderNewline.Error()},
		{"invalid recipient", port.Email{To: []string{"not an address"}, Subject: "s", Text: "t"}, "invalid recipient"},
		{"invalid reply-to", port.Email{To: []string{"a@example.test"}, ReplyTo: "nope", Subject: "s", Text: "t"}, "invalid Reply-To"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildMessage(tt.msg, from, testNow, "<id@x.test>")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestNewMessageIDUsesFromDomain(t *testing.T) {
	id, err := newMessageID("noreply@calendium.test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@calendium.test>") || len(id) != len("<>@calendium.test")+32 {
		t.Fatalf("id = %q, want <32 hex chars@calendium.test>", id)
	}
	other, _ := newMessageID("noreply@calendium.test")
	if other == id {
		t.Fatal("message ids must be unique")
	}
	if fallback, _ := newMessageID("no-at-sign"); !strings.HasSuffix(fallback, "@calendium>") {
		t.Fatalf("fallback id = %q, want the calendium domain", fallback)
	}
}
