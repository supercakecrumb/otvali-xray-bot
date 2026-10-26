package vpnmonitor

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHiddenServiceStateCannotBeInferredFromPublicStatus(t *testing.T) {
	r := testReport(testTime)
	before := RenderOverview(Snapshot{Available: true, Report: r}, false) + RenderRoute(Snapshot{Available: true, Report: r}, "stockholm", false)
	for i := range r.Routes {
		r.Routes[i].State = "down"
		for j := range r.Routes[i].Checks {
			c := &r.Routes[i].Checks[j]
			if c.Category == "own" {
				c.State = "fail"
				c.ErrorCode = "timeout"
			}
		}
	}
	s := Snapshot{Available: true, Report: r}
	after := RenderOverview(s, false) + RenderRoute(s, "stockholm", false)
	if before != after {
		t.Fatal("private failures changed public rendering")
	}
	for _, route := range r.Routes {
		public := PublicRoute(route, false)
		if public.State != "ok" {
			t.Fatal("private failure survived in public route aggregate")
		}
		for _, c := range public.Checks {
			if c.Category == "own" {
				t.Fatal("private check survived projection")
			}
		}
	}
	admin := RenderOverview(s, true) + RenderRoute(s, "stockholm", true)
	if !strings.Contains(admin, "Наши сервисы") || !strings.Contains(admin, "Home Assistant — истекло время ожидания") {
		t.Fatal("admin diagnostics disappeared")
	}
}

func TestUnknownChecksExplainMissingMeasurement(t *testing.T) {
	r := testReport(testTime)
	for i := range r.Routes[0].Checks {
		c := &r.Routes[0].Checks[i]
		if c.ID == "own-home-assistant" {
			c.State = "unknown"
			c.ErrorCode = "probe_budget_exhausted"
		}
		if c.ID == "own-home-dokploy" {
			c.State = "unknown"
			c.ErrorCode = "profile_unavailable"
		}
	}
	text := RenderRoute(Snapshot{Available: true, Report: r}, r.Routes[0].ID, true)
	for _, want := range []string{"Home Assistant — проверка не выполнена: исчерпан лимит времени", "Домашняя панель — проверка не выполнена: конфигурация недоступна для проверки"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing measurement lost its actual reason")
		}
	}
	if strings.Contains(text, "нет данных") {
		t.Fatal("vague missing data remained")
	}
}

func TestTransferRenderingIsExplicitAndFitsTelegram(t *testing.T) {
	r := testReportV2(testTime)
	for i := range r.Routes[0].Checks {
		c := &r.Routes[0].Checks[i]
		c.BytesSent = 65536
		c.BytesReceived = 262144
		c.VerifiedBody = true
		c.Attempts = 10
		c.Successes = 10
	}
	text := RenderRoute(Snapshot{Available: true, Report: r}, r.Routes[0].ID, true)
	for _, want := range []string{"YouTube · сайт и обложки", "Telegram · MTProto", "данные проверены", "↑ 64.0 КиБ", "↓ 256.0 КиБ", "10/10 запросов", "потери IP-пакетов здесь не измеряются"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing transfer explanation: %s", want)
		}
	}
	if utf8.RuneCountInString(text) > 3900 {
		t.Fatal("route details exceed safe Telegram message size")
	}
}

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
