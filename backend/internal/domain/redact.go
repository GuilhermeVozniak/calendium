package domain

import (
	"regexp"
	"strings"
)

// RedactEmail reduces an address to its domain ("***@example.com") for
// logs: enough to diagnose a provider or domain problem, without recording
// who was being written to. Anything that is not addr@domain becomes "***".
func RedactEmail(addr string) string {
	at := strings.LastIndexByte(addr, '@')
	if at < 0 || at == len(addr)-1 {
		return "***"
	}
	return "***@" + strings.ToLower(addr[at+1:])
}

// redactedError carries a scrubbed message over the original chain, so
// errors.Is/As still see the provider error while logging prints no address.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// RedactEmailsInError returns err with every occurrence of the given
// addresses (case-insensitive) in its text replaced by RedactEmail. The
// result unwraps to err. An error whose text contains none of them is
// returned unchanged; nil stays nil.
func RedactEmailsInError(err error, addrs ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	out := msg
	for _, a := range addrs {
		if a == "" {
			continue
		}
		out = regexp.MustCompile("(?i)"+regexp.QuoteMeta(a)).ReplaceAllLiteralString(out, RedactEmail(a))
	}
	if out == msg {
		return err
	}
	return &redactedError{msg: out, err: err}
}
