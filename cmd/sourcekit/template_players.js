// __NAME__ — площадка на чужих плеерах: скрипт находит тайтлы и iframe
// плееров (Kodik, CVH, Alloha), а серии, озвучки, поток и разметку пропуска
// добывает платформа. Модель — «страница = один сезон», как у аниме-площадок.
// Справочник: docs/script-sources.md. Проверка: go run ./cmd/sourcekit check providers/sources/__ID__.js -q "запрос" -fetch

var SITE = "https://example.org";

source({
  id: "__ID__",
  name: "__NAME__",
  version: "0.1.0",
  kind: "anime",
  note: "Аниме: плееры Kodik и CVH. Kodik открывается только с российского адреса.",
  hosts: ["example.org"],
  home: SITE + "/",
  thumbs: true, // у CVH есть кадры серий

  search: function (query, limit) {
    var doc = html.parse(http.text(SITE + "/search", { query: { q: query } }), SITE);
    return doc.find(".item").slice(0, limit).map(function (el) {
      return { id: el.attr("data-id"), title: el.first(".title").text() };
    });
  },

  title: function (id) {
    var doc = html.parse(http.text(SITE + "/anime/" + id), SITE);
    var h1 = doc.first("h1");
    if (!h1) throw NotFound("тайтла " + id + " нет");
    return {
      id: id,
      url: SITE + "/anime/" + id,
      title: h1.text(),
      season: 1,
      mal: "", // номер MyAnimeList — лучший ключ склейки аниме
      aired: 12, // сколько серий вышло, если плееры сами не скажут
    };
  },

  // Плееры серии: хотя бы один.
  players: function (id, episode) {
    var doc = html.parse(http.text(SITE + "/anime/" + id), SITE);
    var kodik = doc.first("iframe[src*='kodik']");
    return {
      kodik: kodik ? kodik.abs("src") : "",
      // cvh: { pub: "…", mal: "…" },
      // alloha: "https://…",
      referer: SITE + "/anime/" + id,
    };
  },
});
