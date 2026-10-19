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
const clientRoutingProfileJSON = `{"Name":"Otvali RU direct, other VPN","GlobalProxy":"true","DirectSites":["geosite:private","geosite:category-ru"],"DirectIp":["geoip:ru","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16","169.254.0.0/16","127.0.0.0/8","224.0.0.0/4","255.255.255.255","::1/128","fc00::/7","fe80::/10"],"ProxySites":[],"ProxyIp":[],"BlockSites":[],"BlockIp":[],"DomainStrategy":"IPIfNonMatch","FakeDNS":"false"}`

const clientRoutingGuideText = "<b>Client routing: RU direct, other traffic VPN</b>\n\n" +
	"This optional Happ / INCY profile sends Russian domain categories, Russian IPs and local/private addresses directly. Traffic that does not match goes through your selected VPN server.\n\n" +
	"<b>Direct on your device</b> uses your regular Internet connection and ISP IP. <b>Direct on a Moscow server</b> uses the Moscow server’s IP for Russian traffic that reaches it. These are separate routing decisions.\n\n" +
	"Choose your app below, then copy and import its routing link only if you want these rules. Reading this guide does not activate them. Importing can activate the profile after its geo files are ready. Select the imported profile, enable its routing for the subscription, and reconnect to apply changes.\n\n" +
	"<b>Your own rules:</b> add a domain or IP/CIDR to Direct to bypass the VPN, or Proxy to use the selected VPN. Avoid conflicting entries in both lists. Keep unmatched traffic set to Proxy/VPN. Refresh geo data, reconnect, then test a Russian site and a foreign site. Domain/IP categories are not a guarantee for every site.\n\n" +
	"Happ JSON subscriptions can prevent manual routing imports or edits; use a standard VLESS/base64 subscription for custom routing. Keep your existing Finland profile for connection fallback."

func (b *Bot) handleStockholmRoutingGuide(bot *telego.Bot, update telego.Update) {
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Happ routing profile").WithCallbackData(CallbackStockholmHappRouting)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("INCY routing profile").WithCallbackData(CallbackStockholmIncyRouting)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("📖 Setup guide").WithCallbackData(CallbackStockholmGuide)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Get my subscription").WithCallbackData(CallbackStockholmSubscription)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Choose server").WithCallbackData(CallbackGetKey)),
	)
	b.showStockholmGuide(bot, update, CallbackStockholmRouting, clientRoutingGuideText, keyboard)
}

func (b *Bot) handleStockholmHappRouting(bot *telego.Bot, update telego.Update) {
	b.showClientRoutingProfile(bot, update, "Happ", "happ", CallbackStockholmHappRouting, "https://www.happ.su/main/dev-docs/routing")
}

func (b *Bot) handleStockholmIncyRouting(bot *telego.Bot, update telego.Update) {
	b.showClientRoutingProfile(bot, update, "INCY", "incy", CallbackStockholmIncyRouting, "https://docs.incy.cc/en/routing/")
}

func (b *Bot) showClientRoutingProfile(bot *telego.Bot, update telego.Update, appName, scheme, callback, docsURL string) {
	// This public configuration contains no subscription, account or server keys.
	link := scheme + "://routing/add/" + base64.StdEncoding.EncodeToString([]byte(clientRoutingProfileJSON))
	text := "<b>" + appName + ": RU direct, other traffic VPN</b>\n\n" +
		"Copy the routing link below and import it through your app’s routing-profile clipboard/import option. Select the subscription to which it should apply. Importing can activate it once geo files are ready. Select the imported profile, enable its routing, and reconnect. A profile with the same name may be updated.\n\n" +
		"<code>" + html.EscapeString(link) + "</code>\n\n" +
		"Russian domains/IPs and private networks go Direct; unmatched traffic goes through the VPN. DNS resolver settings use app defaults; this profile turns FakeDNS off.\n\n" +
		"For your own exceptions, edit Direct / Proxy domains or IP/CIDR lists, avoid conflicting rules, and reconnect after saving. If Happ locks routing for a JSON subscription, use the standard VLESS/base64 format instead.\n\n" +
		"Test a Russian site and a foreign site after reconnecting. This routing link is not your private subscription URL; keep the subscription URL to yourself."
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton(appName+": official routing docs").WithURL(docsURL)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Routing guide").WithCallbackData(CallbackStockholmRouting)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Get my subscription").WithCallbackData(CallbackStockholmSubscription)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Choose server").WithCallbackData(CallbackGetKey)),
	)
	b.showStockholmGuide(bot, update, callback, text, keyboard)
}
