#!/usr/bin/env bash
#
# Неподписанный IPA: сборка Release под устройство без подписи и упаковка в
# Payload/Kino.app. Ставится через AltStore или Sideloadly своим Apple ID
# (бесплатный аккаунт — на 7 дней); подписанный IPA требует Apple Developer.
#
#   ios/scripts/build-unsigned-ipa.sh [выходной.ipa]
#
# Адрес сервера по умолчанию — KINO_URL в окружении или ios/Config/Local.xcconfig;
# пусто — приложение спросит адрес при первом запуске.
set -euo pipefail

IOS="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT=${1:-"$IOS/build/kino-unsigned.ipa"}
case "$OUT" in /*) ;; *) OUT="$PWD/$OUT" ;; esac
WORK=$(mktemp -d)
trap 'rm -rf -- "$WORK"' EXIT

args=(
	-project "$IOS/Kino.xcodeproj" -scheme Kino -configuration Release
	-sdk iphoneos -destination 'generic/platform=iOS'
	-archivePath "$WORK/Kino.xcarchive" -derivedDataPath "$WORK/dd"
	CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO CODE_SIGN_IDENTITY="" DEVELOPMENT_TEAM=""
)
[[ -z "${KINO_URL:-}" ]] || args+=("KINO_URL=${KINO_URL}")

xcodebuild "${args[@]}" archive -quiet

APP=$(find "$WORK/Kino.xcarchive/Products/Applications" -maxdepth 1 -name '*.app' | head -1)
[[ -d "$APP" ]] || { echo "ОШИБКА: в архиве нет .app" >&2; exit 1; }
mkdir -p "$WORK/ipa/Payload" "$(dirname "$OUT")"
cp -R "$APP" "$WORK/ipa/Payload/"
(cd "$WORK/ipa" && zip -qry "$OUT" Payload)
echo "IPA: $OUT ($(du -h "$OUT" | cut -f1)), $(defaults read "$APP/Info.plist" CFBundleShortVersionString 2>/dev/null || echo '?')"
