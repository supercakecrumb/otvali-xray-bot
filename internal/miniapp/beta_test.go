package miniapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

type fakeBetaStore struct {
	*fakeStore
	rows   map[int64]database.BetaReport
	writes int
}

func (f *fakeBetaStore) actor(id int64) (*database.User, error) {
	user, err := f.GetUserByTelegramID(id)
	if err != nil || user == nil || user.TelegramID == nil || *user.TelegramID != id {
		return nil, database.ErrBetaForbidden
	}
	return user, nil
}
func (f *fakeBetaStore) CreateBetaReport(_ context.Context, id int64, c database.BetaReport, now time.Time) (database.BetaReport, bool, error) {
	user, err := f.actor(id)
	if err != nil {
		return c, false, err
	}
	for _, row := range f.rows {
		if row.UserID == user.ID && row.RequestID == c.RequestID {
			if row.PayloadHash != c.PayloadHash {
				return c, false, database.ErrBetaConflict
			}
			return row, true, nil
		}
	}
	c.ID = int64(len(f.rows) + 1)
	c.UserID = user.ID
	c.Status = "new"
	c.CreatedAt = now
	c.UpdatedAt = now
	c.AuthorUsername = user.Username
	c.AuthorTelegramID = id
	f.rows[c.ID] = c
	f.writes++
	return c, false, nil
}
func (f *fakeBetaStore) ListBetaReports(_ context.Context, id int64, admin bool, before int64) ([]database.BetaReport, error) {
	user, err := f.actor(id)
	if err != nil {
		return nil, err
	}
	if admin && !user.IsAdmin {
		return nil, database.ErrBetaForbidden
	}
	rows := []database.BetaReport{}
	for i := int64(len(f.rows)); i > 0; i-- {
		row := f.rows[i]
		if (before == 0 || i < before) && (admin || row.UserID == user.ID) {
			rows = append(rows, row)
			if len(rows) == 26 {
				break
			}
		}
	}
	return rows, nil
}
func (f *fakeBetaStore) GetBetaReport(_ context.Context, id, reportID int64) (database.BetaReport, error) {
	user, err := f.actor(id)
	if err != nil {
		return database.BetaReport{}, err
	}
	row, ok := f.rows[reportID]
	if !ok || (!user.IsAdmin && row.UserID != user.ID) {
		return database.BetaReport{}, database.ErrBetaNotFound
	}
	return row, nil
}
func (f *fakeBetaStore) UpdateBetaReport(ctx context.Context, id, reportID int64, status, note string, now time.Time) (database.BetaReport, error) {
	user, err := f.actor(id)
	if err != nil {
		return database.BetaReport{}, err
	}
	if !user.IsAdmin {
		return database.BetaReport{}, database.ErrBetaForbidden
	}
	row, err := f.GetBetaReport(ctx, id, reportID)
	if err != nil {
		return row, err
	}
	row.Status = status
	row.Note = note
	row.UpdatedAt = now
	f.rows[reportID] = row
	f.writes++
	return row, nil
}
func fixtureBeta() (*Service, *fakeBetaStore, *fakeManaged) {
	service, base, managed, _ := fixtureService()
	base.user.ID = 1
	first, second, admin := int64(42), int64(99), int64(7)
	base.users = map[int64]*database.User{first: base.user, second: {ID: 2, TelegramID: &second, Username: "second"}, admin: {ID: 3, TelegramID: &admin, Username: "owner", IsAdmin: true}}
	store := &fakeBetaStore{fakeStore: base, rows: map[int64]database.BetaReport{}}
	service.store = store
	service.now = func() time.Time { return testNow }
	return service, store, managed
}
func goodBeta() BetaSubmission {
	results := []BetaResult{}
	for _, item := range betaTests {
		results = append(results, BetaResult{ID: item.ID, State: "pass"})
	}
	return BetaSubmission{RequestID: "00000000-0000-4000-8000-000000000001", BetaContent: BetaContent{RouteID: "sto", App: "incy", AppVersion: "3.7.9", Device: "Pixel 7", OS: "android", City: "Петрозаводск", NetworkType: "wifi", Provider: "Не знаю", Symptom: "works", Results: results, Comments: "Видео играет. Сообщения доставляются."}}
}
func betaHandler(t *testing.T, s *Service) *handler {
	t.Helper()
	value, err := NewHandler(Config{Enabled: true, AllowedOrigins: []string{testOrigin}}, testToken, s, fakeHealth{})
	if err != nil {
		t.Fatal(err)
	}
	h := value.(*handler)
	h.now = func() time.Time { return testNow }
	return h
}
func betaRequest(t *testing.T, h http.Handler, path string, actor int64, body any) *httptestResponse {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response := request(h, "/api/miniapp/beta/"+path, string(raw), func(r *http.Request) { r.Header.Set("Authorization", "tma "+initData(actor)) })
	return &httptestResponse{code: response.Code, body: response.Body.String(), headers: response.Header()}
}

