package domain

import (
	"testing"
	"time"
)

func TestEscalationDeadlinesBySeverity(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		severity Severity
		want     time.Duration
	}{{SeverityStandard, 4 * time.Hour}, {SeverityUrgent, 30 * time.Minute}, {SeverityCritical, 5 * time.Minute}}
	for _, test := range tests {
		e, err := NewEscalation("e", "d", "r", "", test.severity, "需要及时协调后续支持服务", now)
		if err != nil {
			t.Fatal(err)
		}
		if got := e.AcknowledgementDueAt.Sub(now); got != test.want {
			t.Fatalf("severity=%s got=%s want=%s", test.severity, got, test.want)
		}
	}
}

func TestEscalationOwnershipAndExpiry(t *testing.T) {
	now := time.Now().UTC()
	e, _ := NewEscalation("e", "d", "r", "", SeverityUrgent, "需要及时协调后续支持服务", now)
	if _, err := e.Resolve("coordinator", now); err == nil {
		t.Fatal("unacknowledged escalation resolved")
	}
	e, err := e.Acknowledge("coordinator", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Resolve("other", now.Add(2*time.Minute)); err == nil {
		t.Fatal("other coordinator resolved escalation")
	}
	e, err = e.Resolve("coordinator", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != EscalationResolved {
		t.Fatalf("status = %s", e.Status)
	}

	overdue, _ := NewEscalation("e2", "d", "r", "", SeverityCritical, "需要立即升级无人确认的支持事项", now)
	overdue, err = overdue.Expire(now.Add(6 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if overdue.Status != EscalationExpired {
		t.Fatalf("status = %s", overdue.Status)
	}
}
