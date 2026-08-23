package domain

import (
	"fmt"
	"strings"
	"time"
)

type ConsentStatus string

const (
	ConsentPending   ConsentStatus = "pending"
	ConsentGranted   ConsentStatus = "granted"
	ConsentWithdrawn ConsentStatus = "withdrawn"
)

type Resident struct {
	ID               string
	DistrictID       string
	ExternalRef      string
	FullName         string
	BirthDate        time.Time
	HouseholdID      string
	ConsentStatus    ConsentStatus
	ConsentExpiresAt *time.Time
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func NewResident(id, districtID, externalRef, fullName, householdID string, birthDate, now time.Time) (Resident, error) {
	resident := Resident{
		ID:            strings.TrimSpace(id),
		DistrictID:    strings.TrimSpace(districtID),
		ExternalRef:   strings.TrimSpace(externalRef),
		FullName:      strings.TrimSpace(fullName),
		BirthDate:     dateOnly(birthDate),
		HouseholdID:   strings.TrimSpace(householdID),
		ConsentStatus: ConsentPending,
		Version:       1,
		CreatedAt:     now.UTC(),
		UpdatedAt:     now.UTC(),
	}
	if err := resident.Validate(now); err != nil {
		return Resident{}, err
	}
	return resident, nil
}

func (r Resident) Validate(now time.Time) error {
	if r.ID == "" || r.DistrictID == "" || r.ExternalRef == "" || r.HouseholdID == "" {
		return fmt.Errorf("resident identity fields are required")
	}
	if len([]rune(r.FullName)) < 2 || len([]rune(r.FullName)) > 80 {
		return fmt.Errorf("resident full name length is invalid")
	}
	if r.BirthDate.After(dateOnly(now)) {
		return fmt.Errorf("birth date cannot be in the future")
	}
	if r.BirthDate.AddDate(55, 0, 0).After(dateOnly(now)) {
		return fmt.Errorf("resident does not meet the program age floor")
	}
	switch r.ConsentStatus {
	case ConsentPending, ConsentWithdrawn:
		if r.ConsentExpiresAt != nil {
			return fmt.Errorf("inactive consent cannot have an expiry")
		}
	case ConsentGranted:
		if r.ConsentExpiresAt == nil || !r.ConsentExpiresAt.After(now) {
			return fmt.Errorf("granted consent requires a future expiry")
		}
	default:
		return fmt.Errorf("unknown consent status %q", r.ConsentStatus)
	}
	return nil
}

func (r Resident) GrantConsent(until, now time.Time) (Resident, error) {
	if !until.After(now.Add(24 * time.Hour)) {
		return Resident{}, fmt.Errorf("consent must remain valid for more than one day")
	}
	r.ConsentStatus = ConsentGranted
	until = until.UTC()
	r.ConsentExpiresAt = &until
	r.Version++
	r.UpdatedAt = now.UTC()
	return r, r.Validate(now)
}

func (r Resident) WithdrawConsent(now time.Time) Resident {
	r.ConsentStatus = ConsentWithdrawn
	r.ConsentExpiresAt = nil
	r.Version++
	r.UpdatedAt = now.UTC()
	return r
}

func (r Resident) HasActiveConsent(now time.Time) bool {
	return r.ConsentStatus == ConsentGranted && r.ConsentExpiresAt != nil && now.Before(*r.ConsentExpiresAt)
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
