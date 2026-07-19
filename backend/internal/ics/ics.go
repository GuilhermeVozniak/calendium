// Package ics implements the subset of RFC 5545 needed to consume public
// ICS subscription feeds: line unfolding (§3.1), text escaping (§3.3.11),
// DATE and DATE-TIME values with TZID/UTC (§3.3.4–5), VEVENT components,
// and a bounded RRULE expander for FREQ=DAILY/WEEKLY/MONTHLY/YEARLY with
// INTERVAL, COUNT, UNTIL, and (weekly) BYDAY. Unknown properties,
// components, and RRULE parts are skipped, never fatal.
package ics

import (
	"strings"
	// Embed the timezone database so TZID resolution works on hosts
	// without a system zoneinfo directory.
	_ "time/tzdata"
)

// unfold splits raw ICS bytes into logical content lines: CRLF and bare LF
// are both accepted, lines starting with a space or tab are continuations
// of the previous line (§3.1, with the single leading marker removed), and
// blank lines are dropped. An orphan continuation with no preceding line is
// tolerated as a standalone line.
func unfold(data []byte) []string {
	physical := strings.Split(string(data), "\n")
	// Collect segments per logical line and join once at the end so a
	// pathological fold of many thousand parts stays linear.
	var segs [][]string
	for _, l := range physical {
		l = strings.TrimSuffix(l, "\r")
		if len(l) > 0 && (l[0] == ' ' || l[0] == '\t') {
			cont := l[1:]
			if n := len(segs); n > 0 {
				segs[n-1] = append(segs[n-1], cont)
				continue
			}
			l = cont // orphan continuation
		}
		segs = append(segs, []string{l})
	}
	lines := make([]string, 0, len(segs))
	for _, s := range segs {
		joined := strings.Join(s, "")
		if joined == "" {
			continue
		}
		lines = append(lines, joined)
	}
	return lines
}

// contentLine is one parsed RFC 5545 content line: NAME[;PARAM=VALUE...]:VALUE.
type contentLine struct {
	name   string            // upper-cased
	params map[string]string // keys upper-cased; quotes stripped from values
	value  string            // raw value (unescape with unescapeText where TEXT)
}

// parseContentLine splits a logical line into name, parameters, and value.
// The name/value separator is the first colon outside of double quotes.
// Malformed lines (no separator, empty name) return ok=false; malformed
// individual parameters are skipped, never fatal.
func parseContentLine(s string) (contentLine, bool) {
	colon := -1
	inQuotes := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuotes = !inQuotes
		case ':':
			if !inQuotes {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon < 0 {
		return contentLine{}, false
	}
	cl := contentLine{
		params: map[string]string{},
		value:  s[colon+1:],
	}

	head := s[:colon]
	parts := splitOutsideQuotes(head, ';')
	cl.name = strings.ToUpper(strings.TrimSpace(parts[0]))
	if cl.name == "" {
		return contentLine{}, false
	}
	for _, p := range parts[1:] {
		k, v, found := strings.Cut(p, "=")
		if !found {
			continue // parameter without '=': skip, tolerant
		}
		k = strings.ToUpper(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		cl.params[k] = strings.Trim(v, `"`)
	}
	return cl, true
}

// splitOutsideQuotes splits s on sep, ignoring separators inside
// double-quoted spans. An unterminated quote consumes to end of string.
func splitOutsideQuotes(s string, sep byte) []string {
	var out []string
	start := 0
	inQuotes := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuotes = !inQuotes
		case sep:
			if !inQuotes {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// unescapeText reverses RFC 5545 §3.3.11 TEXT escaping: \n and \N become
// newlines; \, \; and \\ become the literal character. Unknown escapes keep
// the escaped character; a trailing lone backslash is kept verbatim.
func unescapeText(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(s) {
			b.WriteByte('\\') // trailing lone backslash
			break
		}
		i++
		switch s[i] {
		case 'n', 'N':
			b.WriteByte('\n')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
