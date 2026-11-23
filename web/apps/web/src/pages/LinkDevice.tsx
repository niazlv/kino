import { DeviceLink } from '../components/DeviceLink'

/**
 * Подтверждение входа устройства по ссылке из QR.
 *
 * Телевизор или приложение показали QR; человек навёл камеру телефона, где
 * уже вошёл, и решает здесь, пускать ли устройство в свой аккаунт. Без кода
 * в адресе — то же поле, что в профиле: ввести код руками.
 */
export function LinkDevice({ code }: { code: string }) {
  return (
    <div className="devlink">
      <h1>Подключить устройство</h1>
      {!code && <p className="muted">Введите код, который показало устройство.</p>}
      {/* key: новый код из адреса — новая проверка с чистого листа. */}
      <DeviceLink key={code} code={code} />
      <p className="muted small">
        Подключённые устройства и выход на них — в <a className="lnk" href="#/profile">профиле</a>.
      </p>
    </div>
  )
}
