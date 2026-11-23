package kit

import (
	"testing"
	"time"
)

func TestLoadRoutes_LegacyProxyBecomesDefault(t *testing.T) {
	t.Setenv("EGRESS_ROUTES", "")
	t.Setenv("KINOPUB_PROXY", "http://172.18.0.1:10819")
	r, err := LoadRoutes(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", DefaultRoute, DirectRoute} {
		if _, ok := r.Client(name); !ok {
			t.Fatalf("маршрут %q должен быть всегда", name)
		}
	}
	if _, ok := r.Client("vpn"); ok {
		t.Fatal("необъявленный маршрут не должен находиться — тихий выход напрямую хуже отказа")
	}
}

func TestLoadRoutes_ParsesSpec(t *testing.T) {
	t.Setenv("EGRESS_ROUTES", "vpn=http://10.0.0.1:3128, ru=socks5://10.0.0.2:1080 ,direct=")
	t.Setenv("KINOPUB_PROXY", "")
	r, err := LoadRoutes(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Names()
	want := []string{"default", "direct", "ru", "vpn"}
	if len(got) != len(want) {
		t.Fatalf("маршруты %v, ожидались %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("маршруты %v, ожидались %v", got, want)
		}
	}
}

func TestLoadRoutes_RejectsGarbage(t *testing.T) {
	t.Setenv("EGRESS_ROUTES", "vpn")
	if _, err := LoadRoutes(time.Second); err == nil {
		t.Fatal("запись без адреса должна отвергаться")
	}
	t.Setenv("EGRESS_ROUTES", "vpn=::::")
	if _, err := LoadRoutes(time.Second); err == nil {
		t.Fatal("битый адрес прокси должен отвергаться")
	}
}
