package playback

import (
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Формы хостов сняты с живого kinopub: мастер на api.service-kp.com,
// сегменты на <uuid>.ams-static-NN.cdntogo.net с меняющимся номером сервера.
func TestOriginAllowed_MatchesRealCDNShapes(t *testing.T) {
	u := Upstream{AllowedOrigins: []string{"api.service-kp.com", "cdntogo.net"}}

	allowed := []string{
		"api.service-kp.com",
		"86feaca6-c052-436f-8b78-99cea0ec1571.ams-static-01.cdntogo.net",
		"86feaca6-c052-436f-8b78-99cea0ec1571.ams-static-15.cdntogo.net",
		"cdntogo.net",
		"API.Service-KP.com",     // регистр не должен влиять
		"api.service-kp.com:443", // порт отбрасывается
		"api.service-kp.com.",    // корневая точка тоже
	}
	for _, h := range allowed {
		if !u.OriginAllowed(h) {
			t.Errorf("хост %q должен проходить", h)
		}
	}

	// Суффиксное сравнение обязано держать границу метки, иначе
	// защита от SSRF превращается в decoration.
	denied := []string{
		"evil-cdntogo.net",
		"cdntogo.net.attacker.example",
		"notapi.service-kp.com.evil.example",
		"service-kp.com",
		"localhost",
		"169.254.169.254", // метаданные облака — классическая цель SSRF
		"",
	}
	for _, h := range denied {
		if u.OriginAllowed(h) {
			t.Errorf("хост %q НЕ должен проходить", h)
		}
	}
}

func TestOriginAllowed_EmptyListDeniesEverything(t *testing.T) {
	var u Upstream
	if u.OriginAllowed("api.service-kp.com") {
		t.Fatal("пустой список обязан запрещать всё")
	}
}

// Ссылка живёт 24 часа (измерено), фильм — два с половиной.
// Обновление остаётся страховкой, но окно должно срабатывать.
func TestTicket_RefreshWindow(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	tk := Ticket{
		V: TicketSchemaVersion, ID: kernel.TicketID("t"),
		Upstream:    Upstream{URL: "https://x/y.m3u8", AllowedOrigins: []string{"x"}},
		ExpiresAt:   now.Add(24 * time.Hour),
		Refreshable: true,
	}
	if tk.NeedsRefresh(now) {
		t.Fatal("свежий суточный тикет обновлять рано")
	}
	if !tk.NeedsRefresh(now.Add(24*time.Hour - time.Minute)) {
		t.Fatal("за минуту до истечения обновление обязано сработать")
	}
	if tk.Expired(now.Add(23 * time.Hour)) {
		t.Fatal("тикет не должен считаться истёкшим раньше срока")
	}
}

// Реклама, вклеенная в поток: чужой хост вырезается, если тикет так велит,
// а свой — только по явному списку.
func TestSegmentDropped(t *testing.T) {
	u := Upstream{AllowedOrigins: []string{"cdn.example"}}
	if u.SegmentDropped("ads.example") {
		t.Fatal("без DropOutsideOrigins чужой хост не вырезается — его отвергает OriginAllowed")
	}
	u.DropOutsideOrigins = true
	if !u.SegmentDropped("ads.example") || u.SegmentDropped("s1.cdn.example") {
		t.Fatal("DropOutsideOrigins вырезает не то")
	}
	u.DropOutsideOrigins = false
	u.DropHosts = []string{"promo.cdn.example"}
	if !u.SegmentDropped("x.promo.cdn.example") || u.SegmentDropped("evil-promo.cdn.example") {
		t.Fatal("DropHosts сравнивает не по границе метки")
	}
}
