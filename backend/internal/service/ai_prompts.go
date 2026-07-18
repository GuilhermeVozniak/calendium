// Package service (this file): prompts and structured-output shapes for the
// AI job handlers in ai_jobs.go. Each job kind appends its own
// self-contained section below so parallel tasks never collide on the same
// hunk.
package service

// --- voice_profile ---

// voiceProfileOut is the CompleteJSON target for runVoiceProfile.
type voiceProfileOut struct {
	Profile string `json:"profile"` // 5-10 bullet style guide
}

const voiceProfileSystem = "Analyze these emails the user wrote and produce a " +
	"compact style profile (greeting/sign-off habits, formality, sentence " +
	"length, emoji/punctuation quirks, typical structure) as 5-10 terse " +
	"bullets an assistant can follow to write in their voice. Respond as " +
	`JSON: {"profile": "..."}`

const voiceSampleCount = 25 // newest sent messages fed to the profiler

// voiceMinSamples is the minimum sent-message count before runVoiceProfile
// bothers calling the LLM; below this there isn't enough signal to learn a
// style from yet (the 30-day re-enqueue retries once more mail exists).
const voiceMinSamples = 5
