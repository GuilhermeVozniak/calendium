package service

import (
	"strings"

	"calendium/backend/internal/domain"
	"calendium/backend/internal/port"
)

// ClassifySplit assigns a split-inbox category to an incoming message at
// ingest, using header heuristics (docs/architecture.md). Precedence:
//
//  1. VIP senders (user-curated; nil disables)         → vip
//  2. Calendar invites (text/calendar part, .ics)      → calendar
//  3. List-Unsubscribe present                         → news (social networks → social)
//  4. Social-network senders                           → social
//  5. Teammates (sender shares the account's
//     non-freemail domain)                             → team
//  6. Direct, non-bulk personal mail                   → important
//  7. Everything else                                  → other
func ClassifySplit(in port.IncomingMessage, accountEmail string, vips map[string]struct{}) domain.InboxSplit {
	headers := make(map[string]string, len(in.Headers))
	for k, v := range in.Headers {
		headers[strings.ToLower(k)] = v
	}
	from := strings.ToLower(strings.TrimSpace(in.Message.From.Email))
	if _, ok := vips[from]; ok {
		return domain.SplitVIP
	}
	if isCalendarInvite(in, headers) {
		return domain.SplitCalendar
	}
	fromDomain := emailDomain(from)
	if _, ok := headers["list-unsubscribe"]; ok {
		if socialDomains[fromDomain] {
			return domain.SplitSocial
		}
		return domain.SplitNews
	}
	if socialDomains[fromDomain] {
		return domain.SplitSocial
	}
	acctDomain := emailDomain(strings.ToLower(strings.TrimSpace(accountEmail)))
	if fromDomain != "" && fromDomain == acctDomain && !freemailDomains[acctDomain] {
		return domain.SplitTeam
	}
	if isDirect(in.Message, accountEmail) && !isBulk(headers) {
		return domain.SplitImportant
	}
	return domain.SplitOther
}

func isCalendarInvite(in port.IncomingMessage, headers map[string]string) bool {
	if ct := strings.ToLower(headers["content-type"]); strings.Contains(ct, "text/calendar") {
		return true
	}
	for _, a := range in.Message.Attachments {
		if strings.Contains(strings.ToLower(a.MimeType), "text/calendar") ||
			strings.HasSuffix(strings.ToLower(a.Filename), ".ics") {
			return true
		}
	}
	return false
}

func isBulk(headers map[string]string) bool {
	switch strings.ToLower(headers["precedence"]) {
	case "bulk", "list", "junk":
		return true
	}
	return headers["list-id"] != ""
}

func isDirect(m domain.Message, accountEmail string) bool {
	for _, to := range m.To {
		if strings.EqualFold(strings.TrimSpace(to.Email), strings.TrimSpace(accountEmail)) {
			return true
		}
	}
	return false
}

func emailDomain(addr string) string {
	i := strings.LastIndexByte(addr, '@')
	if i < 0 || i == len(addr)-1 {
		return ""
	}
	return addr[i+1:]
}

// freemailDomains never count as "team" domains.
var freemailDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "outlook.com": true,
	"hotmail.com": true, "live.com": true, "msn.com": true,
	"yahoo.com": true, "icloud.com": true, "me.com": true, "mac.com": true,
	"aol.com": true, "proton.me": true, "protonmail.com": true, "gmx.com": true,
}

// socialDomains route to the social split.
var socialDomains = map[string]bool{
	"facebookmail.com": true, "facebook.com": true, "instagram.com": true,
	"twitter.com": true, "x.com": true, "linkedin.com": true,
	"tiktok.com": true, "redditmail.com": true, "reddit.com": true,
	"pinterest.com": true, "discord.com": true, "snapchat.com": true,
}
