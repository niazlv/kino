// Команда probe — эксперимент фазы 0 из ADR-0003.
//
// От её результата зависит схема доставки медиа, поэтому она выполняется
// ДО написания media-gateway: узнать это после — дорого.
//
// Проверяет по CDN-ссылке, полученной твоими существующими инструментами:
//
//	а) привязана ли ссылка к IP запросившего
//	б) отдаёт ли CDN CORS-заголовки (без них hls.js не работает,
//	   а нативный HLS на iOS работает — отсюда «на айфоне ок, в хроме нет»)
//	в) реальный срок жизни ссылки
//	г) нужны ли чувствительные заголовки (если нет — gateway вообще
//	   не касается секретов, и оговорка ADR-0004 закрывается сама)
//
// Использование:
//
//	probe -url 'https://cdn.../master.m3u8?sig=...' [-ua ...] [-referer ...]
//	probe -url ... -wait 35m     # перепроверить протухание
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type result struct {
	name    string
	ok      bool
	unknown bool
	detail  string
}

func main() {
	var (
		raw     = flag.String("url", "", "CDN-ссылка, полученная с сервера")
		urlFile = flag.String("url-file", "", "файл со ссылкой — предпочтительнее -url: "+
			"подписанный URL из argv виден в списке процессов")
		ua      = flag.String("ua", "", "User-Agent, который требует площадка")
		referer = flag.String("referer", "", "Referer, который требует площадка")
		wait    = flag.Duration("wait", 0, "подождать и перепроверить протухание")
		origin  = flag.String("origin", "https://kino.example.com", "Origin для проверки CORS")
	)
	flag.Parse()
	if *urlFile != "" {
		b, err := os.ReadFile(*urlFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "чтение %s: %v\n", *urlFile, err)
			os.Exit(2)
		}
		*raw = strings.TrimSpace(string(b))
	}
	if *raw == "" {
		flag.Usage()
		os.Exit(2)
	}

	u, err := url.Parse(*raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "битый url: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("Фаза 0 · проверка доставки\nхост: %s\n\n", u.Host)

	var out []result
	out = append(out, checkHeaders(*raw, *ua, *referer))
	out = append(out, checkCORS(*raw, *origin, *ua))
	out = append(out, checkExpiry(u))
	out = append(out, checkPlaylist(*raw, *ua))
	ipRes := describeIPCheck(*raw)

	for _, r := range out {
		mark := "✗"
		switch {
		case r.unknown:
			mark = "?"
		case r.ok:
			mark = "✓"
		}
		fmt.Printf("  %s %-28s %s\n", mark, r.name, r.detail)
	}
	fmt.Printf("  ? %-28s %s\n", ipRes.name, ipRes.detail)

	if *wait > 0 {
		fmt.Printf("\nЖду %s и перепроверяю протухание…\n", *wait)
		time.Sleep(*wait)
		code, err := status(*raw, *ua, *referer)
		if err != nil {
			fmt.Printf("  ✗ ссылка через %s: ошибка %v\n", *wait, err)
		} else {
			fmt.Printf("  через %s код %d (%s)\n", *wait, code,
				map[bool]string{true: "жива", false: "протухла"}[code < 400])
		}
	}

	printVerdict(out)
}

// (г) Нужны ли чувствительные заголовки. Если ссылка открывается голой —
// upstream.headers в тикете пуст, и gateway не видит секретов вообще.
func checkHeaders(raw, ua, referer string) result {
	bare, errBare := status(raw, "", "")
	full, errFull := status(raw, ua, referer)

	switch {
	case errBare == nil && bare < 400:
		return result{name: "заголовки не нужны", ok: true,
			detail: fmt.Sprintf("голый запрос -> %d; upstream.headers пуст, gateway не касается секретов", bare)}
	case errFull == nil && full < 400:
		return result{name: "нужны заголовки",
			detail: fmt.Sprintf("голый -> %d, с UA/Referer -> %d; см. оговорку ADR-0004", bare, full)}
	default:
		return result{name: "доступность ссылки", unknown: true,
			detail: fmt.Sprintf("голый -> %d/%v, с заголовками -> %d/%v", bare, errBare, full, errFull)}
	}
}

// (б) CORS. Нужен только для hls.js; нативный HLS на iOS обходится без него,
// поэтому отсутствие CORS даёт «на айфоне работает, в хроме нет».
func checkCORS(raw, origin, ua string) result {
	req, _ := http.NewRequest(http.MethodGet, raw, nil)
	req.Header.Set("Origin", origin)
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	resp, err := client().Do(req)
	if err != nil {
		return result{name: "CORS", unknown: true, detail: err.Error()}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))

	allow := resp.Header.Get("Access-Control-Allow-Origin")
	if allow == "*" || allow == origin {
		return result{name: "CORS", ok: true,
			detail: "Access-Control-Allow-Origin: " + allow + " -> hls.js сможет читать напрямую"}
	}
	return result{name: "CORS",
		detail: "заголовка нет -> hls.js напрямую не сможет, прямая отдача отпадает"}
}

