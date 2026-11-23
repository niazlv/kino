// Copyright (C) 2026 niazlv
// Перенесено из kinopub-downloader/pkg/browserhttp: ядру нужен только клиент (ADR-0018).
// SPDX-License-Identifier: MIT OR GPL-3.0-or-later

//go:build !android

package browserhttp

import (
	"net"
	"time"
)

// NewDialer is the exported alias used by other packages.
func NewDialer() *net.Dialer { return newDialer() }

func newDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
}
