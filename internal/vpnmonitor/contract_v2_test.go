package vpnmonitor

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testReportV2(at time.Time) Report {
	r := testReport(at)
	r.Version = 2
	for i := range r.Routes {
		for j := range r.Routes[i].Checks {
			r.Routes[i].Checks[j].Attempts = 1
			r.Routes[i].Checks[j].Successes = 1
		}
		for _, id := range []string{"world-youtube", "world-telegram", "world-download", "world-upload"} {
			r.Routes[i].Checks = append(r.Routes[i].Checks, Check{ID: id, Category: "world", State: "ok", LatencyMS: 100, Attempts: 1, Successes: 1, BytesReceived: 1024, VerifiedBody: true})
		}
	}
	return r
}

func TestReportVersionsSupportOrderedRolloutWithoutWeakerValidation(t *testing.T) {
	for _, r := range []Report{testReport(testTime), testReportV2(testTime)} {
		if err := Validate(r, testTime); err != nil {
			t.Fatal("valid rolling deployment report rejected", r.Version)
		}
	}
	cases := map[string]func(*Report){
		"missing v2 target":       func(r *Report) { r.Routes[0].Checks = r.Routes[0].Checks[:15] },
		"unknown version":         func(r *Report) { r.Version = 3 },
		"v2 checks in v1":         func(r *Report) { r.Version = 1 },
		"negative bytes":          func(r *Report) { r.Routes[0].Checks[0].BytesReceived = -1 },
		"excess bytes":            func(r *Report) { r.Routes[0].Checks[0].BytesSent = 10*1024*1024 + 1 },
		"excess attempts":         func(r *Report) { r.Routes[0].Checks[0].Attempts = 11 },
		"success above attempts":  func(r *Report) { r.Routes[0].Checks[0].Successes = 2 },
		"success without attempt": func(r *Report) { r.Routes[0].Checks[0].Attempts = 0; r.Routes[0].Checks[0].Successes = 0 },
		"failed all successful":   func(r *Report) { r.Routes[0].Checks[0].State = "fail" },
		"unknown attempted":       func(r *Report) { r.Routes[0].Checks[0].State = "unknown" },
		"app body unchecked":      func(r *Report) { r.Routes[0].Checks[12].VerifiedBody = false },
		"empty verified body":     func(r *Report) { r.Routes[0].Checks[12].BytesReceived = 0 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := testReportV2(testTime)
			change(&r)
			if Validate(r, testTime) == nil {
				t.Fatal("invalid delivery evidence accepted")
			}
		})
	}
	r := testReport(testTime)
	r.Routes[0].Checks[0].Attempts = 1
	if Validate(r, testTime) == nil {
		t.Fatal("v2 metrics accepted under v1")
	}
	r = testReportV2(testTime)
	r.Routes[0].Checks[0] = Check{ID: "ru-yandex", Category: "ru", State: "unknown", ErrorCode: "source_route_changed"}
	if err := Validate(r, testTime); err != nil {
		t.Fatal("explicit unattempted setup failure rejected")
	}
}

func TestSignedV2ReceiptAndV1Compatibility(t *testing.T) {
	secret := strings.Repeat("x", 32)
	for _, report := range []Report{testReport(testTime), testReportV2(testTime)} {
		sink := &memoryIngester{}
		h := NewHandler(secret, sink).(*receiver)
		h.now = func() time.Time { return testTime }
		body, _ := json.Marshal(report)
		r := signedRequest(body, testTime, secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || sink.calls != 1 {
			t.Fatal("signed compatible report rejected", report.Version, w.Code)
		}
	}
}
