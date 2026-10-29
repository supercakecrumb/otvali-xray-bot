package miniapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

var (
	ErrInvalidRequest = errors.New("invalid request")
	ErrSensitiveData  = errors.New("sensitive data")
	ErrConflict       = errors.New("conflict")
	ErrRateLimited    = errors.New("rate limited")
)

type BetaRoute struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type BetaTest struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Instruction string `json:"instruction"`
}
type BetaPlan struct {
	Version       string      `json:"version"`
	IsAdmin       bool        `json:"is_admin"`
	Routes        []BetaRoute `json:"routes"`
	Tests         []BetaTest  `json:"tests"`
	PrivacyNotice string      `json:"privacy_notice"`
}
type BetaResult struct {
	ID    string `json:"id"`
	State string `json:"state"`
}
type BetaContent struct {
	RouteID     string       `json:"route_id"`
	App         string       `json:"app"`
	AppVersion  string       `json:"app_version"`
	Device      string       `json:"device"`
	OS          string       `json:"os"`
	City        string       `json:"city"`
	NetworkType string       `json:"network_type"`
	Provider    string       `json:"provider"`
	Symptom     string       `json:"symptom"`
	Results     []BetaResult `json:"results"`
	Comments    string       `json:"comments"`
}
type BetaSubmission struct {
	RequestID string `json:"request_id"`
	BetaContent
}
type BetaAuthor struct {
	Username   string `json:"username"`
	TelegramID int64  `json:"telegram_id"`
}
type BetaReport struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Status    string    `json:"status"`
	// Note is an explicit reply visible to the author, never an internal note.
	Note string `json:"note"`
	BetaContent
	Author *BetaAuthor `json:"author,omitempty"`
}
type BetaPage struct {
	Reports      []BetaReport `json:"reports"`
	NextBeforeID int64        `json:"next_before_id,omitempty"`
}

type betaStore interface {
	CreateBetaReport(context.Context, int64, database.BetaReport, time.Time) (database.BetaReport, bool, error)
	ListBetaReports(context.Context, int64, bool, int64) ([]database.BetaReport, error)
	GetBetaReport(context.Context, int64, int64) (database.BetaReport, error)
	UpdateBetaReport(context.Context, int64, int64, string, string, time.Time) (database.BetaReport, error)
}
type betaBackend interface {
	BetaPlan(context.Context, int64) (BetaPlan, error)
	BetaSubmit(context.Context, int64, BetaSubmission) (BetaReport, bool, error)
	BetaReports(context.Context, int64, string, int64) (BetaPage, error)
	BetaDetail(context.Context, int64, int64) (BetaReport, error)
	BetaUpdate(context.Context, int64, int64, string, string) (BetaReport, error)
}

var betaTests = []BetaTest{
	{"connection", "Подключение", "Выберите один маршрут, подключитесь и дождитесь результата. Повторите отдельно для домашнего Wi-Fi и мобильного интернета."},
	{"ru_browsing", "Российские сайты", "Откройте Яндекс или VK и перейдите на другую страницу. Отметьте, загружается ли содержимое."},
	{"world_browsing", "Зарубежные сайты", "Откройте Wikipedia или другой привычный зарубежный сайт. Проверьте загрузку страниц и изображений."},
	{"youtube", "Видео YouTube", "Запустите видео на 1–2 минуты и перемотайте. Отметьте качество и остановки в комментарии. Открытия главной страницы недостаточно."},
	{"telegram_messages", "Сообщения Telegram", "Отправьте сообщение в «Избранное» и проверьте его доставку. Не нужно писать другим людям."},
	{"telegram_media", "Медиа Telegram", "Загрузите фото или небольшое видео из чата и отправьте файл в «Избранное». Отметьте, в какую сторону передача не работает."},
}

func (s *Service) BetaPlan(ctx context.Context, actor int64) (BetaPlan, error) {
	user, err := s.bound(ctx, actor)
	if err != nil {
		return BetaPlan{}, err
	}
	routes := []BetaRoute{}
	if s.managed != nil && s.managed.Eligible(ctx, actor) {
		routes = append(routes, BetaRoute{"sto", "Sweden, Stockholm"}, BetaRoute{"fi", "Finland, Helsinki (direct)"}, BetaRoute{"msk-sto", "Moscow → Stockholm"}, BetaRoute{"msk-fi", "Moscow → Finland"})
	}
	if _, err := s.legacyAccessServer(ctx, user); err == nil {
		routes = append(routes, BetaRoute{"legacy_fi", "Старый отдельный ключ Finland"})
	}
	routes = append(routes, BetaRoute{"other", "Не знаю / другой маршрут"})
	return BetaPlan{Version: "2026-10-29", IsAdmin: user.IsAdmin, Routes: routes, Tests: append([]BetaTest(nil), betaTests...), PrivacyNotice: "Отчёт видите вы и администраторы Otvali. Укажите только город и оператора; можно написать «Не знаю». Не присылайте ключи, ссылки подписки, IP-адреса, точный адрес, переписки или пароли. Это ваши наблюдения, а не автоматическая диагностика устройства."}, nil
}

