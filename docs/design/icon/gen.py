"""Генератор кандидатов иконки kino. python3 gen.py — пересобирает candidates/, preview.html, compare.html."""
import pathlib, math, sys, subprocess, tempfile
HERE = pathlib.Path(__file__).parent
OUT = HERE / 'candidates'; OUT.mkdir(exist_ok=True)
GREEN, DARK, INK = '#3ddc84', '#0e1116', '#06130b'

def circ(cx, cy, r):
    return f"M{cx-r:g} {cy:g}a{r:g} {r:g} 0 1 0 {2*r:g} 0a{r:g} {r:g} 0 1 0 {-2*r:g} 0z"

# --- глифы в viewBox 0 0 64 64: функция цвета -> разметка ---
def ticket(c):
    # Билет крупнее первой версии (50×30 вместо 44×26): в 16 px треугольник
    # иначе сливался в пятно. Стуб справа отделён перфорацией.
    outline = ("M13 17H51A6 6 0 0 1 57 23V26.5A5.5 5.5 0 0 0 57 37.5V41A6 6 0 0 1 51 47"
               "H13A6 6 0 0 1 7 41V37.5A5.5 5.5 0 0 0 7 26.5V23A6 6 0 0 1 13 17Z")
    tri = "M24 23L39 32L24 41Z"
    dashes = "".join(f"M45.5 {y}h3v3h-3z" for y in (21, 26.6, 32.2, 37.8))
    return f'<path fill="{c}" fill-rule="evenodd" d="{outline}{tri}{dashes}"/>'

def ring(c):
    return (f'<circle cx="32" cy="32" r="20" fill="none" stroke="{c}" stroke-width="6.5" opacity=".3"/>'
            f'<path d="M32 12A20 20 0 1 1 12 32" fill="none" stroke="{c}" stroke-width="6.5" stroke-linecap="round"/>'
            f'<path fill="{c}" d="M26.5 22L44.5 32L26.5 42Z"/>')

def reel(c):
    a = [(32, 20.5)] + [(32 + 11.5*math.cos(math.radians(t)), 32 + 11.5*math.sin(math.radians(t))) for t in (30, 150)]
    d = circ(32, 32, 21) + circ(32, 32, 3.5) + "".join(circ(round(x, 2), round(y, 2), 5) for x, y in a)
    return f'<path fill="{c}" fill-rule="evenodd" d="{d}"/>'

def house(c):
    body = "M29.68 12.9A3.67 3.67 0 0 1 34.32 12.9L54 29V48A4 4 0 0 1 50 52H14A4 4 0 0 1 10 48V29Z"
    return f'<path fill="{c}" fill-rule="evenodd" d="{body}M27 32L40 39L27 46Z"/>'

def current(c):
    return f'<path fill="{c}" d="M21.3 13.3v37.4l29.4-18.7z"/>'

def tile(inner, bg, gid=None, rx=14):
    defs, fill = '', bg
    if gid:
        defs = (f'<defs><radialGradient id="{gid}" cx="18%" cy="-5%" r="95%">'
                f'<stop offset="0" stop-color="#1d2734"/><stop offset="1" stop-color="{DARK}"/></radialGradient></defs>')
        fill = f'url(#{gid})'
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">{defs}'
            f'<rect width="64" height="64" rx="{rx}" fill="{fill}"/>{inner}</svg>')

def mono(glyph):
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">{glyph("currentColor")}</svg>'

C = [
 ('ticket', 'Билет', 'Билет в кино с кнопкой «играть». Внутри платформы каждый просмотр — это выпущенный тикет.', ticket),
 ('ring',   'Кольцо', 'Незакрытое кольцо прогресса вокруг «играть»: продолжить с того места, где остановился.', ring),
 ('reel',   'Бобина', 'Кинобобина: слово «kino» буквально. Без «играть» — знак читается сам по себе.', reel),
 ('house',  'Дом', 'Домик, у которого дверь — кнопка «играть»: домашний кинотеатр для семьи.', house),
 ('current','Сейчас', 'Текущий знак для сравнения: зелёный квадрат с треугольником.', current),
]
for key, _, _, g in C:
    (OUT / f'{key}-dark.svg').write_text(tile(g(GREEN), DARK, gid=f'g-{key}'))
    (OUT / f'{key}-green.svg').write_text(tile(g(INK), GREEN))
    (OUT / f'{key}-mono.svg').write_text(mono(g))

