package vpnmonitor

import (
	"fmt"
	"html"
	"strings"
	"time"
)

func PublicRoute(route Route, owner bool) Route {
	if owner {
		return route
	}
	copy := route
	copy.Checks = nil
	for _, c := range route.Checks {
		if c.Category != "own" || c.ID == "own-guide" || c.ID == "own-api" || c.ID == "own-subscription" {
			copy.Checks = append(copy.Checks, c)
		}
	}
	return copy
}
func icon(state string) string {
	switch state {
	case "ok":
		return "🟢"
	case "down":
		return "🔴"
	case "degraded":
		return "🟡"
	default:
		return "⚪"
	}
}
func checkedAt(r Report, owner bool) string {
	place := "Проверка из России"
	if owner {
		place = "Проверка с домашнего сервера"
	}
	return place + " · " + r.CheckedAt.In(time.FixedZone("MSK", 3*3600)).Format("02.01 15:04") + " МСК"
}

func RenderOverview(s Snapshot, owner bool) string {
	if !s.Available {
		return "<b>Состояние VPN</b>\n\n⚪ Результатов пока нет. Первый замер появится после запуска проверки.\n\nЭто проверка соединения из России, не устройства пользователя."
	}
	if s.Stale {
		return "<b>Состояние VPN</b>\n\n⚪ Свежих результатов нет более 15 минут. Сейчас доступность неизвестна.\n\nПоследний замер: " + checkedAt(s.Report, owner) + ".\n\nРезультаты обновляются автоматически; повторное открытие не запускает проверку."
	}
	routes := map[string]Route{}
	good, total, serviceIssues := 0, 0, false
	for _, r := range s.Report.Routes {
		r = PublicRoute(r, owner)
		routes[r.ID] = r
		if r.ID != "direct" {
			total++
			if NetworkState(r) == "ok" {
				good++
			}
		}
		if CategoryState(r, "own") != "ok" {
			serviceIssues = true
		}
	}
	var b strings.Builder
	b.WriteString("<b>Состояние VPN</b>\n" + checkedAt(s.Report, owner) + "\n\n")
	if good == total && total > 0 {
		b.WriteString("Интернет работает через все маршруты.\n")
	} else {
		b.WriteString(fmt.Sprintf("Интернет без замечаний: %d из %d маршрутов.\n", good, total))
	}
	if good > 0 && good < total {
		for _, id := range RouteOrder {
			if id != "direct" && NetworkState(routes[id]) == "ok" {
				b.WriteString("Можно подключиться: " + RouteLabels[id] + ".\n")
				break
			}
		}
	}
	label := "Сервисы Otvali"
	if owner {
		label = "Наши сервисы"
	}
	b.WriteString("\n<b>Россия · Мир · " + label + "</b>\n")
	for _, id := range RouteOrder {
		r, ok := routes[id]
		if !ok {
			continue
		}
		if id == "direct" {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("%s %s %s  %s\n", icon(CategoryState(r, "ru")), icon(CategoryState(r, "world")), icon(CategoryState(r, "own")), RouteLabels[id]))
	}
	b.WriteString("\n🟢 работает · 🟡 частично · 🔴 недоступно · ⚪ нет данных")
	if serviceIssues {
		b.WriteString("\n\nПроблема отдельного сервиса не означает, что VPN отключён. Подробнее — в карточке маршрута.")
	}
	b.WriteString("\n\nЗамеры каждые 5 минут. Результат на вашем устройстве может отличаться.")
	return b.String()
}

func RenderRoute(s Snapshot, id string, owner bool) string {
	if !s.Available || s.Stale {
		return RenderOverview(s, owner)
	}
	var selected *Route
	for _, r := range s.Report.Routes {
		if r.ID == id {
			x := PublicRoute(r, owner)
			selected = &x
			break
		}
	}
	if selected == nil {
		return "Маршрут не найден. Обновите статус."
	}
	var b strings.Builder
	b.WriteString("<b>" + html.EscapeString(RouteLabels[id]) + "</b>\n" + checkedAt(s.Report, owner) + "\n")
	for _, category := range Categories {
		label := CategoryLabels[category]
		if category == "own" && !owner {
			label = "Сервисы Otvali"
		}
		b.WriteString("\n<b>" + label + "</b>\n")
		for _, c := range selected.Checks {
			if c.Category != category {
				continue
			}
			result := "нет данных"
			if c.State == "ok" {
				result = fmt.Sprintf("ответ %d мс", c.LatencyMS)
			} else if c.State == "fail" {
				result = checkFailureText(c)
			}
			b.WriteString(icon(mapCheck(c.State)) + " " + CheckLabels[c.ID] + " — " + result + "\n")
		}
	}
	if NetworkState(*selected) == "ok" && CategoryState(*selected, "own") != "ok" {
		b.WriteString("\nИнтернет через маршрут работает; проблема касается отдельных сервисов.\n")
	}
	b.WriteString("\nЭто время ответа тестового запроса, не скорость загрузки. Замер относится к проверочной конфигурации.")
	return b.String()
}

// Error codes are fixed collector categories, never raw server/client errors.
// A successful HTTP response can still fail content or exit verification.
func checkFailureText(c Check) string {
	switch c.ErrorCode {
	case "exit_mismatch":
		return "выход через другой сервер"
	case "timeout":
		return "истекло время ожидания"
	case "dns_failed":
		return "ошибка DNS"
	case "tls_failed":
		return "ошибка TLS"
	case "connection_failed":
		return "не удалось подключиться"
	case "request_failed":
		return "не удалось выполнить запрос"
	case "exit_response_invalid":
		return "не удалось проверить выход"
	case "exit_expectation_missing":
		return "проверка выхода не настроена"
	case "subscription_invalid":
		return "подписка не читается"
	case "subscription_unavailable":
		return "подписка недоступна"
	case "profile_invalid":
		return "конфигурация не читается"
	case "profile_unavailable", "legacy_config_unavailable":
		return "конфигурация недоступна для проверки"
	case "client_start_failed", "client_stopped", "client_unavailable":
		return "не удалось запустить проверочное соединение"
	case "source_route_changed":
		return "подключение проверочного сервера изменилось"
	case "probe_error":
		return "ошибка проверки"
	case "unexpected_http":
		if c.HTTPStatus >= 100 {
			return fmt.Sprintf("неожиданный ответ HTTP %d", c.HTTPStatus)
		}
		return "неожиданный ответ сервиса"
	default:
		return "проверка не пройдена"
	}
}
func mapCheck(state string) string {
	if state == "fail" {
		return "down"
	}
	return state
}
