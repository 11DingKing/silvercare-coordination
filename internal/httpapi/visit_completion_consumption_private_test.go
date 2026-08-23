package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/auth"
	"github.com/11DingKing/silvercare-coordination/internal/domain"
)

func TestFailedVisitCompletionDoesNotConsumeAuthorization(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	now := fixture.now.Now()
	hash, err := auth.HashPassword("provider password")
	if err != nil {
		t.Fatal(err)
	}
	stamp := now.Format(time.RFC3339Nano)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := fixture.repo.DB().ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("prepare visit completion: %v", err)
		}
	}
	exec(`INSERT INTO users(id,district_id,email,password_hash,display_name,role,active,created_at,updated_at) VALUES('provider_user','district_1','provider@example.test',?,'上门服务员','provider',1,?,?)`, hash, stamp, stamp)
	exec(`INSERT INTO residents(id,district_id,external_ref,full_name,birth_date,household_id,consent_status,consent_expires_at,version,created_at,updated_at) VALUES('resident_visit','district_1','EXT-VISIT','刘桂英','1945-02-01','HOUSE-VISIT','granted',?,1,?,?)`, now.AddDate(1, 0, 0).Format(time.RFC3339Nano), stamp, stamp)
	exec(`INSERT INTO assessments(id,resident_id,assessor_id,status,support_level,evidence_json,valid_until,version,created_at,updated_at) VALUES('assessment_visit','resident_visit','coordinator_1','approved',2,'{}',?,1,?,?)`, now.AddDate(1, 0, 0).Format(time.RFC3339Nano), stamp, stamp)
	exec(`INSERT INTO support_plans(id,resident_id,assessment_id,coordinator_id,status,starts_at,ends_at,goals_json,version,created_at,updated_at) VALUES('plan_visit','resident_visit','assessment_visit','coordinator_1','active',?,?,'[]',1,?,?)`, now.Add(-2*time.Hour).Format(time.RFC3339Nano), now.Add(2*time.Hour).Format(time.RFC3339Nano), stamp, stamp)
	exec(`INSERT INTO providers(id,district_id,name,accreditation_status,capabilities_json,capacity_per_day,version,created_at,updated_at) VALUES('provider_visit','district_1','银龄照护站','active','["home_support"]',8,1,?,?)`, stamp, stamp)
	exec(`INSERT INTO authorizations(id,plan_id,district_id,service_code,status,units_authorized,units_consumed,unit_price_cents,reserved_cents,starts_at,ends_at,version,created_at,updated_at) VALUES('authorization_visit','plan_visit','district_1','home_support','active',3,0,1000,3000,?,?,1,?,?)`, now.Add(-2*time.Hour).Format(time.RFC3339Nano), now.Add(2*time.Hour).Format(time.RFC3339Nano), stamp, stamp)
	exec(`INSERT INTO visits(id,authorization_id,provider_id,assigned_user_id,resident_id,status,scheduled_start,scheduled_end,checked_in_at,completed_at,evidence_json,version,created_at,updated_at) VALUES('visit_consumption','authorization_visit','provider_visit','provider_user','resident_visit','checked_in',?,?,?,NULL,NULL,3,?,?)`, now.Add(-20*time.Minute).Format(time.RFC3339Nano), now.Add(40*time.Minute).Format(time.RFC3339Nano), now.Add(-15*time.Minute).Format(time.RFC3339Nano), stamp, stamp)
	exec(`CREATE TRIGGER reject_visit_completion_event BEFORE INSERT ON outbox_events WHEN NEW.topic = 'visit.complete' BEGIN SELECT RAISE(ABORT,'forced completion event failure'); END`)
	login := performJSON(t, fixture.api, http.MethodPost, "/v1/sessions", "", map[string]any{"district_id": "district_1", "email": "provider@example.test", "password": "provider password"})
	if login.Code != http.StatusCreated {
		t.Fatalf("login=%d %s", login.Code, login.Body.String())
	}
	var loginBody struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"at": now.Add(20 * time.Minute).Format(time.RFC3339Nano), "evidence": map[string]string{"resident_confirmation": "confirmed", "service_note": "completed"}, "expected_version": 3}
	failed := performJSON(t, fixture.api, http.MethodPost, "/v1/visits/visit_consumption/complete", loginBody.Data.Token, payload)
	if failed.Code == http.StatusOK {
		t.Fatal("completion unexpectedly succeeded")
	}
	var used, authVersion, visitVersion int
	if err := fixture.repo.DB().QueryRowContext(ctx, "SELECT units_consumed,version FROM authorizations WHERE id='authorization_visit'").Scan(&used, &authVersion); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.DB().QueryRowContext(ctx, "SELECT version FROM visits WHERE id='visit_consumption'").Scan(&visitVersion); err != nil {
		t.Fatal(err)
	}
	if used != 0 || authVersion != 1 || visitVersion != 3 {
		t.Errorf("partial completion consumed=%d auth_version=%d visit_version=%d", used, authVersion, visitVersion)
	}
	if _, err := fixture.repo.DB().ExecContext(ctx, "DROP TRIGGER reject_visit_completion_event"); err != nil {
		t.Fatal(err)
	}
	retry := performJSON(t, fixture.api, http.MethodPost, "/v1/visits/visit_consumption/complete", loginBody.Data.Token, payload)
	if retry.Code != http.StatusOK {
		t.Fatalf("retry=%d %s", retry.Code, retry.Body.String())
	}
	if err := fixture.repo.DB().QueryRowContext(ctx, "SELECT units_consumed FROM authorizations WHERE id='authorization_visit'").Scan(&used); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := fixture.repo.DB().QueryRowContext(ctx, "SELECT status FROM visits WHERE id='visit_consumption'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if used != 1 || status != string(domain.VisitCompleted) {
		t.Errorf("retry state used=%d status=%s", used, status)
	}
}
