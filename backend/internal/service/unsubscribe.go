package service

import "strings"

// unsubscribeInfo carries the parsed List-Unsubscribe targets of one message.
type unsubscribeInfo struct {
	Mailto   string
	URL      string
	OneClick bool
}

// parseListUnsubscribe extracts unsubscribe targets from the RFC 2369
// List-Unsubscribe header (comma-separated, angle-bracketed mailto:/http(s):
// URIs; first of each kind wins) and flags RFC 8058 one-click support when
// List-Unsubscribe-Post declares "List-Unsubscribe=One-Click". Header keys
// and values are matched case-insensitively; one-click requires an HTTP URL.
func parseListUnsubscribe(headers map[string]string) unsubscribeInfo {
	var info unsubscribeInfo
	var raw, post string
	for k, v := range headers {
		switch strings.ToLower(k) {
		case "list-unsubscribe":
			raw = v
		case "list-unsubscribe-post":
			post = v
		}
	}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimSuffix(strings.TrimPrefix(part, "<"), ">")
		lower := strings.ToLower(part)
		switch {
		case strings.HasPrefix(lower, "mailto:") && info.Mailto == "":
			info.Mailto = part
		case (strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")) && info.URL == "":
			info.URL = part
		}
	}
	info.OneClick = info.URL != "" &&
		strings.EqualFold(strings.TrimSpace(post), "List-Unsubscribe=One-Click")
	return info
}

// optionalString returns nil for "" so empty parses stay NULL in the mirror.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
