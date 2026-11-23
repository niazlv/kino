// __NAME__ — площадка со своими потоками (HLS или MP4).
// Справочник: docs/script-sources.md. Проверка: go run ./cmd/sourcekit check providers/sources/__ID__.js -q "запрос" -fetch

var SITE = "https://example.org";

source({
  id: "__ID__",
  name: "__NAME__",
  version: "0.1.0",
  kind: "general", // general — фильмы и сериалы, anime — весь каталог аниме
  note: "Что тут есть и чем площадка капризна (ключ, российский адрес).",
  hosts: ["example.org"], // куда скрипту можно ходить (поддомены тоже)
  home: SITE + "/",       // проверка живости

  // Поиск: массив тайтлов, лучшие первыми, не больше limit.
  search: function (query, limit) {
    var doc = html.parse(http.text(SITE + "/search", { query: { q: query } }), SITE);
    return doc.find(".item").slice(0, limit).map(function (el) {
      return {
        id: el.attr("data-id"),
        title: el.first(".title").text(),
        year: text.year(el.text()),
        poster: el.first("img") ? el.first("img").abs("src") : "",
      };
    });
  },

  // Карточка тайтла по номеру площадки.
  title: function (id) {
    var doc = html.parse(http.text(SITE + "/title/" + id), SITE);
    var h1 = doc.first("h1");
    if (!h1) throw NotFound("тайтла " + id + " нет");
    return {
      id: id,
      title: h1.text(),
      kind: "series", // или "movie"
      year: text.year(doc.text()),
      plot: doc.first(".description") ? doc.first(".description").text() : "",
      kp: "", imdb: "", // номера Кинопоиска и IMDb — ключи склейки с другими площадками
    };
  },

  // Серии: у фильма одна «серия» {season: 0, episode: 1}.
  episodes: function (id) {
    return [{ season: 1, episode: 1, title: "Серия 1" }];
  },

  // Поток серии: адрес HLS (.m3u8) или файла MP4.
  resolve: function (req) {
    // req: {id, season, episode, variant, variantLabel, maxHeight}
    throw NotFound("резолв ещё не написан");
  },
});
