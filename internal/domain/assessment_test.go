package domain

import (
	"testing"
	"time"
)

func TestAssessmentRequiresEvidenceBeforeApproval(t *testing.T) {
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	a, err := NewAssessment("assessment_1", "resident_1", "user_1", 3, now.AddDate(0, 6, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Submit(now); err == nil {
		t.Fatal("empty assessment submitted")
	}
	a, err = a.PutEvidence("mobility", "requires support", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Submit(now); err == nil {
		t.Fatal("single evidence assessment submitted")
	}
	a, err = a.PutEvidence("household", "lives alone", now)
	if err != nil {
		t.Fatal(err)
	}
	a, err = a.Submit(now)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if a.Status != AssessmentSubmitted {
		t.Fatalf("status = %s", a.Status)
	}
	a, err = a.Approve(now.Add(time.Hour))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if a.Status != AssessmentApproved {
		t.Fatalf("status = %s", a.Status)
	}
}

func TestAssessmentEvidenceIsolatedOnChange(t *testing.T) {
	now := time.Now().UTC()
	a, err := NewAssessment("assessment_1", "resident_1", "user_1", 2, now.AddDate(0, 1, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	a.Evidence["existing"] = "kept"
	updated, err := a.PutEvidence("new", "value", now)
	if err != nil {
		t.Fatal(err)
	}
	updated.Evidence["existing"] = "changed"
	if a.Evidence["existing"] != "kept" {
		t.Fatal("assessment evidence shares mutable map")
	}
}

func TestAssessmentRejectsInvalidSupportLevels(t *testing.T) {
	now := time.Now().UTC()
	for _, level := range []int{-1, 0, 6, 100} {
		if _, err := NewAssessment("assessment_1", "resident_1", "user_1", level, now.AddDate(0, 1, 0), now); err == nil {
			t.Fatalf("level %d accepted", level)
		}
	}
}

func TestExpiredAssessmentCannotApprove(t *testing.T) {
	now := time.Now().UTC()
	a := Assessment{ID: "a", ResidentID: "r", AssessorID: "u", Status: AssessmentSubmitted, SupportLevel: 2, Evidence: map[string]string{"one": "1", "two": "2"}, ValidUntil: now.Add(-time.Minute), Version: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	if _, err := a.Approve(now); err == nil {
		t.Fatal("expired assessment approved")
	}
}
