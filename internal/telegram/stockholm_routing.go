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

const clientRoutingGuideText = "<b>Маршрутизация</b>\n\nНеобязательно: российские сайты — напрямую, остальные — через VPN.\n\nВыберите приложение. Что означает «напрямую» и как проверить IP — в инструкции."

func (b *Bot) routingWebsiteRow() [][]telego.InlineKeyboardButton {
	if website := b.guideURL("routing"); website != "" {
		return [][]telego.InlineKeyboardButton{tu.InlineKeyboardRow(tu.InlineKeyboardButton("Инструкция").WithURL(website))}
	}
	return nil
}

func (b *Bot) handleStockholmRoutingGuide(bot *telego.Bot, update telego.Update) {
	rows := [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Happ").WithCallbackData(CallbackStockholmHappRouting), tu.InlineKeyboardButton("INCY").WithCallbackData(CallbackStockholmIncyRouting)),
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
	linkCallback := CallbackStockholmHappRoutingLink
	if scheme == "incy" {
		linkCallback = CallbackStockholmIncyRoutingLink
	}
	text := "<b>Правила для " + appName + "</b>\n\nИмпортируйте ссылку как профиль маршрутизации. После загрузки геоданных выберите профиль и переподключитесь.\n\nЕсли геоданные не загрузились, обход российских сайтов может не работать."
	rows := [][]telego.InlineKeyboardButton{tu.InlineKeyboardRow(tu.InlineKeyboardButton("Показать ссылку правил").WithCallbackData(linkCallback))}
	rows = append(rows, b.routingWebsiteRow()...)
	rows = append(rows, navigationRows(CallbackStockholmRouting)...)
	b.showStockholmGuide(bot, update, callback, text, tu.InlineKeyboard(rows...))
}

func (b *Bot) handleStockholmHappRoutingLink(bot *telego.Bot, update telego.Update) {
	b.showClientRoutingLink(bot, update, "Happ", "happ", CallbackStockholmHappRoutingLink, CallbackStockholmHappRouting)
}

func (b *Bot) handleStockholmIncyRoutingLink(bot *telego.Bot, update telego.Update) {
	b.showClientRoutingLink(bot, update, "INCY", "incy", CallbackStockholmIncyRoutingLink, CallbackStockholmIncyRouting)
}

func (b *Bot) showClientRoutingLink(bot *telego.Bot, update telego.Update, appName, scheme, callback, parent string) {
	// The unchanged public profile is revealed only after the explicit action.
	link := scheme + "://routing/add/" + base64.StdEncoding.EncodeToString([]byte(clientRoutingProfileJSON))
	text := "<b>Ссылка правил для " + appName + "</b>\n\n<code>" + html.EscapeString(link) + "</code>\n\nИмпортируйте как профиль маршрутизации, затем переподключитесь."
	b.showStockholmGuide(bot, update, callback, text, tu.InlineKeyboard(navigationRows(parent)...))
}
