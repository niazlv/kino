/**
 * Заглушки на время загрузки: тени той же формы, что и содержимое, вместо
 * надписи «загрузка…». Раскладка не прыгает, когда приходят данные, и
 * человек сразу понимает, что именно сейчас появится.
 */
export function GridSkeleton({ n = 12 }: { n?: number }) {
  return (
    <div className="grid" aria-hidden>
      {Array.from({ length: n }, (_, i) => (
        <div className="tile" key={i}>
          <div className="art sk" />
          <div className="sk sk-line" />
          <div className="sk sk-line short" />
        </div>
      ))}
    </div>
  )
}

/** Заглушка истории: кадры 16/9, а не постеры — форма та же, что придёт. */
export function CardsSkeleton({ n = 8 }: { n?: number }) {
  return (
    <div className="hgrid" aria-hidden>
      {Array.from({ length: n }, (_, i) => (
        <div className="hcard" key={i}>
          <span className="hframe sk" />
          <span className="sk sk-line" />
          <span className="sk sk-line short" />
        </div>
      ))}
    </div>
  )
}

export function RowsSkeleton({ n = 8 }: { n?: number }) {
  return (
    <div className="eplist" aria-hidden>
      {Array.from({ length: n }, (_, i) => (
        <div className="hrow" key={i}>
          <span className="hshot sk" />
          <span className="hmain" style={{ flex: 1 }}>
            <span className="sk sk-line" style={{ marginTop: 0 }} />
            <span className="sk sk-line short" />
          </span>
        </div>
      ))}
    </div>
  )
}

export function TitleSkeleton() {
  return (
    <div aria-hidden>
      <div className="sk sk-line" style={{ width: 120, marginTop: 0 }} />
      <div className="sk sk-line" style={{ width: 320, height: 24, marginTop: 8 }} />
      <div className="playbox sk" style={{ aspectRatio: '16/9', marginTop: 16 }} />
    </div>
  )
}
