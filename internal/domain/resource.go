package domain

import (
	"fmt"
	"strings"
	"time"
)

type ResourceStatus string

const (
	ResourceAvailable   ResourceStatus = "available"
	ResourceAssigned    ResourceStatus = "assigned"
	ResourceMaintenance ResourceStatus = "maintenance"
	ResourceQuarantine  ResourceStatus = "quarantine"
	ResourceRetired     ResourceStatus = "retired"
)

type Resource struct {
	ID                 string
	DistrictID         string
	ResourceType       string
	SerialNumber       string
	Status             ResourceStatus
	AssignedResidentID string
	AssignedAt         *time.Time
	DueAt              *time.Time
	Version            int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func NewResource(id, districtID, resourceType, serial string, now time.Time) (Resource, error) {
	r := Resource{
		ID: id, DistrictID: districtID, ResourceType: strings.TrimSpace(resourceType), SerialNumber: strings.TrimSpace(serial),
		Status: ResourceAvailable, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	return r, r.Validate()
}

func (r Resource) Validate() error {
	if r.ID == "" || r.DistrictID == "" || r.ResourceType == "" || r.SerialNumber == "" {
		return fmt.Errorf("resource identity is incomplete")
	}
	if r.Status == ResourceAssigned {
		if r.AssignedResidentID == "" || r.AssignedAt == nil || r.DueAt == nil || !r.DueAt.After(*r.AssignedAt) {
			return fmt.Errorf("assigned resource requires resident and valid loan window")
		}
	} else if r.AssignedResidentID != "" || r.AssignedAt != nil || r.DueAt != nil {
		return fmt.Errorf("unassigned resource cannot retain loan ownership")
	}
	return nil
}

func (r Resource) Assign(resident Resident, dueAt, now time.Time) (Resource, error) {
	if r.Status != ResourceAvailable {
		return Resource{}, fmt.Errorf("only available resources can be assigned")
	}
	if resident.DistrictID != r.DistrictID || !resident.HasActiveConsent(now) {
		return Resource{}, fmt.Errorf("resource assignment requires resident district and consent")
	}
	if !dueAt.After(now.Add(24*time.Hour)) || dueAt.After(now.AddDate(1, 0, 0)) {
		return Resource{}, fmt.Errorf("resource due date is outside the allowed loan period")
	}
	now = now.UTC()
	dueAt = dueAt.UTC()
	r.Status = ResourceAssigned
	r.AssignedResidentID = resident.ID
	r.AssignedAt = &now
	r.DueAt = &dueAt
	r.Version++
	r.UpdatedAt = now
	return r, r.Validate()
}

func (r Resource) Return(needsQuarantine bool, now time.Time) (Resource, error) {
	if r.Status != ResourceAssigned {
		return Resource{}, fmt.Errorf("only assigned resources can be returned")
	}
	if needsQuarantine {
		r.Status = ResourceQuarantine
	} else {
		r.Status = ResourceAvailable
	}
	r.AssignedResidentID = ""
	r.AssignedAt = nil
	r.DueAt = nil
	r.Version++
	r.UpdatedAt = now.UTC()
	return r, r.Validate()
}

func (r Resource) ReleaseFromQuarantine(now time.Time) (Resource, error) {
	if r.Status != ResourceQuarantine {
		return Resource{}, fmt.Errorf("resource is not quarantined")
	}
	r.Status = ResourceAvailable
	r.Version++
	r.UpdatedAt = now.UTC()
	return r, nil
}
