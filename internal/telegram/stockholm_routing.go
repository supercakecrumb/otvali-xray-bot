package telegram

import (
	"encoding/base64"
	"html"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// App contracts: https://www.happ.su/main/dev-docs/routing.md and
// https://docs.incy.cc/en/routing/. Geo categories use category-ru, not ru.
// DNS resolvers/geofile locations are omitted so the apps use their defaults.
const clientRoutingProfileJSON = `{"Name":"Otvali RU direct, other VPN","GlobalProxy":"true","DirectSites":["geosite:private","geosite:category-ru"],"DirectIp":["geoip:ru","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16","169.254.0.0/16","127.0.0.0/8","224.0.0.0/4","255.255.255.255","::1/128","fc00::/7","fe80::/10"],"ProxySites":[],"ProxyIp":[],"BlockSites":[],"BlockIp":[],"DomainStrategy":"IPOnDemand","FakeDNS":"false"}`

const clientRoutingGuideText = "<b>Маршрутизация</b>\n\nРоссийские сайты — напрямую, остальной трафик — через VPN. Это необязательные правила для Happ / INCY.\n\nНа устройстве «напрямую» означает ваш обычный IP; на маршруте через Москву — московский IP. Для российского сайта с иностранным IP выберите прямой зарубежный сервер и исключение Proxy.\n\nВыберите приложение для импорта правил. Подробности и проверка IP — на сайте."

func (b *Bot) routingWebsiteRow() [][]telego.InlineKeyboardButton {
	if website := b.guideURL("routing"); website != "" {
		return [][]telego.InlineKeyboardButton{tu.InlineKeyboardRow(tu.InlineKeyboardButton("📖 Подробнее на сайте").WithURL(website))}
	}
	return nil
}

func (b *Bot) handleStockholmRoutingGuide(bot *telego.Bot, update telego.Update) {
	rows := [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Правила для Happ").WithCallbackData(CallbackStockholmHappRouting)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Правила для INCY").WithCallbackData(CallbackStockholmIncyRouting)),
	}
	rows = append(rows, b.routingWebsiteRow()...)
	rows = append(rows, helpVpnBackKeyboard.InlineKeyboard...)
	b.showStockholmGuide(bot, update, CallbackStockholmRouting, clientRoutingGuideText, tu.InlineKeyboard(rows...))
}

func (b *Bot) handleStockholmHappRouting(bot *telego.Bot, update telego.Update) {
	b.showClientRoutingProfile(bot, update, "Happ", "happ", CallbackStockholmHappRouting)
}

func (b *Bot) handleStockholmIncyRouting(bot *telego.Bot, update telego.Update) {
	b.showClientRoutingProfile(bot, update, "INCY", "incy", CallbackStockholmIncyRouting)
}

func (b *Bot) showClientRoutingProfile(bot *telego.Bot, update telego.Update, appName, scheme, callback string) {
	// This opt-in configuration is public and contains no account credentials.
	link := scheme + "://routing/add/" + base64.StdEncoding.EncodeToString([]byte(clientRoutingProfileJSON))
	text := "<b>Правила для " + appName + "</b>\n\nСкопируйте ссылку и импортируйте её как профиль маршрутизации:\n\n<code>" + html.EscapeString(link) + "</code>\n\nИмпорт может включить правила после загрузки геоданных. Выберите профиль и переподключитесь. Обновите геоданные: нужны category-ru и geoip:ru. Если загрузка не удалась, не полагайтесь на обход российских сайтов."
	rows := b.routingWebsiteRow()
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Маршрутизация").WithCallbackData(CallbackStockholmRouting)), helpVpnBackKeyboard.InlineKeyboard[0])
	b.showStockholmGuide(bot, update, callback, text, tu.InlineKeyboard(rows...))
}
