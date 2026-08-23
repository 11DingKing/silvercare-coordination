package domain

import (
	"fmt"
	"time"
)

type AuthorizationStatus string

const (
	AuthorizationReserved  AuthorizationStatus = "reserved"
	AuthorizationActive    AuthorizationStatus = "active"
	AuthorizationExhausted AuthorizationStatus = "exhausted"
	AuthorizationCancelled AuthorizationStatus = "cancelled"
	AuthorizationExpired   AuthorizationStatus = "expired"
)

type Authorization struct {
	ID              string
	PlanID          string
	DistrictID      string
	ServiceCode     string
	Status          AuthorizationStatus
	UnitsAuthorized int
	UnitsConsumed   int
	UnitPriceCents  int64
	ReservedCents   int64
	StartsAt        time.Time
	EndsAt          time.Time
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func NewAuthorization(id, planID, districtID, serviceCode string, units int, unitPrice int64, startsAt, endsAt, now time.Time) (Authorization, error) {
	a := Authorization{
		ID: id, PlanID: planID, DistrictID: districtID, ServiceCode: serviceCode,
		Status: AuthorizationReserved, UnitsAuthorized: units, UnitPriceCents: unitPrice,
		ReservedCents: int64(units) * unitPrice, StartsAt: startsAt.UTC(), EndsAt: endsAt.UTC(),
		Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	if err := a.Validate(); err != nil {
		return Authorization{}, err
	}
	return a, nil
}

func (a Authorization) Validate() error {
	if a.ID == "" || a.PlanID == "" || a.DistrictID == "" || a.ServiceCode == "" {
		return fmt.Errorf("authorization identity is incomplete")
	}
	if a.UnitsAuthorized <= 0 || a.UnitPriceCents <= 0 {
		return fmt.Errorf("authorization units and price must be positive")
	}
	if a.UnitsConsumed < 0 || a.UnitsConsumed > a.UnitsAuthorized {
		return fmt.Errorf("authorization consumption is outside its allowance")
	}
	if a.ReservedCents != int64(a.UnitsAuthorized-a.UnitsConsumed)*a.UnitPriceCents {
		return fmt.Errorf("authorization reservation does not match remaining units")
	}
	if !a.EndsAt.After(a.StartsAt) {
		return fmt.Errorf("authorization end must follow start")
	}
	return nil
}

func (a Authorization) Activate(plan SupportPlan, now time.Time) (Authorization, error) {
	if a.Status != AuthorizationReserved {
		return Authorization{}, fmt.Errorf("only reserved authorizations can activate")
	}
	if plan.ID != a.PlanID || plan.Status != PlanActive {
		return Authorization{}, fmt.Errorf("authorization requires its active support plan")
	}
	if now.Before(a.StartsAt) || !now.Before(a.EndsAt) {
		return Authorization{}, fmt.Errorf("authorization is outside its service period")
	}
	a.Status = AuthorizationActive
	a.Version++
	a.UpdatedAt = now.UTC()
	return a, nil
}

func (a Authorization) Consume(units int, now time.Time) (Authorization, error) {
	if a.Status != AuthorizationActive {
		return Authorization{}, fmt.Errorf("authorization is not active")
	}
	if units <= 0 || a.UnitsConsumed+units > a.UnitsAuthorized {
		return Authorization{}, fmt.Errorf("requested units exceed the remaining authorization")
	}
	a.UnitsConsumed += units
	a.ReservedCents -= int64(units) * a.UnitPriceCents
	if a.UnitsConsumed == a.UnitsAuthorized {
		a.Status = AuthorizationExhausted
	}
	a.Version++
	a.UpdatedAt = now.UTC()
	return a, a.Validate()
}

func (a Authorization) Cancel(now time.Time) (Authorization, int64, error) {
	if a.Status != AuthorizationReserved && a.Status != AuthorizationActive {
		return Authorization{}, 0, fmt.Errorf("authorization cannot be cancelled from %s", a.Status)
	}
	release := a.ReservedCents
	a.ReservedCents = 0
	a.Status = AuthorizationCancelled
	a.Version++
	a.UpdatedAt = now.UTC()
	return a, release, nil
}

func (a Authorization) Covers(moment time.Time) bool {
	return (a.Status == AuthorizationActive || a.Status == AuthorizationReserved) &&
		!moment.Before(a.StartsAt) && moment.Before(a.EndsAt)
}
