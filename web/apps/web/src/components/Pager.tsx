/**
 * Постраничная навигация с номерами.
 *
 * Номера, а не только «назад/вперёд»: по ним видно, сколько всего, и можно
 * прыгнуть в середину. Окно вокруг текущей плюс края, между ними многоточие —
 * иначе у истории в двадцать страниц полоса не влезла бы в телефон.
 */
export function Pager({ page, pages, onChange }:
  { page: number; pages: number; onChange: (p: number) => void }) {
  if (pages <= 1) return null
  const items: (number | '…')[] = []
  const around = 2
  let last = 0
  for (let p = 1; p <= pages; p++) {
    const show = p === 1 || p === pages || Math.abs(p - page) <= around
    if (!show) continue
    if (last && p - last > 1) items.push('…')
    items.push(p)
    last = p
  }
  return (
    <nav className="pager" aria-label="Страницы">
      <button disabled={page <= 1} onClick={() => onChange(page - 1)} aria-label="Предыдущая">‹</button>
      {items.map((it, i) => it === '…'
        ? <span key={`d${i}`} className="dots" aria-hidden>…</span>
        : <button key={it} className={it === page ? 'on' : ''} aria-current={it === page ? 'page' : undefined}
                  onClick={() => onChange(it)}>{it}</button>)}
      <button disabled={page >= pages} onClick={() => onChange(page + 1)} aria-label="Следующая">›</button>
    </nav>
  )
}