CSS = '''
body{margin:0;background:#0e1116;color:#eaeef4;font:14px/1.5 -apple-system,system-ui,"Segoe UI",Roboto,sans-serif;padding:28px 32px 60px}
h1{font-size:20px;margin:0 0 6px} .lead{color:#8c95a6;margin:0 0 26px}
.cand{border-top:1px solid #262d38;padding:22px 0}
.head{display:flex;align-items:baseline;gap:14px;margin-bottom:14px} .head h2{font-size:16px;margin:0} .head p{margin:0;color:#8c95a6}
.row{display:flex;align-items:center;gap:26px;flex-wrap:wrap}
.big svg{width:128px;height:128px;display:block;filter:drop-shadow(0 10px 24px rgba(0,0,0,.45))}
.sizes{display:flex;align-items:flex-end;gap:12px;position:relative;padding-bottom:20px}
.sizes svg{display:block} .s64 svg{width:64px;height:64px} .s32 svg{width:32px;height:32px} .s16 svg{width:16px;height:16px}
.lbl{position:absolute;left:0;bottom:0;color:#8c95a6;font-size:12px}
.ctx{display:flex;flex-direction:column;gap:10px}
.brand{display:flex;align-items:center;gap:9px;font-weight:700;font-size:17px;letter-spacing:-.01em;background:#171b22;border:1px solid #262d38;border-radius:10px;padding:8px 14px 8px 10px;width:max-content}
.dot svg{width:24px;height:24px;display:block}
.tab{display:flex;align-items:center;gap:8px;background:#2b2f36;color:#d6dbe3;border-radius:8px 8px 0 0;padding:7px 12px;font-size:12px;width:max-content}
.fav svg{width:16px;height:16px;display:block} .x{color:#8c95a6;margin-left:6px}
/* сравнение двух */
.cmp{display:grid;grid-template-columns:1fr 1fr;gap:28px;margin-top:8px}
.col{background:#12161d;border:1px solid #262d38;border-radius:14px;padding:20px}
.col h2{margin:0 0 4px;font-size:18px} .col .why{color:#8c95a6;margin:0 0 18px;min-height:42px}
.line{display:flex;align-items:center;gap:18px;margin:14px 0;flex-wrap:wrap}
.cap{color:#8c95a6;font-size:12px;width:100%;margin:-6px 0 0}
.phone{position:relative;width:250px;height:150px;border-radius:14px;overflow:hidden;
  background:radial-gradient(160px 120px at 70% 10%,#3b3f6b,transparent 70%),radial-gradient(200px 160px at 10% 90%,#1e4d3b,transparent 70%),#151a2c;
  display:flex;gap:18px;padding:22px 20px;box-sizing:border-box}
.app{display:flex;flex-direction:column;align-items:center;gap:5px;font-size:10px;color:#fff;text-shadow:0 1px 2px rgba(0,0,0,.6)}
.app svg,.app i{width:54px;height:54px;border-radius:13px;display:block;box-shadow:0 4px 10px rgba(0,0,0,.35)}
.app i{background:linear-gradient(160deg,#4a5260,#2b313b)}
.tvb{width:320px;height:180px;border-radius:8px;overflow:hidden;position:relative;
  background:radial-gradient(360px 200px at 12% -10%,#203042,#0e1116 70%);display:flex;align-items:center;gap:20px;padding:0 34px;box-sizing:border-box;border:1px solid #262d38}
.tvb svg{width:92px;height:92px;flex:none}
.tvb .w{font-size:56px;font-weight:800;letter-spacing:-.03em;line-height:1}
.tvb .s{color:#8c95a6;font-size:13px;margin-top:4px}
.ttl{display:flex;align-items:center;gap:10px;background:#171b22;border:1px solid #262d38;border-radius:10px;padding:10px 14px}
.ttl svg{width:36px;height:36px}
'''

def row(key, title, idea, g):
    dark = tile(g(GREEN), DARK, gid=f'p-{key}'); green = tile(g(INK), GREEN)
    return f'''
<section class="cand"><div class="head"><h2>{title}</h2><p>{idea}</p></div>
  <div class="row"><div class="big">{dark}</div><div class="big">{green}</div>
    <div class="sizes"><div class="s64">{dark}</div><div class="s32">{dark}</div><div class="s16">{dark}</div><span class="lbl">64 · 32 · 16</span></div>
    <div class="ctx"><div class="brand"><span class="dot">{green}</span> kino</div><div class="brand"><span class="dot">{dark}</span> kino</div>
      <div class="tab"><span class="fav">{dark}</span><span>kino</span><span class="x">×</span></div></div>
  </div></section>'''

