package telegram

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/miniapp"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
	"github.com/supercakecrumb/otvali-xray-bot/internal/vpnmonitor"
)

type uxPreviewScreen struct {
	Text      string                          `json:"text"`
	ParseMode string                          `json:"parse_mode,omitempty"`
	Keyboard  [][]telego.InlineKeyboardButton `json:"keyboard"`
	Image     string                          `json:"image,omitempty"`
}

type uxPreviewMeta struct {
	Fictional      bool              `json:"fictional"`
	Commands       map[string]string `json:"commands"`
	CallbackRoutes map[string]string `json:"callback_routes"`
	Labels         map[string]string `json:"labels"`
}

// TestExportUXPreview is opt-in: it renders the real user-facing views and
// handlers through an in-memory Telegram transport. It never starts a bot,
// connects to a database/provider, or reads credentials from the environment.
//
//	OTVALI_UX_PREVIEW_PATH=/absolute/preview/bot-screens.json go test ./internal/telegram -run '^TestExportUXPreview$' -count=1
func TestExportUXPreview(t *testing.T) {
	outputPath := os.Getenv("OTVALI_UX_PREVIEW_PATH")
	if outputPath == "" {
		t.Skip("set OTVALI_UX_PREVIEW_PATH to export fictional user screens")
	}
	if !filepath.IsAbs(outputPath) {
		t.Fatal("the preview output must be an absolute path")
	}
	const subURL = "https://example.invalid/fictional-subscription"
	const managedID = "0123456789abcdef"
	const managedURL = "vless://00000000-0000-4000-8000-000000000001@example.invalid:443?security=reality#Fictional"
	const legacyURL = "vless://00000000-0000-4000-8000-000000000002@example.invalid:443?security=reality#Fictional%20legacy"
	keys := []remnawave.ServerKey{{ID: managedID, Name: "Stockholm · пример", URL: managedURL}}
	issuer := &fakeIndividualKeyIssuer{
		fakeStockholmIssuer: fakeStockholmIssuer{eligible: true, subscription: remnawave.Subscription{URL: subURL}},
		keys:                keys,
	}
	b, caller, _ := qrTestBot(t, issuer)
	filtered := testBotWithDisabledServer(t)
	b.sh = filtered.sh
	miniApp, err := WithMiniAppURL("https://otvali.aurorass.art", []string{"https://otvali.aurorass.art"})
	if err != nil {
		t.Fatal(err)
	}
	miniApp(b)
	guide, err := WithVPNHelpURL("https://otvali.aurorass.art")
	if err != nil {
		t.Fatal(err)
	}
	guide(b)

	screens := make(map[string]uxPreviewScreen)
	readString := func(body map[string]json.RawMessage, key string) string {
		t.Helper()
		if len(body[key]) == 0 {
			return ""
		}
		var value string
		if err := json.Unmarshal(body[key], &value); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		return value
	}
	save := func(id string) {
		t.Helper()
		if len(caller.calls) == 0 {
			t.Fatalf("screen %s was not rendered", id)
		}
		call := caller.calls[len(caller.calls)-1]
		if call.method != "sendMessage" && call.method != "editMessageText" && call.method != "sendPhoto" {
			t.Fatalf("screen %s ended with %s", id, call.method)
		}
		text := readString(call.body, "text")
		if text == "" {
			text = readString(call.body, "caption")
		}
		if text == "" {
			t.Fatalf("screen %s has no text", id)
		}
		var keyboard telego.InlineKeyboardMarkup
		if len(call.body["reply_markup"]) > 0 {
			if err := json.Unmarshal(call.body["reply_markup"], &keyboard); err != nil {
				t.Fatal(err)
			}
		}
		screen := uxPreviewScreen{Text: text, ParseMode: readString(call.body, "parse_mode"), Keyboard: keyboard.InlineKeyboard}
		if screen.Keyboard == nil {
			screen.Keyboard = [][]telego.InlineKeyboardButton{}
		}
		if call.method == "sendPhoto" {
			if len(caller.images) != 1 {
				t.Fatalf("screen %s needs one captured PNG", id)
			}
			screen.Image = "data:image/png;base64," + base64.StdEncoding.EncodeToString(caller.images[0])
		}
		screens[id] = screen
		caller.calls, caller.images = nil, nil
	}
	callback := func(data string) telego.Update {
		u := stockholmUpdate()
		u.CallbackQuery.Data = data
		return u
	}
	helpScreen := func(id, data string) {
		t.Helper()
		b.handleHelpCallback(b.bot, callback(data))
		save(id)
	}

	b.handleHelp(b.bot, qrCommand("/start"))
	save("home")
	helpScreen("help", CallbackHelpSupport)
	helpScreen("setup", CallbackHelpVPNSetup)
	helpScreen("setup_devices", CallbackHelpVPNDevices)
	for _, page := range []struct{ id, callback string }{
		{"windows", CallbackHelpVPNWindows}, {"macos", CallbackHelpVPNMacOS},
		{"android", CallbackHelpVPNAndroid}, {"ios", CallbackHelpVPNIOS}, {"linux", CallbackHelpVPNLinux},
		{"how_it_works", CallbackHelpHowItWorks}, {"telegram", CallbackHelpTelegram},
	} {
		helpScreen(page.id, page.callback)
	}
	// The "telegram" screen above is the unconfigured stub. With the private
	// provider configured, opening Telegram issues every route at once.
	proxy := proxyFixture()
	WithTelegramProxy(proxy)(b)
	b.handleTelegramProxy(b.bot, qrCommand("/telegram"))
	save("telegram_proxies")
	proxy.credentials, proxy.unavailable = proxy.credentials[:3], []string{"msk-stockholm"}
	b.handleTelegramProxyCallback(b.bot, callback(CallbackTelegramProxy))
	save("telegram_proxies_partial")
	proxy.err = telegramproxy.ErrUnavailable
	b.handleTelegramProxyCallback(b.bot, callback(CallbackTelegramProxy))
	save("telegram_proxies_error")
	b.telegramProxy = nil

	b.handleStockholmRoutingGuide(b.bot, callback(CallbackStockholmRouting))
	save("routing")
	b.handleStockholmHappRouting(b.bot, callback(CallbackStockholmHappRouting))
	save("routing_happ")
	b.handleStockholmIncyRouting(b.bot, callback(CallbackStockholmIncyRouting))
	save("routing_incy")
	b.handleStockholmHappRoutingLink(b.bot, callback(CallbackStockholmHappRoutingLink))
	save("routing_happ_link")
	b.handleStockholmIncyRoutingLink(b.bot, callback(CallbackStockholmIncyRoutingLink))
	save("routing_incy_link")

	b.handleGetKeyCallback(b.bot, callback(CallbackGetKey))
	save("access")
	b.handleStockholmSubscription(b.bot, callback(CallbackStockholmSubscription))
	save("subscription")
	b.stockholm = nil
	b.handleStockholmSubscription(b.bot, callback(CallbackStockholmSubscription))
	save("subscription_missing")
	b.stockholm = issuer
	issuer.err = remnawave.ErrInactive
	b.handleStockholmSubscription(b.bot, callback(CallbackStockholmSubscription))
	save("subscription_disabled")
	issuer.err = remnawave.ErrUnavailable
	b.handleStockholmSubscription(b.bot, callback(CallbackStockholmSubscription))
	save("subscription_error")
	issuer.err = nil

	inbound, actor := 1, int64(42)
	legacyServer := database.Server{ID: 8, Country: "Finland", City: "Helsinki", InboundID: &inbound}
	legacyServers := []database.Server{legacyServer}
	keyView := func(id string, servers []database.Server, managed []remnawave.ServerKey, legacyFailed bool, managedErr error) {
		text, keyboard := b.individualKeysView(servers, managed, legacyFailed, managedErr)
		screens[id] = uxPreviewScreen{Text: text, ParseMode: telego.ModeHTML, Keyboard: keyboard.InlineKeyboard}
	}
	keyView("keys", legacyServers, keys, false, nil)
	keyView("keys_legacy", legacyServers, nil, false, nil)
	keyView("keys_empty", nil, nil, false, nil)
	keyView("keys_error", nil, nil, true, remnawave.ErrUnavailable)
	b.handleManagedKey(b.bot, callback(CallbackManagedKeyPrefix+managedID))
	save("key_managed")
	screens["key_legacy"] = uxPreviewScreen{
		Text: legacyKeyCaption("Finland, Helsinki", legacyURL), ParseMode: telego.ModeMarkdownV2,
		Keyboard: qrKeyDeliveryKeyboard(CallbackQRLegacyPrefix + "8").InlineKeyboard,
	}
	issuer.keysErr = remnawave.ErrUnavailable
	b.handleManagedKey(b.bot, callback(CallbackManagedKeyPrefix+managedID))
	save("key_error")
	issuer.keysErr = nil

	b.qrStore = &qrLegacyStoreFake{user: &database.User{TelegramID: &actor, Username: "fictional_actor"}, server: &legacyServer}
	b.qrLegacy = &qrLegacyIssuerFake{key: legacyURL}
	b.handleQR(b.bot, qrCommand("/qr"))
	save("qr_menu")
	b.handleQRKey(b.bot, qrCommand("/qr_key"))
	save("qr_keys")
	b.handleQRSubscription(b.bot, qrCommand("/qr_subscription"))
	save("qr_subscription")
	b.handleQRCallback(b.bot, callback(CallbackQRManagedPrefix+managedID))
	save("qr_managed")
	b.handleQRCallback(b.bot, callback(CallbackQRLegacyPrefix+"8"))
	save("qr_legacy")
	issuer.err = remnawave.ErrUnavailable
	b.handleQRSubscription(b.bot, qrCommand("/qr_subscription"))
	save("qr_error")
	issuer.err = nil

	checks := []vpnmonitor.Check{
		{ID: "ru-yandex", Category: "ru", State: "ok", LatencyMS: 42},
		{ID: "ru-vk", Category: "ru", State: "ok", LatencyMS: 42},
		{ID: "world-google", Category: "world", State: "ok", LatencyMS: 42},
		{ID: "world-youtube", Category: "world", State: "ok", LatencyMS: 42, VerifiedBody: true, Attempts: 1, Successes: 1, BytesReceived: 1024},
		{ID: "world-telegram", Category: "world", State: "ok", LatencyMS: 42, VerifiedBody: true, Attempts: 1, Successes: 1, BytesReceived: 64, BytesSent: 64},
		{ID: "world-download", Category: "world", State: "ok", LatencyMS: 42, VerifiedBody: true, Attempts: 1, Successes: 1, BytesReceived: 1024},
		{ID: "world-upload", Category: "world", State: "ok", LatencyMS: 42, VerifiedBody: true, Attempts: 1, Successes: 1, BytesReceived: 64, BytesSent: 1024},
		// The ordinary-user fixture must discard this private-service sentinel.
		{ID: "own-home-assistant", Category: "own", State: "fail", ErrorCode: "timeout"},
	}
	probeTime := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	snapshot := vpnmonitor.Snapshot{Available: true, Report: vpnmonitor.Report{Version: 2, CheckedAt: probeTime, IntervalSeconds: vpnmonitor.Interval}}
	for _, id := range vpnmonitor.RouteOrder {
		kind := "vpn"
		if id == "direct" {
			kind = "direct"
		}
		snapshot.Report.Routes = append(snapshot.Report.Routes, vpnmonitor.Route{ID: id, Kind: kind, Checks: checks})
	}
	network := &fakeNetworkReader{snapshot: snapshot}
	WithNetworkStatus(network, 0)(b)
	b.handleNetworkStatusCallback(b.bot, callback(CallbackNetworkStatus))
	save("network_status")
	b.handleNetworkStatusCallback(b.bot, callback(CallbackNetworkDetails))
	save("network_details")
	for _, id := range vpnmonitor.RouteOrder {
		b.handleNetworkStatusCallback(b.bot, callback(CallbackNetworkRoute+id))
		save("route_" + id)
	}
	network.snapshot = vpnmonitor.Snapshot{}
	b.handleNetworkStatusCallback(b.bot, callback(CallbackNetworkStatus))
	save("network_empty")

	account := &fakeAccountReader{account: miniapp.Account{Managed: true, ManagedState: "ready", LegacyFinlandState: "ready", CheckedAt: probeTime, CheckScope: "account_access"}}
	WithAccountStatus(account)(b)
	b.handleAccountStatusCallback(b.bot, callback(CallbackAccountStatus))
	save("account_ready")
	account.account.ManagedState = "access_disabled"
	b.handleAccountStatusCallback(b.bot, callback(CallbackAccountStatus))
	save("account_revoked")
	b.accountStatus = nil
	b.handleAccountStatusCallback(b.bot, callback(CallbackAccountStatus))
	save("account_unavailable")

	b.handleBeta(b.bot, qrCommand("/beta"))
	save("feedback")
	b.miniAppURL = ""
	b.handleBeta(b.bot, qrCommand("/beta"))
	save("feedback_fallback")
	invite := qrCommand("/invite")
	invite.Message.From.Username = ""
	b.handleInvite(b.bot, invite)
	save("invite_usage")

	meta := uxPreviewMeta{
		Fictional: true,
		Commands: map[string]string{
			"/start": "home", "/help": "help", "/get_key": "access", "/qr": "qr_menu", "/qr_subscription": "qr_subscription",
			"/qr_key": "qr_keys", "/help_setup": "setup", "/status": "network_status", "/account": "account_ready", "/beta": "feedback", "/invite": "invite_usage",
			"/telegram": "telegram_proxies",
		},
		CallbackRoutes: map[string]string{
			CallbackHelpBack: "home", CallbackHelpSupport: "help", CallbackHelpVPNSetup: "setup", CallbackStockholmGuide: "setup", CallbackHelpVPNDevices: "setup_devices",
			CallbackHelpVPNWindows: "windows", CallbackHelpVPNMacOS: "macos", CallbackHelpVPNAndroid: "android", CallbackHelpVPNIOS: "ios", CallbackHelpVPNLinux: "linux",
			CallbackHelpHowItWorks: "how_it_works", CallbackHelpTelegram: "telegram", CallbackHelpBeta: "feedback",
			CallbackGetKey: "access", CallbackStockholmSubscription: "subscription", CallbackIndividualKeys: "keys", CallbackManagedKeyPrefix + managedID: "key_managed", "getkey_8": "key_legacy",
			CallbackStockholmRouting: "routing", CallbackStockholmHappRouting: "routing_happ", CallbackStockholmIncyRouting: "routing_incy",
			CallbackStockholmHappRoutingLink: "routing_happ_link", CallbackStockholmIncyRoutingLink: "routing_incy_link",
			CallbackQRMenu: "qr_menu", CallbackQRKeys: "qr_keys", CallbackQRSubscription: "qr_subscription", CallbackQRManagedPrefix + managedID: "qr_managed", CallbackQRLegacyPrefix + "8": "qr_legacy",
			CallbackNetworkStatus: "network_status", CallbackNetworkDetails: "network_details", CallbackAccountStatus: "account_ready",
			CallbackTelegramProxy: "telegram_proxies", CallbackTelegramProxyIssue + "finland": "telegram_proxies",
		},
		Labels: map[string]string{
			"home": "Главная", "help": "Помощь", "setup": "Установка", "setup_devices": "Выбор устройства", "windows": "Windows", "macos": "macOS", "android": "Android", "ios": "iOS", "linux": "Linux",
			"how_it_works": "Как это работает", "telegram": "Telegram", "telegram_proxies": "Прокси Telegram", "telegram_proxies_partial": "Часть прокси недоступна", "telegram_proxies_error": "Прокси недоступны", "routing": "Маршрутизация", "routing_happ": "Правила Happ", "routing_incy": "Правила INCY", "routing_happ_link": "Ссылка правил Happ", "routing_incy_link": "Ссылка правил INCY",
			"access": "Подписка и ключи", "subscription": "Подписка", "subscription_missing": "Подписка недоступна", "subscription_disabled": "Доступ отключён", "subscription_error": "Ошибка подписки",
			"keys": "Все отдельные ключи", "keys_legacy": "Прежний ключ", "keys_empty": "Ключей пока нет", "keys_error": "Список ключей недоступен", "key_managed": "Отдельный ключ", "key_legacy": "Ключ Финляндии", "key_error": "Ошибка ключа",
			"qr_menu": "QR-коды", "qr_keys": "Выбор ключа для QR", "qr_subscription": "QR подписки", "qr_managed": "QR отдельного ключа", "qr_legacy": "QR прежнего ключа", "qr_error": "Ошибка QR",
			"network_status": "Проверка VPN", "network_details": "Выбор маршрута", "network_empty": "Нет замеров",
			"account_ready": "Мой доступ", "account_revoked": "Доступ отключён", "account_unavailable": "Не удалось проверить доступ", "feedback": "Отзыв", "feedback_fallback": "Форма недоступна", "invite_usage": "Приглашение",
		},
	}
	for _, id := range vpnmonitor.RouteOrder {
		meta.CallbackRoutes[CallbackNetworkRoute+id] = "route_" + id
		meta.Labels["route_"+id] = vpnmonitor.RouteLabels[id]
	}
	homeActions := 0
	for _, row := range screens["home"].Keyboard {
		homeActions += len(row)
	}
	if homeActions != 4 {
		t.Fatal("the home preview must have exactly four actions")
	}
	for command, id := range meta.Commands {
		if _, ok := screens[id]; !ok {
			t.Fatalf("command %s targets missing screen %s", command, id)
		}
	}
	for callback, id := range meta.CallbackRoutes {
		if _, ok := screens[id]; !ok {
			t.Fatalf("callback %s targets missing screen %s", callback, id)
		}
	}
	for id, screen := range screens {
		if meta.Labels[id] == "" {
			t.Fatalf("screen %s has no navigation label", id)
		}
		if strings.Contains(screen.Text, "Home Assistant") || strings.Contains(screen.Text, "Наши сервисы") || strings.Contains(screen.Text, "own-home-assistant") {
			t.Fatalf("screen %s exposed a private service", id)
		}
		for _, row := range screen.Keyboard {
			for _, button := range row {
				if button.CallbackData != "" && meta.CallbackRoutes[button.CallbackData] == "" {
					t.Fatalf("screen %s has an unmapped callback %s", id, button.CallbackData)
				}
			}
		}
	}
	result := make(map[string]any, len(screens)+1)
	for id, screen := range screens {
		result[id] = screen
	}
	result["_meta"] = meta
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 1024*1024 {
		t.Fatal("the fictional preview must fit within 1 MiB")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(screens))
	for id := range screens {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	t.Logf("exported %d fictional screens: %s", len(ids), strings.Join(ids, ", "))
}
