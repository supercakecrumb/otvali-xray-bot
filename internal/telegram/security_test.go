package telegram

import (
	"github.com/mymmrac/telego"
	"testing"
)

func TestPrivateActorUpdate(t *testing.T) {
	for _, tc := range []struct {
		kind        string
		chat, actor int64
		want        bool
	}{{"private", 7, 7, true}, {"group", -7, 7, false}, {"private", 7, 8, false}} {
		msg := &telego.Message{Chat: telego.Chat{ID: tc.chat, Type: tc.kind}, From: &telego.User{ID: tc.actor}}
		if got := isPrivateActorUpdate(telego.Update{Message: msg}); got != tc.want {
			t.Errorf("message authorization = %v", got)
		}
		if got := isPrivateActorUpdate(telego.Update{CallbackQuery: &telego.CallbackQuery{From: telego.User{ID: tc.actor}, Message: msg}}); got != tc.want {
			t.Errorf("callback authorization = %v", got)
		}
	}
	if isPrivateActorUpdate(telego.Update{}) {
		t.Fatal("empty update accepted")
	}
}