(HERE / 'preview.html').write_text(f'''<!doctype html><html lang="ru"><meta charset="utf-8"><title>Иконка kino — варианты</title><style>{CSS}</style>
<h1>Иконка kino — варианты</h1><p class="lead">Слева: на тёмной плашке и на зелёной. Дальше: тот же знак в 64, 32 и 16 пикселей, в шапке меню и во вкладке браузера.</p>
{"".join(row(k,t,i,g) for k,t,i,g in C)}</html>''')

def col(key, title, why, g):
    dark = tile(g(GREEN), DARK, gid=f'c-{key}'); green = tile(g(INK), GREEN)
    darkb = tile(g(GREEN), DARK, gid=f'b-{key}')
    return f'''<div class="col"><h2>{title}</h2><p class="why">{why}</p>
  <div class="line"><div class="big">{dark}</div><div class="big">{green}</div>
    <div class="sizes"><div class="s64">{dark}</div><div class="s32">{dark}</div><div class="s16">{dark}</div><span class="lbl">64 · 32 · 16</span></div></div>
  <div class="line"><div class="phone"><div class="app">{dark}kino</div><div class="app"><i></i>Фото</div><div class="app"><i></i>Почта</div></div>
    <div class="phone"><div class="app">{green}kino</div><div class="app"><i></i>Фото</div><div class="app"><i></i>Почта</div></div>
    <p class="cap">Экран телефона: тёмная и зелёная плашка рядом с чужими иконками.</p></div>
  <div class="line"><div class="tab"><span class="fav">{dark}</span><span>kino</span><span class="x">×</span></div>
    <div class="tab"><span class="fav">{green}</span><span>kino</span><span class="x">×</span></div>
    <div class="brand"><span class="dot">{green}</span> kino</div><div class="brand"><span class="dot">{dark}</span> kino</div>
    <p class="cap">Вкладка браузера (16 px) и шапка меню (24 px).</p></div>
  <div class="line"><div class="tvb">{darkb}<div><div class="w">kino</div><div class="s">семейный кинозал</div></div></div>
    <p class="cap">Баннер Android TV 320×180: так приложение выглядит в списке на телевизоре.</p></div>
</div>'''

two = [c for c in C if c[0] in ('ticket', 'ring')]
(HERE / 'compare.html').write_text(f'''<!doctype html><html lang="ru"><meta charset="utf-8"><title>Билет или кольцо</title><style>{CSS}</style>
<h1>Билет или кольцо</h1><p class="lead">Оба знака доведены: билет крупнее и с большим треугольником, у кольца толще обод. Каждый — в тех местах, где иконка живёт на самом деле.</p>
<div class="cmp">{"".join(col(k,t,i,g) for k,t,i,g in two)}</div></html>''')
print('ok')

def export(dest: pathlib.Path):
    """Боевые файлы для web/apps/web/public. Нужен rsvg-convert (brew install librsvg)."""
    dest.mkdir(parents=True, exist_ok=True)
    glyph = ticket(INK)
    rounded = tile(glyph, GREEN)            # скруглённая плашка, прозрачные углы
    flat = tile(glyph, GREEN, rx=0)         # во весь квадрат: iOS скругляет сам
    # Maskable: платформа режет свою форму, гарантирован только круг 80% —
    # глиф уменьшен, чтобы углы билета не попали под нож.
    safe = tile(f'<g transform="translate(32 32) scale(.8) translate(-32 -32)">{glyph}</g>', GREEN, rx=0)
    (dest / 'icon.svg').write_text(rounded)
    # Админка рендерится на сервере и живёт своей статикой: та же копия туда.
    admin = HERE.parents[2] / 'internal' / 'transport' / 'httpadmin' / 'static' / 'icon.svg'
    admin.write_text(rounded)
    jobs = [('apple-touch-icon.png', flat, 180), ('icon-192.png', rounded, 192),
            ('icon-512.png', rounded, 512), ('icon-maskable-512.png', safe, 512)]
    with tempfile.TemporaryDirectory() as td:
        for name, svg, px in jobs:
            src = pathlib.Path(td) / (name[:-4] + '.svg'); src.write_text(svg)
            subprocess.run(['rsvg-convert', '-w', str(px), '-h', str(px), str(src), '-o', str(dest / name)], check=True)
    print('exported to', dest)

