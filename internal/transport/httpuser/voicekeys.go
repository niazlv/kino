package httpuser

import (
	"net/http"
	"strings"

	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/provider"
	"github.com/niazleushkin/kino/internal/platform/httpx"
)

// Ключи дорожек потока — сервером, а не клиентом.
//
// У kinopub озвучка — дорожка внутри общего потока, и её подпись
// («01. Дублированный. DUB (RUS)») плеер узнаёт только из плейлиста.
// Считай ключ по ней сам клиент — копией правила сервера на TypeScript и
// Kotlin, — любое новое правило склейки требовало бы выпуска приложения, а
// псевдонимы студий из админки клиенту не видны вовсе. Поэтому клиент
// спрашивает: подписи — сюда, ключ и подпись для человека — отсюда.

const (
	voiceKeysMax  = 64
	voiceLabelMax = 200
)

type voiceKeysReq struct {
	Labels []string `json:"labels"`
}

type voiceKeyDTO struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type voiceKeysResp struct {
	Keys map[string]voiceKeyDTO `json:"keys"`
}

func (s *Server) handleVoiceKeys(w http.ResponseWriter, r *http.Request) {
	var in voiceKeysReq
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Fail(w, r, s.log, err)
		return
	}
	if len(in.Labels) > voiceKeysMax {
		httpx.Fail(w, r, s.log, kernel.ErrInvalidInput)
		return
	}
	out := voiceKeysResp{Keys: make(map[string]voiceKeyDTO, len(in.Labels))}
	for _, l := range in.Labels {
		if strings.TrimSpace(l) == "" || len(l) > voiceLabelMax {
			continue
		}
		out.Keys[l] = voiceKeyDTO{Key: provider.VoiceKey(l), Title: provider.VoiceTitle(l)}
	}
	httpx.JSON(w, http.StatusOK, out)
}
