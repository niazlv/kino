// Copyright (C) 2026 niazlv
// Перенесено из kinopub-downloader/pkg/browserhttp: ядру нужен только клиент (ADR-0018).
// SPDX-License-Identifier: MIT OR GPL-3.0-or-later

//go:build android

package browserhttp

import (
	"context"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// NewDialer is the exported alias used by other packages.
func NewDialer() *net.Dialer { return newDialer() }

func newDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Resolver:  androidResolver(),
	}
}

// androidResolver picks how names are resolved on Android.
//
//   - KINOPUB_DNS=host[:port][,host...] — explicit servers, used in turn.
//   - A cgo build (the one shipped inside the kino APK) uses the system
//     resolver (getaddrinfo → netd): it honours the phone's DNS, Private
//     DNS and VPN. A hard-coded public server fails on networks that block
//     it, and the app's downloads silently died there.
//   - A pure-Go build (Termux) can't reach netd's [::1]:53 proxy and has no
//     /etc/resolv.conf, so it asks public servers, falling through a list
//     rather than betting on one (round robin across retries).
func androidResolver() *net.Resolver {
	servers := publicDNS
	if env := strings.TrimSpace(os.Getenv("KINOPUB_DNS")); env != "" {
		servers = nil
		for _, s := range strings.Split(env, ",") {
			if s = strings.TrimSpace(s); s != "" {
				if !strings.Contains(s, ":") {
					s += ":53"
				}
				servers = append(servers, s)
			}
		}
	} else if systemResolver {
		return nil // net.DefaultResolver: getaddrinfo via cgo
	}
	// A UDP "connection" succeeds even to an unreachable server, so a Dial
	// error can't drive failover; instead each resolver retry goes to the
	// next server in turn.
	var next atomic.Uint32
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			s := servers[int(next.Add(1)-1)%len(servers)]
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, s)
		},
	}
}

// publicDNS: Google, Cloudflare, Yandex — one of them is usually reachable.
var publicDNS = []string{"8.8.8.8:53", "1.1.1.1:53", "77.88.8.8:53"}
