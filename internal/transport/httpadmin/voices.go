package httpadmin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/niazleushkin/kino/internal/app/voicenames"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// Озвучки: какие подписи разных площадок значат одну студию.
//
// Вид озвучки без студии («DUB», «Дублированный») сводит разбор подписи сам.
// Студии под разными именами сводит таблица псевдонимов: часть выучена по
// транслиту, остальное решает человек здесь — по подсказкам похожих
// написаний или вручную. Ошибку обучения он разъединяет, и пара
// запоминается как разная.

// VoiceNames — что админке нужно от app/voicenames.
type VoiceNames interface {
	Overview(ctx context.Context, q string) (voicenames.Overview, error)
	Merge(ctx context.Context, from, to, note string) error
	Split(ctx context.Context, from string) error
	Reject(ctx context.Context, a, b string) error
	Learn(ctx context.Context) (int, error)
}

// WithVoiceNames включает раздел «Озвучки».
func (s *Server) WithVoiceNames(v VoiceNames) *Server {
	s.voices = v
	return s
}

func (s *Server) showVoices(w http.ResponseWriter, r *http.Request) {
	if s.voices == nil {
		s.fail(w, r, kernel.ErrNotFound)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	o, err := s.voices.Overview(r.Context(), q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "voices", "Озвучки", s.voicesPage(q, o))
}

// studioView — студия так, как её показать: площадки по-человечески,
// прочие написания, ссылка на тайтл примера.
type studioView struct {
	Key    string
	Title  string
	Others []string
	Names  string
	Hits   int
	Link   string
}

type suggestionView struct {
	A, B studioView
	Why  string
}

func (s *Server) studioView(st voicenames.Studio) studioView {
	v := studioView{Key: st.Key, Title: st.Title, Hits: st.Hits}
	seen := map[string]bool{st.Title: true}
	for _, x := range st.Spellings {
		if !seen[x.Title] {
			seen[x.Title] = true
			v.Others = append(v.Others, x.Title)
		}
	}
	names := make([]string, 0, len(st.Providers))
	for _, p := range st.Providers {
		names = append(names, s.providerName(p))
	}
	v.Names = strings.Join(names, ", ")
	if st.Example.TitleID > 0 {
		v.Link = Prefix + "/titles/" + strconv.FormatInt(int64(st.Example.TitleID), 10)
	}
	return v
}

func (s *Server) voicesPage(q string, o voicenames.Overview) map[string]any {
	sugg := make([]suggestionView, 0, len(o.Suggestions))
	for _, x := range o.Suggestions {
		sugg = append(sugg, suggestionView{A: s.studioView(x.A), B: s.studioView(x.B), Why: x.Why})
	}
	studios := make([]studioView, 0, len(o.Studios))
	for _, st := range o.Studios {
		studios = append(studios, s.studioView(st))
	}
	return map[string]any{
		"Q": q, "Aliases": o.Aliases, "Suggestions": sugg, "Studios": studios, "Seen": o.Seen,
	}
}

// voiceAction — общая часть правок: форма, журнал, возврат на страницу с
// тем же поиском. Неверный ввод — подсказка на странице, а не страница
// ошибки: опечатка в форме не повод терять поиск.
func (s *Server) voiceAction(w http.ResponseWriter, r *http.Request, action string,
	do func() (target, flash string, err error)) {
	if s.voices == nil {
		s.fail(w, r, kernel.ErrNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, kernel.ErrInvalidInput)
		return
	}
	target, flash, err := do()
	back := Prefix + "/voices"
	q := url.Values{}
	if v := strings.TrimSpace(r.PostFormValue("q")); v != "" {
		q.Set("q", v)
	}
	switch {
	case errors.Is(err, kernel.ErrInvalidInput):
		q.Set("m", "voice_bad")
	case errors.Is(err, kernel.ErrNotFound):
		q.Set("m", "voice_gone")
	case err != nil:
		s.fail(w, r, err)
		return
	default:
		q.Set("m", flash)
		after := map[string]string{}
		for k := range r.PostForm {
			after[k] = r.PostFormValue(k)
		}
		_ = s.audit.Record(r.Context(), actorFrom(r.Context()).UserID, action, target,
			r.PostFormValue("note"), nil, after)
	}
	http.Redirect(w, r, back+"?"+q.Encode(), http.StatusSeeOther)
}

func (s *Server) mergeVoice(w http.ResponseWriter, r *http.Request) {
	s.voiceAction(w, r, "voice.merge", func() (string, string, error) {
		from, to := r.PostFormValue("from"), r.PostFormValue("to")
		return "voice:" + from, "voice_merged", s.voices.Merge(r.Context(), from, to, strings.TrimSpace(r.PostFormValue("note")))
	})
}

func (s *Server) splitVoice(w http.ResponseWriter, r *http.Request) {
	s.voiceAction(w, r, "voice.split", func() (string, string, error) {
		from := r.PostFormValue("from")
		return "voice:" + from, "voice_split", s.voices.Split(r.Context(), from)
	})
}

func (s *Server) rejectVoice(w http.ResponseWriter, r *http.Request) {
	s.voiceAction(w, r, "voice.reject", func() (string, string, error) {
		a, b := r.PostFormValue("a"), r.PostFormValue("b")
		return "voice:" + a + "|" + b, "voice_rejected", s.voices.Reject(r.Context(), a, b)
	})
}

func (s *Server) learnVoices(w http.ResponseWriter, r *http.Request) {
	s.voiceAction(w, r, "voice.learn", func() (string, string, error) {
		_, err := s.voices.Learn(r.Context())
		return "voice:*", "voice_learned", err
	})
}
