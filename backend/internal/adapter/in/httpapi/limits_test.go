package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"calendium/backend/internal/domain"
)

// limitDetails returns the decoded error.details object (nil when absent).
func limitDetails(e errorDetail) map[string]any {
	m, _ := e.Details.(map[string]any)
	return m
}

func TestFieldCheckCollector(t *testing.T) {
	var fc fieldCheck
	fc.title("title", strings.Repeat("a", 500))
	fc.text("notes", strings.Repeat("b", 64<<10))
	fc.email("email", strings.Repeat("c", 320))
	fc.url("url", strings.Repeat("d", 2048))
	fc.list("items", 500)
	fc.emails("to", []string{"a@example.com"})
	fc.urls("links", []string{"https://example.com"})
	if err := fc.err(); err != nil {
		t.Fatalf("values at the limit must pass: %v", err)
	}
	fc.title("title", strings.Repeat("a", 501))
	fc.text("notes", strings.Repeat("b", (64<<10)+1)) // second failure is ignored: first wins
	err := fc.err()
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	if err.Error() != "validation failed: title exceeds 500" {
		t.Fatalf("err text = %q", err.Error())
	}
	var le *limitError
	if !errors.As(err, &le) || le.field != "title" || le.limit != 500 {
		t.Fatalf("limitError = %+v", le)
	}
}

func TestFieldCheckListHelpers(t *testing.T) {
	var fc fieldCheck
	fc.urls("links", []string{"https://" + strings.Repeat("x", 2041)})
	var le *limitError
	if !errors.As(fc.err(), &le) || le.field != "links" || le.limit != maxURLRunes {
		t.Fatalf("urls element over limit: %v", fc.err())
	}
	fc = fieldCheck{}
	fc.emails("to", make([]string, 501))
	if !errors.As(fc.err(), &le) || le.limit != maxArrayItems {
		t.Fatalf("emails count over limit: %v", fc.err())
	}
	fc = fieldCheck{}
	long := strings.Repeat("x", 501)
	fc.optTitle("name", nil)
	fc.optText("notes", nil)
	if fc.err() != nil {
		t.Fatal("nil optional fields must pass")
	}
	fc.optTitle("name", &long)
	if !errors.As(fc.err(), &le) || le.field != "name" {
		t.Fatalf("optTitle over limit: %v", fc.err())
	}
}

// Review Focus 3: titles/emails/urls count runes, text counts bytes.
func TestFieldCheckRunesVersusBytes(t *testing.T) {
	var fc fieldCheck
	fc.title("title", strings.Repeat("é", 500)) // 500 runes, 1000 bytes
	if err := fc.err(); err != nil {
		t.Fatalf("500 multi-byte runes must pass: %v", err)
	}
	fc.title("title", strings.Repeat("a", 501))
	if err := fc.err(); err == nil {
		t.Fatal("501 ASCII runes must fail")
	}
	fc = fieldCheck{}
	fc.text("notes", strings.Repeat("é", 32768)) // 32768 runes, 65536 bytes = limit
	if err := fc.err(); err != nil {
		t.Fatalf("exactly 64 KiB must pass: %v", err)
	}
	fc.text("notes", strings.Repeat("é", 32769)) // 65538 bytes
	if err := fc.err(); err == nil {
		t.Fatal("64 KiB + 2 bytes of 2-byte runes must fail (bytes, not runes)")
	}
}

