// Две мелочи, без которых админка была бы неудобной, и ничего сверх.
// Внешних скриптов нет: страница показывает секреты.

// Копирование: пароль, otpauth-адрес, ссылка на инвайт.
document.addEventListener('click', e => {
  const b = e.target.closest('[data-copy]')
  if (!b || !navigator.clipboard) return
  navigator.clipboard.writeText(b.dataset.copy).then(() => {
    const was = b.textContent
    b.textContent = 'Скопировано'
    b.classList.add('done')
    setTimeout(() => { b.textContent = was; b.classList.remove('done') }, 1600)
  }).catch(() => {})
})

// Подтверждение опасных действий. Текст лежит на форме: сервер знает,
// кого блокируем, а скрипт — нет.
document.addEventListener('submit', e => {
  const f = e.target
  if (f.dataset.confirm && !confirm(f.dataset.confirm)) e.preventDefault()
})

// Подсказки при слиянии и привязке: под input лежит datalist, который
// заполняется поиском по каталогу. Выбор подставляет номер — форма
// по-прежнему шлёт голый id, но человеку его знать уже не нужно.
let suggestTimer = null
document.addEventListener('input', e => {
  const input = e.target.closest('[data-suggest]')
  if (!input) return
  const list = document.getElementById(input.getAttribute('list'))
  if (!list) return
  clearTimeout(suggestTimer)
  const q = input.value.trim()
  if (q.length < 2) { list.innerHTML = ''; return }
  suggestTimer = setTimeout(async () => {
    try {
      const res = await fetch('/admin/titles/suggest?kind=${encodeURIComponent(input.dataset.suggest)}&q=${encodeURIComponent(q)}`,
        { headers: { 'Accept': 'application/json' } })
      if (!res.ok) return
      const data = await res.json()
      list.innerHTML = ''
      for (const it of (data.items ?? [])) {
        const opt = document.createElement('option')
        opt.value = it.value
        opt.label = it.label
        list.appendChild(opt)
      }
    } catch { /* подсказка не обязана работать — форма и так живёт */ }
  }, 250)
})
