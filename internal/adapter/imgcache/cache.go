// Package imgcache — дисковый кеш картинок каталога.
//
// Браузерного кеша недостаточно: он у каждого свой, не переживает
// обновление страницы с Cmd+Shift+R и не помогает другому пользователю.
// А на странице сериала кадров полтора десятка сразу, и каждый — поход
// через VPN к площадке.
//
// Кеш на диске: первый заход медленный один раз и для всех, дальше отдача
// локальная.
package imgcache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type Cache struct {
	dir string
	// maxBytes — потолок. Ноль означает «без предела», и это допустимо
	// только в тестах: в работе кеш без потолка молча съедает диск.
	maxBytes int64
	writes   int64
	// Один и тот же кадр запрашивают все карточки страницы разом. Без
	// объединения одинаковых запросов первый заход на сериал — это
	// полтора десятка одновременных походов к площадке.
	mu       sync.Mutex
	inFlight map[string]chan struct{}
}

func New(dir string, maxBytes int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Cache{dir: dir, maxBytes: maxBytes, inFlight: make(map[string]chan struct{})}, nil
}

func (c *Cache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	// Раскладка по двум уровням: десятки тысяч файлов в одном каталоге
	// заметно замедляют файловую систему.
	return filepath.Join(c.dir, h[:2], h[2:4], h)
}

// Get отдаёт содержимое и тип, если картинка уже лежит.
func (c *Cache) Get(key string) (data []byte, contentType string, ok bool) {
	p := c.path(key)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", false
	}
	ct, err := os.ReadFile(p + ".t")
	if err != nil {
		ct = []byte("image/jpeg")
	}
	// Отметка обращения нужна вытеснению: без неё «давно не спрашивали»
	// не отличить от «давно положили».
	c.touch(p)
	return b, string(ct), true
}

// Put сохраняет картинку. Запись идёт через временный файл: оборванная
// загрузка не должна оставить в кеше половину картинки.
func (c *Cache) Put(key string, data []byte, contentType string) error {
	p := c.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(p+".t", []byte(contentType), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return err
	}
	c.maybeSweep()
	return nil
}

// Do возвращает картинку из кеша либо получает её через fetch ровно один
// раз, сколько бы запросов ни пришло одновременно.
func (c *Cache) Do(key string, fetch func() ([]byte, string, error)) ([]byte, string, error) {
	if b, ct, ok := c.Get(key); ok {
		return b, ct, nil
	}

	c.mu.Lock()
	if wait, busy := c.inFlight[key]; busy {
		c.mu.Unlock()
		<-wait
		if b, ct, ok := c.Get(key); ok {
			return b, ct, nil
		}
		return nil, "", errors.New("imgcache: параллельная загрузка не удалась")
	}
	done := make(chan struct{})
	c.inFlight[key] = done
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.inFlight, key)
		c.mu.Unlock()
		close(done)
	}()

	b, ct, err := fetch()
	if err != nil {
		return nil, "", err
	}
	if err := c.Put(key, b, ct); err != nil {
		// Не смогли сохранить — отдаём как есть: неработающий кеш не повод
		// не показать картинку.
		return b, ct, nil
	}
	return b, ct, nil
}

var _ = io.Discard
