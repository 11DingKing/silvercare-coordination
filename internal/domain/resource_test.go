package domain

import (
	"testing"
	"time"
)

func TestResourceAssignmentAndQuarantine(t *testing.T) {
	now := time.Now().UTC()
	r, err := NewResource("resource_1", "district_1", "assistive_device", "SN-001", now)
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.Assign(activeResident(now), now.AddDate(0, 1, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != ResourceAssigned || r.AssignedResidentID != "resident_1" {
		t.Fatalf("resource=%+v", r)
	}
	r, err = r.Return(true, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != ResourceQuarantine || r.AssignedResidentID != "" || r.DueAt != nil {
		t.Fatalf("resource=%+v", r)
	}
	r, err = r.ReleaseFromQuarantine(now.Add(24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != ResourceAvailable {
		t.Fatalf("status = %s", r.Status)
	}
}

func TestResourceRejectsCrossDistrictResident(t *testing.T) {
	now := time.Now().UTC()
	r, _ := NewResource("resource_1", "district_1", "device", "SN-001", now)
	resident := activeResident(now)
	resident.DistrictID = "district_other"
	if _, err := r.Assign(resident, now.AddDate(0, 1, 0), now); err == nil {
		t.Fatal("cross-district assignment accepted")
	}
}
