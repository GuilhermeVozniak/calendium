package httpapi

import "regexp"

// sanitizeSignatureHTML is the conservative allowlist sanitizer applied to
// every account signature at the HTTP boundary (handleSetSignature), before
// it ever reaches AccountService.SetSignature/storage. Signatures are
// authored by the account owner in a contentEditable rich-text editor
// (apps/web settings page) and later rendered both there (dangerouslySetInnerHTML
// preview) and inside sent-mail HTML — so raw execCommand output, or a
// signature crafted to smuggle a payload, must never reach storage unscrubbed.
//
// This intentionally is NOT a general-purpose HTML sanitizer: it strips the
// specific vectors execCommand-authored or hand-crafted signature HTML can
// carry — script/style/iframe/object/embed elements, on* event-handler
// attributes, and javascript: URLs in href/src — while leaving ordinary
// formatting markup (<p>, <br>, <b>, <i>, <a href="https://...">, <span
// style="...">) untouched. Go stdlib only (no x/net/html): regexp-based
// rather than a real tokenizer, which is sufficient for this narrow,
// well-defined threat model.
func sanitizeSignatureHTML(in string) string {
	s := in
	for _, tag := range dangerousSignatureTags {
		s = tag.paired.ReplaceAllString(s, "")
		// A tag whose raw content is inherently unsafe even unrendered
		// (script/style — JS and CSS, never HTML-escaped by the browser)
		// must not survive as trailing text when malformed HTML left it
		// unclosed; everything from the stray opening tag onward is dropped.
		if tag.orphanToEnd != nil {
			s = tag.orphanToEnd.ReplaceAllString(s, "")
		}
		s = tag.stray.ReplaceAllString(s, "")
	}
	s = reOnEventAttr.ReplaceAllString(s, "")
	s = reJSHrefDouble.ReplaceAllString(s, `$1="#"`)
	s = reJSHrefSingle.ReplaceAllString(s, `$1="#"`)
	return s
}

type dangerousTagPattern struct {
	paired      *regexp.Regexp
	stray       *regexp.Regexp
	orphanToEnd *regexp.Regexp // non-nil only for script/style; see sanitizeSignatureHTML
}

// dangerousSignatureTags removes <script>, <style>, <iframe>, <object>, and
// <embed> — content and all — including any stray/self-closing form left
// after a malformed paired match (e.g. an unclosed <script>).
var dangerousSignatureTags = func() []dangerousTagPattern {
	tags := []string{"script", "style", "iframe", "object", "embed"}
	patterns := make([]dangerousTagPattern, len(tags))
	for i, tag := range tags {
		p := dangerousTagPattern{
			paired: regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>.*?</` + tag + `\s*>`),
			stray:  regexp.MustCompile(`(?is)</?` + tag + `\b[^>]*>`),
		}
		if tag == "script" || tag == "style" {
			p.orphanToEnd = regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>.*$`)
		}
		patterns[i] = p
	}
	return patterns
}()

// reOnEventAttr strips on* event-handler attributes (onerror=, onclick=,
// onload=, ...) in any quoting style.
var reOnEventAttr = regexp.MustCompile(`(?is)\s+on[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)

// reJSHrefDouble / reJSHrefSingle neutralize javascript: URLs in href/src
// attributes (double- and single-quoted forms respectively — RE2 has no
// backreferences, so the two quote styles need separate patterns).
var (
	reJSHrefDouble = regexp.MustCompile(`(?is)(href|src)\s*=\s*"\s*javascript:[^"]*"`)
	reJSHrefSingle = regexp.MustCompile(`(?is)(href|src)\s*=\s*'\s*javascript:[^']*'`)
)
