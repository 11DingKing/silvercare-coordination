package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/11DingKing/silvercare-coordination/internal/domain"
)

func TestFailedResourceAssignmentPreservesAvailableDevice(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	now := fixture.now.Now()
	timestamp := now.Format(time.RFC3339Nano)
	if _, err := fixture.repo.DB().ExecContext(ctx, `
		INSERT INTO residents(
			id, district_id, external_ref, full_name, birth_date, household_id,
			consent_status, consent_expires_at, version, created_at, updated_at
		) VALUES('resident_device', 'district_1', 'EXT-DEVICE', '周桂芳', '1949-11-03',
			'household_device', 'granted', ?, 1, ?, ?);
		INSERT INTO resources(
			id, district_id, resource_type, serial_number, status, assigned_resident_id,
			assigned_at, due_at, version, created_at, updated_at
		) VALUES('resource_device', 'district_1', 'walker', 'WALKER-001', 'available',
			NULL, NULL, NULL, 1, ?, ?);
		CREATE TRIGGER reject_resource_assignment_event
		BEFORE INSERT ON outbox_events
		WHEN NEW.topic = 'resource.assigned'
		BEGIN
			SELECT RAISE(ABORT, 'forced resource assignment event failure');
		END;
	`, now.AddDate(1, 0, 0).Format(time.RFC3339Nano), timestamp, timestamp, timestamp, timestamp); err != nil {
		t.Fatalf("prepare resource assignment scenario: %v", err)
	}

	token := loginToken(t, fixture)
	payload := map[string]any{
		"resident_id":      "resident_device",
		"due_at":           now.AddDate(0, 2, 0).Format(time.RFC3339Nano),
		"expected_version": 1,
	}
	failed := performJSON(t, fixture.api, http.MethodPost, "/v1/resources/resource_device/assign", token, payload)
	if failed.Code != http.StatusConflict {
		t.Errorf("failed assignment status=%d body=%s", failed.Code, failed.Body.String())
	}

	device, err := fixture.repo.ResourceByID(ctx, nil, "resource_device", "district_1")
	if err != nil {
		t.Fatal(err)
	}
	if device.Status != domain.ResourceAvailable || device.AssignedResidentID != "" || device.AssignedAt != nil || device.DueAt != nil || device.Version != 1 {
		t.Errorf("failed assignment changed device=%+v", device)
	}
	var audits, events int
	if err := fixture.repo.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE object_type = 'resource' AND object_id = 'resource_device'").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox_events WHERE topic = 'resource.assigned' AND aggregate_id = 'resource_device'").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if audits != 0 || events != 0 {
		t.Errorf("failed assignment persisted audits=%d events=%d", audits, events)
	}

	if _, err := fixture.repo.DB().ExecContext(ctx, "DROP TRIGGER reject_resource_assignment_event"); err != nil {
		t.Fatal(err)
	}
	succeeded := performJSON(t, fixture.api, http.MethodPost, "/v1/resources/resource_device/assign", token, payload)
	if succeeded.Code != http.StatusOK {
		t.Errorf("assignment after recovery status=%d body=%s", succeeded.Code, succeeded.Body.String())
	}
	device, err = fixture.repo.ResourceByID(ctx, nil, "resource_device", "district_1")
	if err != nil {
		t.Fatal(err)
	}
	if device.Status != domain.ResourceAssigned || device.AssignedResidentID != "resident_device" || device.AssignedAt == nil || device.DueAt == nil || device.Version != 2 {
		t.Errorf("successful assignment device=%+v", device)
	}
}