// (в) Срок жизни: вытаскиваем из подписи, если она в query.
func checkExpiry(u *url.URL) result {
	now := time.Now()
	for _, k := range []string{"expires", "expire", "exp", "e", "valid_until", "ttl", "ends"} {
		v := u.Query().Get(k)
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			continue
		}
		var exp time.Time
		switch {
		case n > 1e12: // миллисекунды
			exp = time.UnixMilli(n)
		case n > 1e9: // секунды
			exp = time.Unix(n, 0)
		default: // относительный TTL
			exp = now.Add(time.Duration(n) * time.Second)
		}
		left := time.Until(exp).Round(time.Minute)
		ok := left > 0
		note := ""
		if left < 150*time.Minute {
			note = "; КОРОЧЕ полнометражного фильма -> проактивное обновление тикета обязательно"
		}
		return result{name: "срок жизни ссылки", ok: ok,
			detail: fmt.Sprintf("параметр %s -> осталось %s%s", k, left, note)}
	}
	return result{name: "срок жизни ссылки", unknown: true,
		detail: "в query не найден; перезапусти с -wait 35m, чтобы измерить"}
}

// Разбор плейлиста: сколько дорожек, какие кодеки, куда ведут сегменты.
func checkPlaylist(raw, ua string) result {
	body, err := fetch(raw, ua, "")
	if err != nil {
		return result{name: "плейлист", unknown: true, detail: err.Error()}
	}
	text := string(body)
	if !strings.HasPrefix(strings.TrimSpace(text), "#EXTM3U") {
		return result{name: "плейлист", unknown: true, detail: "не HLS-манифест (progressive mp4?)"}
	}

	audio := strings.Count(text, "#EXT-X-MEDIA:TYPE=AUDIO")
	variants := strings.Count(text, "#EXT-X-STREAM-INF")
	hevc := strings.Contains(text, "hvc1") || strings.Contains(text, "hev1")

	base, _ := url.Parse(raw)
	crossOrigin := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if ref, err := base.Parse(line); err == nil && ref.Host != base.Host {
			crossOrigin = true
			break
		}
	}

	notes := []string{fmt.Sprintf("вариантов %d, аудиодорожек %d", variants, audio)}
	if hevc {
		notes = append(notes, "есть HEVC -> нужен H.264-рендишен для части браузеров")
	}
	if crossOrigin {
		notes = append(notes, "сегменты на ДРУГОМ хосте -> allowed_origins должен включать оба")
	}
	if audio > 1 {
		notes = append(notes, "несколько озвучек -> переключение дорожек обязательно")
	}
	return result{name: "плейлист", ok: true, detail: strings.Join(notes, "; ")}
}

// (а) IP-привязку с одной машины не проверить: нужен второй выход в сеть.
func describeIPCheck(raw string) result {
	return result{name: "привязка к IP", unknown: true, detail: fmt.Sprintf(
		"проверь вручную с другой сети (телефон по LTE):\n      curl -o /dev/null -s -w '%%{http_code}\\n' '%s'\n"+
			"      403/404 -> ссылка привязана к IP: резолв и отдача обязаны идти с одного egress", raw)}
}

func printVerdict(rs []result) {
	fmt.Println("\nВывод для ADR-0003:")
	corsOK := false
	for _, r := range rs {
		if r.name == "CORS" && r.ok {
			corsOK = true
		}
	}
	if !corsOK {
		fmt.Println("  CORS отсутствует -> прямая отдача ссылки клиенту отпадает.")
		fmt.Println("  Дефолт (полное проксирование) подтверждён.")
	} else {
		fmt.Println("  CORS есть. Если привязки к IP нет, режим redirect применим —")
		fmt.Println("  но дефолтом остаётся проксирование: оно снимает протухание и утечку ссылок.")
	}
	fmt.Println("  Результат занеси в docs/adr/0003-media-delivery.md и сними с неё статус «ожидает подтверждения».")
}

func client() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		// Редиректы не следуем: нам важно увидеть исходный ответ CDN,
		// а в gateway они и вовсе ограничены allowed_origins.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func status(raw, ua, referer string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", "bytes=0-1023") // не тянем весь файл
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<12))
	return resp.StatusCode, nil
}

func fetch(raw, ua, referer string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, raw, nil)
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("код %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
