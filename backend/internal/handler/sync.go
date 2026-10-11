package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"cafe-backend/internal/middleware"
	"cafe-backend/internal/syncutil"
	"github.com/go-chi/chi/v5"
)

// SyncHandler exposes the bidirectional offline-sync protocol:
//
//	POST /api/sync/apply  — apply one client-originated mutation (idempotent
//	                      via processed_events) by replaying it through the
//	                      router. Replayed requests carry X-Sync-Origin so the
//	                      sync-log middleware does not re-enqueue them.
//	GET  /api/sync/events — return the cloud's own change feed since a cursor,
//	                      scoped to a store.
type SyncHandler struct {
	DB     *sql.DB
	Router http.Handler
}

func NewSyncHandler(db *sql.DB) *SyncHandler {
	return &SyncHandler{DB: db}
}

type syncApplyRequest struct {
	EventID   string          `json:"eventId"`
	Method    string          `json:"method"`
	Path      string          `json:"path"`
	Body      json.RawMessage `json:"body"`
	CreatedAt string          `json:"createdAt"`
}

// Apply replays a single client mutation against this server's router.
// Already-processed eventIds return {duplicate:true} without re-applying.
func (s *SyncHandler) Apply(w http.ResponseWriter, r *http.Request) {
	var req syncApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeSyncJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON payload"})
		return
	}
	if req.EventID == "" || req.Method == "" || req.Path == "" {
		writeSyncJSON(w, http.StatusBadRequest, map[string]string{"error": "eventId, method and path are required"})
		return
	}

	// Only mutating API writes may be replayed. Rejecting /sync prevents
	// recursive apply chains and /auth prevents credential-flow replays.
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		writeSyncJSON(w, http.StatusBadRequest, map[string]string{"error": "method must be a mutating verb"})
		return
	}
	if strings.HasPrefix(req.Path, "/sync") || strings.HasPrefix(req.Path, "/auth") ||
		strings.HasPrefix(req.Path, "/ws") {
		writeSyncJSON(w, http.StatusBadRequest, map[string]string{"error": "path is not replayable"})
		return
	}

	// Idempotency: apply each event exactly once.
	var actorID interface{}
	if claims, ok := middleware.GetUserFromContext(r.Context()); ok {
		actorID = claims.ID
	}
	res, err := s.DB.ExecContext(r.Context(),
		"INSERT INTO processed_events (event_id, modified_by) VALUES ($1, $2) ON CONFLICT (event_id) DO NOTHING",
		req.EventID, actorID)
	if err != nil {
		writeSyncJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeSyncJSON(w, http.StatusOK, map[string]interface{}{"applied": false, "duplicate": true})
		return
	}

	// Last-write-wins: skip replays older than the entity's most recent
	// mutation (which may have been a cloud-side write). Without this,
	// concurrent edits on both sides diverge permanently.
	entityKey := syncutil.EntityKey(req.Path, req.Body)
	eventTS := parseSyncTS(req.CreatedAt)
	if entityKey != "" && !eventTS.IsZero() {
		var lastTS time.Time
		if err := s.DB.QueryRowContext(r.Context(),
			"SELECT last_ts FROM sync_entity_ts WHERE entity_key = $1",
			entityKey).Scan(&lastTS); err == nil && !eventTS.After(lastTS) {
			writeSyncJSON(w, http.StatusOK, map[string]interface{}{"applied": false, "stale": true})
			return
		}
	}

	replay, err := newReplayRequest(r.Context(), req.Method, req.Path, req.Body)
	if err != nil {
		s.unmarkProcessed(r, req.EventID)
		writeSyncJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	replay.Header.Set("Content-Type", "application/json")
	replay.Header.Set("X-Sync-Origin", "client")
	if auth := r.Header.Get("Authorization"); auth != "" {
		replay.Header.Set("Authorization", auth)
	}

	rec := httptest.NewRecorder()
	s.Router.ServeHTTP(rec, replay)

	if rec.Code >= 400 {
		// Let the client retry: un-mark so a later push re-applies.
		s.unmarkProcessed(r, req.EventID)
		writeSyncJSON(w, http.StatusBadGateway, map[string]interface{}{
			"applied": false, "status": rec.Code, "error": rec.Body.String(),
		})
		return
	}
	if entityKey != "" && !eventTS.IsZero() {
		// Record the event's origin time so later pushes compare correctly.
		_, _ = s.DB.ExecContext(r.Context(),
			`INSERT INTO sync_entity_ts (entity_key, last_ts, modified_by) VALUES ($1, $2, $3)
			 ON CONFLICT (entity_key) DO UPDATE SET last_ts = EXCLUDED.last_ts, modified_by = EXCLUDED.modified_by
			 WHERE EXCLUDED.last_ts > sync_entity_ts.last_ts`,
			entityKey, eventTS, actorID)
	}
	writeSyncJSON(w, http.StatusOK, map[string]interface{}{"applied": true, "status": rec.Code})
}

