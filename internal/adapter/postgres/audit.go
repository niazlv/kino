package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// AuditRepo — журнал служебных действий. Пишется всегда, читается редко,
// но именно он отвечает на вопрос «почему у мамы вчера пропал доступ».
type AuditRepo struct{ db *pgxpool.Pool }

func NewAuditRepo(db *pgxpool.Pool) *AuditRepo { return &AuditRepo{db} }

func (r *AuditRepo) Record(ctx context.Context, actor kernel.UserID,
	action, target, reason string, before, after any) error {
	b, err := json.Marshal(before)
	if err != nil {
		b = []byte("null")
	}
	a, err := json.Marshal(after)
	if err != nil {
		a = []byte("null")
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO admin_audit (actor_id, action, target, before, after, reason)
		VALUES ($1,$2,$3,$4,$5,$6)`, actor, action, target, b, a, reason)
	return pg.MapError(err)
}

type AuditEntry struct {
	ID      int64
	ActorID kernel.UserID
	// ActorLogin — логин на момент чтения. Журнал хранит идентификатор:
	// логин может смениться, а «кто это был» должно оставаться верным.
	ActorLogin string
	Action     string
	Target     string
	Reason     string
	// Before и After — JSON как есть; пустая строка, если нечего показывать.
	Before    string
	After     string
	CreatedAt time.Time
}

// AuditFilter — чем сузить журнал. Пустое поле — не сужать.
type AuditFilter struct {
	Actor  int64  // 0 — все исполнители
	Action string // точное действие; с точкой в конце — префикс («source.»)
}

// Recent листает журнал от свежего к старому. before = 0 — с начала;
// иначе — записи старше указанной. Курсор по id, а не по странице:
// журнал только растёт, и смещение бы «плыло».
func (r *AuditRepo) Recent(ctx context.Context, before int64, limit int) ([]AuditEntry, error) {
	return r.Filtered(ctx, AuditFilter{}, before, limit)
}

// Filtered — тот же журнал с сужением по исполнителю и действию.
//
// Действие с точкой на конце — префикс группы («source.» — все правки
// привязок), без точки — точное совпадение. Это решение знает журнал:
// точки в именах действий — его же конвенция.
func (r *AuditRepo) Filtered(ctx context.Context, f AuditFilter, before int64, limit int) ([]AuditEntry, error) {
	rows, err := r.db.Query(ctx, `
		SELECT a.id, a.actor_id, COALESCE(u.login, '#' || a.actor_id::text),
		       a.action, COALESCE(a.target, ''), COALESCE(a.reason, ''),
		       COALESCE(a.before::text, ''), COALESCE(a.after::text, ''),
		       a.created_at
		FROM admin_audit a
		LEFT JOIN users u ON u.id = a.actor_id
		WHERE ($1 = 0 OR a.id < $1)
		  AND ($2 = 0 OR a.actor_id = $2)
		  AND ($3 = '' OR a.action = $3
		              OR (right($3, 1) = '.' AND a.action LIKE $3 || '%'))
		ORDER BY a.id DESC LIMIT $4`, before, f.Actor, f.Action, limit)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorLogin, &e.Action, &e.Target,
			&e.Reason, &e.Before, &e.After, &e.CreatedAt); err != nil {
			return nil, pg.MapError(err)
		}
		if e.Before == "null" {
			e.Before = ""
		}
		if e.After == "null" {
			e.After = ""
		}
		out = append(out, e)
	}
	return out, pg.MapError(rows.Err())
}

// Actions — встречавшиеся в журнале действия: для выпадающего списка
// фильтра. Действий конечное число (их пишет код), список маленький.
func (r *AuditRepo) Actions(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT DISTINCT action FROM admin_audit ORDER BY action`)
	if err != nil {
		return nil, pg.MapError(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, pg.MapError(err)
		}
		out = append(out, a)
	}
	return out, pg.MapError(rows.Err())
}
