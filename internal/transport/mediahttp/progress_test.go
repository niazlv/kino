package mediahttp

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

// Прогресс внешнего плеера — восстановление по фактам отдачи сегментов.
// Проверяется именно контракт «до какого сегмента отдали, до того и досмотрено»
// и экономность доклада.
func TestProgressTracker(t *testing.T) {
	tr := newProgressTracker()
	id := kernel.TicketID("t1")

	const playlist = `#EXTM3U
#EXT-X-TARGETDURATION:6
#EXTINF:10.0,
seg0.ts
#EXTINF:10.0,
seg1.ts
#EXTINF:10.0,
seg2.ts
#EXTINF:10.0,
seg3.ts
#EXT-X-ENDLIST
`
	base, err := url.Parse("https://cdn.example.com/v/idx.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(30 * time.Minute)
	tr.Playlist(id, base.String(), playlist, base, expiry, nil)

	// Мастер-плейлист (без EXTINF) карту не трогает.
	tr.Playlist(id, "https://cdn.example.com/master.m3u8",
		"#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv/idx.m3u8\n", base, expiry, nil)

	hitURL := func(seg string) string {
		u, err := base.Parse(seg)
		if err != nil {
			t.Fatal(err)
		}
		return u.String()
	}

	if _, _, ok := tr.Hit(id, "https://other.example/x.ts"); ok {
		t.Fatal("чужой адрес не должен считаться сегментом")
	}

	// Первые 30 секунд не докладываются: порог шага не набран.
	if _, _, ok := tr.Hit(id, hitURL("seg0.ts")); ok {
		t.Fatal("первый сегмент не должен вызывать доклад")
	}
	if _, _, ok := tr.Hit(id, hitURL("seg1.ts")); ok {
		t.Fatal("10 секунд просмотра не должны вызывать доклад")
	}
	if _, _, ok := tr.Hit(id, hitURL("seg2.ts")); ok {
		t.Fatal("20 секунд просмотра не должны вызывать доклад")
	}
	// Последний сегмент докладывается всегда — это граница «досмотрено».
	pos, total, ok := tr.Hit(id, hitURL("seg3.ts"))
	if !ok || pos != 30_000 || total != 40_000 {
		t.Fatalf("pos=%d total=%d ok=%v, want 30000/40000/true", pos, total, ok)
	}

	// Перемотка назад не возвращает позицию: maxIdx только растёт.
	if _, _, ok := tr.Hit(id, hitURL("seg0.ts")); ok {
		t.Fatal("перемотка назад не должна докладывать")
	}
}

// Обновление плейлиста с новыми подписанными адресами (после refresh тикета)
// заменяет карту того же адреса: старые сегменты умирают вместе с подписью.
func TestProgressTracker_PlaylistRefresh(t *testing.T) {
	tr := newProgressTracker()
	id := kernel.TicketID("t2")
	expiry := time.Now().Add(30 * time.Minute)

	playlistURL := "https://cdn.example.com/v/idx.m3u8"
	base, _ := url.Parse(playlistURL)
	body := func(host string) string {
		return "#EXTM3U\n#EXTINF:60.0,\nhttps://" + host + "/s0.ts\n#EXT-X-ENDLIST\n"
	}
	tr.Playlist(id, playlistURL, body("cdn.example.com"), base, expiry, nil)
	// Тот же адрес плейлиста, новые подписи у сегментов.
	tr.Playlist(id, playlistURL, body("node2.cdn.example.com"), base, expiry, nil)

	pos, total, ok := tr.Hit(id, "https://node2.cdn.example.com/s0.ts")
	if !ok || pos != 0 || total != 60_000 {
		t.Fatalf("pos=%d total=%d ok=%v, want 0/60000/true", pos, total, ok)
	}
	// Адрес из старой подписи больше не считается сегментом этого плейлиста.
	if _, _, ok := tr.Hit(id, "https://cdn.example.com/s0.ts"); ok {
		t.Fatal("адрес из старой подписи не должен срабатывать")
	}
}

type fakeObserver struct {
	tickets  []kernel.TicketID
	position []int64
	duration []int64
	err      error
}

func (f *fakeObserver) Segments(_ context.Context, t playback.Ticket, pos, dur int64) error {
	f.tickets = append(f.tickets, t.ID)
	f.position = append(f.position, pos)
	f.duration = append(f.duration, dur)
	return f.err
}

func TestParseMediaSegments_NeedsEXTINF(t *testing.T) {
	base, _ := url.Parse("https://cdn.example.com/a/b.m3u8")
	spans, urls := parseMediaSegments("#EXTM3U\n#EXT-X-VERSION:3\n", base)
	if len(spans) != 0 || len(urls) != 0 {
		t.Fatalf("мастер-плейлист дал %d сегментов", len(spans))
	}
}
