package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

type VisitStatus string

const (
	VisitScheduled VisitStatus = "scheduled"
	VisitAccepted  VisitStatus = "accepted"
	VisitEnroute   VisitStatus = "enroute"
	VisitCheckedIn VisitStatus = "checked_in"
	VisitCompleted VisitStatus = "completed"
	VisitCancelled VisitStatus = "cancelled"
	VisitDisputed  VisitStatus = "disputed"
)

type Visit struct {
	ID              string
	AuthorizationID string
	ProviderID      string
	AssignedUserID  string
	ResidentID      string
	Status          VisitStatus
	ScheduledStart  time.Time
	ScheduledEnd    time.Time
	CheckedInAt     *time.Time
	CompletedAt     *time.Time
	Evidence        map[string]string
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func NewVisit(id string, authorization Authorization, provider Provider, residentID string, start, end, now time.Time) (Visit, error) {
	v := Visit{
		ID: id, AuthorizationID: authorization.ID, ProviderID: provider.ID, ResidentID: residentID,
		Status: VisitScheduled, ScheduledStart: start.UTC(), ScheduledEnd: end.UTC(), Evidence: map[string]string{},
		Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	if !authorization.Covers(start) || !authorization.Covers(end.Add(-time.Nanosecond)) {
		return Visit{}, fmt.Errorf("visit falls outside the authorization period")
	}
	if !provider.Supports(authorization.ServiceCode) {
		return Visit{}, fmt.Errorf("provider cannot deliver the authorized service")
	}
	if err := v.Validate(); err != nil {
		return Visit{}, err
	}
	return v, nil
}

func (v Visit) Validate() error {
	if v.ID == "" || v.AuthorizationID == "" || v.ProviderID == "" || v.ResidentID == "" {
		return fmt.Errorf("visit identity is incomplete")
	}
	if !v.ScheduledEnd.After(v.ScheduledStart) || v.ScheduledEnd.Sub(v.ScheduledStart) > 12*time.Hour {
		return fmt.Errorf("visit window is invalid")
	}
	if v.Status == VisitCompleted && (v.CheckedInAt == nil || v.CompletedAt == nil || len(v.Evidence) == 0) {
		return fmt.Errorf("completed visit requires check-in, completion, and evidence")
	}
	return nil
}

func (v Visit) Assign(userID string, now time.Time) (Visit, error) {
	if v.Status != VisitScheduled || userID == "" {
		return Visit{}, fmt.Errorf("only an unassigned scheduled visit can be assigned")
	}
	v.AssignedUserID = userID
	v.Version++
	v.UpdatedAt = now.UTC()
	return v, nil
}

func (v Visit) Accept(userID string, now time.Time) (Visit, error) {
	if v.Status != VisitScheduled || v.AssignedUserID != userID {
		return Visit{}, fmt.Errorf("visit can only be accepted by its assigned provider user")
	}
	v.Status = VisitAccepted
	v.Version++
	v.UpdatedAt = now.UTC()
	return v, nil
}

func (v Visit) StartTravel(userID string, now time.Time) (Visit, error) {
	if v.Status != VisitAccepted || v.AssignedUserID != userID {
		return Visit{}, fmt.Errorf("accepted visit ownership is required")
	}
	v.Status = VisitEnroute
	v.Version++
	v.UpdatedAt = now.UTC()
	return v, nil
}

func (v Visit) CheckIn(userID string, at, now time.Time) (Visit, error) {
	if v.Status != VisitEnroute || v.AssignedUserID != userID {
		return Visit{}, fmt.Errorf("enroute visit ownership is required")
	}
	if at.Before(v.ScheduledStart.Add(-30*time.Minute)) || at.After(v.ScheduledEnd) {
		return Visit{}, fmt.Errorf("check-in is outside the permitted window")
	}
	at = at.UTC()
	v.CheckedInAt = &at
	v.Status = VisitCheckedIn
	v.Version++
	v.UpdatedAt = now.UTC()
	return v, nil
}

func (v Visit) Complete(userID string, evidence map[string]string, at, now time.Time) (Visit, error) {
	if v.Status != VisitCheckedIn || v.AssignedUserID != userID || v.CheckedInAt == nil {
		return Visit{}, fmt.Errorf("checked-in visit ownership is required")
	}
	if at.Before(*v.CheckedInAt) || at.Sub(*v.CheckedInAt) < 10*time.Minute {
		return Visit{}, fmt.Errorf("visit completion duration is too short")
	}
	if evidence["resident_confirmation"] == "" || evidence["service_note"] == "" {
		return Visit{}, fmt.Errorf("completion requires resident confirmation and a service note")
	}
	v.Evidence = cloneStringMap(evidence)
	at = at.UTC()
	v.CompletedAt = &at
	v.Status = VisitCompleted
	v.Version++
	v.UpdatedAt = now.UTC()
	return v, v.Validate()
}

func (v Visit) EvidenceJSON() (string, error) {
	if len(v.Evidence) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(v.Evidence)
	if err != nil {
		return "", fmt.Errorf("encode visit evidence: %w", err)
	}
	return string(raw), nil
}

func WindowsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}
