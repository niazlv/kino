// Passkey (WebAuthn) в браузере: три церемонии — вход, подтверждение и
// регистрация. Кнопка несёт адреса begin/finish и, при необходимости, куда
// вернуться. Ничего своего в протокол не добавляется: только перекодировка
// base64url <-> байты, которой требует navigator.credentials.
(() => {
  const enc = b => btoa(String.fromCharCode(...new Uint8Array(b)))
    .replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  const dec = s => Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0))
  const supported = !!(window.PublicKeyCredential && navigator.credentials)

  // Кнопки скрыты по умолчанию: без поддержки в браузере они только путают.
  document.querySelectorAll('[data-passkey]').forEach(b => { if (supported) b.hidden = false })

  async function post(url, body) {
    const r = await fetch(url, {
      method: 'POST', credentials: 'same-origin',
      headers: { 'content-type': 'application/json' },
      body: body ? JSON.stringify(body) : '{}',
    })
    const j = await r.json().catch(() => ({}))
    if (!r.ok) { const e = new Error(j.error || ('Ошибка ' + r.status)); e.next = j.next; throw e }
    return j
  }

  function creation(o) {
    const pk = o.publicKey
    pk.challenge = dec(pk.challenge)
    pk.user.id = dec(pk.user.id)
    ;(pk.excludeCredentials || []).forEach(c => { c.id = dec(c.id) })
    return { publicKey: pk }
  }
  function request(o) {
    const pk = o.publicKey
    pk.challenge = dec(pk.challenge)
    ;(pk.allowCredentials || []).forEach(c => { c.id = dec(c.id) })
    return { publicKey: pk }
  }
  function serialize(c) {
    const r = c.response
    const out = {
      id: c.id, rawId: enc(c.rawId), type: c.type,
      clientExtensionResults: c.getClientExtensionResults ? c.getClientExtensionResults() : {},
      response: { clientDataJSON: enc(r.clientDataJSON) },
    }
    if (c.authenticatorAttachment) out.authenticatorAttachment = c.authenticatorAttachment
    if (r.attestationObject) {
      out.response.attestationObject = enc(r.attestationObject)
      out.response.transports = r.getTransports ? r.getTransports() : []
    } else {
      out.response.authenticatorData = enc(r.authenticatorData)
      out.response.signature = enc(r.signature)
      if (r.userHandle) out.response.userHandle = enc(r.userHandle)
    }
    return out
  }
  const withQuery = (url, k, v) => url + (url.includes('?') ? '&' : '?') + k + '=' + encodeURIComponent(v)

  // Метка «в этом браузере ключ уже работал». Ставится после любой удачной
  // церемонии. Нужна только автозапуску ниже: на устройстве без ключа он
  // вместо формы входа показал бы пустое системное окно. Хранилище бывает
  // закрыто — тогда просто без автозапуска.
  const MARK = 'kino-admin-passkey'
  const marked = () => { try { return localStorage.getItem(MARK) === '1' } catch (_) { return false } }
  const mark = () => { try { localStorage.setItem(MARK, '1') } catch (_) {} }

  // Автозапуск: приложение открывает /admin/login?passkey=1, и вход сразу
  // спрашивает отпечаток. Параметр убирается из адреса, чтобы «обновить»
  // не запускало ключ заново.
  let auto = false
  let autoStart = false
  const loginBtn = document.querySelector('[data-passkey="login"]')
  const params = new URLSearchParams(location.search)
  if (params.has('passkey')) {
    params.delete('passkey')
    const q = params.toString()
    history.replaceState(null, '', location.pathname + (q ? '?' + q : '') + location.hash)
    autoStart = supported && !!loginBtn && marked()
  }

  document.addEventListener('click', async e => {
    const b = e.target.closest('[data-passkey]')
    if (!b) return
    e.preventDefault()
    const automatic = auto
    auto = false
    const errEl = document.querySelector('[data-passkey-error]')
    const say = t => { if (errEl) { errEl.textContent = t; errEl.hidden = !t } }
    say(''); b.disabled = true
    try {
      const kind = b.dataset.passkey
      const options = await post(b.dataset.begin)
      const cred = kind === 'register'
        ? await navigator.credentials.create(creation(options))
        : await navigator.credentials.get(request(options))
      let finish = b.dataset.finish
      if (kind === 'register') {
        const name = prompt('Как назвать этот ключ?', b.dataset.name || 'Мой ключ')
        if (name === null) { b.disabled = false; return }
        finish = withQuery(finish, 'name', name)
      }
      if (b.dataset.next) finish = withQuery(finish, 'next', b.dataset.next)
      const res = await post(finish, serialize(cred))
      mark()
      location.href = res.next || '/admin/'
    } catch (err) {
      if (err && err.next) { location.href = err.next; return }
      // Отказ на автозапуске — не ошибка: человек мог закрыть окно, чтобы
      // войти паролем. Подсказываем, что кнопка никуда не делась.
      if (automatic && err && err.name === 'NotAllowedError') say('Ключ не подтверждён. Нажмите «Войти по passkey» или войдите паролем.')
      else if (err && err.name === 'NotAllowedError') say('Отменено или не подтверждено на устройстве.')
      else if (err && err.name === 'InvalidStateError') say('Этот ключ уже зарегистрирован.')
      else say((err && err.message) || String(err))
      b.disabled = false
    }
  })

  // После обработчика: нажатие до него ушло бы в пустоту.
  if (autoStart) { auto = true; loginBtn.click() }
})()
