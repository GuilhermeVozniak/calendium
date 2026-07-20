package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"calendium/backend/internal/domain"
)

// maxBodyBytes bounds request bodies (drafts with inline images included).
const maxBodyBytes = 10 << 20

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON reads a bounded JSON body into dst; malformed input becomes a
// domain validation error (HTTP 400).
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: invalid JSON body: %v", domain.ErrValidation, err)
	}
	return nil
}

// optionalField resolves the tri-state of a clearable PATCH field from a
// pre-decoded JSON object (encoding/json alone collapses `null` and absent
// for pointer fields): absent key → nil outer pointer (leave unchanged);
// explicit JSON null → non-nil outer, nil inner (clear); a value → both
// levels set. Feeds double-pointer patch fields such as domain.TaskPatch.
func optionalField[T any](raw map[string]json.RawMessage, key string) (**T, error) {
	msg, ok := raw[key]
	if !ok {
		return nil, nil
	}
	var inner *T
	if err := json.Unmarshal(msg, &inner); err != nil {
		return nil, fmt.Errorf("%w: invalid %q: %v", domain.ErrValidation, key, err)
	}
	return &inner, nil
}

// statusFor maps domain sentinel errors to HTTP status codes and stable
// machine-readable error codes.
func statusFor(err error) (int, string) {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest, "validation_failed"
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrPaymentRequired):
		return http.StatusPaymentRequired, "payment_required"
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, domain.ErrUnprocessable):
		return http.StatusUnprocessableEntity, "unprocessable"
	case errors.Is(err, domain.ErrSelfHosted):
		return http.StatusNotImplemented, "self_hosted"
	case errors.Is(err, domain.ErrNotImplemented):
		return http.StatusNotImplemented, "not_implemented"
	case errors.Is(err, domain.ErrAIOutput):
		return http.StatusBadGateway, "ai_output_invalid"
	case errors.Is(err, domain.ErrAIUnavailable):
		return http.StatusServiceUnavailable, "ai_unavailable"
	case errors.Is(err, domain.ErrRateLimited):
		return http.StatusTooManyRequests, "rate_limited"
	default:
		return http.StatusInternalServerError, "internal"
	}
}

// safeMessage returns a stable, client-safe message for an error code. Raw
// wrapped Go error text is never returned to clients (it can leak internal
// details); full detail is logged server-side by writeError.
func safeMessage(code string) string {
	switch code {
	case "validation_failed":
		return "The request was invalid."
	case "unauthorized":
		return "Authentication is required or has failed."
	case "forbidden":
		return "You do not have permission to perform this action."
	case "payment_required":
		return "An active subscription is required."
	case "not_found":
		return "The requested resource was not found."
	case "conflict":
		return "The request conflicts with the current state of the resource."
	case "unprocessable":
		return "The calendar feed could not be fetched or parsed."
	case "self_hosted":
		return "Billing is disabled on self-hosted instances."
	case "not_implemented":
		return "This feature is not yet available."
	case "ai_output_invalid":
		return "The AI returned an unexpected response."
	case "ai_unavailable":
		return "AI features are not available on this deployment."
	case "rate_limited":
		return "You have exceeded the usage limit. Please try again later."
	default:
		return "Internal server error."
	}
}

// writeError renders the `{ "error": { code, message } }` envelope with a
// stable per-code message. Every failure is logged server-side with full
// detail; the wrapped error text is never echoed to clients.
func (s *server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := statusFor(err)
	if status >= http.StatusInternalServerError {
		s.deps.Logger.Error("request failed",
			"method", r.Method, "path", r.URL.Path, "status", status, "error", err)
	} else {
		s.deps.Logger.Info("request rejected",
			"method", r.Method, "path", r.URL.Path, "status", status, "code", code, "error", err)
	}
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: safeMessage(code)}})
}