// parseSyncTS accepts RFC3339-ish timestamps ("2026-09-24T19:42:19.638066Z",
// or the space-separated form Postgres drivers emit) and returns UTC.
func parseSyncTS(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano, "2006-01-02T15:04:05.999999Z07:00",
		"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// newReplayRequest builds the in-process replay of a client mutation. The
// parent request's chi RouteCtx is stripped first: chi's ServeHTTP reuses a
// RouteCtx found on the request context (sub-route dispatch), which would keep
// the parent's RouteMethod/RoutePath and route this replay as POST
// /sync/apply — i.e. PUT/PATCH events would 405.
func newReplayRequest(parent context.Context, method, path string, body []byte) (*http.Request, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	var bodyReader *bytes.Reader
	if len(body) > 0 && string(body) != "null" {
		bodyReader = bytes.NewReader(body)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	ctx := context.WithValue(parent, chi.RouteCtxKey, nil)
	return http.NewRequestWithContext(ctx, method, "/api"+path, bodyReader)
}

func (s *SyncHandler) unmarkProcessed(r *http.Request, eventID string) {
	_, _ = s.DB.ExecContext(r.Context(),
		"DELETE FROM processed_events WHERE event_id = $1", eventID)
}

// Events returns the cloud change feed. `since` is the last seq the client
// applied; `since=-1` returns only the current `latest` cursor (used after a
// full snapshot so history isn't replayed). `storeId` scopes the feed — the
// local system serves exactly one store.
func (s *SyncHandler) Events(w http.ResponseWriter, r *http.Request) {
	claims, ok := middleware.GetUserFromContext(r.Context())
	if !ok {
		writeSyncJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return
	}

	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	storeID := r.URL.Query().Get("storeId")
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	if limit <= 0 || limit > 1000 {
		limit = 500
	}

	// The change feed is store-scoped; verify the caller may read it.
	if storeID == "" {
		storeID = claims.StoreID
	}
	if !s.canAccessStore(r, claims, storeID) {
		writeSyncJSON(w, http.StatusForbidden, map[string]string{"error": "Access denied to this store"})
		return
	}

	var latest int64
	if err := s.DB.QueryRowContext(r.Context(),
		"SELECT COALESCE(MAX(seq), 0) FROM sync_events").Scan(&latest); err != nil {
		writeSyncJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if since < 0 {
		writeSyncJSON(w, http.StatusOK, map[string]interface{}{"latest": latest, "events": []interface{}{}})
		return
	}

	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT seq, method, path, body, COALESCE(store_id, ''), created_at
		FROM sync_events
		WHERE seq > $1 AND (store_id = $2 OR store_id IS NULL OR store_id = '')
		ORDER BY seq ASC
		LIMIT $3`, since, storeID, limit)
	if err != nil {
		writeSyncJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	type evt struct {
		Seq       int64           `json:"seq"`
		Method    string          `json:"method"`
		Path      string          `json:"path"`
		Body      json.RawMessage `json:"body"`
		StoreID   string          `json:"storeId"`
		CreatedAt time.Time       `json:"createdAt"`
	}
	events := []evt{}
	for rows.Next() {
		var e evt
		var body []byte
		if err := rows.Scan(&e.Seq, &e.Method, &e.Path, &body, &e.StoreID, &e.CreatedAt); err != nil {
			continue
		}
		e.Body = body
		events = append(events, e)
	}
	writeSyncJSON(w, http.StatusOK, map[string]interface{}{"latest": latest, "events": events})
}

// canAccessStore mirrors the handler-layer store RBAC for the sync feed:
// superadmin → all stores, business_owner → user_stores membership,
// business_admin/staff → their assigned store only.
func (s *SyncHandler) canAccessStore(r *http.Request, claims *middleware.UserClaims, storeID string) bool {
	if storeID == "" {
		return false
	}
	switch claims.Role {
	case "superadmin":
		return true
	case "business_owner":
		var exists bool
		if err := s.DB.QueryRowContext(r.Context(),
			"SELECT EXISTS(SELECT 1 FROM user_stores WHERE user_id = $1 AND store_id = $2)",
			claims.ID, storeID).Scan(&exists); err != nil {
			return false
		}
		return exists
	default: // business_admin, staff
		return claims.StoreID == storeID
	}
}

func writeSyncJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
