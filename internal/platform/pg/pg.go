// Package pg — пул соединений и общие мелочи работы с Postgres.
// Инфраструктура: доменные и app-пакеты сюда не смотрят.
package pg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 16
	cfg.MaxConnLifetime = time.Hour
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}

// MapError переводит ошибки драйвера в доменную таксономию — благодаря
// этому app и transport не знают про коды PostgreSQL.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgxNoRows) {
		return kernel.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%s: %w", pgErr.ConstraintName, kernel.ErrConflict)
		case "23503": // foreign_key_violation
			return fmt.Errorf("%s: %w", pgErr.ConstraintName, kernel.ErrInvalidInput)
		case "23514": // check_violation
			return fmt.Errorf("%s: %w", pgErr.ConstraintName, kernel.ErrInvalidInput)
		}
	}
	return err
}
