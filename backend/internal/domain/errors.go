// Package domain holds Calendium's pure business entities and invariants.
// It mirrors packages/shared/src/types.ts and the REST contract in
// docs/architecture.md — keep the three in sync. This package may only
// import the Go standard library.
package domain

import "errors"

// Sentinel errors. Services wrap these with fmt.Errorf("%w: ...") and the
// HTTP adapter maps them to status codes:
//
//	ErrNotFound        → 404
//	ErrUnauthorized    → 401
//	ErrForbidden       → 403
//	ErrPaymentRequired → 402
//	ErrValidation      → 400
//	ErrConflict        → 409
//	ErrSelfHosted      → 501
//	ErrNotImplemented  → 501
//	ErrAIUnavailable   → 503
//	ErrAIOutput        → 502
//	ErrRateLimited     → 429
//	ErrAlreadySubscribed  → 409
//	ErrNoBillingProfile   → 400
//	ErrBillingUnavailable → 502
//	ErrUpstream           → 502
var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden marks a caller who is authenticated and known (e.g. a team
	// member) but lacks the required role/permission. Mapped to 403. Non-members
	// keep getting ErrNotFound so resource existence is never leaked.
	ErrForbidden       = errors.New("forbidden")
	ErrPaymentRequired = errors.New("payment required")
	ErrValidation      = errors.New("validation failed")
	ErrConflict        = errors.New("conflict")
	// ErrUpstream marks a failure of a service the PLATFORM depends on with
	// the platform's own credentials (AI gateway, the Better Auth JWKS
	// endpoint) — never a per-user provider grant, which keeps
	// ErrUnauthorized so services can drive a token refresh. The HTTP adapter
	// maps it to 502 Bad Gateway; the provider is named only in server logs.
	// (The Paddle billing adapter keeps its own ErrBillingUnavailable.)
	ErrUpstream = errors.New("upstream unavailable")
	// ErrUnprocessable marks input that is syntactically valid but cannot be
	// acted on (e.g. a well-formed subscription URL whose feed cannot be
	// fetched or parsed). The HTTP adapter maps it to 422 Unprocessable
	// Entity, distinct from ErrValidation's 400.
	ErrUnprocessable = errors.New("unprocessable")
	// ErrSelfHosted marks an operation that is unavailable on self-hosted
	// instances (the billing endpoints). The HTTP adapter maps it to
	// 501 Not Implemented.
	ErrSelfHosted = errors.New("self-hosted")
	// ErrNotImplemented marks a method that is a temporary stub during M2.5
	// buildout (the real implementation lands in a later task). Stubs must
	// return this sentinel rather than a bare error string, so callers can
	// errors.Is against it and it must never survive to a shipped,
	// feature-complete endpoint. The HTTP adapter maps it to 501 Not
	// Implemented (error code "not_implemented", distinct from
	// ErrSelfHosted's "self_hosted").
	ErrNotImplemented = errors.New("not implemented")
	// ErrAIUnavailable marks that AI is not configured on this deployment
	// (no API key). The HTTP adapter maps it to 503 Service Unavailable.
	ErrAIUnavailable = errors.New("ai unavailable")
	// ErrAIOutput marks that the model returned output the caller could not
	// parse. The HTTP adapter maps it to 502 Bad Gateway.
	ErrAIOutput = errors.New("ai output invalid")
	// ErrRateLimited marks that the caller exhausted a usage budget. The HTTP
	// adapter maps it to 429 Too Many Requests.
	ErrRateLimited = errors.New("rate limited")
	// ErrAlreadySubscribed marks a checkout attempt by a user who already has
	// a live provider subscription (active, past_due or paused). The HTTP
	// adapter maps it to 409 "already_subscribed"; clients open the portal.
	ErrAlreadySubscribed = errors.New("already subscribed")
	// ErrNoBillingProfile marks a portal request for a user with no provider
	// customer yet. Mapped to 400 "no_billing_profile".
	ErrNoBillingProfile = errors.New("no billing profile")
	// ErrBillingUnavailable wraps any failure talking to the payments
	// provider. Mapped to 502 "billing_unavailable".
	ErrBillingUnavailable = errors.New("billing unavailable")
)
