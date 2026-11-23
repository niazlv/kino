package catalog

import "time"

// FullWalkPolicy — когда обходить каталог целиком.
//
// Полный обход — десятки тысяч запросов к площадке под одним аккаунтом с
// просмотром, и площадка на такое отвечает ограничением темпа всем сразу.
// Поэтому целиком — редко и ночью, когда никто не смотрит; между ними
// синхронизация идёт по курсору и стоит единицы запросов.
type FullWalkPolicy struct {
	// Every — не чаще, чем раз в столько. Ноль — полный обход не нужен никогда.
	Every time.Duration
	// NightFrom..NightTo — часы ночного окна по Loc, конец исключительно.
	// Окно может переходить через полночь: 23..5.
	NightFrom, NightTo int
	Loc                *time.Location
}

// InNight — попадает ли момент в ночное окно.
func (p FullWalkPolicy) InNight(now time.Time) bool {
	loc := p.Loc
	if loc == nil {
		loc = time.UTC
	}
	h := now.In(loc).Hour()
	if p.NightFrom == p.NightTo {
		// Пустое окно и окно «весь день» неразличимы; считаем, что ночи нет,
		// иначе ошибка в настройке превратилась бы в обход в любой час.
		return false
	}
	if p.NightFrom < p.NightTo {
		return h >= p.NightFrom && h < p.NightTo
	}
	return h >= p.NightFrom || h < p.NightTo
}

// WantFull — пора ли идти целиком: ночь и прошлый полный обход старее Every.
// lastFull нулевой — целиком ещё не ходили, идём в первую же ночь.
func (p FullWalkPolicy) WantFull(now, lastFull time.Time) bool {
	if p.Every <= 0 || !p.InNight(now) {
		return false
	}
	return lastFull.IsZero() || now.Sub(lastFull) >= p.Every
}