type httptestResponse struct {
	code    int
	body    string
	headers http.Header
}

func TestBetaPrivateLifecycleAndIdentity(t *testing.T) {
	s, store, managed := fixtureBeta()
	h := betaHandler(t, s)
	result := betaRequest(t, h, "report", 42, goodBeta())
	if result.code != 200 || strings.Contains(result.body, "author") || strings.Contains(result.body, "telegram_id") || strings.Contains(result.body, "request_id") {
		t.Fatalf("private submission failed: status %d", result.code)
	}
	if store.rows[1].UserID != 1 || len(managed.issued) != 0 || len(managed.read) != 0 {
		t.Fatal("identity not stored from binding or beta issued credentials")
	}
	for _, tc := range []struct {
		actor int64
		path  string
		body  any
		want  int
	}{{99, "report/detail", map[string]any{"id": 1}, 404}, {99, "reports", map[string]any{"scope": "admin"}, 403}, {99, "report/update", map[string]any{"id": 1, "status": "resolved", "note": "Done"}, 403}, {7, "reports", map[string]any{"scope": "admin"}, 200}, {7, "report/update", map[string]any{"id": 1, "status": "need_info", "note": "Проверьте ещё мобильный интернет."}, 200}} {
		got := betaRequest(t, h, tc.path, tc.actor, tc.body)
		if got.code != tc.want {
			t.Fatalf("%s actor %d: status %d", tc.path, tc.actor, got.code)
		}
	}
	own := betaRequest(t, h, "report/detail", 42, map[string]any{"id": 1})
	if own.code != 200 || !strings.Contains(own.body, "need_info") || !strings.Contains(own.body, "мобильный") || strings.Contains(own.body, "author") {
		t.Fatal("author must see status/reply without identity fields")
	}
	other := betaRequest(t, h, "reports", 99, map[string]any{"scope": "mine"})
	if other.code != 200 || !strings.Contains(other.body, `"reports":[]`) {
		t.Fatal("own list leaked another reporter")
	}
	store.users[7].IsAdmin = false
	revoked := betaRequest(t, h, "report/detail", 7, map[string]any{"id": 1})
	if revoked.code != 404 {
		t.Fatal("revoked admin retained read access")
	}
}
func TestBetaRetryIsScopedCanonicalAndImmutable(t *testing.T) {
	s, store, _ := fixtureBeta()
	body := goodBeta()
	first, dup, err := s.BetaSubmit(context.Background(), 42, body)
	if err != nil || dup {
		t.Fatal("first submit failed")
	}
	reversed := append([]BetaResult(nil), body.Results...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	body.Results = reversed
	retry, dup, err := s.BetaSubmit(context.Background(), 42, body)
	if err != nil || !dup || retry.ID != first.ID || store.writes != 1 {
		t.Fatal("canonical retry created a duplicate")
	}
	body.Comments = "Теперь таймаут"
	if _, _, err := s.BetaSubmit(context.Background(), 42, body); !errors.Is(err, ErrConflict) || store.writes != 1 {
		t.Fatal("conflicting retry overwrote original")
	}
	if _, dup, err := s.BetaSubmit(context.Background(), 99, body); err != nil || dup || store.writes != 2 {
		t.Fatal("request ID must be scoped to bound user")
	}
}
func TestBetaValidationAndSensitiveData(t *testing.T) {
	for _, value := range []string{"vless://credential@host:443", "https://sub.example/api/sub/private", "www.example.com/private", "00000000-0000-4000-8000-000000000001", "192.168.0.10", "2001:db8::1", "password=hunter2", strings.Repeat("A", 90)} {
		t.Run(fmt.Sprint(len(value)), func(t *testing.T) {
			s, store, _ := fixtureBeta()
			body := goodBeta()
			body.Comments = value
			if _, _, err := s.BetaSubmit(context.Background(), 42, body); !errors.Is(err, ErrSensitiveData) || store.writes != 0 {
				t.Fatal("sensitive data reached persistence")
			}
		})
	}
	for _, mutate := range []func(*BetaSubmission){func(b *BetaSubmission) { b.City = "" }, func(b *BetaSubmission) { b.Provider = strings.Repeat("я", 101) }, func(b *BetaSubmission) { b.Comments = "bad\x00text" }, func(b *BetaSubmission) { b.Device = "bad\u202etext" }, func(b *BetaSubmission) { b.RouteID = "own-home-assistant" }, func(b *BetaSubmission) { b.Results[0] = b.Results[1] }, func(b *BetaSubmission) { b.Results = b.Results[:1] }, func(b *BetaSubmission) {
		for i := range b.Results {
			b.Results[i].State = "not_tested"
		}
	}, func(b *BetaSubmission) { b.RequestID = "not-uuid" }, func(b *BetaSubmission) { b.Comments = string([]byte{0xff}) }} {
		s, store, _ := fixtureBeta()
		body := goodBeta()
		mutate(&body)
		if _, _, err := s.BetaSubmit(context.Background(), 42, body); !errors.Is(err, ErrInvalidRequest) || store.writes != 0 {
			t.Fatal("invalid diagnostic form accepted")
		}
	}
}
func TestBetaHTTPBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		mutate           func(*http.Request)
	}{
		{"author spoof", "report", `{"telegram_id":99}`, nil},
		{"oversized", "report", `{"comments":"` + strings.Repeat("x", 17000) + `"}`, nil},
		{"malformed", "plan", `{`, nil}, {"trailing", "plan", `{} {}`, nil}, {"null", "plan", `null`, nil},
		{"no auth", "plan", `{}`, func(r *http.Request) { r.Header.Del("Authorization") }},
		{"bad origin", "plan", `{}`, func(r *http.Request) { r.Header.Set("Origin", "https://wrong.example") }},
		{"get", "plan", `{}`, func(r *http.Request) { r.Method = "GET" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, _ := fixtureBeta()
			h := betaHandler(t, s)
			response := request(h, "/api/miniapp/beta/"+tc.path, tc.body, tc.mutate)
			if response.Code < 400 || store.writes != 0 {
				t.Fatal("boundary accepted invalid request")
			}
			if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("beta response became cacheable")
			}
		})
	}
	s, _, _ := fixtureBeta()
	h := betaHandler(t, s)
	body, _ := json.Marshal(goodBeta())
	escaped := strings.Replace(string(body), "Петрозаводск", `\ud800`, 1)
	r := request(h, "/api/miniapp/beta/report", escaped, nil)
	if r.Code != 400 {
		t.Fatal("invalid surrogate silently substituted")
	}
}
func TestBetaPlanIsAuthorizedMetadataOnlyAndPaging(t *testing.T) {
	s, store, managed := fixtureBeta()
	plan, err := s.BetaPlan(context.Background(), 42)
	if err != nil || plan.IsAdmin || len(plan.Tests) != 6 || len(plan.Routes) != 6 {
		t.Fatal("plan missing public tests/routes")
	}
	raw, _ := json.Marshal(plan)
	for _, secret := range []string{"home-assistant", "dokploy", "jellyfin", "vless://", "telegram_id", "subscription"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private service/credential leaked into plan")
		}
	}
	managed.eligible = false
	plan, err = s.BetaPlan(context.Background(), 42)
	if err != nil || len(plan.Routes) != 2 {
		t.Fatal("plan exposed ineligible managed routes")
	}
	if _, _, err = s.BetaSubmit(context.Background(), 42, goodBeta()); !errors.Is(err, ErrForbidden) {
		t.Fatal("ineligible route submitted")
	}
	managed.eligible = true
	for i := 1; i <= 30; i++ {
		body := goodBeta()
		body.RequestID = fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		if _, _, err = s.BetaSubmit(context.Background(), 42, body); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.BetaReports(context.Background(), 42, "mine", 0)
	if err != nil || len(page.Reports) != 25 || page.NextBeforeID != 6 {
		t.Fatal("bounded paging failed")
	}
	second, err := s.BetaReports(context.Background(), 42, "mine", page.NextBeforeID)
	if err != nil || len(second.Reports) != 5 || second.NextBeforeID != 0 {
		t.Fatal("second page wrong")
	}
	store.users[42].TelegramID = nil
	if _, err = s.BetaReports(context.Background(), 42, "mine", 0); !errors.Is(err, ErrForbidden) {
		t.Fatal("unbound user retained report access")
	}
}
