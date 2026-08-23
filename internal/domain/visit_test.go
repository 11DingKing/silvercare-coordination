package domain

import (
	"testing"
	"time"
)

func visitFixture(t *testing.T, now time.Time) Visit {
	t.Helper()
	a, err := NewAuthorization("auth_1", "plan_1", "district_1", "home_support", 2, 3000, now.Add(-time.Hour), now.AddDate(0, 1, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	a.Status = AuthorizationActive
	p, err := NewProvider("provider_1", "district_1", "安心服务", []string{"home_support"}, 5, now)
	if err != nil {
		t.Fatal(err)
	}
	p, err = p.Activate(now)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVisit("visit_1", a, p, "resident_1", now.Add(time.Hour), now.Add(2*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVisitLifecycleAndOwnership(t *testing.T) {
	now := time.Date(2026, 8, 23, 8, 0, 0, 0, time.UTC)
	v := visitFixture(t, now)
	v, err := v.Assign("provider_user", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Accept("other_user", now); err == nil {
		t.Fatal("other provider accepted visit")
	}
	v, err = v.Accept("provider_user", now)
	if err != nil {
		t.Fatal(err)
	}
	v, err = v.StartTravel("provider_user", now.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	v, err = v.CheckIn("provider_user", now.Add(time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	v, err = v.Complete("provider_user", map[string]string{"resident_confirmation": "signed", "service_note": "completed household support"}, now.Add(90*time.Minute), now.Add(90*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != VisitCompleted || v.CompletedAt == nil {
		t.Fatalf("status=%s completed=%v", v.Status, v.CompletedAt)
	}
}

func TestVisitCompletionRequiresEvidenceAndDuration(t *testing.T) {
	now := time.Now().UTC()
	v := visitFixture(t, now)
	v, _ = v.Assign("provider_user", now)
	v, _ = v.Accept("provider_user", now)
	v, _ = v.StartTravel("provider_user", now)
	v, _ = v.CheckIn("provider_user", now.Add(time.Hour), now)
	if _, err := v.Complete("provider_user", map[string]string{}, now.Add(2*time.Hour), now); err == nil {
		t.Fatal("evidence-free visit completed")
	}
	if _, err := v.Complete("provider_user", map[string]string{"resident_confirmation": "yes", "service_note": "done"}, now.Add(time.Hour+5*time.Minute), now); err == nil {
		t.Fatal("too-short visit completed")
	}
}

func TestVisitCheckInWindow(t *testing.T) {
	now := time.Now().UTC()
	v := visitFixture(t, now)
	v, _ = v.Assign("provider_user", now)
	v, _ = v.Accept("provider_user", now)
	v, _ = v.StartTravel("provider_user", now)
	if _, err := v.CheckIn("provider_user", now, now); err == nil {
		t.Fatal("early check-in accepted")
	}
	if _, err := v.CheckIn("provider_user", now.Add(2*time.Hour+time.Minute), now); err == nil {
		t.Fatal("late check-in accepted")
	}
}

func TestWindowsOverlap(t *testing.T) {
	base := time.Now().UTC()
	tests := []struct {
		name                       string
		aStart, aEnd, bStart, bEnd time.Time
		want                       bool
	}{
		{"overlap", base, base.Add(2 * time.Hour), base.Add(time.Hour), base.Add(3 * time.Hour), true},
		{"touching", base, base.Add(time.Hour), base.Add(time.Hour), base.Add(2 * time.Hour), false},
		{"contained", base, base.Add(4 * time.Hour), base.Add(time.Hour), base.Add(2 * time.Hour), true},
		{"separate", base, base.Add(time.Hour), base.Add(2 * time.Hour), base.Add(3 * time.Hour), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := WindowsOverlap(test.aStart, test.aEnd, test.bStart, test.bEnd); got != test.want {
				t.Fatalf("got %v want %v", got, test.want)
			}
		})
	}
}
