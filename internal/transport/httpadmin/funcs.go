package httpadmin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"image/png"
	"strings"
	"time"
	"unicode"

	"github.com/pquerna/otp"
)

// Функции шаблонов. Всё, что связано с ПОКАЗОМ: относительное время,
// человеческие подписи к машинным кодам, инициалы. Логики здесь нет.
var funcs = template.FuncMap{
	"since":    since,
	"until":    until,
	"date":     date,
	"initials": initials,
	"bytes":    humanBytes,
	"action":   actionLabel,
	"pretty":   prettyJSON,
	"device":   deviceLabel,
	"space":    spaceLabel,
	"plural":   plural,
}

var months = [...]string{"янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

// toTime принимает и значение, и указатель: в моделях время бывает
// необязательным, а шаблону удобнее одна функция на оба случая.
func toTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, !t.IsZero()
	case *time.Time:
		if t == nil {
			return time.Time{}, false
		}
		return *t, !t.IsZero()
	}
	return time.Time{}, false
}

func since(v any) string {
	t, ok := toTime(v)
	if !ok {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "только что"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	case d < 48*time.Hour:
		return "вчера"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d дн назад", int(d.Hours()/24))
	}
	return date(t)
}

func until(v any) string {
	t, ok := toTime(v)
	if !ok {
		return "—"
	}
	d := time.Until(t)
	switch {
	case d <= 0:
		return "истёк"
	case d < time.Hour:
		return fmt.Sprintf("через %d мин", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("через %d ч", int(d.Hours()))
	}
	return fmt.Sprintf("через %d дн", int(d.Hours()/24))
}

func date(v any) string {
	t, ok := toTime(v)
	if !ok {
		return "—"
	}
	t = t.Local()
	return fmt.Sprintf("%d %s %d, %02d:%02d", t.Day(), months[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}

// initials — две буквы для аватара: по имени, если оно из двух слов,
// иначе первые две буквы того, что есть.
func initials(login, name string) string {
	words := strings.Fields(name)
	if len(words) >= 2 {
		return upperFirst(words[0]) + upperFirst(words[1])
	}
	src := name
	if src == "" {
		src = login
	}
	r := []rune(src)
	if len(r) >= 2 {
		return strings.ToUpper(string(r[:2]))
	}
	return strings.ToUpper(src)
}

func upperFirst(s string) string {
	for _, r := range s {
		return string(unicode.ToUpper(r))
	}
	return ""
}

func humanBytes(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1f ГБ", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.0f МБ", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.0f КБ", float64(n)/k)
	}
	return fmt.Sprintf("%d Б", n)
}

var actionLabels = map[string]string{
	"invite.create":        "выпустил инвайт",
	"invite.revoke":        "отозвал инвайт",
	"user.set_disabled":    "изменил блокировку",
	"user.set_role":        "изменил роль",
	"user.reset_password":  "сбросил пароль",
	"user.provision_totp":  "выпустил второй фактор",
	"user.enroll_totp":     "настроил свой второй фактор",
	"user.reset_totp":      "снял второй фактор",
	"user.add_passkey":     "добавил passkey",
	"user.remove_passkey":  "удалил passkey",
	"user.revoke_sessions": "завершил сессии",
	"user.create":          "завёл пользователя",
	"user.import_kinopub":  "перенёс из kinopub",
	"voice.merge":          "склеил озвучки",
	"voice.split":          "разъединил озвучки",
	"voice.reject":         "назвал озвучки разными",
	"voice.learn":          "свёл озвучки по транслиту",

	"session.device.approve_for": "подключил устройство",
	"session.device.approve":     "подключил себе устройство",
	"session.device.deny":        "не пустил устройство",
	"session.sign_out":           "вышел на устройстве",
	"session.sign_out_others":    "вышел на остальных устройствах",
}

func actionLabel(code string) string {
	if l, ok := actionLabels[code]; ok {
		return l
	}
	return code
}

func prettyJSON(s string) string {
	if s == "" || s == "null" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}

func deviceLabel(kind string) string {
	switch kind {
	case "desktop":
		return "компьютер"
	case "ios":
		return "iPhone / iPad"
	case "android":
		return "Android"
	case "tv":
		return "телевизор"
	case "admin-ui":
		return "админка"
	case "":
		return "—"
	}
	return kind
}

func spaceLabel(s any) string {
	switch fmt.Sprint(s) {
	case "user":
		return "витрина"
	case "admin":
		return "админка"
	case "service":
		return "служебное"
	}
	return fmt.Sprint(s)
}

// plural — «1 сессия», «2 сессии», «5 сессий».
func plural(n int, one, few, many string) string {
	a := n % 100
	b := a % 10
	switch {
	case a > 10 && a < 20:
		return fmt.Sprintf("%d %s", n, many)
	case b > 1 && b < 5:
		return fmt.Sprintf("%d %s", n, few)
	case b == 1:
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// qrDataURI рисует QR по otpauth-адресу прямо в страницу: картинка не
// должна уходить ни на диск, ни в сторонний сервис.
func qrDataURI(otpauth string) (template.URL, error) {
	key, err := otp.NewKeyFromURL(otpauth)
	if err != nil {
		return "", err
	}
	img, err := key.Image(220, 220)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())), nil
}
