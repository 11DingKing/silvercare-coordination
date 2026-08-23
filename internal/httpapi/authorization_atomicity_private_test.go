package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestFailedAuthorizationDoesNotConsumeDistrictBudget(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	now := fixture.now.Now()
	timestamp := now.Format(time.RFC3339Nano)
	if _, err := fixture.repo.DB().ExecContext(ctx, `
		UPDATE districts
		SET monthly_budget_cents = 10000, reserved_budget_cents = 0,
			settled_budget_cents = 0, version = 1, updated_at = ?
		WHERE id = 'district_1';
		INSERT INTO residents(
			id, district_id, external_ref, full_name, birth_date, household_id,
			consent_status, consent_expires_at, version, created_at, updated_at
		) VALUES('resident_budget', 'district_1', 'EXT-BUDGET', '王慧敏', '1952-06-08',
			'household_budget', 'granted', ?, 1, ?, ?);
		INSERT INTO assessments(
			id, resident_id, assessor_id, status, support_level, evidence_json,
			valid_until, version, created_at, updated_at
		) VALUES('assessment_budget', 'resident_budget', 'coordinator_1', 'approved', 3,
			'["home visit"]', ?, 1, ?, ?);
		INSERT INTO support_plans(
			id, resident_id, assessment_id, coordinator_id, status, starts_at, ends_at,
			goals_json, version, created_at, updated_at
		) VALUES('plan_budget', 'resident_budget', 'assessment_budget', 'coordinator_1',
			'active', ?, ?, '["居家支持"]', 1, ?, ?);
		CREATE TRIGGER reject_authorization_event
		BEFORE INSERT ON outbox_events
		WHEN NEW.topic = 'authorization.reserved'
		BEGIN
			SELECT RAISE(ABORT, 'forced authorization event failure');
		END;
	`, timestamp, now.AddDate(1, 0, 0).Format(time.RFC3339Nano), timestamp, timestamp,
		now.AddDate(1, 0, 0).Format(time.RFC3339Nano), timestamp, timestamp,
		now.Add(-time.Hour).Format(time.RFC3339Nano), now.AddDate(0, 1, 0).Format(time.RFC3339Nano), timestamp, timestamp); err != nil {
		t.Fatalf("prepare authorization scenario: %v", err)
	}

	token := loginToken(t, fixture)
	payload := map[string]any{
		"service_code":     "HOME_SUPPORT",
		"units":            6,
		"unit_price_cents": 1000,
		"starts_at":        now.Format(time.RFC3339Nano),
		"ends_at":          now.AddDate(0, 0, 14).Format(time.RFC3339Nano),
	}
	failed := performJSON(t, fixture.api, http.MethodPost, "/v1/plans/plan_budget/authorizations", token, payload)
	if failed.Code != http.StatusConflict {
		t.Errorf("failed authorization status=%d body=%s", failed.Code, failed.Body.String())
	}

	budget, err := fixture.repo.DistrictBudget(ctx, nil, "district_1")
	if err != nil {
		t.Fatal(err)
	}
	if budget.ReservedCents != 0 {
		t.Errorf("failed authorization reserved budget=%d, want 0", budget.ReservedCents)
	}
	var authorizations, audits, events int
	if err := fixture.repo.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM authorizations WHERE plan_id = 'plan_budget'").Scan(&authorizations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE object_type = 'authorization'").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox_events WHERE topic = 'authorization.reserved'").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if authorizations != 0 || audits != 0 || events != 0 {
		t.Errorf("failed request persisted authorizations=%d audits=%d events=%d", authorizations, audits, events)
	}

	if _, err := fixture.repo.DB().ExecContext(ctx, "DROP TRIGGER reject_authorization_event"); err != nil {
		t.Fatal(err)
	}
	succeeded := performJSON(t, fixture.api, http.MethodPost, "/v1/plans/plan_budget/authorizations", token, payload)
	if succeeded.Code != http.StatusCreated {
		t.Errorf("authorization after recovery status=%d body=%s", succeeded.Code, succeeded.Body.String())
	}
}
