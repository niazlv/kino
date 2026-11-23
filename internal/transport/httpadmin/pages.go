package httpadmin

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/niazleushkin/kino/internal/adapter/postgres"
	"github.com/niazleushkin/kino/internal/app/auth"
	"github.com/niazleushkin/kino/internal/domain/account"
	"github.com/niazleushkin/kino/internal/domain/kernel"
)

// ── обзор ─────────────────────────────────────────────────────────────────

type providerView struct {
	postgres.ProviderState
	Status      string // ok | error | stale | never | first — класс для чипа
	StatusLabel string
	Error       string
	// Name — как площадку называет витрина; Refs — сколько её элементов
	// уже привязано к каталогу.
	Name string
	Refs int64
}

// providerStatus отвечает на вопрос «а площадка вообще живая» — первый,
// который возникает, когда что-то ломается.
func providerStatus(p postgres.ProviderState) providerView {
	v := providerView{ProviderState: p}
	if p.LastError != nil {
		v.Error = *p.LastError
	}
	switch {
	case p.LastRunAt == nil:
		v.Status, v.StatusLabel = "never", "не запускался"
	case v.Error != "":
		v.Status, v.StatusLabel = "error", "ошибка"
	case time.Since(*p.LastRunAt) > 3*time.Hour:
		v.Status, v.StatusLabel = "stale", "давно не запускался"
	default:
		v.Status, v.StatusLabel = "ok", "в порядке"
	}
	return v
}

// providers — все подключённые площадки, а не только те, что уже
// закончили обход.
//
// Строка состояния пишется по окончании прохода, а первый проход новой
// площадки идёт часами: без подключённых из конфига площадка всё это
// время выглядела бы отсутствующей. Растущее число привязок — видимый
// признак того, что обход идёт.
func (s *Server) providers(r *http.Request) ([]providerView, error) {
	states, err := s.sync.List(r.Context())
	if err != nil {
		return nil, err
	}
	refs, err := s.sync.RefCounts(r.Context())
	if err != nil {
		return nil, err
	}
	byID := map[string]postgres.ProviderState{}
	for _, p := range states {
		byID[p.ProviderID] = p
	}
	ids := append([]string(nil), s.providerIDs...)
	for _, p := range states {
		if !contains(ids, p.ProviderID) {
			ids = append(ids, p.ProviderID)
		}
	}
	out := make([]providerView, 0, len(ids))
	for _, id := range ids {
		st, synced := byID[id]
		if !synced {
			st = postgres.ProviderState{ProviderID: id}
		}
		v := providerStatus(st)
		v.Name, v.Refs = s.providerName(id), refs[id]
		if !synced && v.Refs > 0 {
			v.Status, v.StatusLabel = "first", "первый обход идёт"
		}
		out = append(out, v)
	}
	return out, nil
}

func contains(list []string, x string) bool {
	for _, v := range list {
		if v == x {
			return true
		}
	}
	return false
}

func (s *Server) showDashboard(w http.ResponseWriter, r *http.Request) {
	ctx, a := r.Context(), actorFrom(r.Context())
	users, err := s.auth.ListUsers(ctx, a)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	invites, err := s.auth.ListOpenInvites(ctx, a)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	provs, err := s.providers(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	counts, err := s.sync.CatalogCounts(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	totals, err := s.play.DailyTotals(ctx, 7)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	audit, err := s.audit.Recent(ctx, 0, 8)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	var active7, disabled, admins int
	for _, u := range users {
		if u.Disabled {
			disabled++
		}
		if u.Role == account.RoleAdmin {
			admins++
		}
		if u.LastLoginAt != nil && time.Since(*u.LastLoginAt) < 7*24*time.Hour {
			active7++
		}
	}
	var sessions7, errors7 int
	var bytes7 int64
	for _, d := range totals {
		sessions7 += d.Sessions
		errors7 += d.Errors
		bytes7 += d.Bytes
	}
	s.render(w, r, "dashboard", "Обзор", map[string]any{
		"Users": len(users), "Active7": active7, "Disabled": disabled, "Admins": admins,
		"Invites": len(invites), "Catalog": counts, "Providers": provs,
		"Sessions7": sessions7, "Errors7": errors7, "Bytes7": bytes7, "Audit": audit,
	})
}

// ── пользователи ──────────────────────────────────────────────────────────

type userRow struct {
	account.User
	Sessions int
}

func (s *Server) showUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.auth.ListUsers(r.Context(), actorFrom(r.Context()))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	counts, err := s.sessions.ActiveCounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows := make([]userRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, userRow{User: u, Sessions: counts[u.ID]})
	}
	s.render(w, r, "users", "Пользователи", map[string]any{"Users": rows})
}

func userID(r *http.Request) (kernel.UserID, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, kernel.ErrInvalidInput
	}
	return kernel.UserID(id), nil
}

