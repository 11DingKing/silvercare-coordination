package domain

import (
	"testing"
	"time"
)

func activeResident(now time.Time) Resident {
	until := now.AddDate(1, 0, 0)
	return Resident{ID: "resident_1", DistrictID: "district_1", ExternalRef: "EXT", FullName: "赵明德", BirthDate: now.AddDate(-70, 0, 0), HouseholdID: "household_1", ConsentStatus: ConsentGranted, ConsentExpiresAt: &until, Version: 2, CreatedAt: now, UpdatedAt: now}
}

func approvedAssessment(now time.Time) Assessment {
	return Assessment{ID: "assessment_1", ResidentID: "resident_1", AssessorID: "user_1", Status: AssessmentApproved, SupportLevel: 3, Evidence: map[string]string{"a": "1", "b": "2"}, ValidUntil: now.AddDate(0, 6, 0), Version: 4, CreatedAt: now, UpdatedAt: now}
}

func TestSupportPlanActivationChecksRelatedEntities(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p, err := NewSupportPlan("plan_1", "resident_1", "assessment_1", "user_1", now, now.AddDate(0, 3, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.SubmitForReview(now); err == nil {
		t.Fatal("goal-less plan submitted")
	}
	p, err = p.AddGoal("保持每周居家支持履约连续", now)
	if err != nil {
		t.Fatal(err)
	}
	p, err = p.SubmitForReview(now)
	if err != nil {
		t.Fatal(err)
	}
	p, err = p.Activate(approvedAssessment(now), activeResident(now), now)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if p.Status != PlanActive {
		t.Fatalf("status = %s", p.Status)
	}
}

func TestSupportPlanRejectsMismatchedAssessment(t *testing.T) {
	now := time.Now().UTC()
	p, _ := NewSupportPlan("plan_1", "resident_1", "assessment_1", "user_1", now, now.AddDate(0, 1, 0), now)
	p, _ = p.AddGoal("维持稳定的家庭支持安排", now)
	p, _ = p.SubmitForReview(now)
	a := approvedAssessment(now)
	a.ResidentID = "resident_other"
	if _, err := p.Activate(a, activeResident(now), now); err == nil {
		t.Fatal("mismatched assessment activated plan")
	}
}

func TestSupportPlanSuspendAndResume(t *testing.T) {
	now := time.Now().UTC()
	p := SupportPlan{ID: "plan_1", ResidentID: "resident_1", AssessmentID: "assessment_1", CoordinatorID: "user_1", Status: PlanActive, StartsAt: now, EndsAt: now.AddDate(0, 3, 0), Goals: []string{"goal"}, Version: 3, CreatedAt: now, UpdatedAt: now}
	p, err := p.Suspend(now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != PlanSuspended {
		t.Fatalf("status = %s", p.Status)
	}
	p, err = p.Resume(activeResident(now), approvedAssessment(now), now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != PlanActive {
		t.Fatalf("status = %s", p.Status)
	}
}