def export_android(res: pathlib.Path):
    """Иконки и заставка Android-оболочки (web/apps/android).

    Adaptive icon: фон — сплошной зелёный, передний план — глиф на холсте
    108dp, уменьшенный так, чтобы углы билета остались внутри круга 66dp,
    который лаунчер показывает при любой форме маски. Квадрат и круг —
    для Android 7, где adaptive-иконок ещё нет. Знак заставки — та же
    плашка, что фавикон.
    """
    glyph = ticket(INK)
    dens = {'mdpi': 1, 'hdpi': 1.5, 'xhdpi': 2, 'xxhdpi': 3, 'xxxhdpi': 4}
    fg = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 108 108">'
          f'<g transform="translate(25.84 25.84) scale(.88)">{glyph}</g></svg>')
    legacy = tile(glyph, GREEN)
    circle = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><circle cx="32" cy="32" r="32" fill="{GREEN}"/>'
              f'<g transform="translate(32 32) scale(.86) translate(-32 -32)">{glyph}</g></svg>')
    with tempfile.TemporaryDirectory() as td:
        def png(svg, px, out):
            src = pathlib.Path(td) / 'x.svg'; src.write_text(svg)
            out.parent.mkdir(parents=True, exist_ok=True)
            subprocess.run(['rsvg-convert', '-w', str(px), '-h', str(px), str(src), '-o', str(out)], check=True)
        for d, k in dens.items():
            png(fg, round(108 * k), res / f'mipmap-{d}' / 'ic_launcher_foreground.png')
            png(legacy, round(48 * k), res / f'mipmap-{d}' / 'ic_launcher.png')
            png(circle, round(48 * k), res / f'mipmap-{d}' / 'ic_launcher_round.png')
            png(legacy, round(96 * k), res / f'drawable-{d}' / 'splash_mark.png')
    # Баннер полки Android TV: 320×180 в xhdpi, знак и имя на фоне витрины.
    mark = tile(glyph, GREEN)
    inner = mark[mark.index('>') + 1:-len('</svg>')]
    banner = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 320 180">'
              f'<rect width="320" height="180" fill="{DARK}"/>'
              f'<svg x="44" y="42" width="96" height="96" viewBox="0 0 64 64">{inner}</svg>'
              f'<text x="164" y="112" font-family="Helvetica Neue, Helvetica, Arial, sans-serif" font-weight="700" '
              f'font-size="62" fill="#eaeef4" letter-spacing="-2">kino</text></svg>')
    with tempfile.TemporaryDirectory() as td:
        src = pathlib.Path(td) / 'banner.svg'; src.write_text(banner)
        out = res / 'drawable-xhdpi' / 'banner.png'; out.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(['rsvg-convert', '-w', '320', '-h', '180', str(src), '-o', str(out)], check=True)
    # Иконка уведомлений: Android красит её в один цвет и берёт только альфу,
    # поэтому белый глиф без плашки — иначе в шторке было бы пятно.
    import re as _re
    d = _re.search(r'd="([^"]+)"', ticket('#fff')).group(1)
    (res / 'drawable' / 'ic_stat_kino.xml').write_text(
        '<?xml version="1.0" encoding="utf-8"?>\n'
        '<vector xmlns:android="http://schemas.android.com/apk/res/android"\n'
        '    android:width="24dp" android:height="24dp" android:viewportWidth="64" android:viewportHeight="64">\n'
        f'    <path android:fillColor="#FFFFFFFF" android:fillType="evenOdd" android:pathData="{d}"/>\n'
        '</vector>\n')
    (res / 'values' / 'ic_launcher_background.xml').write_text(
        '<?xml version="1.0" encoding="utf-8"?>\n<resources>\n'
        f'    <color name="ic_launcher_background">{GREEN}</color>\n</resources>\n')
    print('android res ->', res)

if __name__ == '__main__' and '--android' in sys.argv:
    export_android(HERE.parents[2] / 'web' / 'apps' / 'android' / 'android' / 'app' / 'src' / 'main' / 'res')

if __name__ == '__main__' and '--export' in sys.argv:
    export(HERE.parents[2] / 'web' / 'apps' / 'web' / 'public')
