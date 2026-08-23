package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type PlanStatus string

const (
	PlanDraft     PlanStatus = "draft"
	PlanReview    PlanStatus = "review"
	PlanActive    PlanStatus = "active"
	PlanSuspended PlanStatus = "suspended"
	PlanCompleted PlanStatus = "completed"
	PlanCancelled PlanStatus = "cancelled"
)

type SupportPlan struct {
	ID            string
	ResidentID    string
	AssessmentID  string
	CoordinatorID string
	Status        PlanStatus
	StartsAt      time.Time
	EndsAt        time.Time
	Goals         []string
	Version       int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func NewSupportPlan(id, residentID, assessmentID, coordinatorID string, startsAt, endsAt, now time.Time) (SupportPlan, error) {
	plan := SupportPlan{
		ID: id, ResidentID: residentID, AssessmentID: assessmentID, CoordinatorID: coordinatorID,
		Status: PlanDraft, StartsAt: startsAt.UTC(), EndsAt: endsAt.UTC(), Goals: []string{},
		Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	if err := plan.Validate(); err != nil {
		return SupportPlan{}, err
	}
	return plan, nil
}

func (p SupportPlan) Validate() error {
	if p.ID == "" || p.ResidentID == "" || p.AssessmentID == "" || p.CoordinatorID == "" {
		return fmt.Errorf("support plan identity is incomplete")
	}
	if !p.EndsAt.After(p.StartsAt) {
		return fmt.Errorf("support plan end must follow start")
	}
	if p.EndsAt.Sub(p.StartsAt) > 370*24*time.Hour {
		return fmt.Errorf("support plan cannot exceed one year")
	}
	return nil
}

func (p SupportPlan) AddGoal(goal string, now time.Time) (SupportPlan, error) {
	if p.Status != PlanDraft {
		return SupportPlan{}, fmt.Errorf("goals can only change while a plan is draft")
	}
	goal = strings.TrimSpace(goal)
	if len([]rune(goal)) < 5 || len([]rune(goal)) > 240 {
		return SupportPlan{}, fmt.Errorf("goal length must be between 5 and 240 characters")
	}
	for _, existing := range p.Goals {
		if existing == goal {
			return SupportPlan{}, fmt.Errorf("duplicate support goal")
		}
	}
	p.Goals = append(append([]string(nil), p.Goals...), goal)
	p.Version++
	p.UpdatedAt = now.UTC()
	return p, nil
}

func (p SupportPlan) SubmitForReview(now time.Time) (SupportPlan, error) {
	if p.Status != PlanDraft {
		return SupportPlan{}, fmt.Errorf("only draft plans can enter review")
	}
	if len(p.Goals) == 0 {
		return SupportPlan{}, fmt.Errorf("a support plan requires at least one goal")
	}
	p.Status = PlanReview
	p.Version++
	p.UpdatedAt = now.UTC()
	return p, nil
}

func (p SupportPlan) Activate(assessment Assessment, resident Resident, now time.Time) (SupportPlan, error) {
	if p.Status != PlanReview {
		return SupportPlan{}, fmt.Errorf("only plans in review can activate")
	}
	if assessment.ID != p.AssessmentID || assessment.ResidentID != p.ResidentID || assessment.Status != AssessmentApproved {
		return SupportPlan{}, fmt.Errorf("plan requires its resident's approved assessment")
	}
	if !assessment.ValidUntil.After(now) {
		return SupportPlan{}, fmt.Errorf("plan assessment has expired")
	}
	if resident.ID != p.ResidentID || !resident.HasActiveConsent(now) {
		return SupportPlan{}, fmt.Errorf("resident consent is not active")
	}
	p.Status = PlanActive
	p.Version++
	p.UpdatedAt = now.UTC()
	return p, nil
}

func (p SupportPlan) Suspend(now time.Time) (SupportPlan, error) {
	if p.Status != PlanActive {
		return SupportPlan{}, fmt.Errorf("only active plans can be suspended")
	}
	p.Status = PlanSuspended
	p.Version++
	p.UpdatedAt = now.UTC()
	return p, nil
}

func (p SupportPlan) Resume(resident Resident, assessment Assessment, now time.Time) (SupportPlan, error) {
	if p.Status != PlanSuspended {
		return SupportPlan{}, fmt.Errorf("only suspended plans can resume")
	}
	if !resident.HasActiveConsent(now) || assessment.Status != AssessmentApproved || !assessment.ValidUntil.After(now) {
		return SupportPlan{}, fmt.Errorf("consent and assessment must remain valid")
	}
	p.Status = PlanActive
	p.Version++
	p.UpdatedAt = now.UTC()
	return p, nil
}

func (p SupportPlan) GoalsJSON() (string, error) {
	raw, err := json.Marshal(p.Goals)
	if err != nil {
		return "", fmt.Errorf("encode plan goals: %w", err)
	}
	return string(raw), nil
}
