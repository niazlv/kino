package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	migrations "github.com/niazleushkin/kino/db/migrations"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

// Применение миграций. Отдельная команда, а не автозапуск при старте core:
// схему меняет явное действие, а не побочный эффект деплоя.
func main() {
	cmd := flag.String("cmd", "up", "up | down | status | version")
	flag.Parse()

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "POSTGRES_DSN не задан")
		os.Exit(2)
	}

	ctx := context.Background()
	pool, err := pg.NewPool(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		fmt.Fprintf(os.Stderr, "goose: %v\n", err)
		os.Exit(1)
	}

	// Пропущенные номера применяются, а не роняют выкатку: ветви живут
	// параллельно, и миграция из влитой позже ветви может оказаться
	// младше уже применённой. Миграции здесь независимы друг от друга —
	// у каждой своя таблица или свои колонки.
	if err := goose.RunWithOptionsContext(ctx, *cmd, db, ".", nil, goose.WithAllowMissing()); err != nil {
		fmt.Fprintf(os.Stderr, "миграции (%s): %v\n", *cmd, err)
		os.Exit(1)
	}
}
