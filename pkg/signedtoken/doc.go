// Package signedtoken — короткоживущие подписанные токены в пути URL.
//
// Нужен потому, что нативный HLS-плеер в iOS Safari не подставляет HTTP-
// заголовки в запросы сегментов: авторизация медиа обязана жить в URL
// или в same-origin cookie, но не в Authorization (ADR-0003).
package signedtoken
