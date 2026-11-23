// Package sqlite — второе хранилище платформы: один файл на устройстве,
// один пользователь, без серверов (ADR-0015). Реализует те же порты app-слоя,
// что и adapter/postgres, но только пользовательское подмножество: ленивый
// каталог и состояние владельца. Обходчик, админка и телеметрия здесь
// не существуют.
//
// Драйвер — modernc.org/sqlite: чистый Go без cgo, иначе сборке не
// переехать на телефон.
package sqlite

import (
	"database/sql"
	"database/sql/driver"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // драйвер «sqlite»: чистый Go, без cgo

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

//go:embed schema.sql
var schemaSQL string

// DB — соединение и общие помощники. Репозитории держат *DB так же,
// как postgres-репозитории держат *pgxpool.Pool.
type DB struct {
	*sql.DB
	log *slog.Logger
}

// Open открывает (и при необходимости создаёт) файл базы, включает WAL и
// накатывает схему. Схема идемпотентна (CREATE IF NOT EXISTS), версия —
// для будущих переездов, пока их нет.
func Open(path string, log *slog.Logger) (*DB, error) {
	if log == nil {
		log = slog.Default()
	}
	dsn := "file:" + path + "?" + url.Values{
		"_pragma": []string{
			"busy_timeout(10000)",
			"journal_mode(WAL)",
			"foreign_keys(1)",
			"synchronous(NORMAL)",
		},
	}.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	// Один писатель в SQLite — пусть читатели ждут друг друга вежливо, а не
	// ловят SQLITE_BUSY посреди открытия тайтла.
	db.SetMaxOpenConns(1)
	d := &DB{DB: db, log: log}
	if err := d.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate() error {
	if _, err := d.Exec(schemaSQL); err != nil {
		return fmt.Errorf("sqlite схема: %w", err)
	}
	// search_queries.hits появилась позже самой таблицы: у уже созданных баз её
	// дописываем. На новой базе колонка есть из схемы — «duplicate column» не беда.
	if _, err := d.Exec(`ALTER TABLE search_queries ADD COLUMN hits INTEGER NOT NULL DEFAULT 1`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("sqlite схема: %w", err)
	}
	var v int
	if err := d.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v < 1 {
		if _, err := d.Exec("PRAGMA user_version = 1"); err != nil {
			return err
		}
	}
	d.log.Info("sqlite готова", "user_version", v)
	return nil
}

// MapError — те же смыслы, что pg.MapError: не нашли — NotFound, конфликт —
// Conflict. Отличать «нет» от «конфликта» обязан слой app, поэтому здесь.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return kernel.ErrNotFound
	}
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: %v", kernel.ErrConflict, err)
	}
	return err
}

func isUniqueViolation(err error) bool {
	if errors.Is(err, driver.ErrBadConn) {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// ---- время и массивы ----
//
// Время в базе — миллисекунды от эпохи, NULL — «не сказано». Массивы —
// JSON-текст: единая форма для genres/countries и selection.

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func msPtr(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func timeOf(n sql.NullInt64) time.Time {
	if !n.Valid || n.Int64 == 0 {
		return time.Time{}
	}
	return time.UnixMilli(n.Int64)
}

func timePtrOf(n sql.NullInt64) *time.Time {
	if !n.Valid || n.Int64 == 0 {
		return nil
	}
	t := time.UnixMilli(n.Int64)
	return &t
}

func nowMs() int64 { return time.Now().UnixMilli() }

func putStrings(v []string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func scanStrings(s sql.NullString) []string {
	if !s.Valid || s.String == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s.String), &out)
	return out
}

func putStringsPtr(v []string) any {
	if len(v) == 0 {
		return nil
	}
	return putStrings(v)
}
