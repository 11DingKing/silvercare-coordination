package domain

import (
	"fmt"
	"strings"
	"time"
)

type Severity string

const (
	SeverityStandard Severity = "standard"
	SeverityUrgent   Severity = "urgent"
	SeverityCritical Severity = "critical"
)

type EscalationStatus string

const (
	EscalationOpen         EscalationStatus = "open"
	EscalationAcknowledged EscalationStatus = "acknowledged"
	EscalationResolved     EscalationStatus = "resolved"
	EscalationExpired      EscalationStatus = "expired"
)

type Escalation struct {
	ID                   string
	DistrictID           string
	ResidentID           string
	VisitID              string
	Severity             Severity
	Status               EscalationStatus
	Summary              string
	AcknowledgementDueAt time.Time
	AcknowledgedBy       string
	AcknowledgedAt       *time.Time
	ResolvedAt           *time.Time
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func NewEscalation(id, districtID, residentID, visitID string, severity Severity, summary string, now time.Time) (Escalation, error) {
	deadline, err := acknowledgementDeadline(severity, now)
	if err != nil {
		return Escalation{}, err
	}
	e := Escalation{
		ID: id, DistrictID: districtID, ResidentID: residentID, VisitID: visitID,
		Severity: severity, Status: EscalationOpen, Summary: strings.TrimSpace(summary),
		AcknowledgementDueAt: deadline, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	return e, e.Validate()
}

func (e Escalation) Validate() error {
	if e.ID == "" || e.DistrictID == "" || e.ResidentID == "" || len([]rune(e.Summary)) < 10 {
		return fmt.Errorf("escalation identity or summary is invalid")
	}
	if !e.AcknowledgementDueAt.After(e.CreatedAt) {
		return fmt.Errorf("acknowledgement deadline must follow creation")
	}
	if e.Status == EscalationAcknowledged && (e.AcknowledgedBy == "" || e.AcknowledgedAt == nil) {
		return fmt.Errorf("acknowledged escalation requires ownership and time")
	}
	return nil
}

func (e Escalation) Acknowledge(actorID string, now time.Time) (Escalation, error) {
	if e.Status != EscalationOpen || actorID == "" {
		return Escalation{}, fmt.Errorf("only open escalations can be acknowledged")
	}
	if now.After(e.AcknowledgementDueAt) {
		return Escalation{}, fmt.Errorf("acknowledgement deadline has passed")
	}
	now = now.UTC()
	e.Status = EscalationAcknowledged
	e.AcknowledgedBy = actorID
	e.AcknowledgedAt = &now
	e.Version++
	e.UpdatedAt = now
	return e, nil
}

func (e Escalation) Resolve(actorID string, now time.Time) (Escalation, error) {
	if e.Status != EscalationAcknowledged || e.AcknowledgedBy != actorID {
		return Escalation{}, fmt.Errorf("acknowledging actor must resolve escalation")
	}
	now = now.UTC()
	e.Status = EscalationResolved
	e.ResolvedAt = &now
	e.Version++
	e.UpdatedAt = now
	return e, nil
}

func (e Escalation) Expire(now time.Time) (Escalation, error) {
	if e.Status != EscalationOpen || now.Before(e.AcknowledgementDueAt) {
		return Escalation{}, fmt.Errorf("escalation is not overdue and open")
	}
	e.Status = EscalationExpired
	e.Version++
	e.UpdatedAt = now.UTC()
	return e, nil
}

func acknowledgementDeadline(severity Severity, now time.Time) (time.Time, error) {
	switch severity {
	case SeverityStandard:
		return now.UTC().Add(4 * time.Hour), nil
	case SeverityUrgent:
		return now.UTC().Add(30 * time.Minute), nil
	case SeverityCritical:
		return now.UTC().Add(5 * time.Minute), nil
	default:
		return time.Time{}, fmt.Errorf("unknown severity %q", severity)
	}
}
