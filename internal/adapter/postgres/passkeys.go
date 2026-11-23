package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

type PasskeyRepo struct{ db *pgxpool.Pool }

func NewPasskeyRepo(db *pgxpool.Pool) *PasskeyRepo { return &PasskeyRepo{db} }

const passkeyCols = `id, user_id, credential_id, public_key, aaguid, sign_count, transports,
                     backup_eligible, backed_up, name, created_at, last_used_at`

func scanPasskey(row pgx.Row) (account.Passkey, error) {
	var p account.Passkey
	var count int64
	err := row.Scan(&p.ID, &p.UserID, &p.CredentialID, &p.PublicKey, &p.AAGUID, &count,
		&p.Transports, &p.BackupEligible, &p.BackedUp, &p.Name, &p.CreatedAt, &p.LastUsedAt)
	p.SignCount = uint32(count)
	return p, pg.MapError(err)
}

func (r *PasskeyRepo) Create(ctx context.Context, p account.Passkey) (account.Passkey, error) {
	return scanPasskey(r.db.QueryRow(ctx, `
		INSERT INTO webauthn_credentials
			(user_id, credential_id, public_key, aaguid, sign_count, transports,
			 backup_eligible, backed_up, name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+passkeyCols,
		p.UserID, p.CredentialID, p.PublicKey, p.AAGUID, int64(p.SignCount), p.Transports,
		p.BackupEligible, p.BackedUp, p.Name))
}

func (r *PasskeyRepo) ByCredentialID(ctx context.Context, credentialID []byte) (account.Passkey, error) {
	return scanPasskey(r.db.QueryRow(ctx,
		`SELECT `+passkeyCols+` FROM webauthn_credentials WHERE credential_id = $1`, credentialID))
}

func (r *PasskeyRepo) ListForUser(ctx context.Context, u kernel.UserID) ([]account.Passkey, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+passkeyCols+` FROM webauthn_credentials WHERE user_id = $1 ORDER BY created_at`, u)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []account.Passkey
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, pg.MapError(rows.Err())
}

// Touch обновляет счётчик подписей и время использования. Счётчик —
// защита от клона ключа: подпись со счётчиком не больше прежнего значит,
// что ключ скопировали.
func (r *PasskeyRepo) Touch(ctx context.Context, id int64, signCount uint32, at time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE webauthn_credentials SET sign_count = $2, last_used_at = $3 WHERE id = $1`,
		id, int64(signCount), at)
	return pg.MapError(err)
}

func (r *PasskeyRepo) Delete(ctx context.Context, id int64, u kernel.UserID) error {
	ct, err := r.db.Exec(ctx,
		`DELETE FROM webauthn_credentials WHERE id = $1 AND user_id = $2`, id, u)
	if err != nil {
		return pg.MapError(err)
	}
	if ct.RowsAffected() == 0 {
		return kernel.ErrNotFound
	}
	return nil
}
