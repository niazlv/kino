package provider

// SegmentKind — что за отрезок серии можно пропустить.
type SegmentKind string

const (
	// SegIntro — опенинг, заставка.
	SegIntro SegmentKind = "intro"
	// SegOutro — эндинг, титры.
	SegOutro SegmentKind = "outro"
	// SegRecap — пересказ прошлых серий.
	SegRecap SegmentKind = "recap"
	// SegPreview — анонс следующей серии после титров.
	SegPreview SegmentKind = "preview"
	// SegSkip — площадка велела пропустить, но не сказала что. Плееры
	// Kodik и Alloha отдают голые интервалы «от-до»: заставка это или
	// титры, решает плеер по месту в серии — длину знает только он.
	SegSkip SegmentKind = "skip"
)

// Segment — отрезок серии, который можно пропустить.
//
// Разметка привязана к файлу, а не к серии: у дубляжа своя заставка
// студии, и опенинг сдвинут на секунды против оригинала. Поэтому у
// разметки чужого файла (AniSkip размечает японский релиз) указана длина
// того файла: плеер берёт её, только если длина его потока совпала.
type Segment struct {
	Kind    SegmentKind
	StartMS int64
	EndMS   int64
	// ForDurationMS — длина файла, по которому размечено, когда разметка
	// не с этого самого потока. 0 — снята с него же и точна.
	ForDurationMS int64
}

// Valid — отрезок вообще осмыслен: конец после начала и не длиннее
// десяти минут. Длиннее — это уже не заставка, а ошибка разметки, и
// пропуск съел бы серию.
func (s Segment) Valid() bool {
	return s.StartMS >= 0 && s.EndMS > s.StartMS && s.EndMS-s.StartMS <= 10*60*1000
}
