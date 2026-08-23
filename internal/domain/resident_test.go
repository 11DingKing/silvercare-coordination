package domain

import (
	"testing"
	"time"
)

func TestResidentLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	resident, err := NewResident("resident_1", "district_1", "EXT-1", "张建国", "household_1", now.AddDate(-70, 0, 0), now)
	if err != nil {
		t.Fatalf("new resident: %v", err)
	}
	if resident.ConsentStatus != ConsentPending {
		t.Fatalf("status = %s", resident.ConsentStatus)
	}
	if resident.HasActiveConsent(now) {
		t.Fatal("pending consent reported active")
	}

	granted, err := resident.GrantConsent(now.AddDate(0, 6, 0), now)
	if err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	if !granted.HasActiveConsent(now.Add(time.Hour)) {
		t.Fatal("granted consent reported inactive")
	}
	if granted.Version != 2 {
		t.Fatalf("version = %d", granted.Version)
	}

	withdrawn := granted.WithdrawConsent(now.Add(2 * time.Hour))
	if withdrawn.ConsentStatus != ConsentWithdrawn {
		t.Fatalf("status = %s", withdrawn.ConsentStatus)
	}
	if withdrawn.ConsentExpiresAt != nil {
		t.Fatal("withdrawn consent retained expiry")
	}
	if withdrawn.HasActiveConsent(now) {
		t.Fatal("withdrawn consent reported active")
	}
}

func TestResidentValidation(t *testing.T) {
	now := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		birth    time.Time
		fullName string
		wantErr  bool
	}{
		{"eligible", now.AddDate(-65, 0, 0), "李秀兰", false},
		{"age floor", now.AddDate(-54, 0, 0), "李秀兰", true},
		{"future birth", now.AddDate(1, 0, 0), "李秀兰", true},
		{"short name", now.AddDate(-65, 0, 0), "李", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewResident("resident_1", "district_1", "EXT-1", test.fullName, "household_1", test.birth, now)
			if (err != nil) != test.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestConsentRequiresUsefulFutureWindow(t *testing.T) {
	now := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	resident, err := NewResident("resident_1", "district_1", "EXT-1", "王桂英", "household_1", now.AddDate(-75, 0, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resident.GrantConsent(now.Add(12*time.Hour), now); err == nil {
		t.Fatal("short consent window accepted")
	}
	if _, err := resident.GrantConsent(now.Add(48*time.Hour), now); err != nil {
		t.Fatalf("valid consent rejected: %v", err)
	}
}
