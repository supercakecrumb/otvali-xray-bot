package vpnmonitor

import (
	"strings"
	"testing"
)

func TestFailedChecksExplainCauseInsteadOfSuccessfulHTTPStatus(t *testing.T) {
	cases := map[string]string{
		"exit_mismatch":         "выход через другой сервер",
		"timeout":               "истекло время ожидания",
		"dns_failed":            "ошибка DNS",
		"tls_failed":            "ошибка TLS",
		"connection_failed":     "не удалось подключиться",
		"exit_response_invalid": "не удалось проверить выход",
		"subscription_invalid":  "подписка не читается",
		"private_future_code":   "проверка не пройдена",
	}
	for code, want := range cases {
		t.Run(code, func(t *testing.T) {
			r := testReport(testTime)
			for i := range r.Routes {
				if r.Routes[i].ID == "stockholm" {
					for j := range r.Routes[i].Checks {
						if r.Routes[i].Checks[j].ID == "world-exit" {
							c := &r.Routes[i].Checks[j]
							c.State = "fail"
							c.ErrorCode = code
							c.HTTPStatus = 200
						}
					}
				}
			}
			text := RenderRoute(Snapshot{Available: true, Report: r}, "stockholm", false)
			if !strings.Contains(text, "🔴 Выход в интернет — "+want) || strings.Contains(text, "HTTP 200") || strings.Contains(text, code) {
				t.Fatal("failed content/transport check displayed an unrelated HTTP status or raw code")
			}
		})
	}
	if got := checkFailureText(Check{ErrorCode: "unexpected_http", HTTPStatus: 403}); got != "неожиданный ответ HTTP 403" {
		t.Fatal("an actual unexpected HTTP response lost its status")
	}
}
