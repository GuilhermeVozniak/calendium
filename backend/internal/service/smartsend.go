package service

import (
	"time"

	"calendium/backend/internal/domain"
)

const (
	// smartSendMinOpens is the minimum recorded opens before a suggestion
	// is offered; below it SuggestSendTime returns domain.ErrNotFound.
	smartSendMinOpens = 5
	// assumedLocalPeakHour anchors timezone inference: people's open-rate
	// peak is assumed to sit around 10:00 local (mid-morning email block).
	assumedLocalPeakHour = 10
	// smartSendLeadTime is the minimum future distance of a suggestion.
	smartSendLeadTime = 5 * time.Minute
)

// suggestFromHistogram converts a UTC open-hour histogram into a Smart Send
// suggestion. Deterministic and pure for testability.
func suggestFromHistogram(hist [24]int, email string, now time.Time) (domain.SendSuggestion, bool) {
	total := 0
	for _, c := range hist {
		total += c
	}
	if total < smartSendMinOpens {
		return domain.SendSuggestion{}, false
	}
	// Circularly smoothed peak: score(h) = hist[h-1] + 2*hist[h] + hist[h+1].
	peak, best := 0, -1
	for h := 0; h < 24; h++ {
		score := hist[(h+23)%24] + 2*hist[h] + hist[(h+1)%24]
		if score > best {
			peak, best = h, score
		}
	}
	window := hist[(peak+23)%24] + hist[peak] + hist[(peak+1)%24]
	confidence := float64(window) / float64(total)

	// Inferred offset: peak UTC hour ≈ 10:00 local ⇒ offset = peak - 10,
	// normalized into [-12, 13].
	offset := peak - assumedLocalPeakHour
	for offset < -12 {
		offset += 24
	}
	for offset > 13 {
		offset -= 24
	}

	// Next occurrence of the peak UTC hour, at least smartSendLeadTime out.
	next := time.Date(now.Year(), now.Month(), now.Day(), peak, 0, 0, 0, time.UTC)
	if !next.After(now.Add(smartSendLeadTime)) {
		next = next.Add(24 * time.Hour)
	}
	return domain.SendSuggestion{
		Email:          email,
		SuggestedAt:    next,
		UTCOffsetHours: offset,
		Confidence:     confidence,
		SampleSize:     total,
	}, true
}