func (s *Server) showUser(w http.ResponseWriter, r *http.Request) {
	id, err := userID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	a := actorFrom(r.Context())
	u, err := s.auth.GetUser(r.Context(), a, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sessions, err := s.auth.ListSessions(r.Context(), a, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	keys, err := s.auth.ListPasskeys(r.Context(), a, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data := map[string]any{
		"User": u, "Sessions": sessions, "IsSelf": u.ID == a.UserID, "Passkeys": keys,
		"Import": nil,
		// Sudo — код свежий, опасные действия пройдут без повторного ввода.
		"Sudo": (auth.Policy{}).CanActSensitive(a, time.Now()) == nil,
	}
	if j, ok := s.imports.lastFor(id); ok {
		data["Import"] = j
	}
	if rv := s.take(r.URL.Query().Get("reveal")); rv != nil {
		data["Reveal"] = rv
		if rv.Kind == "totp" {
			if qr, err := qrDataURI(rv.Value); err == nil {
				data["RevealQR"] = qr
			}
		}
	}
	s.render(w, r, "user", u.DisplayName, data)
}

// userAction — общая обвязка для POST по пользователю: разобрать id,
// выполнить, вернуться на карточку с подтверждением.
func (s *Server) userAction(w http.ResponseWriter, r *http.Request,
	do func(id kernel.UserID, reason string) (suffix string, err error)) {
	id, err := userID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, kernel.ErrInvalidInput)
		return
	}
	card := Prefix + "/users/" + strconv.FormatInt(int64(id), 10)
	suffix, err := do(id, r.PostFormValue("reason"))
	if errors.Is(err, auth.ErrStepUp) {
		// Sudo-режим: код вводился давно. Спросить заново и вернуть на
		// карточку; действие человек повторит сам — POST не воспроизвести.
		http.Redirect(w, r, Prefix+"/factor?m=stepup&next="+url.QueryEscape(card), http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, card+suffix, http.StatusSeeOther)
}

func (s *Server) disableUser(w http.ResponseWriter, r *http.Request) {
	s.userAction(w, r, func(id kernel.UserID, reason string) (string, error) {
		return "?m=blocked", s.auth.SetDisabled(r.Context(), actorFrom(r.Context()), id, true, reason)
	})
}

func (s *Server) enableUser(w http.ResponseWriter, r *http.Request) {
	s.userAction(w, r, func(id kernel.UserID, reason string) (string, error) {
		return "?m=enabled", s.auth.SetDisabled(r.Context(), actorFrom(r.Context()), id, false, reason)
	})
}

func (s *Server) setRole(w http.ResponseWriter, r *http.Request) {
	s.userAction(w, r, func(id kernel.UserID, reason string) (string, error) {
		role := account.Role(r.PostFormValue("role"))
		return "?m=role", s.auth.SetRole(r.Context(), actorFrom(r.Context()), id, role, reason)
	})
}

func (s *Server) revokeSessions(w http.ResponseWriter, r *http.Request) {
	s.userAction(w, r, func(id kernel.UserID, reason string) (string, error) {
		return "?m=signed_out", s.auth.RevokeSessions(r.Context(), actorFrom(r.Context()), id, reason)
	})
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	s.userAction(w, r, func(id kernel.UserID, reason string) (string, error) {
		pw, err := s.auth.ResetPassword(r.Context(), actorFrom(r.Context()), id, reason)
		if err != nil {
			return "", err
		}
		return "?reveal=" + s.stash("password", pw), nil
	})
}

func (s *Server) provisionTOTP(w http.ResponseWriter, r *http.Request) {
	s.userAction(w, r, func(id kernel.UserID, reason string) (string, error) {
		url, err := s.auth.ProvisionTOTP(r.Context(), actorFrom(r.Context()), id, reason)
		if err != nil {
			return "", err
		}
		return "?reveal=" + s.stash("totp", url), nil
	})
}

// ── инвайты ───────────────────────────────────────────────────────────────

type inviteRow struct {
	account.Invite
	Link      string
	CreatedBy string
	UsedBy    string
	Used      bool
}

func (s *Server) showInvites(w http.ResponseWriter, r *http.Request) {
	ctx, a := r.Context(), actorFrom(r.Context())
	open, err := s.auth.ListOpenInvites(ctx, a)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	recent, err := s.auth.ListRecentInvites(ctx, a, 20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	users, err := s.auth.ListUsers(ctx, a)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	logins := map[kernel.UserID]string{}
	for _, u := range users {
		logins[u.ID] = u.Login
	}
	base := s.publicBase(r)
	row := func(i account.Invite) inviteRow {
		out := inviteRow{
			Invite: i,
			// Ссылка сама подставляет код в форму регистрации: человеку
			// остаётся придумать логин и пароль.
			Link:      base + "/#/invite/" + i.Code,
			CreatedBy: logins[i.CreatedBy],
			Used:      i.UsedBy != nil,
		}
		if i.UsedBy != nil {
			out.UsedBy = logins[*i.UsedBy]
		}
		return out
	}
	openRows := make([]inviteRow, 0, len(open))
	for _, i := range open {
		openRows = append(openRows, row(i))
	}
	recentRows := make([]inviteRow, 0, len(recent))
	for _, i := range recent {
		recentRows = append(recentRows, row(i))
	}
	s.render(w, r, "invites", "Инвайты", map[string]any{"Open": openRows, "Recent": recentRows})
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	if _, err := s.auth.CreateInvite(r.Context(), actorFrom(r.Context())); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, Prefix+"/invites?m=invite_created", http.StatusSeeOther)
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.RevokeInvite(r.Context(), actorFrom(r.Context()), chi.URLParam(r, "code")); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, Prefix+"/invites?m=invite_revoked", http.StatusSeeOther)
}

// ── площадки и воспроизведение ────────────────────────────────────────────

func (s *Server) showProviders(w http.ResponseWriter, r *http.Request) {
	provs, err := s.providers(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	counts, err := s.sync.CatalogCounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, "providers", "Площадки", map[string]any{
		"Providers": provs, "Catalog": counts, "IDs": s.providerIDs,
		"Auth": s.authViews(),
	})
}

// requestRescan — «пересканировать сейчас»: заявка пишется в базу, и её
// берёт обходчик. Не прямой запуск: админка и обходчик — разные процессы,
// и полный ход всё равно растягивается на часы; кнопка только двигает
// расписание, а не заменяет его.
func (s *Server) requestRescan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "provider")
	// Только подключённые площадки: заявка для неизвестного id — опечатка,
	// и оставлять её в базе незачем.
	known := false
	for _, p := range s.providerIDs {
		if p == id {
			known = true
			break
		}
	}
	if !known {
		s.fail(w, r, kernel.ErrNotFound)
		return
	}
	if err := s.sync.RequestRescan(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.audit.Record(r.Context(), actorFrom(r.Context()).UserID, "provider.rescan",
		id, r.PostFormValue("reason"), nil, map[string]any{"requested": true})
	http.Redirect(w, r, Prefix+"/providers?m=rescan", http.StatusSeeOther)
}

