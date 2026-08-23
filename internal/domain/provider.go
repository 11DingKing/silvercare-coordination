package domain

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

type AccreditationStatus string

const (
	AccreditationPending   AccreditationStatus = "pending"
	AccreditationActive    AccreditationStatus = "active"
	AccreditationSuspended AccreditationStatus = "suspended"
	AccreditationExpired   AccreditationStatus = "expired"
)

type Provider struct {
	ID                  string
	DistrictID          string
	Name                string
	AccreditationStatus AccreditationStatus
	Capabilities        []string
	CapacityPerDay      int
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func NewProvider(id, districtID, name string, capabilities []string, dailyCapacity int, now time.Time) (Provider, error) {
	p := Provider{
		ID: id, DistrictID: districtID, Name: strings.TrimSpace(name),
		AccreditationStatus: AccreditationPending,
		Capabilities:        normalizeCapabilities(capabilities), CapacityPerDay: dailyCapacity,
		Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	if err := p.Validate(); err != nil {
		return Provider{}, err
	}
	return p, nil
}

func (p Provider) Validate() error {
	if p.ID == "" || p.DistrictID == "" || len([]rune(p.Name)) < 2 {
		return fmt.Errorf("provider identity is invalid")
	}
	if len(p.Capabilities) == 0 || p.CapacityPerDay <= 0 || p.CapacityPerDay > 500 {
		return fmt.Errorf("provider requires capabilities and a valid daily capacity")
	}
	return nil
}

func (p Provider) Activate(now time.Time) (Provider, error) {
	if p.AccreditationStatus != AccreditationPending && p.AccreditationStatus != AccreditationSuspended {
		return Provider{}, fmt.Errorf("provider cannot activate from %s", p.AccreditationStatus)
	}
	p.AccreditationStatus = AccreditationActive
	p.Version++
	p.UpdatedAt = now.UTC()
	return p, nil
}

func (p Provider) Suspend(now time.Time) (Provider, error) {
	if p.AccreditationStatus != AccreditationActive {
		return Provider{}, fmt.Errorf("only active providers can be suspended")
	}
	p.AccreditationStatus = AccreditationSuspended
	p.Version++
	p.UpdatedAt = now.UTC()
	return p, nil
}

func (p Provider) Supports(serviceCode string) bool {
	return p.AccreditationStatus == AccreditationActive && slices.Contains(p.Capabilities, serviceCode)
}

func (p Provider) CapabilitiesJSON() (string, error) {
	raw, err := json.Marshal(p.Capabilities)
	if err != nil {
		return "", fmt.Errorf("encode provider capabilities: %w", err)
	}
	return string(raw), nil
}

func normalizeCapabilities(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}