func (s *Service) betaRepository(ctx context.Context) (betaStore, error) {
	store, ok := s.requestStore(ctx).(betaStore)
	if !ok {
		return nil, ErrUnavailable
	}
	return store, nil
}
func betaError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, database.ErrBetaForbidden):
		return ErrForbidden
	case errors.Is(err, database.ErrBetaNotFound):
		return ErrNotFound
	case errors.Is(err, database.ErrBetaConflict):
		return ErrConflict
	case errors.Is(err, database.ErrBetaRateLimited):
		return ErrRateLimited
	default:
		return ErrUnavailable
	}
}
func betaView(record database.BetaReport, admin bool) (BetaReport, error) {
	var content BetaContent
	if json.Unmarshal([]byte(record.PayloadJSON), &content) != nil {
		return BetaReport{}, ErrUnavailable
	}
	value := BetaReport{ID: record.ID, CreatedAt: record.CreatedAt.UTC(), UpdatedAt: record.UpdatedAt.UTC(), Status: record.Status, Note: record.Note, BetaContent: content}
	if admin {
		value.Author = &BetaAuthor{Username: record.AuthorUsername, TelegramID: record.AuthorTelegramID}
	}
	return value, nil
}
func (s *Service) BetaSubmit(ctx context.Context, actor int64, input BetaSubmission) (BetaReport, bool, error) {
	user, err := s.bound(ctx, actor)
	if err != nil {
		return BetaReport{}, false, err
	}
	if !betaRequestID.MatchString(input.RequestID) {
		return BetaReport{}, false, ErrInvalidRequest
	}
	content, err := validateBetaContent(input.BetaContent)
	if err != nil {
		return BetaReport{}, false, err
	}
	plan, err := s.BetaPlan(ctx, actor)
	if err != nil {
		return BetaReport{}, false, err
	}
	allowed := false
	for _, route := range plan.Routes {
		if route.ID == content.RouteID {
			allowed = true
			break
		}
	}
	if !allowed {
		return BetaReport{}, false, ErrForbidden
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return BetaReport{}, false, ErrInvalidRequest
	}
	digest := sha256.Sum256(raw)
	store, err := s.betaRepository(ctx)
	if err != nil {
		return BetaReport{}, false, err
	}
	row, duplicate, err := store.CreateBetaReport(ctx, actor, database.BetaReport{RequestID: input.RequestID, PayloadJSON: string(raw), PayloadHash: hex.EncodeToString(digest[:])}, s.now())
	if err != nil {
		return BetaReport{}, false, betaError(err)
	}
	report, err := betaView(row, user.IsAdmin)
	return report, duplicate, err
}
func (s *Service) BetaReports(ctx context.Context, actor int64, scope string, before int64) (BetaPage, error) {
	user, err := s.bound(ctx, actor)
	if err != nil {
		return BetaPage{}, err
	}
	if (scope != "mine" && scope != "admin") || before < 0 {
		return BetaPage{}, ErrInvalidRequest
	}
	if scope == "admin" && !user.IsAdmin {
		return BetaPage{}, ErrForbidden
	}
	store, err := s.betaRepository(ctx)
	if err != nil {
		return BetaPage{}, err
	}
	rows, err := store.ListBetaReports(ctx, actor, scope == "admin", before)
	if err != nil {
		return BetaPage{}, betaError(err)
	}
	result := BetaPage{Reports: []BetaReport{}}
	more := len(rows) > 25
	if more {
		rows = rows[:25]
	}
	for _, row := range rows {
		item, err := betaView(row, user.IsAdmin)
		if err != nil {
			return BetaPage{}, err
		}
		result.Reports = append(result.Reports, item)
	}
	if more {
		result.NextBeforeID = rows[len(rows)-1].ID
	}
	return result, nil
}
func (s *Service) BetaDetail(ctx context.Context, actor, id int64) (BetaReport, error) {
	user, err := s.bound(ctx, actor)
	if err != nil {
		return BetaReport{}, err
	}
	if id <= 0 {
		return BetaReport{}, ErrInvalidRequest
	}
	store, err := s.betaRepository(ctx)
	if err != nil {
		return BetaReport{}, err
	}
	row, err := store.GetBetaReport(ctx, actor, id)
	if err != nil {
		return BetaReport{}, betaError(err)
	}
	return betaView(row, user.IsAdmin)
}
func (s *Service) BetaUpdate(ctx context.Context, actor, id int64, status, note string) (BetaReport, error) {
	user, err := s.bound(ctx, actor)
	if err != nil {
		return BetaReport{}, err
	}
	if !user.IsAdmin {
		return BetaReport{}, ErrForbidden
	}
	if id <= 0 || !oneOf(status, "new", "investigating", "need_info", "resolved") {
		return BetaReport{}, ErrInvalidRequest
	}
	note, err = betaText(note, 1000, true)
	if err != nil {
		return BetaReport{}, err
	}
	if status == "need_info" && note == "" {
		return BetaReport{}, ErrInvalidRequest
	}
	store, err := s.betaRepository(ctx)
	if err != nil {
		return BetaReport{}, err
	}
	row, err := store.UpdateBetaReport(ctx, actor, id, status, note, s.now())
	if err != nil {
		return BetaReport{}, betaError(err)
	}
	return betaView(row, true)
}

