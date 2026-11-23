package memory

import (
	"context"

	appplayback "github.com/niazleushkin/kino/internal/app/playback"
	appvoice "github.com/niazleushkin/kino/internal/app/voicenames"
	"github.com/niazleushkin/kino/internal/domain/kernel"
	"github.com/niazleushkin/kino/internal/domain/playback"
)

// Sessions — no-op SessionSink: движок не собирает телеметрию сеансов
// (дух ADR-0005 — не копить то, на что некому смотреть). Порт нужен
// playback.Service как обязательный, поэтому заглушка.
type Sessions struct{}

func (Sessions) Open(context.Context, playback.Session) error  { return nil }
func (Sessions) Close(context.Context, playback.Session) error { return nil }

// VoiceNames — no-op Store псевдонимов студий: на устройстве их некому
// править и некому учить. Разбор подписей озвучек работает и без таблицы.
type VoiceNames struct{}

func (VoiceNames) Aliases(context.Context) ([]appvoice.Alias, error) {
	return nil, nil
}

func (VoiceNames) PutAlias(context.Context, appvoice.Alias) error { return nil }
func (VoiceNames) DeleteAlias(context.Context, string) error      { return nil }

func (VoiceNames) Rejected(context.Context) ([][2]string, error) { return nil, nil }
func (VoiceNames) Reject(context.Context, string, string) error  { return nil }

func (VoiceNames) RecordSpellings(context.Context, []appvoice.Spelling) error { return nil }
func (VoiceNames) Spellings(context.Context) ([]appvoice.Spelling, error)     { return nil, nil }

// Observe — SpellingSink для playback.Service: написания озвучек на
// устройстве никуда не записываются.
func (VoiceNames) Observe(kernel.EpisodeID, string, string) {}

// Проверка, что заглушки по-прежнему удовлетворяют портам.
var (
	_ appplayback.SessionSink = Sessions{}
	_ appvoice.Store          = VoiceNames{}
)
