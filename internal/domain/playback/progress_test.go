package playback

import (
	"testing"
	"time"
)

// Регрессия: отчёт со второго устройства приходит БЕЗ статуса (клиент шлёт
// только позицию). Слияние обязано восстановить статус, иначе просмотр
// молча выпадает из «продолжить смотреть».
func TestMerge_RestoresStatusWhenIncomingHasNone(t *testing.T) {
	base := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	stored := Progress{PositionMS: 900_000, DurationMS: 2_700_000,
		Status: StatusInProgress, UpdatedAt: base}
	incoming := Progress{PositionMS: 300_000, DurationMS: 2_700_000,
		UpdatedAt: base.Add(time.Minute)} // статуса нет

	got := stored.Merge(incoming)
	if got.PositionMS != 900_000 {
		t.Fatalf("position = %d, want 900000", got.PositionMS)
	}
	if got.Status != StatusInProgress {
		t.Fatalf("status = %q, want in_progress", got.Status)
	}
}

func TestMerge_MarksCompletedFromPosition(t *testing.T) {
	now := time.Now()
	stored := Progress{PositionMS: 100, DurationMS: 2_700_000, Status: StatusInProgress, UpdatedAt: now}
	got := stored.Merge(Progress{PositionMS: 2_650_000, DurationMS: 2_700_000, UpdatedAt: now})
	if got.Status != StatusCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
}

// Брошенный ранее просмотр возвращается в работу от самого факта просмотра.
func TestMerge_DroppedComesBackOnNewProgress(t *testing.T) {
	now := time.Now()
	stored := Progress{PositionMS: 100, DurationMS: 2_700_000, Status: StatusDropped, UpdatedAt: now}
	got := stored.Merge(Progress{PositionMS: 200_000, DurationMS: 2_700_000, UpdatedAt: now})
	if got.Status != StatusInProgress {
		t.Fatalf("status = %q, want in_progress", got.Status)
	}
}

// Регрессия: свежесть слияния меряется UpdatedAt (сервер), а не client_ts
// (устройство). Ушедшие на два часа назад часы телевизора не должны
// навсегда проигрывать записям с честных часов: отчёт, пришедший позже,
// побеждает независимо от того, что написано в его client_ts.
func TestMerge_ClientClockSkewCannotBeatLaterReport(t *testing.T) {
	base := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	// Честные часы записали позицию в полдень; телевизор с отставшими
	// часами отчитался позже (UpdatedAt — полдень + 10 минут), но его
	// client_ts уверяет, что было 10 утра.
	stored := Progress{PositionMS: 900_000, DurationMS: 2_700_000,
		Status: StatusInProgress, ClientTS: base, UpdatedAt: base}
	incoming := Progress{PositionMS: 1_200_000, DurationMS: 2_700_000,
		ClientTS: base.Add(-2 * time.Hour), UpdatedAt: base.Add(10 * time.Minute)}

	got := stored.Merge(incoming)
	if got.PositionMS != 1_200_000 {
		t.Fatalf("position = %d, want 1200000 (позже пришедший отчёт побеждает)", got.PositionMS)
	}
}

func TestMerge_SameDeviceKeepsRewindAndRejectsDelayedReport(t *testing.T) {
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	before := Progress{PositionMS: 900_000, DurationMS: 2_700_000,
		DeviceID: "session-1", UpdatedAt: base, ClientTS: base}
	back := Progress{PositionMS: 120_000, DurationMS: 2_700_000,
		DeviceID: "session-1", UpdatedAt: base.Add(time.Second), ClientTS: base.Add(time.Second)}
	got := before.Merge(back)
	if got.PositionMS != back.PositionMS {
		t.Fatalf("rewind lost: %d", got.PositionMS)
	}
	before.UpdatedAt = base.Add(2 * time.Second) // старый запрос долетел позже
	if late := got.Merge(before); late.PositionMS != back.PositionMS {
		t.Fatalf("delayed request undid rewind: %d", late.PositionMS)
	}
	back.PositionMS = 0
	back.ClientTS = base.Add(3 * time.Second)
	if zero := got.Merge(back); zero.PositionMS != 0 {
		t.Fatalf("rewind to the beginning lost: %d", zero.PositionMS)
	}
}

func TestMerge_ExactPositionReplacesLegacyBufferEstimate(t *testing.T) {
	now := time.Now()
	stored := Progress{PositionMS: 900_000, DurationMS: 2_700_000, UpdatedAt: now}
	got := stored.Merge(Progress{PositionMS: 300_000, DeviceID: "session-1",
		UpdatedAt: now, ClientTS: now})
	if got.PositionMS != 300_000 || got.DurationMS != stored.DurationMS {
		t.Fatalf("exact position/duration lost: %+v", got)
	}
}
