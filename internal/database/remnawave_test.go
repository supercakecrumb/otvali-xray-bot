package database

import (
	"context"
	"sync"
	"testing"

	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"gorm.io/gorm/schema"
)

func TestRemnawaveGrantIdentityConstraints(t *testing.T) {
	parsed, err := schema.Parse(&RemnawaveGrant{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Table != "remnawave_grants" {
		t.Fatal("pilot identities must have their own additive table")
	}
	telegram := parsed.FieldsByName["TelegramID"]
	if !telegram.PrimaryKey || telegram.AutoIncrement {
		t.Fatal("the bound Telegram ID must be the caller-supplied primary key")
	}
	for _, name := range []string{"RemoteID", "Username"} {
		field := parsed.FieldsByName[name]
		if !field.Unique || !field.NotNull {
			t.Fatalf("%s must prevent sharing a remote identity across Telegram accounts", name)
		}
	}
}

func TestRemnawaveGrantRejectsIncompleteIdentity(t *testing.T) {
	// An invalid identity must fail before attempting a database write.
	db := &DB{}
	for _, mapping := range []remnawave.Mapping{
		{RemoteID: 1, Username: "test"},
		{TelegramID: 1, Username: "test"},
		{TelegramID: 1, RemoteID: 1, Username: " "},
	} {
		if err := db.SaveGrant(context.Background(), mapping); err == nil {
			t.Fatal("incomplete identity must be rejected")
		}
	}
}
