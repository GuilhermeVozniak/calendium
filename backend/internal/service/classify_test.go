package service

import (
	"testing"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

func incoming(from string, to []string, headers map[string]string, attachments ...domain.Attachment) port.IncomingMessage {
	msg := domain.Message{
		From:        domain.EmailAddress{Email: from},
		Attachments: attachments,
	}
	for _, addr := range to {
		msg.To = append(msg.To, domain.EmailAddress{Email: addr})
	}
	return port.IncomingMessage{Message: msg, Headers: headers}
}

func TestClassifySplit(t *testing.T) {
	const account = "me@acme.com"
	vips := map[string]struct{}{"boss@bigco.com": {}}

	tests := []struct {
		name string
		in   port.IncomingMessage
		want domain.InboxSplit
	}{
		{
			name: "vip sender wins over everything (case-insensitive)",
			in: incoming("Boss@BigCo.com", []string{account}, map[string]string{
				"List-Unsubscribe": "<mailto:u@bigco.com>",
			}),
			want: domain.SplitVIP,
		},
		{
			name: "calendar invite via text/calendar content type",
			in: incoming("scheduler@vendor.com", []string{account}, map[string]string{
				"Content-Type": `multipart/mixed; boundary="x"; text/calendar`,
			}),
			want: domain.SplitCalendar,
		},
		{
			name: "calendar invite via .ics attachment",
			in: incoming("scheduler@vendor.com", []string{account}, nil,
				domain.Attachment{Filename: "Invite.ICS", MimeType: "application/octet-stream"}),
			want: domain.SplitCalendar,
		},
		{
			name: "list-unsubscribe routes to news",
			in: incoming("digest@newsletter.io", []string{account}, map[string]string{
				"List-Unsubscribe": "<https://newsletter.io/u>",
			}),
			want: domain.SplitNews,
		},
		{
			name: "social network bulk mail routes to social, not news",
			in: incoming("notification@facebookmail.com", []string{account}, map[string]string{
				"List-Unsubscribe": "<https://facebook.com/u>",
			}),
			want: domain.SplitSocial,
		},
		{
			name: "social sender without list headers still social",
			in:   incoming("invites@linkedin.com", []string{account}, nil),
			want: domain.SplitSocial,
		},
		{
			name: "same non-freemail domain is team",
			in:   incoming("colleague@acme.com", []string{"other@acme.com"}, nil),
			want: domain.SplitTeam,
		},
		{
			name: "direct personal mail is important",
			in:   incoming("friend@example.org", []string{account}, nil),
			want: domain.SplitImportant,
		},
		{
			name: "direct but Precedence: bulk is not important",
			in: incoming("promo@shop.example", []string{account}, map[string]string{
				"Precedence": "Bulk",
			}),
			want: domain.SplitOther,
		},
		{
			name: "direct but List-Id present is not important",
			in: incoming("dev@lists.example", []string{account}, map[string]string{
				"List-Id": "<dev.lists.example>",
			}),
			want: domain.SplitOther,
		},
		{
			name: "not addressed to the account is other",
			in:   incoming("someone@example.org", []string{"else@example.org"}, nil),
			want: domain.SplitOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifySplit(tt.in, account, vips); got != tt.want {
				t.Fatalf("ClassifySplit() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifySplitFreemailNeverTeam(t *testing.T) {
	// Two gmail.com users sharing a "domain" must not be classified as team;
	// direct non-bulk mail lands in important instead.
	in := incoming("friend@gmail.com", []string{"me@gmail.com"}, nil)
	if got := ClassifySplit(in, "me@gmail.com", nil); got != domain.SplitImportant {
		t.Fatalf("freemail same-domain: got %q, want %q", got, domain.SplitImportant)
	}
}
