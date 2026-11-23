package postgres

// Псевдонимы и написания озвучек против настоящей базы (KINO_PG_DSN, см.
// catalog_match_db_test.go). Ключи — свои, с приставкой zzt, и за собой
// убираются.

import (
	"context"
	"os"
	"testing"

	"github.com/niazleushkin/kino/internal/app/voicenames"
	"github.com/niazleushkin/kino/internal/platform/pg"
)

func TestVoiceNamesRepo(t *testing.T) {
	dsn := os.Getenv("KINO_PG_DSN")
	if dsn == "" {
		t.Skip("нужна база стенда: KINO_PG_DSN")
	}
	ctx := context.Background()
	pool, err := pg.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	clean := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM voice_aliases WHERE from_key LIKE 'zzt%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM voice_alias_rejects WHERE a LIKE 'zzt%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM voice_spellings WHERE key LIKE 'zzt%'`)
	}
	clean()
	t.Cleanup(clean)
	r := NewVoiceNamesRepo(pool)

	// Написания: счётчик растёт, повтор не плодит строк.
	sp := []voicenames.Spelling{{Key: "zzta", Title: "Zzt A", Provider: "kinopub", Hits: 2},
		{Key: "zztb", Title: "Zzt B", Provider: "lordfilm", Hits: 1, Episode: 1}}
	for i := 0; i < 2; i++ {
		if err := r.RecordSpellings(ctx, sp); err != nil {
			t.Fatal(err)
		}
	}
	all, err := r.Spellings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hits := map[string]int{}
	for _, s := range all {
		hits[s.Key] += s.Hits
	}
	if hits["zzta"] != 4 || hits["zztb"] != 2 {
		t.Fatalf("счётчики: %v", hits)
	}

	// Цепочка разворачивается, петля снимается, «разные» забываются.
	if err := r.Reject(ctx, "zztc", "zztb"); err != nil {
		t.Fatal(err)
	}
	if err := r.PutAlias(ctx, voicenames.Alias{From: "zztc", To: "zztb", Origin: "auto"}); err != nil {
		t.Fatal(err)
	}
	if err := r.PutAlias(ctx, voicenames.Alias{From: "zztb", To: "zzta", Origin: "admin", Note: "проверка"}); err != nil {
		t.Fatal(err)
	}
	as, err := r.Aliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	to := map[string]string{}
	for _, a := range as {
		to[a.From] = a.To
	}
	if to["zztc"] != "zzta" || to["zztb"] != "zzta" {
		t.Fatalf("цепочка не развёрнута: %v", to)
	}
	rej, err := r.Rejected(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range rej {
		if p == [2]string{"zztb", "zztc"} {
			t.Fatal("склеенная пара осталась «разной»")
		}
	}
	if err := r.PutAlias(ctx, voicenames.Alias{From: "zzta", To: "zztc", Origin: "admin"}); err != nil {
		t.Fatal(err)
	}
	as, _ = r.Aliases(ctx)
	for _, a := range as {
		if a.From == a.To {
			t.Fatalf("петля: %+v", a)
		}
	}
	if err := r.DeleteAlias(ctx, "zztb"); err != nil {
		t.Fatal(err)
	}
}
