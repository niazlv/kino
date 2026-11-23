package segcache

import "testing"

// Формы адресов сняты с живого CDN площадки: подпись сидит вторым
// сегментом пути и меняется в каждом тикете, хвост стабилен.
const pat = `^https?://[^/]+/hls/[^/]+/(.+)$`

func TestKey_StableAcrossSignatures(t *testing.T) {
	const host = "https://86feaca6.ams-static-02.cdntogo.net"
	a, ok := Key(host+"/hls/aWQ9ODEyOTY7MTc4ODUwMjMwOA/8/df/LaOk.mp4/seg-1-v1.ts", pat)
	if !ok {
		t.Fatal("ключ не построен")
	}
	b, _ := Key(host+"/hls/aWQ9ODEyOTY7MTc4ODUwMjMxMw/8/df/LaOk.mp4/seg-1-v1.ts", pat)
	if a != b {
		t.Fatal("подпись попала в ключ — кеш будет промахиваться на каждом тикете")
	}
}

// Площадка выдаёт свой uuid в поддомене на каждый тикет — измерено на живых
// адресах. Хост в ключе означал бы промах всегда.
func TestKey_StableAcrossHosts(t *testing.T) {
	const tail = "/hls/SIG/8/df/LaOk.mp4/seg-1-v1.ts"
	a, _ := Key("https://86feaca6-c052.ams-static-02.cdntogo.net"+tail, pat)
	b, _ := Key("https://1a5af214-d010.ams-static-07.cdntogo.net"+tail, pat)
	if a != b {
		t.Fatal("хост попал в ключ — попаданий между тикетами не будет")
	}
}

// Query у подписанных ссылок тоже меняется: жадный хвост правила утащил бы
// его в ключ, и попаданий не было бы.
func TestKey_IgnoresQuery(t *testing.T) {
	const base = "https://cdn.example/hls/SIG/8/df/x.mp4/seg-1.ts"
	a, _ := Key(base+"?e=111&h=aaa", pat)
	b, _ := Key(base+"?e=222&h=bbb", pat)
	if a != b {
		t.Fatal("query попал в ключ")
	}
}

// Разные сегменты обязаны различаться, иначе кеш отдаст не тот файл.
func TestKey_DistinguishesSegments(t *testing.T) {
	const host = "https://cdn.example"
	a, _ := Key(host+"/hls/SIG/8/df/x.mp4/seg-1.ts", pat)
	b, _ := Key(host+"/hls/SIG/8/df/x.mp4/seg-2.ts", pat)
	if a == b {
		t.Fatal("разные сегменты дали один ключ")
	}
}

// Без правила ключ строится по хосту и пути — безопасно, просто промахов
// больше. Несовпавшее правило ведёт себя так же.
func TestKey_FallsBackWithoutPattern(t *testing.T) {
	a, ok := Key("https://cdn.example/other/seg-1.ts", "")
	if !ok {
		t.Fatal("ключ не построен без правила")
	}
	b, _ := Key("https://cdn.example/other/seg-1.ts", pat)
	if a != b {
		t.Fatal("несовпавшее правило не должно менять ключ")
	}
}
