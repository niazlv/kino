package httpuser

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dp "github.com/niazleushkin/kino/internal/domain/provider"
)

// Ключи дорожек считает сервер — с псевдонимами из админки, которых
// клиент не знает.
func TestVoiceKeys(t *testing.T) {
	t.Cleanup(func() { dp.SetVoiceAliases(nil) })
	dp.SetVoiceAliases(map[string]string{"aniliberty": "anilibria"})
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	body := `{"labels":["01. Дублированный. DUB (RUS)","02. Многоголосый закадровый. AniLiberty (RUS)",""]}`
	w := httptest.NewRecorder()
	s.handleVoiceKeys(w, httptest.NewRequest(http.MethodPost, "/api/v1/voices/keys", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("код %d: %s", w.Code, w.Body)
	}
	var out voiceKeysResp
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if k := out.Keys["01. Дублированный. DUB (RUS)"]; k.Key != "dub" || k.Title != "Дубляж" {
		t.Fatalf("дубляж: %+v", k)
	}
	if k := out.Keys["02. Многоголосый закадровый. AniLiberty (RUS)"]; k.Key != "anilibria" || k.Title != "AniLiberty" {
		t.Fatalf("псевдоним: %+v", k)
	}
	if len(out.Keys) != 2 {
		t.Fatalf("пустая подпись попала в ответ: %+v", out.Keys)
	}
	many := `{"labels":[` + strings.Repeat(`"x",`, voiceKeysMax) + `"x"]}`
	w = httptest.NewRecorder()
	s.handleVoiceKeys(w, httptest.NewRequest(http.MethodPost, "/api/v1/voices/keys", strings.NewReader(many)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("лишние подписи: код %d", w.Code)
	}
}
