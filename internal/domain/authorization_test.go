package domain

import (
	"testing"
	"time"
)

func TestAuthorizationConsumptionTracksReservation(t *testing.T) {
	now := time.Now().UTC()
	a, err := NewAuthorization("auth_1", "plan_1", "district_1", "home_support", 3, 2500, now.Add(-time.Hour), now.AddDate(0, 1, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	plan := SupportPlan{ID: "plan_1", Status: PlanActive}
	a, err = a.Activate(plan, now)
	if err != nil {
		t.Fatal(err)
	}
	a, err = a.Consume(1, now)
	if err != nil {
		t.Fatal(err)
	}
	if a.UnitsConsumed != 1 || a.ReservedCents != 5000 {
		t.Fatalf("consumed=%d reserved=%d", a.UnitsConsumed, a.ReservedCents)
	}
	a, err = a.Consume(2, now)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != AuthorizationExhausted || a.ReservedCents != 0 {
		t.Fatalf("status=%s reserved=%d", a.Status, a.ReservedCents)
	}
}

func TestAuthorizationRejectsOverspend(t *testing.T) {
	now := time.Now().UTC()
	a, _ := NewAuthorization("auth_1", "plan_1", "district_1", "home_support", 2, 2500, now.Add(-time.Hour), now.AddDate(0, 1, 0), now)
	a, _ = a.Activate(SupportPlan{ID: "plan_1", Status: PlanActive}, now)
	if _, err := a.Consume(3, now); err == nil {
		t.Fatal("overspend accepted")
	}
	if a.UnitsConsumed != 0 || a.ReservedCents != 5000 {
		t.Fatal("failed consume mutated authorization")
	}
}

func TestAuthorizationCancellationReleasesRemainingBudget(t *testing.T) {
	now := time.Now().UTC()
	a, _ := NewAuthorization("auth_1", "plan_1", "district_1", "home_support", 4, 1000, now, now.AddDate(0, 1, 0), now)
	cancelled, release, err := a.Cancel(now)
	if err != nil {
		t.Fatal(err)
	}
	if release != 4000 || cancelled.Status != AuthorizationCancelled || cancelled.ReservedCents != 0 {
		t.Fatalf("release=%d status=%s reserved=%d", release, cancelled.Status, cancelled.ReservedCents)
	}
}
