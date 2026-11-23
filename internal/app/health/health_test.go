package health

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/niazleushkin/kino/internal/domain/kernel"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newAt(c *clock) *Tracker {
	t := New(nil)
	t.now = c.now
	return t
}

var errDown = errors.New("таймаут")

// Отказ кладёт площадку на срок, и срок кончается сам.
func TestTracker_DownExpires(t *testing.T) {
	c := &clock{time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)}
	tr := newAt(c)
	if tr.Down("kinopub") {
		t.Fatal("неизвестная площадка считается лежащей")
	}
	tr.Failed("kinopub", errDown)
	if !tr.Down("kinopub") || tr.Down("animego_me") {
		t.Fatal("отказ одной площадки задел не ту")
	}
	c.t = c.t.Add(DownFor)
	if tr.Down("kinopub") {
		t.Fatal("срок кончился, а площадка всё ещё лежит")
	}
}

// Удачный ответ возвращает площадку сразу, не дожидаясь срока.
func TestTracker_RecoveredClears(t *testing.T) {
	c := &clock{time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)}
	tr := newAt(c)
	tr.Failed("kinopub", errDown)
	tr.Recovered("kinopub")
	if tr.Down("kinopub") {
		t.Fatal("ответившая площадка осталась лежащей")
	}
}

// Перепроверка — одна за раз и не чаще ProbeEvery; её исход решает судьбу.
func TestTracker_ProbeIsRationed(t *testing.T) {
	c := &clock{time.Date(2026, 9, 26, 7, 0, 0, 0, time.UTC)}
	tr := newAt(c)
	if _, ok := tr.TryProbe("kinopub"); ok {
		t.Fatal("живую площадку незачем перепроверять")
	}
	tr.Failed("kinopub", errDown)
	done, ok := tr.TryProbe("kinopub")
	if !ok {
		t.Fatal("лежащую площадку не дали перепроверить")
	}
	if _, ok := tr.TryProbe("kinopub"); ok {
		t.Fatal("две перепроверки разом")
	}
	done(errDown)
	if !tr.Down("kinopub") {
		t.Fatal("неудачная перепроверка вернула площадку")
	}
	if _, ok := tr.TryProbe("kinopub"); ok {
		t.Fatal("перепроверка чаще ProbeEvery")
	}
	c.t = c.t.Add(ProbeEvery)
	done, ok = tr.TryProbe("kinopub")
	if !ok {
		t.Fatal("через ProbeEvery перепроверку не дали")
	}
	done(nil)
	if tr.Down("kinopub") {
		t.Fatal("удачная перепроверка не вернула площадку")
	}
}

// «Нужна переавторизация» не кончается сроком: протухший вход сам не
// оживёт, и площадка лежит, пока не ответит или пока вход не примут.
func TestTracker_ReauthIsSticky(t *testing.T) {
	c := &clock{time.Date(2026, 11, 13, 16, 3, 0, 0, time.UTC)}
	tr := newAt(c)
	cause := fmt.Errorf("kinopub: токен отвергнут: %w", kernel.ErrReauth)
	tr.Failed("kinopub", cause)
	if !tr.Down("kinopub") || !tr.NeedsReauth("kinopub") {
		t.Fatal("отказ входа не положил площадку с причиной")
	}
	c.t = c.t.Add(10 * DownFor)
	if !tr.Down("kinopub") {
		t.Fatal("переавторизация кончилась сама, сроком")
	}
	got := tr.Reauths()
	if len(got) != 1 || got[0].Provider != "kinopub" || !got[0].Since.Equal(time.Date(2026, 11, 13, 16, 3, 0, 0, time.UTC)) ||
		got[0].Cause == "" {
		t.Fatalf("для админки: %+v", got)
	}
	// Повторный отказ не сдвигает «с какого момента».
	tr.Failed("kinopub", cause)
	if !tr.Reauths()[0].Since.Equal(got[0].Since) {
		t.Fatal("повторный отказ сдвинул начало")
	}
	// Лежащую по входу перепроверяют, как любую лежащую.
	done, ok := tr.TryProbe("kinopub")
	if !ok {
		t.Fatal("ждущую переавторизации не перепроверить")
	}
	done(cause)
	if !tr.NeedsReauth("kinopub") {
		t.Fatal("неудачная перепроверка сняла переавторизацию")
	}
	tr.Recovered("kinopub")
	if tr.Down("kinopub") || tr.NeedsReauth("kinopub") || len(tr.Reauths()) != 0 {
		t.Fatal("ответившая площадка осталась ждать переавторизации")
	}
}

// Хранитель входа сказал «принят» — снимается только переавторизация;
// сетевой срок, если площадка ещё и не отвечала, идёт дальше.
func TestTracker_ReauthClearedKeepsNetworkDown(t *testing.T) {
	c := &clock{time.Date(2026, 11, 13, 16, 3, 0, 0, time.UTC)}
	tr := newAt(c)
	tr.Failed("kinopub", errDown)
	tr.Failed("kinopub", fmt.Errorf("x: %w", kernel.ErrReauth))
	tr.ReauthCleared("kinopub")
	if tr.NeedsReauth("kinopub") {
		t.Fatal("переавторизация не снята")
	}
	if !tr.Down("kinopub") {
		t.Fatal("вместе с переавторизацией снят и сетевой срок")
	}
	tr.ReauthCleared("animego_me") // неизвестная — без паники
}
