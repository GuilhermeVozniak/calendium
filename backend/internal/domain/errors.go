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
//	ErrPaymentRequired → 402
//	ErrValidation      → 400
//	ErrConflict        → 409
//	ErrSelfHosted      → 501
//	ErrAIUnavailable   → 503
//	ErrAIOutput        → 502
//	ErrRateLimited     → 429
var (
	ErrNotFound        = errors.New("not found")
	ErrUnauthorized    = errors.New("unauthorized")
	ErrPaymentRequired = errors.New("payment required")
	ErrValidation      = errors.New("validation failed")
	ErrConflict        = errors.New("conflict")
	// ErrSelfHosted marks an operation that is unavailable on self-hosted
	// instances (the Stripe billing endpoints). The HTTP adapter maps it to
	// 501 Not Implemented.
	ErrSelfHosted = errors.New("self-hosted")
	// ErrAIUnavailable marks that AI is not configured on this deployment
	// (no API key). The HTTP adapter maps it to 503 Service Unavailable.
	ErrAIUnavailable = errors.New("ai unavailable")
	// ErrAIOutput marks that the model returned output the caller could not
	// parse. The HTTP adapter maps it to 502 Bad Gateway.
	ErrAIOutput = errors.New("ai output invalid")
	// ErrRateLimited marks that the caller exhausted a usage budget. The HTTP
	// adapter maps it to 429 Too Many Requests.
	ErrRateLimited = errors.New("rate limited")
)
