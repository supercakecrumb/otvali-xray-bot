package telegram

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
)

func TestPlatformHelpDistinguishesServerKeysAndSubscriptions(t *testing.T) {
	for _, callback := range []string{CallbackHelpVPNLinux, CallbackHelpVPNWindows, CallbackHelpVPNAndroid, CallbackHelpVPNIOS, CallbackHelpVPNMacOS} {
		t.Run(callback, func(t *testing.T) {
			// Help keeps working without an issuer or any legacy database access.
			b, caller, _ := stockholmTestBot(t, nil)
			update := stockholmUpdate()
			update.CallbackQuery.Data = callback
			b.handleHelpCallback(b.bot, update)
			if len(caller.calls) != 2 || caller.calls[0].method != "editMessageText" || caller.calls[1].method != "answerCallbackQuery" {
				t.Fatal("platform help must render and acknowledge its existing callback")
			}
			var text, mode string
			json.Unmarshal(caller.calls[0].body["text"], &text)
			json.Unmarshal(caller.calls[0].body["parse_mode"], &mode)
			if mode != telego.ModeHTML || len(text) > 4096 {
				t.Fatal("platform instructions must be bounded HTML")
			}
			for _, wanted := range []string{"Happ", "INCY", "https://happ.info/", "https://github.com/INCY-DEV/incy-platforms", "vless://", "https://", "<b>server</b>", "<b>subscription</b>", "Finland"} {
				if !strings.Contains(text, wanted) {
					t.Fatalf("missing import distinction or official source: %s", wanted)
				}
			}
			for _, obsolete := range []string{"hiddify", "curl ", "| bash", "brew install", "sudo "} {
				if strings.Contains(strings.ToLower(text), obsolete) {
					t.Fatal("obsolete client recommendation or executable installer in help")
				}
			}
			if callback == CallbackHelpVPNAndroid && (!strings.Contains(text, "Always-on VPN") || !strings.Contains(text, "before the server connection") || !strings.Contains(text, "approve the system VPN prompt")) {
				t.Fatal("Android permission troubleshooting must address the local VPN interface")
			}
			var keyboard telego.InlineKeyboardMarkup
			json.Unmarshal(caller.calls[0].body["reply_markup"], &keyboard)
			if len(keyboard.InlineKeyboard) != 1 || keyboard.InlineKeyboard[0][0].CallbackData != CallbackHelpVPNSetup {
				t.Fatal("ordinary platform help navigation changed or leaked an eligibility-only guide")
			}
		})
	}
}

func TestHelpSubscriptionNavigationRequiresEligiblePrivateActor(t *testing.T) {
	for _, name := range []string{"eligible", "unapproved", "disabled", "group", "other_actor"} {
		t.Run(name, func(t *testing.T) {
			issuer := &fakeStockholmIssuer{eligible: name != "unapproved"}
			b, _, _ := stockholmTestBot(t, issuer)
			update := stockholmUpdate()
			switch name {
			case "disabled":
				b.stockholm = nil
			case "group":
				update.CallbackQuery.Message = &telego.Message{Chat: telego.Chat{ID: -42, Type: "group"}}
			case "other_actor":
				update.CallbackQuery.From.ID = 7
			}
			before, _ := json.Marshal(helpVpnBackKeyboard)
			got := b.withSubscriptionGuides(update, helpVpnBackKeyboard)
			var callbacks []string
			for _, row := range got.InlineKeyboard {
				for _, button := range row {
					callbacks = append(callbacks, button.CallbackData)
				}
			}
			want := []string{CallbackHelpVPNSetup}
			if name == "eligible" {
				want = []string{CallbackStockholmGuide, CallbackStockholmRouting, CallbackHelpVPNSetup}
			}
			if !reflect.DeepEqual(callbacks, want) || len(issuer.issueIDs) != 0 {
				t.Fatal("guide navigation must preserve authorization and never issue credentials")
			}
			after, _ := json.Marshal(helpVpnBackKeyboard)
			if string(before) != string(after) {
				t.Fatal("shared help keyboard was mutated")
			}
			if name != "eligible" && name != "unapproved" && len(issuer.eligibleIDs) != 0 {
				t.Fatal("non-private or disabled navigation reached eligibility backend")
			}
		})
	}
}

func TestLegacyKeyCaptionPreservesKeyAndEscapesMarkdown(t *testing.T) {
	const key = "vless://00000000-0000-4000-8000-000000000001@example.org:443?security=reality&flow=xtls-rprx-vision#Finland%20test"
	text := legacyKeyCaption("Finland [A]_(test).", key)
	parts := strings.Split(text, "```")
	if len(parts) != 3 || parts[1] != key {
		t.Fatal("the exact server key must remain inside its code block")
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
				t.Fatalf("unescaped MarkdownV2 punctuation outside the key: %q", char)
			}
		}
	}
	if !strings.Contains(text, "Happ or INCY") || !strings.Contains(text, "as a server") || !strings.Contains(text, "not a subscription URL") || strings.Contains(strings.ToLower(text), "hiddify") {
		t.Fatal("legacy delivery must describe a single-server import in the supported apps")
	}
}
