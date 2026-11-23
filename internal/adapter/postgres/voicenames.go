package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/app/voicenames"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// VoiceNamesRepo — написания озвучек и псевдонимы студий (app/voicenames).
type VoiceNamesRepo struct{ db *pgxpool.Pool }

func NewVoiceNamesRepo(db *pgxpool.Pool) *VoiceNamesRepo { return &VoiceNamesRepo{db} }

var _ voicenames.Store = (*VoiceNamesRepo)(nil)

func (r *VoiceNamesRepo) Aliases(ctx context.Context) ([]voicenames.Alias, error) {
	rows, err := r.db.Query(ctx, `
		SELECT from_key, to_key, origin, note, created_at FROM voice_aliases ORDER BY from_key`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out []voicenames.Alias
	for rows.Next() {
		var a voicenames.Alias
		if err := rows.Scan(&a.From, &a.To, &a.Origin, &a.Note, &a.CreatedAt); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, a)
	}
	return out, pg.MapError(rows.Err())
}

// PutAlias ставит псевдоним одной транзакцией: обратный (to → from), если
// был, снимается — иначе вышла бы петля; кто указывал на from,
// переуказывается на to (цепочек не бывает); пара перестаёт числиться
// разной.
func (r *VoiceNamesRepo) PutAlias(ctx context.Context, a voicenames.Alias) error {
	return pg.MapError(pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM voice_aliases WHERE from_key = $2 AND to_key = $1`, a.From, a.To); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE voice_aliases SET to_key = $2 WHERE to_key = $1`, a.From, a.To); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO voice_aliases (from_key, to_key, origin, note) VALUES ($1, $2, $3, $4)
			ON CONFLICT (from_key) DO UPDATE
			SET to_key = EXCLUDED.to_key, origin = EXCLUDED.origin, note = EXCLUDED.note, created_at = now()`,
			a.From, a.To, a.Origin, a.Note); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			DELETE FROM voice_alias_rejects WHERE a = LEAST($1::text, $2::text) AND b = GREATEST($1::text, $2::text)`,
			a.From, a.To)
		return err
	}))
}

func (r *VoiceNamesRepo) DeleteAlias(ctx context.Context, from string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM voice_aliases WHERE from_key = $1`, from)
	return pg.MapError(err)
}

func (r *VoiceNamesRepo) Rejected(ctx context.Context) ([][2]string, error) {
	rows, err := r.db.Query(ctx, `SELECT a, b FROM voice_alias_rejects`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var p [2]string
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, p)
	}
	return out, pg.MapError(rows.Err())
}

func (r *VoiceNamesRepo) Reject(ctx context.Context, a, b string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO voice_alias_rejects (a, b) VALUES (LEAST($1::text, $2::text), GREATEST($1::text, $2::text))
		ON CONFLICT DO NOTHING`, a, b)
	return pg.MapError(err)
}

// RecordSpellings дописывает пачку: счётчик растёт, пример серии —
// последний.
func (r *VoiceNamesRepo) RecordSpellings(ctx context.Context, sp []voicenames.Spelling) error {
	if len(sp) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, s := range sp {
		var ep *int64
		if s.Episode > 0 {
			v := int64(s.Episode)
			ep = &v
		}
		b.Queue(`
			INSERT INTO voice_spellings (key, title, provider_id, episode_id, hits, first_seen, last_seen)
			VALUES ($1, $2, $3, $4, $5, now(), now())
			ON CONFLICT (key, title, provider_id) DO UPDATE
			SET hits = voice_spellings.hits + EXCLUDED.hits,
			    episode_id = COALESCE(EXCLUDED.episode_id, voice_spellings.episode_id),
			    last_seen = now()`,
			s.Key, s.Title, s.Provider, ep, max(s.Hits, 1))
	}
	return pg.MapError(r.db.SendBatch(ctx, b).Close())
}

// Spellings — все написания с тайтлом примера (для ссылки в админке).
func (r *VoiceNamesRepo) Spellings(ctx context.Context) ([]voicenames.Spelling, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.key, s.title, s.provider_id, COALESCE(s.episode_id, 0), COALESCE(e.title_id, 0), s.hits, s.last_seen
		FROM voice_spellings s
		LEFT JOIN canonical_episodes e ON e.id = s.episode_id
		ORDER BY s.key, s.provider_id, s.title`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out []voicenames.Spelling
	for rows.Next() {
		var s voicenames.Spelling
		if err := rows.Scan(&s.Key, &s.Title, &s.Provider, &s.Episode, &s.TitleID, &s.Hits, &s.LastSeen); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, s)
	}
	return out, pg.MapError(rows.Err())
}
