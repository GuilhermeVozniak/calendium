package service

import (
	"testing"
	"time"
)

func TestSuggestFromHistogram(t *testing.T) {
	utc := func(y int, m time.Month, d, h, min int) time.Time {
		return time.Date(y, m, d, h, min, 0, 0, time.UTC)
	}

	t.Run("all opens at UTC 14 suggests next 14:00 UTC with offset +4", func(t *testing.T) {
		var hist [24]int
		hist[14] = 20
		now := utc(2026, 7, 7, 8, 0)

		got, ok := suggestFromHistogram(hist, "a@b.com", now)
		if !ok {
			t.Fatalf("ok = false, want true")
		}
		if got.Email != "a@b.com" {
			t.Errorf("Email = %q, want a@b.com", got.Email)
		}
		want := utc(2026, 7, 7, 14, 0)
		if !got.SuggestedAt.Equal(want) {
			t.Errorf("SuggestedAt = %v, want %v", got.SuggestedAt, want)
		}
		if got.UTCOffsetHours != 4 {
			t.Errorf("UTCOffsetHours = %d, want 4", got.UTCOffsetHours)
		}
		if got.SampleSize != 20 {
			t.Errorf("SampleSize = %d, want 20", got.SampleSize)
		}
	})

	t.Run("opens spread 13-15 confidence is 1.0", func(t *testing.T) {
		var hist [24]int
		hist[13], hist[14], hist[15] = 5, 5, 5
		now := utc(2026, 7, 7, 8, 0)

		got, ok := suggestFromHistogram(hist, "a@b.com", now)
		if !ok {
			t.Fatalf("ok = false, want true")
		}
		if got.Confidence != 1.0 {
			t.Errorf("Confidence = %v, want 1.0", got.Confidence)
		}
	})

	t.Run("4 opens is below the minimum and not ok", func(t *testing.T) {
		var hist [24]int
		hist[14] = 4
		now := utc(2026, 7, 7, 8, 0)

		_, ok := suggestFromHistogram(hist, "a@b.com", now)
		if ok {
			t.Fatalf("ok = true, want false")
		}
	})

	t.Run("now within lead time of today's peak rolls to tomorrow", func(t *testing.T) {
		var hist [24]int
		hist[14] = 10
		now := utc(2026, 7, 7, 13, 58) // 2 min before 14:00, lead time is 5 min

		got, ok := suggestFromHistogram(hist, "a@b.com", now)
		if !ok {
			t.Fatalf("ok = false, want true")
		}
		want := utc(2026, 7, 8, 14, 0)
		if !got.SuggestedAt.Equal(want) {
			t.Errorf("SuggestedAt = %v, want %v (tomorrow)", got.SuggestedAt, want)
		}
	})

	t.Run("peak 2 normalizes offset to -8", func(t *testing.T) {
		var hist [24]int
		hist[2] = 10
		now := utc(2026, 7, 7, 0, 0)

		got, ok := suggestFromHistogram(hist, "a@b.com", now)
		if !ok {
			t.Fatalf("ok = false, want true")
		}
		if got.UTCOffsetHours != -8 {
			t.Errorf("UTCOffsetHours = %d, want -8", got.UTCOffsetHours)
		}
	})

	t.Run("uniform histogram is still ok with confidence 3/24", func(t *testing.T) {
		var hist [24]int
		for h := range hist {
			hist[h] = 1
		}
		now := utc(2026, 7, 7, 0, 0)

		got, ok := suggestFromHistogram(hist, "a@b.com", now)
		if !ok {
			t.Fatalf("ok = false, want true")
		}
		want := 3.0 / 24.0
		if got.Confidence != want {
			t.Errorf("Confidence = %v, want %v", got.Confidence, want)
		}
	})
}
