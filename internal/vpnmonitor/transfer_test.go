package vpnmonitor

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func transferRequest(path string, body []byte, secret string) *http.Request {
	r := httptest.NewRequest("POST", "https://api.example"+path, bytes.NewReader(body))
	stamp := strconv.FormatInt(testTime.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "\n" + path + "\n"))
	mac.Write(body)
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("X-Otvali-Timestamp", stamp)
	r.Header.Set("X-Otvali-Signature", hex.EncodeToString(mac.Sum(nil)))
	return r
}

func TestTransferVerifiesBothDirectionsWithoutReflectingContent(t *testing.T) {
	secret := strings.Repeat("x", 32)
	h := NewTransferHandler(secret).(*transferReceiver)
	h.now = func() time.Time { return testTime }
	nonce := bytes.Repeat([]byte{7}, 32)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, transferRequest(transferDownload, nonce, secret))
	digest := sha256.Sum256(append([]byte("OtvaliProbe/2.0\n"), nonce...))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), bytes.Repeat(digest[:], 8192)) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("download data contract failed")
	}
	body := bytes.Repeat([]byte("test-only-value!"), 4096)
	if len(body) != 65536 {
		t.Fatal("fixture length")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, transferRequest(transferUpload, body, secret))
	var receipt struct {
		Size   int    `json:"size"`
		SHA256 string `json:"sha256"`
	}
	digest = sha256.Sum256(body)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &receipt) != nil || receipt.Size != len(body) || receipt.SHA256 != hex.EncodeToString(digest[:]) || strings.Contains(w.Body.String(), "test-only-value") {
		t.Fatal("upload receipt contract failed")
	}
}

func TestTransferRejectsUnsignedChangedOversizedAndCrossEndpointRequests(t *testing.T) {
	secret := strings.Repeat("x", 32)
	for _, mode := range []string{"unsigned", "body_changed", "path_changed", "report_signature", "old", "future", "duplicate", "length", "oversize", "content_type", "query", "get", "unknown_path"} {
		t.Run(mode, func(t *testing.T) {
			h := NewTransferHandler(secret).(*transferReceiver)
			h.now = func() time.Time { return testTime }
			body := bytes.Repeat([]byte{7}, 32)
			r := transferRequest(transferDownload, body, secret)
			want := 401
			switch mode {
			case "unsigned":
				r.Header.Del("X-Otvali-Signature")
			case "body_changed":
				r.Body = io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{8}, 32)))
			case "path_changed":
				r.URL.Path = transferUpload
			case "report_signature":
				r.Header.Set("X-Otvali-Signature", signedRequest(body, testTime, secret).Header.Get("X-Otvali-Signature"))
			case "old":
				h.now = func() time.Time { return testTime.Add(6 * time.Minute) }
			case "future":
				h.now = func() time.Time { return testTime.Add(-2 * time.Minute) }
			case "duplicate":
				r.Header.Add("X-Otvali-Signature", r.Header.Get("X-Otvali-Signature"))
			case "length":
				r = transferRequest(transferDownload, body[:31], secret)
				want = 400
			case "oversize":
				r = transferRequest(transferDownload, append(body, 0), secret)
				want = 413
			case "content_type":
				r.Header.Set("Content-Type", "text/plain")
				want = 400
			case "query":
				r.URL.RawQuery = "a=b"
				want = 400
			case "get":
				r.Method = "GET"
				want = 405
			case "unknown_path":
				r.URL.Path = "/api/monitor/transfer/elsewhere"
				want = 404
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != want || w.Body.Len() != 0 {
				t.Fatal("invalid transfer accepted or reflected request", mode, w.Code)
			}
		})
	}
}

func TestTransferReplayHasBoundedBandwidthAndConcurrency(t *testing.T) {
	secret := strings.Repeat("x", 32)
	h := NewTransferHandler(secret).(*transferReceiver)
	now := testTime
	h.now = func() time.Time { return now }
	body := bytes.Repeat([]byte{7}, 32)
	for i := 0; i < 14; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, transferRequest(transferDownload, body, secret))
		want := 200
		if i >= 12 {
			want = 429
		}
		if w.Code != want {
			t.Fatal("authenticated replay not rate bounded", i, w.Code)
		}
	}
	now = now.Add(time.Second)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, transferRequest(transferDownload, body, secret))
	if w.Code != 200 {
		t.Fatal("rate bucket did not refill")
	}
	for i := 0; i < 6; i++ {
		h.slots <- struct{}{}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, transferRequest(transferDownload, body, secret))
	if w.Code != 429 {
		t.Fatal("concurrency limit absent")
	}
	for i := 0; i < 6; i++ {
		<-h.slots
	}
}
