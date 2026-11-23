// Copyright (C) 2026 niazlv
// Перенесено из kinopub-downloader/pkg/browserhttp: ядру нужен только клиент (ADR-0018).
// SPDX-License-Identifier: MIT OR GPL-3.0-or-later

//go:build android && cgo

package browserhttp

// systemResolver: cgo build — names go through the system getaddrinfo.
const systemResolver = true
