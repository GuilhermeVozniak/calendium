// Package service (this file): shared prompt-building helpers and CompleteJSON
// decode targets for the background AI job handlers in ai_jobs.go. Each job
// kind appends its own self-contained section below (Tasks 6-14 run in
// parallel, so sections must not depend on one another).
package service

import (
	"fmt"
	"strings"

	"calendium/backend/internal/domain"
)

// --- classify ---

// classifyOut is the CompleteJSON decode target for runClassify: the ids of
// every user-defined classifier (from the numbered rule list
// buildClassifySystem sends) that genuinely matches the message. Ids the
// model returns that don't match a known classifier (hallucinated, or from a
// stale prompt) are dropped by the caller, not trusted here.
type classifyOut struct {
	MatchedIDs []string `json:"matchedClassifierIds"`
}

// buildClassifySystem lists the user's enabled classifiers by id and prompt,
// instructing the model to evaluate each rule against the message
// independently and report only genuine matches.
func buildClassifySystem(cs []domain.AiClassifier) string {
	var b strings.Builder
	b.WriteString("You are Calendium's mail classifier. The user has defined the natural-language " +
		"rules below, each identified by an id. Evaluate the message that follows against EVERY " +
		"rule independently and decide whether it genuinely matches - do not guess or include a " +
		"rule out of caution. Respond with a JSON object of the exact shape " +
		`{"matchedClassifierIds": ["<id>", ...]}` + " listing only the ids of rules that truly " +
		"match, using exactly the ids given below (never invent new ones); return an empty array " +
		"when nothing matches.\n\nRules:\n")
	for _, c := range cs {
		fmt.Fprintf(&b, "- id %q: %s\n", c.ID, c.Prompt)
	}
	return b.String()
}
