// Package postgres — реализации портов слоя app поверх PostgreSQL.
// SQL живёт только здесь: ни domain, ни app его не видят (depguard).
package postgres

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

type UserRepo struct{ db *pgxpool.Pool }

func NewUserRepo(db *pgxpool.Pool) *UserRepo { return &UserRepo{db} }

const userCols = `id, login, display_name, role, disabled,
                  (totp_secret IS NOT NULL) AS totp_enabled, created_at, last_login_at`

func scanUser(row pgx.Row) (account.User, error) {
	var u account.User
	err := row.Scan(&u.ID, &u.Login, &u.DisplayName, &u.Role, &u.Disabled,
		&u.TOTPEnabled, &u.CreatedAt, &u.LastLoginAt)
	return u, pg.MapError(err)
}

func (r *UserRepo) ByID(ctx context.Context, id kernel.UserID) (account.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (r *UserRepo) ByLogin(ctx context.Context, login string) (account.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE login = $1`, login))
}

func (r *UserRepo) List(ctx context.Context) ([]account.User, error) {
	rows, err := r.db.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []account.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, pg.MapError(rows.Err())
}

// Create пишет пользователя и его учётные данные ОДНОЙ транзакцией:
// пользователь без пароля не должен существовать даже мгновение.
func (r *UserRepo) Create(ctx context.Context, u account.User, c account.Credential) (account.User, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return account.User{}, pg.MapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	created, err := scanUser(tx.QueryRow(ctx, `
		INSERT INTO users (login, display_name, role, created_at)
		VALUES ($1, $2, $3, $4)
		RETURNING `+userCols,
		u.Login, u.DisplayName, u.Role, u.CreatedAt))
	if err != nil {
		return account.User{}, err
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO credentials (user_id, hash) VALUES ($1, $2)`,
		created.ID, c.Hash); err != nil {
		return account.User{}, pg.MapError(err)
	}
	return created, pg.MapError(tx.Commit(ctx))
}

func (r *UserRepo) SetDisabled(ctx context.Context, id kernel.UserID, disabled bool) error {
	ct, err := r.db.Exec(ctx, `UPDATE users SET disabled = $2 WHERE id = $1`, id, disabled)
	if err != nil {
		return pg.MapError(err)
	}
	if ct.RowsAffected() == 0 {
		return kernel.ErrNotFound
	}
	return nil
}

func (r *UserRepo) SetRole(ctx context.Context, id kernel.UserID, role account.Role) error {
	ct, err := r.db.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, id, role)
	if err != nil {
		return pg.MapError(err)
	}
	if ct.RowsAffected() == 0 {
		return kernel.ErrNotFound
	}
	return nil
}

func (r *UserRepo) TouchLogin(ctx context.Context, id kernel.UserID, at time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET last_login_at = $2 WHERE id = $1`, id, at)
	return pg.MapError(err)
}

// ── Credentials ────────────────────────────────────────────────────────────

type CredentialRepo struct{ db *pgxpool.Pool }

func NewCredentialRepo(db *pgxpool.Pool) *CredentialRepo { return &CredentialRepo{db} }

func (r *CredentialRepo) ByUser(ctx context.Context, id kernel.UserID) (account.Credential, error) {
	var c account.Credential
	err := r.db.QueryRow(ctx,
		`SELECT user_id, hash FROM credentials WHERE user_id = $1`, id).
		Scan(&c.UserID, &c.Hash)
	return c, pg.MapError(err)
}

// ── Invites ────────────────────────────────────────────────────────────────

type InviteRepo struct{ db *pgxpool.Pool }

func NewInviteRepo(db *pgxpool.Pool) *InviteRepo { return &InviteRepo{db} }

func (r *InviteRepo) ByCode(ctx context.Context, code string) (account.Invite, error) {
	var i account.Invite
	err := r.db.QueryRow(ctx,
		`SELECT code, created_by, expires_at, used_by, used_at FROM invites WHERE code = $1`, code).
		Scan(&i.Code, &i.CreatedBy, &i.ExpiresAt, &i.UsedBy, &i.UsedAt)
	return i, pg.MapError(err)
}

func (r *InviteRepo) Create(ctx context.Context, i account.Invite) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO invites (code, created_by, expires_at) VALUES ($1, $2, $3)`,
		i.Code, i.CreatedBy, i.ExpiresAt)
	return pg.MapError(err)
}

// MarkUsed помечает инвайт использованным ТОЛЬКО если он ещё свободен:
// условие в WHERE, а не в коде — иначе два одновременных запроса с одним
// кодом создадут двух пользователей.
func (r *InviteRepo) MarkUsed(ctx context.Context, code string, by kernel.UserID, at time.Time) error {
	ct, err := r.db.Exec(ctx,
		`UPDATE invites SET used_by = $2, used_at = $3
		 WHERE code = $1 AND used_by IS NULL`, code, by, at)
	if err != nil {
		return pg.MapError(err)
	}
	if ct.RowsAffected() == 0 {
		return kernel.ErrConflict
	}
	return nil
}

// ListOpen — только пригодные. Истёкший инвайт для админа не «открытый»,
// а мусор: показывать его рядом с живыми значит путать, какой отдавать.
func (r *InviteRepo) ListOpen(ctx context.Context) ([]account.Invite, error) {
	return r.list(ctx, `WHERE used_by IS NULL AND expires_at > now() ORDER BY expires_at`)
}

