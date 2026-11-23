#!/usr/bin/env python3
"""Переносит токен из экспорта сессии утилиты в .env.

Экспорт НЕ зашифрован, поэтому файл читается и тут же удаляется вызывающим.
Значения не печатаются — только имена записанных ключей.

Собственная сессия утилиты (`kinopub login --qr`, источник device) едет
целиком — с refresh-токеном и ключами клиента: ядро и обходчик продлевают
её сами. Сессию, заимствованную у приложения на телефоне (`login --app`),
продлевать нельзя — ротация разлогинила бы телефон, — поэтому едет только
access-токен, и когда он истечёт, админка покажет «нужна переавторизация».
"""
import json
import sys
from pathlib import Path

src, dst = Path(sys.argv[1]), Path(sys.argv[2])
sess = json.loads(src.read_text()).get("session", {})

own = sess.get("app_token_source") == "device"
renewable = own and all(sess.get(k) for k in ("app_refresh_token", "app_client_id", "app_client_secret"))
expires = sess.get("app_token_expires_at") or ""
if expires.startswith("0001-01-01"):
    expires = ""  # нулевое время Go — «срок не сказан»

mapping = {
    "KINOPUB_TOKEN": sess.get("app_token", ""),
    "KINOPUB_API_BASE": sess.get("api_base", ""),
    "KINOPUB_USER_AGENT": sess.get("app_user_agent", ""),
    # Пустые значения стирают прежние ключи: продлевать новую сессию
    # refresh-токеном от прошлой бессмысленно.
    "KINOPUB_REFRESH_TOKEN": sess.get("app_refresh_token", "") if renewable else "",
    "KINOPUB_CLIENT_ID": sess.get("app_client_id", "") if renewable else "",
    "KINOPUB_CLIENT_SECRET": sess.get("app_client_secret", "") if renewable else "",
    "KINOPUB_TOKEN_EXPIRES_AT": expires,
}
if not mapping["KINOPUB_TOKEN"]:
    sys.exit("в экспорте нет app_token — нужна сессия API, а не cookie")

lines = dst.read_text().splitlines() if dst.exists() else []
kept = [l for l in lines if not any(l.startswith(k + "=") for k in mapping)]
kept += [f"{k}={v}" for k, v in mapping.items() if v]
dst.write_text("\n".join(kept) + "\n")
print("записаны ключи:", ", ".join(k for k, v in mapping.items() if v))
if not renewable:
    why = ("сессия заимствована у приложения на телефоне" if not own
           else "в экспорте нет refresh-токена или ключей клиента")
    print(f"ВНИМАНИЕ: продлеваться сама не будет — {why}.\n"
          "Для автопродления нужна собственная сессия: kinopub login --qr, затем make kinopub-token.\n"
          "Отданную серверу сессию на Mac больше не продлевать: refresh одноразовый.",
          file=sys.stderr)