type bar struct {
	Day      string
	Label    string
	Sessions int
	Errors   int
	Pct      int // высота столбика, % от максимума за период
}

// showPlayback — качество воспроизведения. Разбивка по движку плеера не
// украшение: у плеера два независимых кодовых пути (hls.js и нативный HLS
// в iOS), и без неё «на айфоне тормозит» неотличимо от проблемы сети.
// Истории просмотров здесь нет намеренно (ADR-0005).
func (s *Server) showPlayback(w http.ResponseWriter, r *http.Request) {
	stats, err := s.play.RecentStats(r.Context(), 7)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	totals, err := s.play.DailyTotals(r.Context(), 30)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byDay := map[string]postgres.DayTotal{}
	for _, d := range totals {
		byDay[d.Day] = d
	}
	// Все 30 дней подряд, включая пустые: дыра в графике — тоже информация.
	bars := make([]bar, 0, 30)
	maxN, total, errs := 0, 0, 0
	for i := 29; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i)
		key := day.Format("2006-01-02")
		d := byDay[key]
		bars = append(bars, bar{Day: key, Label: strconv.Itoa(day.Day()), Sessions: d.Sessions, Errors: d.Errors})
		if d.Sessions > maxN {
			maxN = d.Sessions
		}
		total += d.Sessions
		errs += d.Errors
	}
	for i := range bars {
		if maxN > 0 {
			bars[i].Pct = bars[i].Sessions * 100 / maxN
		}
	}
	s.render(w, r, "playback", "Воспроизведение", map[string]any{
		"Stats": stats, "Bars": bars, "Total": total, "Errors": errs,
	})
}

// ── журнал ────────────────────────────────────────────────────────────────

const auditPage = 50

// showAudit — журнал с фильтрами по исполнителю и действию. Фильтры в GET:
// ссылку «покажи все правки привязок» можно сохранить.
func (s *Server) showAudit(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	f := postgres.AuditFilter{
		Actor:  parseInt64(r.URL.Query().Get("actor")),
		Action: strings.TrimSpace(r.URL.Query().Get("action")),
	}
	if len(f.Action) > 50 {
		s.fail(w, r, kernel.ErrInvalidInput)
		return
	}
	entries, err := s.audit.Filtered(r.Context(), f, before, auditPage)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var next int64
	if len(entries) == auditPage {
		next = entries[len(entries)-1].ID
	}
	// Списки для фильтров: действия — конечное множество из кода, исполнители —
	// те, кто вообще заходил в админку.
	actions, err := s.audit.Actions(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	users, err := s.auth.ListUsers(r.Context(), actorFrom(r.Context()))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := url.Values{}
	if f.Actor != 0 {
		q.Set("actor", strconv.FormatInt(f.Actor, 10))
	}
	if f.Action != "" {
		q.Set("action", f.Action)
	}
	nextURL := ""
	if next != 0 {
		qq := q
		qq.Set("before", strconv.FormatInt(next, 10))
		nextURL = Prefix + "/audit?" + qq.Encode()
	}
	s.render(w, r, "audit", "Журнал действий", map[string]any{
		"Audit": entries, "Next": next, "NextURL": nextURL,
		"F": f, "Actions": actions, "Users": users, "Query": q.Encode(),
	})
}

// parseInt64 — строгий разбор для параметров фильтра: мусор в строке
// означает «фильтра нет», а не ошибку страницы.
func parseInt64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
