package miniapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testToken = "123456:synthetic-token"

var testNow = time.Date(2026, 10, 19, 15, 0, 0, 0, time.UTC)

func sign(values url.Values) string {
	fields := []string{}
	for key := range values {
		if key != "hash" {
			fields = append(fields, key+"="+values.Get(key))
		}
	}
	sort.Strings(fields)
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(testToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(fields, "\n")))
	values.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return values.Encode()
}
func initData(id int64) string {
	return sign(url.Values{"auth_date": {strconv.FormatInt(testNow.Unix(), 10)}, "user": {"{\"id\":" + strconv.FormatInt(id, 10) + ",\"username\":\"untrusted_mutable_name\"}"}, "signature": {"modern-signed-field"}})
}
func TestInitDataValidatesNumericIDAndModernSignature(t *testing.T) {
	const id = int64(4294967338)
	if got, err := ValidateInitData(initData(id), testToken, testNow); err != nil || got != id {
		t.Fatal("signed 64-bit ID with signature must validate")
	}
}
func TestInitDataRejectsForgeryAndAmbiguousOrExpiredInput(t *testing.T) {
	for _, name := range []string{"hash", "tampered_user", "tampered_signature", "expired", "future", "duplicate_user", "duplicate_hash", "duplicate_auth_date", "malformed_encoding", "oversized", "negative_id", "fractional_id", "missing_user", "bot", "newline_key"} {
		t.Run(name, func(t *testing.T) {
			values, _ := url.ParseQuery(initData(42))
			raw := ""
			switch name {
			case "hash":
				values.Set("hash", strings.Repeat("0", 64))
			case "tampered_user":
				values.Set("user", `{"id":99}`)
			case "tampered_signature":
				values.Set("signature", "another-signature")
			case "expired":
				values.Set("auth_date", strconv.FormatInt(testNow.Add(-maxInitDataAge-time.Second).Unix(), 10))
				raw = sign(values)
			case "future":
				values.Set("auth_date", strconv.FormatInt(testNow.Add(31*time.Second).Unix(), 10))
				raw = sign(values)
			case "duplicate_user":
				values.Add("user", `{"id":99}`)
			case "duplicate_hash":
				values.Add("hash", values.Get("hash"))
			case "duplicate_auth_date":
				values.Add("auth_date", values.Get("auth_date"))
			case "malformed_encoding":
				raw = "user=%zz"
			case "oversized":
				raw = strings.Repeat("x", maxInitData+1)
			case "negative_id":
				values.Set("user", `{"id":-1}`)
				raw = sign(values)
			case "fractional_id":
				values.Set("user", `{"id":42.5}`)
				raw = sign(values)
			case "missing_user":
				values.Del("user")
				raw = sign(values)
			case "bot":
				values.Set("user", `{"id":42,"is_bot":true}`)
				raw = sign(values)
			case "newline_key":
				values.Set("a\nb", "ambiguous")
				raw = sign(values)
			}
			if raw == "" {
				raw = values.Encode()
			}
			if _, err := ValidateInitData(raw, testToken, testNow); err == nil {
				t.Fatal("invalid initData accepted")
			}
		})
	}
}
func TestInitDataOneHourBoundary(t *testing.T) {
	if _, err := ValidateInitData(initData(42), testToken, testNow.Add(time.Hour)); err != nil {
		t.Fatal("one-hour boundary should remain valid")
	}
	if _, err := ValidateInitData(initData(42), testToken, testNow.Add(time.Hour+time.Second)); err == nil {
		t.Fatal("launch data older than one hour must expire")
	}
	if _, err := ValidateInitData(initData(42), "wrong-token", testNow); err == nil {
		t.Fatal("another bot token must fail")
	}
}
