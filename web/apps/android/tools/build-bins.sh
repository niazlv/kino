#!/usr/bin/env bash
#
# Собирает исполняемые файлы, которые APK носит с собой как «библиотеки»:
#   libkinopub.so — утилита kinopub (соседний репозиторий kinopub-downloader),
#   libffmpeg.so  — минимальный ffmpeg под arm64 из официальных исходников:
#                   только сшивка (-c copy) TS → MKV/MP4, около 3 МБ.
#   libkinoengine.so — локальный движок платформы (cmd/engine, ADR-0015): режим
#                   «на этом устройстве» работает без сервера. Лежит в нативном
#                   android/, а не в старой оболочке — см. KINO_JNILIBS ниже.
#
# Android запускает исполняемые файлы только из каталога нативных библиотек,
# отсюда имена и расположение. Результат — в jniLibs/arm64-v8a, в git не
# попадает: это артефакты сборки, источник — этот скрипт.
#
# Нужны: Go, Android NDK (r27), git. Запуск:
#   ./web/apps/android/tools/build-bins.sh            — оба
#   ./web/apps/android/tools/build-bins.sh kinopub    — только утилита
#   ./web/apps/android/tools/build-bins.sh ffmpeg     — только ffmpeg
#   ./web/apps/android/tools/build-bins.sh engine     — только локальный движок
#                                                       (all его не собирает: он
#                                                       нужен не каждой сборке)
# KINO_JNILIBS=<каталог> — куда класть результат; для engine по умолчанию
# android/app/src/main/jniLibs/arm64-v8a нативного приложения.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
# Личные значения сборки (запасной токен площадки и т. п.) — из
# deploy/prod/local.env, в git его нет; заданное в окружении главнее.
# В публичных исходниках deploy/prod нет — тогда только окружение.
if [[ -f "$ROOT/deploy/prod/local-env.sh" ]]; then . "$ROOT/deploy/prod/local-env.sh"; fi
TOOL=${KINOPUB_SRC:-"$ROOT/../kinopub-downloader"}
# KINO_JNILIBS=<каталог> — куда класть утилиту и ffmpeg (по умолчанию — старая
# оболочка; нативному android/ и автосборке — android/app/src/main/jniLibs/arm64-v8a).
OUT=${KINO_JNILIBS:-"$ROOT/web/apps/android/android/app/src/main/jniLibs/arm64-v8a"}
CACHE=${KINO_BUILD_CACHE:-"$HOME/Library/Caches/kino-android"}
NDK=${ANDROID_NDK:-$(ls -d "${ANDROID_HOME:-$HOME/Library/Android/sdk}"/ndk/27.* 2>/dev/null | sort -V | tail -1)}
FFMPEG_TAG=${FFMPEG_TAG:-n7.1}
WHAT=${1:-all}

say() { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
die() { printf '\033[31mОШИБКА: %s\033[0m\n' "$*" >&2; exit 1; }
mkdir -p "$OUT" "$CACHE"

if [[ "$WHAT" == all || "$WHAT" == kinopub ]]; then
  say "kinopub -> libkinopub.so"
  [[ -f "$TOOL/go.mod" ]] || die "нет утилиты в $TOOL (KINOPUB_SRC=… чтобы указать)"
  VER=$(git -C "$TOOL" describe --tags --always --dirty 2>/dev/null || echo dev)
  # GOOS=android, а не linux: Android режет faccessat2 через seccomp.
  # cgo (clang из NDK): имена резолвит система — getaddrinfo через netd с
  # частным DNS и VPN телефона. Без cgo утилита спрашивает публичный DNS
  # напрямую, а он у части сетей закрыт — загрузки молча падали.
  [[ -d "$NDK" ]] || die "нет Android NDK (ANDROID_NDK=…)"
  CC_ANDROID="$NDK/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android24-clang"
  # Автосборка (GitHub Actions) — Linux: у NDK там свой каталог.
  [[ -x "$CC_ANDROID" ]] || CC_ANDROID="$NDK/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android24-clang"
  (cd "$TOOL" && CGO_ENABLED=1 CC="$CC_ANDROID" GOOS=android GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w -X main.version=$VER -extldflags=-Wl,-z,max-page-size=16384" \
    -o "$OUT/libkinopub.so" ./cmd/kinopub)
  echo "  $VER, $(du -h "$OUT/libkinopub.so" | cut -f1)"
fi

if [[ "$WHAT" == engine ]]; then
  say "cmd/engine -> libkinoengine.so"
  ENGINE_OUT=${KINO_JNILIBS:-"$ROOT/android/app/src/main/jniLibs/arm64-v8a"}
  mkdir -p "$ENGINE_OUT"
  [[ -d "$NDK" ]] || die "нет Android NDK (ANDROID_NDK=…)"
  VER=$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)
  CC_ANDROID="$NDK/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android24-clang"
  [[ -x "$CC_ANDROID" ]] || CC_ANDROID="$NDK/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android24-clang"
  # Тот же рецепт, что у утилиты, и по тем же причинам: GOOS=android (seccomp
  # режет faccessat2 у linux), cgo — имена резолвит система (netd, частный DNS,
  # VPN), а не публичные серверы напрямую; 16 КБ — страница памяти новых
  # телефонов. SQLite — modernc, чистый Go: cgo нужен только сети. replace на
  # ../kinopub-downloader в go.mod берёт утилиту из соседнего репозитория.
  (cd "$ROOT" && CGO_ENABLED=1 CC="$CC_ANDROID" GOOS=android GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w -X github.com/niazleushkin/kino/providers/lordfilm.defaultToken=${LORDFILM_ALLOHA_DEFAULT_TOKEN:-} -extldflags=-Wl,-z,max-page-size=16384" \
    -o "$ENGINE_OUT/libkinoengine.so" ./cmd/engine)
  TC="$NDK/toolchains/llvm/prebuilt/darwin-x86_64"
  [[ -d "$TC" ]] || TC="$NDK/toolchains/llvm/prebuilt/linux-x86_64"
  while read -r align; do
    (( align >= 0x4000 )) || die "сегмент движка выровнен на $align, а нужно не меньше 16 КБ"
  done < <("$TC/bin/llvm-readelf" -lW "$ENGINE_OUT/libkinoengine.so" | awk '$1 == "LOAD" { print $NF }')
  echo "  $VER, $(du -h "$ENGINE_OUT/libkinoengine.so" | cut -f1)"
  OUT=$ENGINE_OUT
fi

if [[ "$WHAT" == all || "$WHAT" == ffmpeg ]]; then
  say "ffmpeg $FFMPEG_TAG -> libffmpeg.so"
  [[ -d "$NDK" ]] || die "нет Android NDK (ANDROID_NDK=…)"
  TC="$NDK/toolchains/llvm/prebuilt/darwin-x86_64"
  [[ -d "$TC" ]] || TC="$NDK/toolchains/llvm/prebuilt/linux-x86_64"
  SRC="$CACHE/ffmpeg-$FFMPEG_TAG"
  [[ -d "$SRC/.git" ]] || git clone --depth 1 --branch "$FFMPEG_TAG" https://git.ffmpeg.org/ffmpeg.git "$SRC"
  cd "$SRC"
  # Только то, что нужно сшивке: демуксеры входов, муксеры выходов, парсеры
  # и битстрим-фильтры; декодеры звука и видео — потому что без них ffmpeg
  # не узнаёт частоту и каналы дорожки и не пишет заголовок контейнера.
  ./configure --target-os=android --arch=aarch64 --enable-cross-compile --enable-pic \
    --cc="$TC/bin/aarch64-linux-android24-clang" --cxx="$TC/bin/aarch64-linux-android24-clang++" \
    --ar="$TC/bin/llvm-ar" --nm="$TC/bin/llvm-nm" --ranlib="$TC/bin/llvm-ranlib" --strip="$TC/bin/llvm-strip" \
    --sysroot="$TC/sysroot" \
    --disable-everything --disable-programs --enable-ffmpeg --disable-doc --disable-debug \
    --disable-network --disable-autodetect --disable-shared --enable-static --enable-small --pkg-config=false \
    --disable-avdevice --disable-postproc --disable-iconv --disable-zlib --disable-bzlib --disable-lzma \
    --enable-protocol=file,pipe --enable-demuxer=mpegts,mov,matroska,srt,webvtt,concat \
    --enable-muxer=matroska,mp4,mov,srt,webvtt \
    --enable-parser=h264,hevc,aac,ac3,mpegaudio,opus,av1,vp9 \
    --enable-bsf=aac_adtstoasc,h264_mp4toannexb,hevc_mp4toannexb,extract_extradata,dump_extradata,setts \
    --enable-decoder=aac,h264,hevc,ac3,mp3,opus,srt,subrip,webvtt,text,mov_text \
    --enable-encoder=srt,subrip,webvtt,mov_text,text \
    --extra-cflags="-Os -fPIE" --extra-ldflags="-fPIE -pie -Wl,-z,max-page-size=16384" \
    > "$CACHE/ffmpeg-configure.log" 2>&1 \
    || die "configure не прошёл, см. $CACHE/ffmpeg-configure.log"
  make -j"$(sysctl -n hw.ncpu 2>/dev/null || nproc)" > "$CACHE/ffmpeg-make.log" 2>&1 \
    || die "make не прошёл, см. $CACHE/ffmpeg-make.log"
  "$TC/bin/llvm-strip" ffmpeg
  # Есть телефоны со страницей памяти 16 КБ (Android 15+), и там файл с
  # сегментами, выровненными на 4 КБ, не запускается: склейка падала бы
  # после каждой загрузки. NDK r27 по умолчанию выравнивает на 4 КБ — отсюда
  # max-page-size выше и эта проверка. Утилите kinopub флаг не нужен: Go
  # сам выравнивает её на 64 КБ.
  while read -r align; do
    (( align >= 0x4000 )) || die "сегмент ffmpeg выровнен на $align, а нужно не меньше 16 КБ"
  done < <("$TC/bin/llvm-readelf" -lW ffmpeg | awk '$1 == "LOAD" { print $NF }')
  cp ffmpeg "$OUT/libffmpeg.so"
  echo "  $(du -h "$OUT/libffmpeg.so" | cut -f1)"
fi

printf '\n\033[32mГотово: %s\033[0m\n' "$OUT"
