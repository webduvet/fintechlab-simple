package verify

import (
	"testing"
	"time"
)

func TestTriggerDefaultPositiveDecision(t *testing.T) {
	engine := NewEngine(10*time.Millisecond, nil)

	got := engine.Trigger(42)

	if got.MerchantApplicationID != 42 {
		t.Fatalf("MerchantApplicationID = %d, want 42", got.MerchantApplicationID)
	}
	if got.OverallDecision != "APPROVED" {
		t.Fatalf("OverallDecision = %q, want APPROVED", got.OverallDecision)
	}
	if got.AmlDecision != "APPROVED" {
		t.Fatalf("AmlDecision = %q, want APPROVED", got.AmlDecision)
	}
	if got.AmlCompanyDecision != "APPROVED" {
		t.Fatalf("AmlCompanyDecision = %q, want APPROVED", got.AmlCompanyDecision)
	}
	if got.RiskDecision != "LOW" {
		t.Fatalf("RiskDecision = %q, want LOW", got.RiskDecision)
	}
	if got.RiskCompanyDecision != "LOW" {
		t.Fatalf("RiskCompanyDecision = %q, want LOW", got.RiskCompanyDecision)
	}
	if got.RiskScore != 5 {
		t.Fatalf("RiskScore = %d, want 5", got.RiskScore)
	}
	if got.RiskClassification != "LOW" {
		t.Fatalf("RiskClassification = %q, want LOW", got.RiskClassification)
	}
	if got.ManualOverrideDecision != "" {
		t.Fatalf("ManualOverrideDecision = %q, want empty", got.ManualOverrideDecision)
	}
}

func TestTriggerForceDeclineDecision(t *testing.T) {
	engine := NewEngine(10*time.Millisecond, map[int]bool{99: true})

	got := engine.Trigger(99)

	if got.OverallDecision != "DECLINE" {
		t.Fatalf("OverallDecision = %q, want DECLINE", got.OverallDecision)
	}
	if got.AmlDecision != "DECLINED" {
		t.Fatalf("AmlDecision = %q, want DECLINED", got.AmlDecision)
	}
	if got.AmlCompanyDecision != "DECLINED" {
		t.Fatalf("AmlCompanyDecision = %q, want DECLINED", got.AmlCompanyDecision)
	}
	if got.RiskDecision != "HIGH" {
		t.Fatalf("RiskDecision = %q, want HIGH", got.RiskDecision)
	}
	if got.RiskCompanyDecision != "HIGH" {
		t.Fatalf("RiskCompanyDecision = %q, want HIGH", got.RiskCompanyDecision)
	}
	if got.RiskScore != 95 {
		t.Fatalf("RiskScore = %d, want 95", got.RiskScore)
	}
	if got.RiskClassification != "HIGH" {
		t.Fatalf("RiskClassification = %q, want HIGH", got.RiskClassification)
	}
}

func TestGetBeforeDelayElapsedReturnsFalse(t *testing.T) {
	engine := NewEngine(50*time.Millisecond, nil)
	engine.Trigger(7)

	if _, ok := engine.Get(7); ok {
		t.Fatal("Get before delay elapsed = true, want false")
	}
}

func TestGetAfterDelayElapsedReturnsDecision(t *testing.T) {
	engine := NewEngine(10*time.Millisecond, nil)
	engine.Trigger(7)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if d, ok := engine.Get(7); ok {
			if d.MerchantApplicationID != 7 {
				t.Fatalf("MerchantApplicationID = %d, want 7", d.MerchantApplicationID)
			}
			if d.OverallDecision != "APPROVED" {
				t.Fatalf("OverallDecision = %q, want APPROVED", d.OverallDecision)
			}
			if d.CompletedAt == "" {
				t.Fatal("CompletedAt is empty, want an RFC3339 timestamp")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("decision was never recorded within the deadline")
}

func TestGetUnknownMerchantReturnsFalse(t *testing.T) {
	engine := NewEngine(10*time.Millisecond, nil)

	if _, ok := engine.Get(12345); ok {
		t.Fatal("Get for unknown id = true, want false")
	}
}
