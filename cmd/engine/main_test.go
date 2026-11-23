package main

import "testing"

func TestProviderUAsFromEnv(t *testing.T) {
	got := providerUAs([]string{
		"ENGINE_UA_ANILIBRIA=Mine/1",
		"ENGINE_UA_ANIMEGO_ME=Mozilla/5.0 (X; Y) a=b", // «=» внутри значения
		"ENGINE_UA_YUMMY=   ",                         // пусто — не задано
		"ENGINE_UA_=пусто",                            // нет имени
		"ENGINE_USER_AGENT=общая",                     // не про площадку
		"PATH=/usr/bin",
	})
	if len(got) != 2 || got["anilibria"] != "Mine/1" || got["animego_me"] != "Mozilla/5.0 (X; Y) a=b" {
		t.Errorf("%+v", got)
	}
}
