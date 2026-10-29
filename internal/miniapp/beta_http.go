package miniapp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"unicode/utf8"
)

func betaPath(path string) bool {
	switch path {
	case "/api/miniapp/beta/plan", "/api/miniapp/beta/reports", "/api/miniapp/beta/report", "/api/miniapp/beta/report/detail", "/api/miniapp/beta/report/update":
		return true
	}
	return false
}

// Called only after shared origin, initData, quota, concurrency and content-type
// validation. Identity is derived solely from the verified Telegram signature.
func (h *handler) serveBeta(ctx context.Context, w http.ResponseWriter, r *http.Request, actor int64) {
	backend, ok := h.backend.(betaBackend)
	if !ok {
		writeBackendError(w, ErrUnavailable)
		return
	}
	var submission BetaSubmission
	var listing struct {
		Scope    string `json:"scope"`
		BeforeID int64  `json:"before_id"`
	}
	var selected struct {
		ID int64 `json:"id"`
	}
	var update struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	var payload any = &struct{}{}
	switch r.URL.Path {
	case "/api/miniapp/beta/reports":
		payload = &listing
	case "/api/miniapp/beta/report":
		payload = &submission
	case "/api/miniapp/beta/report/detail":
		payload = &selected
	case "/api/miniapp/beta/report/update":
		payload = &update
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16*1024))
	if err != nil || !utf8.Valid(raw) || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		writeBackendError(w, ErrInvalidRequest)
		return
	}
	var compact json.RawMessage
	if json.Unmarshal(raw, &compact) != nil {
		writeBackendError(w, ErrInvalidRequest)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(payload) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeBackendError(w, ErrInvalidRequest)
		return
	}
	switch r.URL.Path {
	case "/api/miniapp/beta/plan":
		result, err := backend.BetaPlan(ctx, actor)
		if err != nil {
			writeBackendError(w, err)
			return
		}
		h.recordActivity(ctx, actor, "miniapp_beta_plan")
		writeJSON(w, 200, map[string]BetaPlan{"plan": result})
	case "/api/miniapp/beta/reports":
		result, err := backend.BetaReports(ctx, actor, listing.Scope, listing.BeforeID)
		if err != nil {
			writeBackendError(w, err)
			return
		}
		h.recordActivity(ctx, actor, "miniapp_beta_reports")
		writeJSON(w, 200, result)
	case "/api/miniapp/beta/report":
		result, duplicate, err := backend.BetaSubmit(ctx, actor, submission)
		if err != nil {
			writeBackendError(w, err)
			return
		}
		if !duplicate {
			h.recordActivity(ctx, actor, "miniapp_beta_submit")
		}
		writeJSON(w, 200, map[string]any{"report": result, "duplicate": duplicate})
	case "/api/miniapp/beta/report/detail":
		result, err := backend.BetaDetail(ctx, actor, selected.ID)
		if err != nil {
			writeBackendError(w, err)
			return
		}
		h.recordActivity(ctx, actor, "miniapp_beta_detail")
		writeJSON(w, 200, map[string]BetaReport{"report": result})
	case "/api/miniapp/beta/report/update":
		result, err := backend.BetaUpdate(ctx, actor, update.ID, update.Status, update.Note)
		if err != nil {
			writeBackendError(w, err)
			return
		}
		h.recordActivity(ctx, actor, "miniapp_beta_update")
		writeJSON(w, 200, map[string]BetaReport{"report": result})
	}
}
