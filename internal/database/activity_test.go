package database

import (
	"strings"
	"testing"
)

func TestActivityRejectsArbitraryNamesAndPayloads(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, item := range []struct{ source, kind, event string }{
		{"telegram", "vless://secret", id}, {"telegram", "message", "secret"}, {"private-source", "message", id}, {"telegram", "miniapp_key_response", id}, {"miniapp", "key_delivered", id},
	} {
		if ValidActivity(item.source, item.kind, item.event) {
			t.Fatal("activity accepted unbounded or misplaced data")
		}
	}
	if !ValidActivity("telegram", "key_delivered", id) || !ValidActivity("miniapp", "miniapp_key_response", id) {
		t.Fatal("supported credential counters rejected")
	}
}
