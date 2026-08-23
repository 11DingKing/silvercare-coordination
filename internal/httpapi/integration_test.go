package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/audit"
	"github.com/11DingKing/silvercare-coordination/internal/auth"
	"github.com/11DingKing/silvercare-coordination/internal/benefit"
	"github.com/11DingKing/silvercare-coordination/internal/clock"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
	"github.com/11DingKing/silvercare-coordination/internal/eligibility"
	"github.com/11DingKing/silvercare-coordination/internal/escalation"
	"github.com/11DingKing/silvercare-coordination/internal/idgen"
	"github.com/11DingKing/silvercare-coordination/internal/outbox"
	"github.com/11DingKing/silvercare-coordination/internal/plan"
	"github.com/11DingKing/silvercare-coordination/internal/provider"
	"github.com/11DingKing/silvercare-coordination/internal/resident"
	"github.com/11DingKing/silvercare-coordination/internal/resource"
	storesqlite "github.com/11DingKing/silvercare-coordination/internal/store/sqlite"
	"github.com/11DingKing/silvercare-coordination/internal/visit"
)

type integrationFixture struct {
	api  *API
	repo *storesqlite.Store
	now  *clock.Fixed
}

func newIntegrationFixture(t *testing.T) integrationFixture {
	t.Helper()
	ctx := context.Background()
	now := clock.NewFixed(time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC))
	repo, err := storesqlite.Open(ctx, filepath.Join(t.TempDir(), "integration.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnsureDistrict(ctx, nil, storesqlite.DistrictBudget{ID: "district_1", Name: "示范街道", Timezone: "Asia/Shanghai", MonthlyCents: 100_000_000, Version: 1, CreatedAt: now.Now(), UpdatedAt: now.Now()}); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("integration password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateUser(ctx, nil, domain.User{ID: "coordinator_1", DistrictID: "district_1", Email: "coordinator@example.test", PasswordHash: hash, DisplayName: "集成测试协调员", Role: domain.RoleCoordinator, Active: true, CreatedAt: now.Now(), UpdatedAt: now.Now()}); err != nil {
		t.Fatal(err)
	}
	ids := &idgen.Sequence{}
	recorder := audit.NewRecorder(repo)
	events := outbox.NewPublisher(repo, ids)
	services := Services{
		Auth:        auth.NewService(repo, ids, now, time.Hour),
		Residents:   resident.NewService(repo, ids, now, recorder, events),
		Eligibility: eligibility.NewService(repo, ids, now, recorder, events),
		Plans:       plan.NewService(repo, ids, now, recorder, events),
		Providers:   provider.NewService(repo, ids, now, recorder, events),
		Benefits:    benefit.NewService(repo, ids, now, recorder, events),
		Visits:      visit.NewService(repo, ids, now, recorder, events),
		Resources:   resource.NewService(repo, ids, now, recorder, events),
		Escalations: escalation.NewService(repo, ids, now, recorder, events),
		Audit:       audit.NewService(repo),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return integrationFixture{api: New(services, repo, ids, logger), repo: repo, now: now}
}

func performJSON(t *testing.T, api http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, payload)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "request-integration")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)
	return recorder
}

func loginToken(t *testing.T, fixture integrationFixture) string {
	t.Helper()
	recorder := performJSON(t, fixture.api, http.MethodPost, "/v1/sessions", "", map[string]any{"district_id": "district_1", "email": "coordinator@example.test", "password": "integration password"})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("login status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Token == "" {
		t.Fatalf("login response=%s", recorder.Body.String())
	}
	return response.Data.Token
}

func TestResidentHTTPTransactionPersistsAuditAndOutbox(t *testing.T) {
	fixture := newIntegrationFixture(t)
	token := loginToken(t, fixture)
	created := performJSON(t, fixture.api, http.MethodPost, "/v1/residents", token, map[string]any{"external_ref": "EXT-001", "full_name": "张建国", "household_id": "HOUSE-001", "birth_date": "1950-03-10"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var response struct {
		Data domain.Resident `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.ID == "" || response.Data.Version != 1 {
		t.Fatalf("resident=%+v", response.Data)
	}
	loaded, err := fixture.repo.ResidentByID(context.Background(), nil, response.Data.ID, "district_1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ExternalRef != "EXT-001" {
		t.Fatalf("loaded=%+v", loaded)
	}
	var auditCount, eventCount int
	if err := fixture.repo.DB().QueryRow("SELECT COUNT(*) FROM audit_events WHERE object_id=?", response.Data.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.DB().QueryRow("SELECT COUNT(*) FROM outbox_events WHERE aggregate_id=?", response.Data.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 || eventCount != 1 {
		t.Fatalf("audit=%d outbox=%d", auditCount, eventCount)
	}

	consent := performJSON(t, fixture.api, http.MethodPost, "/v1/residents/"+response.Data.ID+"/consent", token, map[string]any{"expires_at": fixture.now.Now().AddDate(1, 0, 0).Format(time.RFC3339), "expected_version": 1})
	if consent.Code != http.StatusOK {
		t.Fatalf("consent status=%d body=%s", consent.Code, consent.Body.String())
	}
	loaded, err = fixture.repo.ResidentByID(context.Background(), nil, response.Data.ID, "district_1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConsentStatus != domain.ConsentGranted || loaded.Version != 2 {
		t.Fatalf("loaded=%+v", loaded)
	}
}

func TestDuplicateResidentRollsBackAuditAndOutbox(t *testing.T) {
	fixture := newIntegrationFixture(t)
	token := loginToken(t, fixture)
	payload := map[string]any{"external_ref": "EXT-DUP", "full_name": "李秀兰", "household_id": "HOUSE-002", "birth_date": "1948-05-20"}
	first := performJSON(t, fixture.api, http.MethodPost, "/v1/residents", token, payload)
	if first.Code != http.StatusCreated {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	second := performJSON(t, fixture.api, http.MethodPost, "/v1/residents", token, payload)
	if second.Code != http.StatusConflict {
		t.Fatalf("second=%d %s", second.Code, second.Body.String())
	}
	var residents, audits, events int
	_ = fixture.repo.DB().QueryRow("SELECT COUNT(*) FROM residents WHERE external_ref='EXT-DUP'").Scan(&residents)
	_ = fixture.repo.DB().QueryRow("SELECT COUNT(*) FROM audit_events WHERE action='enroll'").Scan(&audits)
	_ = fixture.repo.DB().QueryRow("SELECT COUNT(*) FROM outbox_events WHERE topic='resident.enrolled'").Scan(&events)
	if residents != 1 || audits != 1 || events != 1 {
		t.Fatalf("residents=%d audits=%d events=%d", residents, audits, events)
	}
}

func TestExpiredSessionRejectedAcrossHTTPBoundary(t *testing.T) {
	fixture := newIntegrationFixture(t)
	token := loginToken(t, fixture)
	fixture.now.Advance(2 * time.Hour)
	recorder := performJSON(t, fixture.api, http.MethodGet, "/v1/me", token, nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	errorObject := response["error"].(map[string]any)
	if errorObject["code"] != "unauthorized" {
		t.Fatalf("error=%#v", errorObject)
	}
}