var betaRequestID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var betaSensitive = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://|www\.|/api/sub/|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b|\b(?:pbk|privatekey|password|token|authorization|sid)\s*[:=]|[a-z0-9_+/=-]{80,}|\b[0-9]{6,12}:[a-z0-9_-]{25,})`)

func betaText(value string, limit int, multiline bool) (string, error) {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return "", ErrInvalidRequest
	}
	for _, r := range value {
		if r == unicode.ReplacementChar || unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\t')) || unicode.In(r, unicode.Cf) {
			return "", ErrInvalidRequest
		}
	}
	value = strings.TrimSpace(value)
	if betaSensitive.MatchString(value) || containsBetaIP(value) {
		return "", ErrSensitiveData
	}
	return value, nil
}
func containsBetaIP(value string) bool {
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || r == ':' || r == '.')
	}) {
		if strings.Contains(part, ":") && net.ParseIP(part) != nil {
			return true
		}
	}
	return false
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
func validateBetaContent(c BetaContent) (BetaContent, error) {
	if !oneOf(c.RouteID, "sto", "fi", "msk-sto", "msk-fi", "legacy_fi", "other") || !oneOf(c.App, "incy", "happ", "other") || !oneOf(c.OS, "android", "ios", "windows", "macos", "linux", "other") || !oneOf(c.NetworkType, "wifi", "mobile", "wired", "other") || !oneOf(c.Symptom, "works", "cannot_connect", "timeout", "no_internet", "slow", "unstable", "partial", "other") {
		return c, ErrInvalidRequest
	}
	for _, field := range []struct {
		value               *string
		limit               int
		multiline, required bool
	}{{&c.AppVersion, 40, false, false}, {&c.Device, 100, false, false}, {&c.City, 80, false, true}, {&c.Provider, 100, false, true}, {&c.Comments, 2000, true, false}} {
		value, err := betaText(*field.value, field.limit, field.multiline)
		if err != nil {
			return c, err
		}
		if field.required && value == "" {
			return c, ErrInvalidRequest
		}
		*field.value = value
	}
	if len(c.Results) != len(betaTests) {
		return c, ErrInvalidRequest
	}
	seen := map[string]bool{}
	tested := false
	for _, result := range c.Results {
		valid := false
		for _, item := range betaTests {
			if result.ID == item.ID {
				valid = true
				break
			}
		}
		if !valid || seen[result.ID] || !oneOf(result.State, "pass", "fail", "slow", "not_tested") {
			return c, ErrInvalidRequest
		}
		seen[result.ID] = true
		tested = tested || result.State != "not_tested"
	}
	if !tested {
		return c, ErrInvalidRequest
	}
	// Canonicalize unordered result selections so identical retried forms retain
	// the same hash, even when the client sends fields/results in another order.
	c.Results = append([]BetaResult(nil), c.Results...)
	sort.Slice(c.Results, func(i, j int) bool { return c.Results[i].ID < c.Results[j].ID })
	return c, nil
}
