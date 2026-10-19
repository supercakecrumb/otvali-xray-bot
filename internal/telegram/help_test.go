package telegram

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"html"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
)

func TestMainAndInstallationNavigationStaySimple(t *testing.T) {
	b, _, _ := stockholmTestBot(t, nil)
	if got := callbacks(helpKeyboard); !reflect.DeepEqual(got, []string{CallbackGetKey, CallbackHelpVPNSetup, CallbackHelpHowItWorks}) || len(helpKeyboard.InlineKeyboard) != 3 {
		t.Fatal("main menu must preserve original three actions")
	}
	for i, want := range []string{"🔑 Получить VPN", "⚙️ Установка VPN", "ℹ️ Как это работает"} {
		if helpKeyboard.InlineKeyboard[i][0].Text != want {
			t.Fatal("main menu copy changed")
		}
	}
	got := callbacks(b.installationKeyboard())
	want := []string{CallbackHelpVPNWindows, CallbackHelpVPNMacOS, CallbackHelpVPNAndroid, CallbackHelpVPNIOS, CallbackHelpVPNLinux, CallbackStockholmRouting, CallbackHelpBack}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("routing must live once under installation alongside OS choices")
	}
	if utf8.RuneCountInString(installationText) > 400 || !strings.Contains(installationText, "1.") || !strings.Contains(installationText, "2.") || !strings.Contains(installationText, "3.") || strings.Contains(installationText, "4.") {
		t.Fatal("installation must remain three short steps")
	}
}

func TestStaticGuidesArePrivateAndNeverIssue(t *testing.T) {
	for _, tc := range []struct {
		callback string
		handler  func(*Bot, *telego.Bot, telego.Update)
	}{
		{CallbackHelpVPNSetup, (*Bot).handleHelpCallback}, {CallbackStockholmGuide, (*Bot).handleStockholmGuide},
		{CallbackHelpVPNWindows, (*Bot).handleHelpCallback}, {CallbackHelpVPNMacOS, (*Bot).handleHelpCallback}, {CallbackHelpVPNAndroid, (*Bot).handleHelpCallback}, {CallbackHelpVPNIOS, (*Bot).handleHelpCallback}, {CallbackHelpVPNLinux, (*Bot).handleHelpCallback},
		{CallbackStockholmRouting, (*Bot).handleStockholmRoutingGuide}, {CallbackStockholmHappRouting, (*Bot).handleStockholmHappRouting}, {CallbackStockholmIncyRouting, (*Bot).handleStockholmIncyRouting},
	} {
		t.Run(tc.callback, func(t *testing.T) {
			issuer := &fakeStockholmIssuer{eligible: false}
			b, caller, _ := stockholmTestBot(t, issuer)
			update := stockholmUpdate()
			update.CallbackQuery.Data = tc.callback
			tc.handler(b, b.bot, update)
			if len(caller.calls) != 2 || caller.calls[0].method != "answerCallbackQuery" || caller.calls[1].method != "editMessageText" || len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
				t.Fatal("static help must acknowledge, stay private and avoid issuer/backend access")
			}
			text := callText(t, caller.calls[1])
			if utf8.RuneCountInString(text) > 4096 || strings.Contains(strings.ToLower(text), "hiddify") || strings.Contains(text, "curl ") {
				t.Fatal("help contains obsolete executable guidance or exceeds Telegram limit")
			}
			for _, cb := range callbacks(callKeyboard(t, caller.calls[1])) {
				if cb == CallbackStockholmSubscription || cb == CallbackGetKey {
					t.Fatal("guide must not inject credential actions")
				}
			}
			if tc.callback == CallbackStockholmGuide && text != installationText {
				t.Fatal("saved setup callback must alias current installation")
			}
			caller.calls = nil
			update.CallbackQuery.From.ID = 7
			tc.handler(b, b.bot, update)
			if len(caller.calls) != 1 || caller.calls[0].method != "answerCallbackQuery" {
				t.Fatal("forged actor must not receive private help")
			}
		})
	}
}

func TestPlatformHelpIsShortAndDistinguishesImportFormats(t *testing.T) {
	for _, text := range []string{InstructionLinux, InstructionWindows, InstructionMacOS, InstructionAndroid, InstructionIOS} {
		if utf8.RuneCountInString(text) > 600 || !strings.Contains(text, "vless://") || !strings.Contains(text, "как сервер") || !strings.Contains(text, "подписку") {
			t.Fatal("short platform guide must distinguish key and subscription")
		}
	}
	if !strings.Contains(InstructionAndroid, "Always-on VPN") || !strings.Contains(InstructionAndroid, "разрешите VPN") {
		t.Fatal("Android VPN permission guidance missing")
	}
}

func TestVPNHelpURLValidationAndAnchors(t *testing.T) {
	for _, raw := range []string{"http://guide.example", "https://user:secret@guide.example", "https://guide.example/?secret=x", "https://guide.example/#routing", "https://guide.example/?", "javascript:secret"} {
		option, err := WithVPNHelpURL(raw)
		if option != nil || err == nil || strings.Contains(err.Error(), raw) {
			t.Fatal("invalid public guide URL must fail without reflecting input")
		}
	}
	b, _, _ := stockholmTestBot(t, nil)
	option, err := WithVPNHelpURL(" https://guide.example/vpn ")
	if err != nil {
		t.Fatal(err)
	}
	option(b)
	if b.guideURL("routing") != "https://guide.example/vpn#routing" {
		t.Fatal("guide anchor mismatch")
	}
	for _, tc := range []struct{ callback, anchor string }{{CallbackHelpVPNWindows, "windows"}, {CallbackHelpVPNMacOS, "macos"}, {CallbackHelpVPNAndroid, "android"}, {CallbackHelpVPNIOS, "ios"}, {CallbackHelpVPNLinux, "linux"}} {
		keyboard := b.platformKeyboard(tc.anchor)
		if keyboard.InlineKeyboard[0][0].URL != "https://guide.example/vpn#"+tc.anchor || keyboard.InlineKeyboard[len(keyboard.InlineKeyboard)-1][0].CallbackData != CallbackHelpVPNSetup {
			t.Fatal("platform website/back navigation mismatch")
		}
	}
	option, err = WithVPNHelpURL("")
	if err != nil {
		t.Fatal(err)
	}
	option(b)
	if b.guideURL("") != "" {
		t.Fatal("empty optional guide must hide website")
	}
}

