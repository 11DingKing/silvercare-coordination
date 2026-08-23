package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

type AssessmentStatus string

const (
	AssessmentDraft      AssessmentStatus = "draft"
	AssessmentSubmitted  AssessmentStatus = "submitted"
	AssessmentApproved   AssessmentStatus = "approved"
	AssessmentExpired    AssessmentStatus = "expired"
	AssessmentSuperseded AssessmentStatus = "superseded"
)

type Assessment struct {
	ID           string
	ResidentID   string
	AssessorID   string
	Status       AssessmentStatus
	SupportLevel int
	Evidence     map[string]string
	ValidUntil   time.Time
	Version      int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func NewAssessment(id, residentID, assessorID string, supportLevel int, validUntil, now time.Time) (Assessment, error) {
	assessment := Assessment{
		ID:           id,
		ResidentID:   residentID,
		AssessorID:   assessorID,
		Status:       AssessmentDraft,
		SupportLevel: supportLevel,
		Evidence:     map[string]string{},
		ValidUntil:   validUntil.UTC(),
		Version:      1,
		CreatedAt:    now.UTC(),
		UpdatedAt:    now.UTC(),
	}
	if err := assessment.Validate(now); err != nil {
		return Assessment{}, err
	}
	return assessment, nil
}

func (a Assessment) Validate(now time.Time) error {
	if a.ID == "" || a.ResidentID == "" || a.AssessorID == "" {
		return fmt.Errorf("assessment identity is incomplete")
	}
	if a.SupportLevel < 1 || a.SupportLevel > 5 {
		return fmt.Errorf("support level must be between 1 and 5")
	}
	if !a.ValidUntil.After(now) && a.Status != AssessmentExpired && a.Status != AssessmentSuperseded {
		return fmt.Errorf("assessment validity must end in the future")
	}
	return nil
}

func (a Assessment) PutEvidence(key, value string, now time.Time) (Assessment, error) {
	if a.Status != AssessmentDraft {
		return Assessment{}, fmt.Errorf("only draft assessments can change evidence")
	}
	if key == "" || value == "" {
		return Assessment{}, fmt.Errorf("evidence key and value are required")
	}
	copy := cloneStringMap(a.Evidence)
	copy[key] = value
	a.Evidence = copy
	a.Version++
	a.UpdatedAt = now.UTC()
	return a, nil
}

func (a Assessment) Submit(now time.Time) (Assessment, error) {
	if a.Status != AssessmentDraft {
		return Assessment{}, fmt.Errorf("only a draft assessment can be submitted")
	}
	if len(a.Evidence) < 2 {
		return Assessment{}, fmt.Errorf("at least two independent evidence items are required")
	}
	a.Status = AssessmentSubmitted
	a.Version++
	a.UpdatedAt = now.UTC()
	return a, nil
}

func (a Assessment) Approve(now time.Time) (Assessment, error) {
	if a.Status != AssessmentSubmitted {
		return Assessment{}, fmt.Errorf("only a submitted assessment can be approved")
	}
	if !a.ValidUntil.After(now) {
		return Assessment{}, fmt.Errorf("expired evidence cannot be approved")
	}
	a.Status = AssessmentApproved
	a.Version++
	a.UpdatedAt = now.UTC()
	return a, nil
}

func (a Assessment) EvidenceJSON() (string, error) {
	raw, err := json.Marshal(a.Evidence)
	if err != nil {
		return "", fmt.Errorf("encode assessment evidence: %w", err)
	}
	return string(raw), nil
}

func cloneStringMap(source map[string]string) map[string]string {
	copy := make(map[string]string, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
