// Package kernel — общее ядро домена (DDD shared kernel): типы идентификаторов
// и таксономия ошибок. Намеренно крошечный: это не свалка для «общего», а
// список того, что обязано быть одинаковым во всех доменных пакетах.
package kernel

import (
	"errors"
	"fmt"
)

// Таксономия ошибок домена. Транспорт мапит её в HTTP/gRPC-коды — благодаря
// этому domain и app ничего не знают про статус-коды.
var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInvalidInput = errors.New("invalid input")
	ErrForbidden    = errors.New("forbidden")
	ErrUnauthorized = errors.New("unauthorized")
	ErrExpired      = errors.New("expired")
	ErrUnavailable  = errors.New("upstream unavailable")

	// ErrBusy — площадка временно ограничила темп запросов. От
	// ErrUnavailable отличается тем, что через минуту всё заработает само:
	// это повод предложить повторить, а не сообщать о поломке.
	ErrBusy = errors.New("upstream busy")

	// ErrUnreachable — до площадки не дошли: сеть, прокси, TLS, таймаут, её
	// 5xx. Частный случай ErrUnavailable (errors.Is отвечает «да» на оба), но
	// говорит о площадке целиком, а не о конкретной серии: «этой серии там
	// нет в нужном виде» не повод обходить площадку для всех, а «площадка
	// молчит» — повод.
	ErrUnreachable = fmt.Errorf("upstream unreachable: %w", ErrUnavailable)

	// ErrReauth — площадка отвергла наш вход, и продлить его само не
	// вышло: нужен человек с новой авторизацией. Тоже частный случай
	// ErrUnavailable — другим площадкам это не мешает, — но, в отличие от
	// «не отвечает», сам не пройдёт. Поэтому одной записи в журнале мало:
	// состояние должно быть видно в админке и в плеере, иначе о нём узнают
	// только по неиграющим сериям.
	ErrReauth = fmt.Errorf("upstream needs reauthorization: %w", ErrUnavailable)
)
