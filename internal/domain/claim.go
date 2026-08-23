package domain

import (
	"fmt"
	"strings"
	"time"
)

type ClaimStatus string

const (
	ClaimDraft     ClaimStatus = "draft"
	ClaimSubmitted ClaimStatus = "submitted"
	ClaimApproved  ClaimStatus = "approved"
	ClaimRejected  ClaimStatus = "rejected"
	ClaimPaid      ClaimStatus = "paid"
	ClaimHeld      ClaimStatus = "held"
)

type Claim struct {
	ID              string
	VisitID         string
	DistrictID      string
	Status          ClaimStatus
	AmountCents     int64
	ReviewerID      string
	RejectionReason string
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func NewClaim(id, districtID string, visit Visit, authorization Authorization, now time.Time) (Claim, error) {
	if visit.Status != VisitCompleted || visit.CompletedAt == nil {
		return Claim{}, fmt.Errorf("only completed visits can create claims")
	}
	if visit.AuthorizationID != authorization.ID || authorization.DistrictID != districtID {
		return Claim{}, fmt.Errorf("claim entities do not share an authorization")
	}
	claim := Claim{
		ID: id, VisitID: visit.ID, DistrictID: districtID, Status: ClaimDraft,
		AmountCents: authorization.UnitPriceCents, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	return claim, claim.Validate()
}

func (c Claim) Validate() error {
	if c.ID == "" || c.VisitID == "" || c.DistrictID == "" || c.AmountCents <= 0 {
		return fmt.Errorf("claim fields are invalid")
	}
	if c.Status == ClaimRejected && strings.TrimSpace(c.RejectionReason) == "" {
		return fmt.Errorf("rejected claim requires a reason")
	}
	if c.Status != ClaimRejected && c.RejectionReason != "" {
		return fmt.Errorf("only rejected claims may retain a rejection reason")
	}
	return nil
}

func (c Claim) Submit(now time.Time) (Claim, error) {
	if c.Status != ClaimDraft && c.Status != ClaimRejected {
		return Claim{}, fmt.Errorf("claim cannot be submitted from %s", c.Status)
	}
	c.Status = ClaimSubmitted
	c.RejectionReason = ""
	c.ReviewerID = ""
	c.Version++
	c.UpdatedAt = now.UTC()
	return c, nil
}

func (c Claim) Approve(reviewerID string, now time.Time) (Claim, error) {
	if c.Status != ClaimSubmitted || reviewerID == "" {
		return Claim{}, fmt.Errorf("submitted claim and reviewer are required")
	}
	c.Status = ClaimApproved
	c.ReviewerID = reviewerID
	c.Version++
	c.UpdatedAt = now.UTC()
	return c, nil
}

func (c Claim) Reject(reviewerID, reason string, now time.Time) (Claim, error) {
	if c.Status != ClaimSubmitted || reviewerID == "" {
		return Claim{}, fmt.Errorf("submitted claim and reviewer are required")
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 5 || len([]rune(reason)) > 500 {
		return Claim{}, fmt.Errorf("rejection reason length is invalid")
	}
	c.Status = ClaimRejected
	c.ReviewerID = reviewerID
	c.RejectionReason = reason
	c.Version++
	c.UpdatedAt = now.UTC()
	return c, nil
}

func (c Claim) MarkPaid(now time.Time) (Claim, error) {
	if c.Status != ClaimApproved {
		return Claim{}, fmt.Errorf("only approved claims can be paid")
	}
	c.Status = ClaimPaid
	c.Version++
	c.UpdatedAt = now.UTC()
	return c, nil
}
