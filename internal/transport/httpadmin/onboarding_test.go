package httpadmin

import (
	"errors"
	"testing"
)

// Переносы идут по одному на всю платформу: аккаунт площадки один.
func TestImportsOneAtATime(t *testing.T) {
	var m imports
	if !m.start(2, "папка") {
		t.Fatal("первый перенос не запустился")
	}
	if m.start(3, "история") {
		t.Fatal("второй перенос запустился, пока идёт первый")
	}
	if j, _ := m.lastFor(2); !j.Running() {
		t.Fatalf("идущий перенос не виден: %+v", j)
	}
	m.finish(2, "перенесено 418 из 419", errors.New("kinopub: rate limited"))
	j, ok := m.lastFor(2)
	if !ok || j.Running() || j.Summary == "" || j.Err == "" {
		t.Fatalf("итог не сохранён: %+v", j)
	}
	if _, ok := m.lastFor(3); ok {
		t.Fatal("у отвергнутого переноса не должно быть записи")
	}
	if !m.start(3, "история") {
		t.Fatal("после окончания следующий перенос не запустился")
	}
}
