package m3u8

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrDrop — ответ Resolve «этого сегмента у клиента быть не должно».
//
// Нужен вырезанию рекламы, вклеенной прямо в плейлист: такой сегмент
// выбрасывается вместе со своими тегами (#EXTINF, разрыв, диапазон байт),
// а не превращает весь плейлист в отказ. Для вложенного плейлиста и для
// ресурсов в атрибутах тегов (ключи, дорожки) вырезать нечего — там ErrDrop
// остаётся ошибкой: без ключа или дорожки поток всё равно не сыграет.
var ErrDrop = errors.New("m3u8: ресурс вырезан")

// segmentTags — теги, которые относятся к СЛЕДУЮЩЕМУ сегменту и должны
// исчезнуть вместе с ним. Разрыв (DISCONTINUITY) сюда входит намеренно:
// рекламная вставка обрамлена разрывами, и оставленный разрыв перед
// выброшенным сегментом лишь добавит плееру пустую перестройку.
var segmentTags = []string{
	"#EXTINF",
	"#EXT-X-BYTERANGE",
	"#EXT-X-DISCONTINUITY",
	"#EXT-X-PROGRAM-DATE-TIME",
	"#EXT-X-CUE-OUT",
	"#EXT-X-CUE-IN",
	"#EXT-X-CUE-OUT-CONT",
	"#EXT-X-GAP",
	"#EXT-X-BITRATE",
}

func isSegmentTag(line string) bool {
	for _, t := range segmentTags {
		if line == t || strings.HasPrefix(line, t+":") {
			return true
		}
	}
	return false
}

// Теги, несущие URI в атрибуте. Пропустить любой из них — значит отдать
// клиенту ссылку мимо нашей авторизации; пропустить EXT-X-MEDIA — получить
// «видео идёт, звука нет», причём плеер не сообщит причину.
var uriAttrTags = []string{
	"#EXT-X-KEY",
	"#EXT-X-MAP",
	"#EXT-X-MEDIA",
	"#EXT-X-I-FRAME-STREAM-INF",
	"#EXT-X-SESSION-KEY",
	"#EXT-X-PART",
	"#EXT-X-PRELOAD-HINT",
	"#EXT-X-RENDITION-REPORT",
}

// Rewriter переписывает URI внутри плейлиста.
//
// Resolve получает АБСОЛЮТНЫЙ адрес, приведённый относительно плейлиста,
// и возвращает адрес, который увидит клиент. Возврат ошибки прекращает
// переписывание: лучше отдать ошибку, чем плейлист с частью ссылок мимо
// авторизации.
type Rewriter struct {
	Base    *url.URL
	Resolve func(absolute *url.URL) (string, error)
}

// Rewrite обрабатывает плейлист целиком.
func (r Rewriter) Rewrite(playlist string) (string, error) {
	var out strings.Builder
	sc := bufio.NewScanner(strings.NewReader(playlist))
	// Строки плейлиста короткие, но атрибуты EXT-X-STREAM-INF бывают длинными.
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	// Теги сегмента копятся до его адреса: решение «оставить или вырезать»
	// принимается по адресу, а теги идут раньше него.
	var pending []string
	flush := func() {
		for _, p := range pending {
			out.WriteString(p)
			out.WriteByte('\n')
		}
		pending = pending[:0]
	}

	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == "":
			flush()
			out.WriteString(line)

		case strings.HasPrefix(trimmed, "#"):
			rewritten, err := r.rewriteTag(trimmed)
			if err != nil {
				return "", err
			}
			if isSegmentTag(trimmed) {
				pending = append(pending, rewritten)
				continue
			}
			flush()
			out.WriteString(rewritten)

		default:
			// Строка без решётки — это сам ресурс: сегмент либо вложенный
			// плейлист варианта.
			u, err := r.absolute(trimmed)
			if err != nil {
				return "", err
			}
			mapped, err := r.Resolve(u)
			if errors.Is(err, ErrDrop) && hasEXTINF(pending) {
				pending = pending[:0]
				continue
			}
			if err != nil {
				return "", err
			}
			flush()
			out.WriteString(mapped)
		}
		out.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("m3u8: чтение: %w", err)
	}
	flush()
	return out.String(), nil
}

// hasEXTINF — вырезать можно только медиасегмент: у него есть #EXTINF.
// Строка-адрес без него — вариант в мастер-плейлисте, и его пропажа
// оставила бы висящий #EXT-X-STREAM-INF.
func hasEXTINF(tags []string) bool {
	for _, t := range tags {
		if strings.HasPrefix(t, "#EXTINF") {
			return true
		}
	}
	return false
}

func (r Rewriter) rewriteTag(line string) (string, error) {
	for _, tag := range uriAttrTags {
		if !strings.HasPrefix(line, tag+":") {
			continue
		}
		return r.rewriteURIAttr(line)
	}
	return line, nil
}

// rewriteURIAttr подменяет значение атрибута URI="..." внутри строки тега.
//
// Разбор идёт по кавычкам, а не по запятым: значения атрибутов сами
// содержат запятые (CODECS="avc1.640028,mp4a.40.2"), и разбиение по ним
// ломает строку.
func (r Rewriter) rewriteURIAttr(line string) (string, error) {
	const key = `URI="`
	idx := strings.Index(line, key)
	if idx < 0 {
		return line, nil // тег без URI — законный случай, например METHOD=NONE
	}
	start := idx + len(key)
	end := strings.IndexByte(line[start:], '"')
	if end < 0 {
		return "", fmt.Errorf("m3u8: незакрытый URI в %q", truncate(line, 80))
	}
	raw := line[start : start+end]
	if raw == "" {
		return line, nil
	}
	u, err := r.absolute(raw)
	if err != nil {
		return "", err
	}
	mapped, err := r.Resolve(u)
	if err != nil {
		return "", err
	}
	return line[:start] + mapped + line[start+end:], nil
}

func (r Rewriter) absolute(ref string) (*url.URL, error) {
	u, err := url.Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("m3u8: битый URI %q: %w", truncate(ref, 60), err)
	}
	if r.Base != nil {
		return r.Base.ResolveReference(u), nil
	}
	return u, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