func TestFieldLimitsViaHandlersMailAccountsMisc(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	emails := func(n int) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = `"a@example.com"`
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	tests := []struct {
		name      string
		method    string
		target    string
		body      string
		wantField string
		wantLimit float64
	}{
		{"draft subject 501", http.MethodPost, "/v1/mail/drafts", `{"accountId":"a1","subject":"` + long(501) + `"}`, "subject", 500},
		{"draft to 501 recipients", http.MethodPost, "/v1/mail/drafts", `{"accountId":"a1","to":` + strings.ReplaceAll(emails(501), `"a@example.com"`, `{"email":"a@example.com"}`) + `}`, "to", 500},
		{"draft cc email 321", http.MethodPut, "/v1/mail/drafts/d1", `{"accountId":"a1","cc":[{"email":"` + long(321) + `"}]}`, "cc", 320},
		{"snippet name 501", http.MethodPost, "/v1/mail/snippets", `{"name":"` + long(501) + `","bodyHtml":"<p>x</p>"}`, "name", 500},
		{"snippet body 64KiB+1", http.MethodPut, "/v1/mail/snippets/s1", `{"name":"n","bodyHtml":"` + long((64<<10)+1) + `"}`, "bodyHtml", 65536},
		{"bulk threadIds 501", http.MethodPost, "/v1/mail/threads/bulk-actions", `{"threadIds":` + emails(501) + `,"action":"archive"}`, "threadIds", 500},
		{"label id 501", http.MethodPost, "/v1/mail/threads/t1/labels", `{"labelId":"` + long(501) + `","add":true}`, "labelId", 500},
		{"reaction emoji 501", http.MethodPost, "/v1/mail/messages/m1/reactions", `{"emoji":"` + long(501) + `"}`, "emoji", 500},
		{"vip senders 501", http.MethodPut, "/v1/accounts/a1/vip-senders", `{"vipSenders":` + emails(501) + `}`, "vipSenders", 500},
		{"auto bcc email 321", http.MethodPut, "/v1/accounts/a1/auto-bcc", `{"autoBcc":["` + long(321) + `"]}`, "autoBcc", 320},
		{"signature 64KiB+1", http.MethodPut, "/v1/accounts/a1/signature", `{"signatureHtml":"` + long((64<<10)+1) + `"}`, "signatureHtml", 65536},
		{"connect redirectUrl 2049", http.MethodPost, "/v1/accounts/connect/google", `{"redirectUrl":"https://` + long(2049) + `"}`, "redirectUrl", 2048},
		{"integration redirectUrl 2049", http.MethodPost, "/v1/integrations/connect/todoist", `{"redirectUrl":"` + long(2049) + `"}`, "redirectUrl", 2048},
		{"ai compose prompt 64KiB+1", http.MethodPost, "/v1/ai/compose", `{"action":"write","prompt":"` + long((64<<10)+1) + `"}`, "prompt", 65536},
		{"ai ask question 64KiB+1", http.MethodPost, "/v1/ai/ask", `{"question":"` + long((64<<10)+1) + `"}`, "question", 65536},
		{"device token 64KiB+1", http.MethodPost, "/v1/devices", `{"platform":"web","token":"` + long((64<<10)+1) + `"}`, "token", 65536},
		{"comment body 64KiB+1", http.MethodPost, "/v1/mail/threads/t1/comments", `{"teamId":"team_1","body":"` + long((64<<10)+1) + `"}`, "body", 65536},
		{"comment edit body 64KiB+1", http.MethodPatch, "/v1/comments/c1", `{"body":"` + long((64<<10)+1) + `"}`, "body", 65536},
		{"crm log contactEmail 321", http.MethodPost, "/v1/crm/log", `{"contactEmail":"` + long(321) + `","subject":"s"}`, "contactEmail", 320},
		{"classifier prompt 64KiB+1", http.MethodPost, "/v1/classifiers", `{"name":"n","prompt":"` + long((64<<10)+1) + `"}`, "prompt", 65536},
		{"classifier update name 501", http.MethodPatch, "/v1/classifiers/c1", `{"name":"` + long(501) + `","prompt":"p"}`, "name", 500},
		{"task title 501", http.MethodPost, "/v1/tasks", `{"title":"` + long(501) + `"}`, "title", 500},
		{"task patch notes 64KiB+1", http.MethodPatch, "/v1/tasks/t1", `{"notes":"` + long((64<<10)+1) + `"}`, "notes", 65536},
		{"subscription url 2049", http.MethodPost, "/v1/calendar-subscriptions", `{"url":"https://` + long(2049) + `"}`, "url", 2048},
		{"subscription patch name 501", http.MethodPatch, "/v1/calendar-subscriptions/s1", `{"name":"` + long(501) + `"}`, "name", 500},
		{"prefs splitOrder 501", http.MethodPut, "/v1/prefs", `{"splitOrder":` + emails(501) + `}`, "splitOrder", 500},
		{"delegation scopes 501", http.MethodPost, "/v1/delegations", `{"assistantEmail":"a@example.com","scopes":` + emails(501) + `}`, "scopes", 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.Integrations = &fakeIntegrationService{}
			h.deps.Collab = &fakeCollabService{}
			h.deps.Crm = &fakeCrmService{}
			h.deps.Delegations = &fakeDelegationService{}
			rec := h.authed(tt.method, tt.target, strings.NewReader(tt.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
			e := decodeErr(t, rec)
			d := limitDetails(e)
			if e.Code != "validation_failed" || d["field"] != tt.wantField || d["limit"] != tt.wantLimit {
				t.Fatalf("envelope = %+v, want validation_failed field=%s limit=%v", e, tt.wantField, tt.wantLimit)
			}
		})
	}
}

func TestFieldLimitsAtLimitPassHandlers(t *testing.T) {
	h := newHarness(t)
	rec := h.authed(http.MethodPost, "/v1/mail/drafts", strings.NewReader(`{"accountId":"a1","subject":"`+strings.Repeat("é", 500)+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("500-rune multi-byte subject: status = %d (body=%s)", rec.Code, rec.Body.String())
	}
}
