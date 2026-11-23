package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Репозитории входа — подмножество для движка: один пользователь и его
// сессия. Пароли, инвайты и passkeys существуют в портах (их требует
// конструктор auth.Service), но в движке не применяются: сессия выдаётся
// без логина (ADR-0015). Недоступные пути честно отвечают ошибкой, а не
// притворяются работающими.

type UserRepo struct{ db *DB }

func NewUserRepo(db *DB) *UserRepo { return &UserRepo{db} }

func scanUser(sc func(...any) error) (account.User, error) {
	var u account.User
	var totp sql.NullString
	var created, lastLogin sql.NullInt64
	if err := sc(&u.ID, &u.Login, &u.DisplayName, &u.Role, &u.Disabled, &totp, &created, &lastLogin); err != nil {
		return account.User{}, MapError(err)
	}
	u.TOTPEnabled = totp.Valid && totp.String != ""
	u.CreatedAt = timeOf(created)
	u.LastLoginAt = timePtrOf(lastLogin)
	return u, nil
}

const userCols = `id, login, display_name, role, disabled, totp_secret, created_at, last_login_at`

func (r *UserRepo) ByID(ctx context.Context, id kernel.UserID) (account.User, error) {
	return scanUser(func(dst ...any) error {
		return r.db.QueryRowContext(ctx,
			`SELECT `+userCols+` FROM users WHERE id = ?`, id).Scan(dst...)
	})
}

func (r *UserRepo) ByLogin(ctx context.Context, login string) (account.User, error) {
	return scanUser(func(dst ...any) error {
		return r.db.QueryRowContext(ctx,
			`SELECT `+userCols+` FROM users WHERE login = ?`, login).Scan(dst...)
	})
}

func (r *UserRepo) List(ctx context.Context) ([]account.User, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	var out []account.User
	for rows.Next() {
		u, err := scanUser(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, MapError(rows.Err())
}

func (r *UserRepo) Create(ctx context.Context, u account.User, c account.Credential) (account.User, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO users (login, display_name, role, disabled, created_at) VALUES (?,?,?,?,?)`,
		u.Login, u.DisplayName, u.Role, boolInt(u.Disabled), ms(u.CreatedAt))
	if err != nil {
		return account.User{}, MapError(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return account.User{}, MapError(err)
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO credentials (user_id, hash) VALUES (?,?)`, id, c.Hash); err != nil {
		return account.User{}, MapError(err)
	}
	return r.ByID(ctx, kernel.UserID(id))
}

func (r *UserRepo) SetDisabled(ctx context.Context, id kernel.UserID, disabled bool) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET disabled = ? WHERE id = ?`, boolInt(disabled), id)
	return MapError(err)
}

func (r *UserRepo) SetRole(ctx context.Context, id kernel.UserID, role account.Role) error {
	_, err := r.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, id)
	return MapError(err)
}

func (r *UserRepo) TouchLogin(ctx context.Context, id kernel.UserID, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE id = ?`, ms(at), id)
	return MapError(err)
}

type CredentialRepo struct{ db *DB }

func NewCredentialRepo(db *DB) *CredentialRepo { return &CredentialRepo{db} }

func (r *CredentialRepo) ByUser(ctx context.Context, id kernel.UserID) (account.Credential, error) {
	var c account.Credential
	err := r.db.QueryRowContext(ctx,
		`SELECT hash FROM credentials WHERE user_id = ?`, id).Scan(&c.Hash)
	return c, MapError(err)
}

func (r *CredentialRepo) SetPassword(ctx context.Context, id kernel.UserID, hash string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE credentials SET hash = ? WHERE user_id = ?`, hash, id)
	return MapError(err)
}

func (r *CredentialRepo) TOTPSecret(ctx context.Context, id kernel.UserID) (string, error) {
	var s sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT totp_secret FROM users WHERE id = ?`, id).Scan(&s)
	if err != nil {
		return "", MapError(err)
	}
	return s.String, nil
}