// Revoke обрезает срок до «сейчас», а не удаляет строку: кто и когда
// выпускал инвайт, остаётся в истории.
func (r *InviteRepo) Revoke(ctx context.Context, code string, at time.Time) error {
	ct, err := r.db.Exec(ctx,
		`UPDATE invites SET expires_at = $2
		 WHERE code = $1 AND used_by IS NULL AND expires_at > $2`, code, at)
	if err != nil {
		return pg.MapError(err)
	}
	if ct.RowsAffected() == 0 {
		return kernel.ErrNotFound
	}
	return nil
}

func (r *InviteRepo) ListRecent(ctx context.Context, limit int) ([]account.Invite, error) {
	return r.list(ctx, `WHERE used_by IS NOT NULL OR expires_at <= now()
		ORDER BY COALESCE(used_at, expires_at) DESC LIMIT `+strconv.Itoa(limit))
}

func (r *InviteRepo) list(ctx context.Context, tail string) ([]account.Invite, error) {
	rows, err := r.db.Query(ctx,
		`SELECT code, created_by, expires_at, used_by, used_at FROM invites `+tail)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []account.Invite
	for rows.Next() {
		var i account.Invite
		if err := rows.Scan(&i.Code, &i.CreatedBy, &i.ExpiresAt, &i.UsedBy, &i.UsedAt); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, i)
	}
	return out, pg.MapError(rows.Err())
}

// ── Sessions ───────────────────────────────────────────────────────────────

type SessionRepo struct{ db *pgxpool.Pool }

func NewSessionRepo(db *pgxpool.Pool) *SessionRepo { return &SessionRepo{db} }

func (r *SessionRepo) Create(ctx context.Context, s account.Session, tokenHash string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO sessions (id, token_hash, user_id, space, device_kind, issued_at, expires_at, factor_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		s.ID, tokenHash, s.UserID, s.Space, s.DeviceKind, s.IssuedAt, s.ExpiresAt, s.FactorAt)
	return pg.MapError(err)
}

// CreateRenewable — сессия устройства: та же строка, но с хешем refresh-
// токена и его сроком.
func (r *SessionRepo) CreateRenewable(ctx context.Context, s account.Session, tokenHash, refreshHash string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO sessions (id, token_hash, user_id, space, device_kind, issued_at, expires_at, factor_at,
		                      refresh_hash, refresh_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		s.ID, tokenHash, s.UserID, s.Space, s.DeviceKind, s.IssuedAt, s.ExpiresAt, s.FactorAt,
		refreshHash, s.RefreshExpiresAt)
	return pg.MapError(err)
}

const sessionColumns = `id, user_id, space, device_kind, issued_at, expires_at, revoked_at, factor_at, refresh_expires_at`

func scanSession(row interface{ Scan(dest ...any) error }) (account.Session, error) {
	var s account.Session
	err := row.Scan(&s.ID, &s.UserID, &s.Space, &s.DeviceKind, &s.IssuedAt, &s.ExpiresAt,
		&s.RevokedAt, &s.FactorAt, &s.RefreshExpiresAt)
	return s, pg.MapError(err)
}

func (r *SessionRepo) ByTokenHash(ctx context.Context, hash string) (account.Session, error) {
	return scanSession(r.db.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE token_hash = $1`, hash))
}

func (r *SessionRepo) ByRefreshHash(ctx context.Context, hash string) (account.Session, error) {
	return scanSession(r.db.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE refresh_hash = $1`, hash))
}

func (r *SessionRepo) Revoke(ctx context.Context, id kernel.SessionID, at time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`, id, at)
	return pg.MapError(err)
}

func (r *SessionRepo) RevokeAllForUser(ctx context.Context, u kernel.UserID, at time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, u, at)
	return pg.MapError(err)
}

func (r *SessionRepo) ListForUser(ctx context.Context, u kernel.UserID) ([]account.Session, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+sessionColumns+`
		FROM sessions WHERE user_id = $1 AND revoked_at IS NULL ORDER BY issued_at DESC`, u)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []account.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, pg.MapError(rows.Err())
}

// ActiveCounts — сколько живых сессий у каждого пользователя. Для админской
// таблицы: одним запросом, а не по одному на строку.
func (r *SessionRepo) ActiveCounts(ctx context.Context) (map[kernel.UserID]int, error) {
	rows, err := r.db.Query(ctx, `
		SELECT user_id, count(*)::int FROM sessions
		WHERE revoked_at IS NULL AND expires_at > now()
		GROUP BY user_id`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	out := map[kernel.UserID]int{}
	for rows.Next() {
		var id kernel.UserID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, pg.MapError(err)
		}
		out[id] = n
	}
	return out, pg.MapError(rows.Err())
}

// TOTPSecret и SetTOTPSecret держатся в CredentialRepo, а не в UserRepo:
// секрет второго фактора не должен подниматься обычным чтением пользователя.
func (r *CredentialRepo) TOTPSecret(ctx context.Context, id kernel.UserID) (string, error) {
	var secret *string
	err := r.db.QueryRow(ctx, `SELECT totp_secret FROM users WHERE id = $1`, id).Scan(&secret)
	if err != nil {
		return "", pg.MapError(err)
	}
	if secret == nil {
		return "", nil
	}
	return *secret, nil
}

func (r *CredentialRepo) SetPassword(ctx context.Context, id kernel.UserID, hash string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE credentials SET hash = $2 WHERE user_id = $1`, id, hash)
	return pg.MapError(err)
}

func (r *CredentialRepo) SetTOTPSecret(ctx context.Context, id kernel.UserID, secret string) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET totp_secret = $2 WHERE id = $1`, id, secret)
	return pg.MapError(err)
}

func (r *CredentialRepo) ClearTOTPSecret(ctx context.Context, id kernel.UserID) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET totp_secret = NULL WHERE id = $1`, id)
	return pg.MapError(err)
}
