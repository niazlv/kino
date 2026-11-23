package catalog

import (
	"testing"
	"time"
)

func TestFullWalkPolicy_OnlyAtNightAndNotTooOften(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	p := FullWalkPolicy{Every: 84 * time.Hour, NightFrom: 2, NightTo: 6, Loc: msk}
	night := time.Date(2026, 9, 10, 3, 0, 0, 0, msk)
	day := time.Date(2026, 9, 10, 15, 0, 0, 0, msk)

	if !p.WantFull(night, time.Time{}) {
		t.Error("ни разу не ходили и ночь — должны идти")
	}
	if p.WantFull(day, time.Time{}) {
		t.Error("днём не идём, даже если ни разу не ходили")
	}
	if p.WantFull(night, night.Add(-24*time.Hour)) {
		t.Error("сутки назад уже ходили — рано")
	}
	if !p.WantFull(night, night.Add(-4*24*time.Hour)) {
		t.Error("четыре дня назад — пора")
	}
}

func TestFullWalkPolicy_WindowAcrossMidnightAndZones(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	p := FullWalkPolicy{Every: time.Hour, NightFrom: 23, NightTo: 5, Loc: msk}
	// 23:30 по Москве — это 20:30 UTC: окно считается по Loc, а не по UTC.
	late := time.Date(2026, 9, 10, 20, 30, 0, 0, time.UTC)
	if !p.InNight(late) {
		t.Error("23:30 по Москве — ночь")
	}
	noon := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC) // 12:00 МСК
	if p.InNight(noon) {
		t.Error("полдень по Москве — не ночь")
	}
	if (FullWalkPolicy{Every: time.Hour, NightFrom: 3, NightTo: 3}).InNight(late) {
		t.Error("пустое окно — ночи нет")
	}
	if (FullWalkPolicy{Every: 0, NightFrom: 0, NightTo: 24}).WantFull(late, time.Time{}) {
		t.Error("Every=0 — полный обход выключен")
	}
}

func TestPacePerTitle(t *testing.T) {
	// 150 минут на 53 тысячи тайтлов — около 170 мс на тайтл.
	p := pacePerTitle(150*time.Minute, 53_000)
	if p < 160*time.Millisecond || p > 180*time.Millisecond {
		t.Errorf("темп %v, ждали около 170 мс", p)
	}
	if pacePerTitle(0, 53_000) != 0 || pacePerTitle(time.Hour, 0) != 0 {
		t.Error("без растяжки или без оценки — без пауз")
	}
	// Оценка занижена в тысячи раз — обход всё равно не растянется на сутки.
	if pacePerTitle(150*time.Minute, 3) != 5*time.Second {
		t.Errorf("пауза не ограничена сверху: %v", pacePerTitle(150*time.Minute, 3))
	}
}
