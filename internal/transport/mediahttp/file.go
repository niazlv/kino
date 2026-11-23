package mediahttp

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// handleFile отдаёт источник одним файлом.
//
// Это и есть «просто скачать»: площадка рядом с HLS отдаёт прогрессивный
// MP4 отдельной ссылкой, и его достаточно протолкнуть через шлюз. Ни сборки
// из сегментов, ни мультиплексирования не нужно — а значит, не нужна и
// утилита. Range уже поддержан общим путём, поэтому докачка и перемотка
// работают сами.
//
// Взамен теряется выбор: в таком файле одна дорожка и одно качество — то,
// что запечено в него площадкой. За выбором дорожек и субтитров по-прежнему
// идут в манифест и утилиту.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	t, err := s.ticket(r)
	if err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if t.Mode == playback.DeliveryLocal {
		httpx.Fail(w, r, s.log, kernel.ErrUnavailable)
		return
	}
	if t.Upstream.Protocol != playback.ProtocolProgressive {
		// Тикет выпущен под HLS: одним файлом такое не отдать, и молчаливо
		// подсунуть плейлист под именем .mp4 — худшее из решений.
		httpx.Fail(w, r, s.log, kernel.ErrUnavailable)
		return
	}

	w.Header().Set("Content-Disposition",
		disposition(r.URL.Query().Get("name"), t.Upstream.URL))
	s.serveUpstreamWhole(w, r, t, t.Upstream.URL)
}

// disposition собирает заголовок с именем файла.
//
// Имя приходит запросом, поэтому чистится жёстко: перевод строки в значении
// заголовка — это расщепление ответа, а разделители пути позволили бы
// предложить браузеру записать файл не туда.
func disposition(name, upstream string) string {
	ext := ".mp4"
	if u, err := url.Parse(upstream); err == nil {
		if e := strings.ToLower(path.Ext(u.Path)); e != "" && len(e) <= 5 {
			ext = e
		}
	}

	clean := sanitizeName(name)
	if clean == "" {
		clean = "video"
	}
	full := clean + ext

	// ASCII-запас для старых клиентов и точное имя по RFC 5987 для остальных:
	// кириллица в filename= без кодирования читается как мусор.
	ascii := asciiFallback(clean) + ext
	return "attachment; filename=\"" + ascii + "\"; filename*=UTF-8''" + url.PathEscape(full)
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f: // управляющие, включая CR и LF
		case strings.ContainsRune(`/\:*?"<>|`, r):
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.Trim(out, ".") // «..» и имена, начинающиеся с точки
	if r := []rune(out); len(r) > 100 {
		out = strings.TrimRight(string(r[:100]), ".")
	}
	return out
}

func asciiFallback(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(strings.TrimSpace(b.String()), "._-")
	if out == "" {
		return "video"
	}
	return out
}