func TestHelpSetupCommandIsRegisteredAndDoesNotIssue(t *testing.T) {
	issuer := &fakeStockholmIssuer{eligible: true}
	b, caller, _ := stockholmTestBot(t, issuer)
	updates := make(chan telego.Update, 1)
	var err error
	b.bh, err = th.NewBotHandler(b.bot, updates)
	if err != nil {
		t.Fatal(err)
	}
	b.registerCommands()
	// Processing is synchronous within a middleware, so completion is signalled
	// after the actual registered handler has run, without timing/sleep polling.
	done := make(chan struct{})
	b.bh.Use(func(bot *telego.Bot, update telego.Update, next th.Handler) { next(bot, update); close(done) })
	stopped := make(chan struct{})
	go func() { b.bh.Start(); close(stopped) }()
	updates <- telego.Update{Message: &telego.Message{MessageID: 19, Chat: telego.Chat{ID: 42, Type: "private"}, From: &telego.User{ID: 42}, Text: "/help_setup", Entities: []telego.MessageEntity{{Type: "bot_command", Offset: 0, Length: 11}}}}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("registered setup command did not complete")
	}
	close(updates)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("handler failed to stop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b.bh.StopWithContext(ctx)
	if len(caller.calls) != 1 || caller.calls[0].method != "sendMessage" || callText(t, caller.calls[0]) != installationText || len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
		t.Fatal("/help_setup must directly send installation without issuing")
	}
}

func TestRoutingProfileIsOnlyShownAfterExplicitAppAction(t *testing.T) {
	for _, tc := range []struct {
		callback, scheme string
		handler          func(*Bot, *telego.Bot, telego.Update)
	}{{CallbackStockholmRouting, "", (*Bot).handleStockholmRoutingGuide}, {CallbackStockholmHappRouting, "happ", (*Bot).handleStockholmHappRouting}, {CallbackStockholmIncyRouting, "incy", (*Bot).handleStockholmIncyRouting}} {
		b, caller, _ := stockholmTestBot(t, nil)
		update := stockholmUpdate()
		update.CallbackQuery.Data = tc.callback
		tc.handler(b, b.bot, update)
		text := callText(t, caller.calls[1])
		if tc.scheme == "" {
			if strings.Contains(text, "<code>") || utf8.RuneCountInString(text) > 600 {
				t.Fatal("reading routing guide must not dump import link")
			}
			continue
		}
		start, end := strings.Index(text, "<code>"), strings.Index(text, "</code>")
		if start < 0 || end <= start {
			t.Fatal("explicit action must provide import link")
		}
		link := html.UnescapeString(text[start+6 : end])
		prefix := tc.scheme + "://routing/add/"
		if !strings.HasPrefix(link, prefix) || strings.Contains(link, "/onadd/") {
			t.Fatal("routing import must be opt-in add contract")
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, prefix))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != clientRoutingProfileJSON {
			t.Fatal("public verified profile changed in UI refactor")
		}
		var profile struct {
			GlobalProxy, DomainStrategy, FakeDNS       string
			DirectSites, DirectIp, ProxySites, ProxyIp []string
		}
		if err := json.Unmarshal(data, &profile); err != nil {
			t.Fatal(err)
		}
		if profile.GlobalProxy != "true" || profile.DomainStrategy != "IPOnDemand" || profile.FakeDNS != "false" || len(profile.ProxySites)+len(profile.ProxyIp) != 0 || !strings.Contains(string(data), "geosite:category-ru") || !strings.Contains(string(data), "geoip:ru") {
			t.Fatal("routing rules lost safe defaults")
		}
	}
}

func TestLegacyKeyCaptionPreservesKeyAndEscapesMarkdown(t *testing.T) {
	const key = "vless://00000000-0000-4000-8000-000000000001@example.org:443?security=reality&flow=xtls-rprx-vision#Finland%20test"
	text := legacyKeyCaption("Finland [A]_(test).", key)
	parts := strings.Split(text, "```")
	if len(parts) != 3 || parts[1] != key {
		t.Fatal("exact key must stay inside fenced code")
	}
	for _, outside := range []string{parts[0], parts[2]} {
		for index, char := range outside {
			if !strings.ContainsRune("_*[]()~`>#+-=|{}.!", char) {
				continue
			}
			backslashes := 0
			for before := index - 1; before >= 0 && outside[before] == '\\'; before-- {
				backslashes++
			}
			if backslashes%2 != 1 {
				t.Fatal("unescaped Markdown punctuation")
			}
		}
	}
	if !strings.Contains(text, "как сервер") || !strings.Contains(text, "/help\\_setup") || utf8.RuneCountInString(parts[2]) > 200 {
		t.Fatal("single key needs short import guidance and setup shortcut")
	}
}
