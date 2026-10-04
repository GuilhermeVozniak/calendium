package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

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
	// RequestID is the X-Request-Id of the failed request so users can quote
	// it to support; absent when the middleware is not in the chain.
	RequestID string `json:"requestId,omitempty"`
	// Details carries structured, client-safe context for specific codes
	// (402 payment_required; field limits fill {"field","limit"}); omitted
	// otherwise.
	Details any `json:"details,omitempty"`
}

// paymentRequiredDetails is the 402 `details` body (docs/payments.md).
type paymentRequiredDetails struct {
	Reason           string     `json:"reason"`
	TrialEndsAt      *time.Time `json:"trialEndsAt,omitempty"`
	CurrentPeriodEnd *time.Time `json:"currentPeriodEnd,omitempty"`
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
	case errors.Is(err, domain.ErrAlreadySubscribed):
		return http.StatusConflict, "already_subscribed"
	case errors.Is(err, domain.ErrNoBillingProfile):
		return http.StatusBadRequest, "no_billing_profile"
	case errors.Is(err, domain.ErrBillingUnavailable):
		return http.StatusBadGateway, "billing_unavailable"
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
	case "already_subscribed":
		return "You already have an active subscription. Manage it from billing."
	case "no_billing_profile":
		return "No billing profile yet. Start a checkout first."
	case "billing_unavailable":
		return "The billing service is temporarily unavailable. Please try again."
	default:
		return "Internal server error."
	}
}

// writeError renders the `{ "error": { code, message, details? } }`
// envelope with a stable per-code message. Every failure is logged
// server-side with full detail; the wrapped error text is never echoed to
// clients. A *domain.PaymentRequiredError adds the typed 402 details.
func (s *server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := statusFor(err)
	reqID := requestIDFrom(r.Context())
	if status >= http.StatusInternalServerError {
		s.deps.Logger.Error("request failed",
			"method", r.Method, "route", routeOf(r), "request_id", reqID,
			"status", status, "error", err)
	} else {
		s.deps.Logger.Info("request rejected",
			"method", r.Method, "route", routeOf(r), "request_id", reqID,
			"status", status, "code", code, "error", err)
	}
	detail := errorDetail{Code: code, Message: safeMessage(code), RequestID: reqID}
	var pr *domain.PaymentRequiredError
	if errors.As(err, &pr) {
		detail.Details = paymentRequiredDetails{Reason: string(pr.Reason), TrialEndsAt: pr.TrialEndsAt, CurrentPeriodEnd: pr.CurrentPeriodEnd}
	}
	writeJSON(w, status, errorBody{Error: detail})
}
