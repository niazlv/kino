package mediahttp

import "context"

// Ограничение одновременных походов наружу.
//
// Канал до площадки один и узкий: сегмент весит 3-4 МБ, а плеер качает их
// впрок по несколько сразу. Без ограничения запрос плейлиста — тридцать
// килобайт, от которых зависит СТАРТ воспроизведения, — встаёт в очередь
// за мегабайтами предзагрузки и ждёт секунды.
//
// Плейлисты пропускаются мимо очереди: они мелкие, их единицы, и именно
// они на критическом пути. Загрузчик-утилита по тем же соображениям держит
// четыре параллельных сегмента.
type limiter struct{ slots chan struct{} }

func newLimiter(n int) *limiter {
	if n <= 0 {
		n = 4
	}
	return &limiter{slots: make(chan struct{}, n)}
}

// acquire занимает слот. Возвращает функцию освобождения; для плейлистов
// вызывается вхолостую — они не ограничиваются.
func (l *limiter) acquire(ctx context.Context, isPlaylist bool) (func(), error) {
	if isPlaylist {
		return func() {}, nil
	}
	select {
	case l.slots <- struct{}{}:
		return func() { <-l.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
