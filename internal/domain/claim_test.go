package domain

import (
	"testing"
	"time"
)

func completedVisit(now time.Time) Visit {
	checked := now.Add(-time.Hour)
	completed := now
	return Visit{ID: "visit_1", AuthorizationID: "auth_1", ProviderID: "provider_1", ResidentID: "resident_1", Status: VisitCompleted, ScheduledStart: now.Add(-2 * time.Hour), ScheduledEnd: now, CheckedInAt: &checked, CompletedAt: &completed, Evidence: map[string]string{"resident_confirmation": "yes"}, Version: 5}
}

func TestClaimReviewLifecycle(t *testing.T) {
	now := time.Now().UTC()
	a := Authorization{ID: "auth_1", DistrictID: "district_1", UnitPriceCents: 2500}
	c, err := NewClaim("claim_1", "district_1", completedVisit(now), a, now)
	if err != nil {
		t.Fatal(err)
	}
	c, err = c.Submit(now)
	if err != nil {
		t.Fatal(err)
	}
	c, err = c.Reject("auditor_1", "服务证据需要重新补充", now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != ClaimRejected || c.RejectionReason == "" {
		t.Fatalf("claim=%+v", c)
	}
	c, err = c.Submit(now)
	if err != nil {
		t.Fatal(err)
	}
	if c.RejectionReason != "" || c.ReviewerID != "" {
		t.Fatal("resubmitted claim retained review state")
	}
	c, err = c.Approve("auditor_1", now)
	if err != nil {
		t.Fatal(err)
	}
	c, err = c.MarkPaid(now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != ClaimPaid {
		t.Fatalf("status = %s", c.Status)
	}
}

func TestClaimRequiresCompletedVisit(t *testing.T) {
	now := time.Now().UTC()
	v := completedVisit(now)
	v.Status = VisitCheckedIn
	v.CompletedAt = nil
	if _, err := NewClaim("claim_1", "district_1", v, Authorization{ID: "auth_1", DistrictID: "district_1", UnitPriceCents: 2500}, now); err == nil {
		t.Fatal("incomplete visit created claim")
	}
}
