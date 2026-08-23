package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestProviderNormalizesCapabilities(t *testing.T) {
	now := time.Now().UTC()
	p, err := NewProvider("provider_1", "district_1", "安心居家服务中心", []string{"MEAL", "home_support", "meal", " "}, 12, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Capabilities, []string{"home_support", "meal"}) {
		t.Fatalf("capabilities = %#v", p.Capabilities)
	}
	if p.Supports("meal") {
		t.Fatal("pending provider reported capable")
	}
	p, err = p.Activate(now)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Supports("meal") {
		t.Fatal("active provider capability missing")
	}
}

func TestProviderAccreditationStateMachine(t *testing.T) {
	now := time.Now().UTC()
	p, _ := NewProvider("provider_1", "district_1", "安心居家服务中心", []string{"meal"}, 12, now)
	p, err := p.Activate(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Activate(now); err == nil {
		t.Fatal("active provider activated twice")
	}
	p, err = p.Suspend(now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Supports("meal") {
		t.Fatal("suspended provider reported capable")
	}
	p, err = p.Activate(now)
	if err != nil {
		t.Fatal(err)
	}
	if p.AccreditationStatus != AccreditationActive {
		t.Fatalf("status = %s", p.AccreditationStatus)
	}
}
