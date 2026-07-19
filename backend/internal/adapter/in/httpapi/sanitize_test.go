package httpapi

import "testing"

// TestSanitizeSignatureHTML pins sanitizeSignatureHTML's conservative
// allowlist behavior (M2.5 review fix, MUST-FIX 1): script/style/iframe/
// object/embed elements, on* event-handler attributes, and javascript: URLs
// in href/src must never survive, while ordinary formatting markup a
// signature editor could plausibly produce is left untouched.
func TestSanitizeSignatureHTML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "benign formatting markup passes through unchanged",
			in:   `<p>Best,<br>Ada Lovelace</p><a href="https://example.com">site</a>`,
			want: `<p>Best,<br>Ada Lovelace</p><a href="https://example.com">site</a>`,
		},
		{
			name: "script tag and its content is stripped",
			in:   `<p>hi</p><script>alert(document.cookie)</script>`,
			want: `<p>hi</p>`,
		},
		{
			name: "style tag and its content is stripped",
			in:   `<style>body{display:none}</style><p>hi</p>`,
			want: `<p>hi</p>`,
		},
		{
			name: "iframe tag and its content is stripped",
			in:   `<p>hi</p><iframe src="https://evil.example"></iframe>`,
			want: `<p>hi</p>`,
		},
		{
			name: "object and embed tags are stripped",
			in:   `<object data="evil.swf"></object><embed src="evil.swf">`,
			want: ``,
		},
		{
			name: "stray unclosed script tag is still stripped",
			in:   `<p>hi</p><script>alert(1)`,
			want: `<p>hi</p>`,
		},
		{
			name: "onerror attribute is stripped, element kept",
			in:   `<img src="x" onerror="alert(1)">`,
			want: `<img src="x">`,
		},
		{
			name: "onclick attribute (single-quoted) is stripped",
			in:   `<div onclick='alert(1)'>hi</div>`,
			want: `<div>hi</div>`,
		},
		{
			name: "javascript: href (double-quoted) is neutralized",
			in:   `<a href="javascript:alert(1)">click</a>`,
			want: `<a href="#">click</a>`,
		},
		{
			name: "javascript: href (single-quoted) is neutralized",
			in:   `<a href='javascript:alert(1)'>click</a>`,
			want: `<a href="#">click</a>`,
		},
		{
			name: "javascript: src is neutralized",
			in:   `<img src="javascript:alert(1)">`,
			want: `<img src="#">`,
		},
		{
			name: "empty signature stays empty",
			in:   "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeSignatureHTML(tt.in); got != tt.want {
				t.Fatalf("sanitizeSignatureHTML(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