func (r *CredentialRepo) SetTOTPSecret(ctx context.Context, id kernel.UserID, secret string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET totp_secret = ? WHERE id = ?`, secret, id)
	return MapError(err)
}

func (r *CredentialRepo) ClearTOTPSecret(ctx context.Context, id kernel.UserID) error {
	_, err := r.db.ExecContext(ctx, `UPDATE users SET totp_secret = NULL WHERE id = ?`, id)
	return MapError(err)
}

type InviteRepo struct{ db *DB }

func NewInviteRepo(db *DB) *InviteRepo { return &InviteRepo{db} }

func (r *InviteRepo) ByCode(ctx context.Context, code string) (account.Invite, error) {
	return account.Invite{}, kernel.ErrNotFound
}

func (r *InviteRepo) Create(ctx context.Context, i account.Invite) error {
	return kernel.ErrUnavailable
}

func (r *InviteRepo) MarkUsed(ctx context.Context, code string, by kernel.UserID, at time.Time) error {
	return kernel.ErrUnavailable
}

func (r *InviteRepo) ListOpen(ctx context.Context) ([]account.Invite, error) {
	return nil, nil
}

func (r *InviteRepo) ListRecent(ctx context.Context, limit int) ([]account.Invite, error) {
	return nil, nil
}

func (r *InviteRepo) Revoke(ctx context.Context, code string, at time.Time) error {
	return kernel.ErrUnavailable
}

type SessionRepo struct{ db *DB }

func NewSessionRepo(db *DB) *SessionRepo { return &SessionRepo{db} }

func scanSession(sc func(...any) error) (account.Session, error) {
	var s account.Session
	var device, refreshHash sql.NullString
	var issued, expires, revoked, factor, refreshExp sql.NullInt64
	if err := sc(&s.ID, &s.UserID, &s.Space, &device, &issued, &expires, &revoked, &factor,
		&refreshHash, &refreshExp); err != nil {
		return account.Session{}, MapError(err)
	}
	s.DeviceKind = device.String
	s.IssuedAt = timeOf(issued)
	s.ExpiresAt = timeOf(expires)
	s.RevokedAt = timePtrOf(revoked)
	s.FactorAt = timePtrOf(factor)
	s.RefreshExpiresAt = timePtrOf(refreshExp)
	return s, nil
}

const sessionCols = `id, user_id, space, device_kind, issued_at, expires_at, revoked_at,
	factor_at, refresh_hash, refresh_expires_at`

func (r *SessionRepo) Create(ctx context.Context, s account.Session, tokenHash string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions (id, token_hash, user_id, space, device_kind, issued_at, expires_at, revoked_at, factor_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		s.ID, tokenHash, s.UserID, s.Space, nullStr(s.DeviceKind),
		ms(s.IssuedAt), ms(s.ExpiresAt), msPtrOf(s.RevokedAt), msPtrOf(s.FactorAt))
	return MapError(err)
}

func (r *SessionRepo) CreateRenewable(ctx context.Context, s account.Session, tokenHash, refreshHash string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions (id, token_hash, user_id, space, device_kind, issued_at, expires_at, revoked_at, factor_at, refresh_hash, refresh_expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		s.ID, tokenHash, s.UserID, s.Space, nullStr(s.DeviceKind),
		ms(s.IssuedAt), ms(s.ExpiresAt), msPtrOf(s.RevokedAt), msPtrOf(s.FactorAt),
		refreshHash, msPtrOf(s.RefreshExpiresAt))
	return MapError(err)
}

func (r *SessionRepo) ByTokenHash(ctx context.Context, hash string) (account.Session, error) {
	return scanSession(func(dst ...any) error {
		return r.db.QueryRowContext(ctx,
			`SELECT `+sessionCols+` FROM sessions WHERE token_hash = ?`, hash).Scan(dst...)
	})
}

func (r *SessionRepo) ByRefreshHash(ctx context.Context, hash string) (account.Session, error) {
	return scanSession(func(dst ...any) error {
		return r.db.QueryRowContext(ctx,
			`SELECT `+sessionCols+` FROM sessions WHERE refresh_hash = ?`, hash).Scan(dst...)
	})
}

func (r *SessionRepo) Revoke(ctx context.Context, id kernel.SessionID, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE id = ?`, ms(at), id)
	return MapError(err)
}

func (r *SessionRepo) RevokeAllForUser(ctx context.Context, u kernel.UserID, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, ms(at), u)
	return MapError(err)
}

func (r *SessionRepo) ListForUser(ctx context.Context, u kernel.UserID) ([]account.Session, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+sessionCols+` FROM sessions
		WHERE user_id = ? AND revoked_at IS NULL
		ORDER BY issued_at DESC`, u)
	if err != nil {
		return nil, MapError(err)
	}
	defer rows.Close()
	var out []account.Session
	for rows.Next() {
		s, err := scanSession(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, MapError(rows.Err())
}

// PasskeyRepo — на устройстве не используется: порт существует ради
// конструктора auth.Service.
type PasskeyRepo struct{ db *DB }

func NewPasskeyRepo(db *DB) *PasskeyRepo { return &PasskeyRepo{db} }

func (r *PasskeyRepo) Create(ctx context.Context, p account.Passkey) (account.Passkey, error) {
	return account.Passkey{}, kernel.ErrUnavailable
}

func (r *PasskeyRepo) ByCredentialID(ctx context.Context, credentialID []byte) (account.Passkey, error) {
	return account.Passkey{}, kernel.ErrNotFound
}

func (r *PasskeyRepo) ListForUser(ctx context.Context, u kernel.UserID) ([]account.Passkey, error) {
	return nil, nil
}

func (r *PasskeyRepo) Touch(ctx context.Context, id int64, signCount uint32, at time.Time) error {
	return kernel.ErrUnavailable
}

func (r *PasskeyRepo) Delete(ctx context.Context, id int64, u kernel.UserID) error {
	return kernel.ErrUnavailable
}
