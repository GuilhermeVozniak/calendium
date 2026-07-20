package postgres

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/domain"
)

// rowScanner abstracts *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

// newID returns a random 32-hex-char identifier for adapter-created rows
// (e.g. attachment ids missing from provider payloads).
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("postgres: crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b[:])
}

// notFound maps sql.ErrNoRows to the domain sentinel.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// mustAffect converts a zero-row write into domain.ErrNotFound.
func mustAffect(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func nullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullStrPtr(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func nullTimePtr(p *time.Time) sql.NullTime {
	if p == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *p, Valid: true}
}

func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

func timePtr(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	t := nt.Time
	return &t
}

func nullFloatPtr(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

func floatPtr(nf sql.NullFloat64) *float64 {
	if !nf.Valid {
		return nil
	}
	f := nf.Float64
	return &f
}

// jsonArray marshals v, coercing nil slices to "[]" so jsonb columns and
// API payloads never carry JSON null where an array is expected.
func jsonArray(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("postgres: marshal json: %w", err)
	}
	if string(b) == "null" {
		return "[]", nil
	}
	return string(b), nil
}

// jsonObject marshals v, coercing a nil map to "{}" so jsonb columns never
// carry JSON null where an object is expected.
func jsonObject(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("postgres: marshal json: %w", err)
	}
	if string(b) == "null" {
		return "{}", nil
	}
	return string(b), nil
}

// unmarshalInto decodes jsonb bytes; empty input leaves dst untouched.
func unmarshalInto(data []byte, dst any) error {
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("postgres: unmarshal json: %w", err)
	}
	return nil
}

// --- cursor pagination -------------------------------------------------------

// encodeThreadCursor encodes the keyset position after t.
func encodeThreadCursor(t domain.Thread) string {
	raw := strconv.FormatInt(t.LastMessageAt.UnixNano(), 10) + "|" + t.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeThreadCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: malformed cursor", domain.ErrValidation)
	}
	nanos, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, "", fmt.Errorf("%w: malformed cursor", domain.ErrValidation)
	}
	n, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: malformed cursor", domain.ErrValidation)
	}
	return time.Unix(0, n).UTC(), id, nil
}

// encodeOpensCursor/decodeOpensCursor encode the Recent Opens keyset
// position (opened_at, id) as base64url("unixMicro:id").
func encodeOpensCursor(t time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%s", t.UnixMicro(), id)))
}

func decodeOpensCursor(s string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: bad cursor", domain.ErrValidation)
	}
	micros, id, ok := strings.Cut(string(raw), ":")
	if !ok {
		return time.Time{}, "", fmt.Errorf("%w: bad cursor", domain.ErrValidation)
	}
	n, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: bad cursor", domain.ErrValidation)
	}
	return time.UnixMicro(n).UTC(), id, nil
}
