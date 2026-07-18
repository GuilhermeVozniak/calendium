package postgres

import (
	"context"
	"time"

	"calendium/backend/internal/domain"
)

// --- port.VoiceProfileRepo ------------------------------------------------------

func (r voiceProfileRepo) Get(ctx context.Context, userID string) (domain.VoiceProfile, error) {
	var p domain.VoiceProfile
	row := r.q(ctx).QueryRowContext(ctx, `
		SELECT user_id, profile, sample_count, model, updated_at
		FROM voice_profiles WHERE user_id = $1`, userID)
	if err := row.Scan(&p.UserID, &p.Profile, &p.SampleCount, &p.Model, &p.UpdatedAt); err != nil {
		return domain.VoiceProfile{}, notFound(err)
	}
	return p, nil
}

func (r voiceProfileRepo) Upsert(ctx context.Context, p domain.VoiceProfile) error {
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now().UTC()
	}
	_, err := r.q(ctx).ExecContext(ctx, `
		INSERT INTO voice_profiles (user_id, profile, sample_count, model, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO UPDATE SET
			profile      = EXCLUDED.profile,
			sample_count = EXCLUDED.sample_count,
			model        = EXCLUDED.model,
			updated_at   = EXCLUDED.updated_at`,
		p.UserID, p.Profile, p.SampleCount, p.Model, p.UpdatedAt)
	return err
}
