package service

// --- ask ---

// askOut is the structured JSON contract for POST /v1/ai/ask: the model's
// prose answer plus the [msg:<id>] ids it actually drew on. Ask validates
// SourceMessageIDs against the candidate set it built, dropping any id the
// model hallucinated before mapping the rest to domain.AiSource.
type askOut struct {
	Answer           string   `json:"answer"`
	SourceMessageIDs []string `json:"sourceMessageIds"`
}

const askSystem = "You are Calendium's assistant. Answer the question using " +
	"ONLY the provided messages, each tagged [msg:<id>]. Cite the ids of the " +
	"messages you actually used. If the answer is not in the messages, say so " +
	`and cite nothing. Respond as JSON: {"answer": "...", "sourceMessageIds": ["..."]}`
