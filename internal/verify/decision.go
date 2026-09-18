// Package verify models the merchant-verification mock: buddy's real
// verification-service, keyed on merchantApplicationId, gates a merchant's
// progression to UNDERWRITING_APPROVED (and can later PAUSE_SETTLEMENTS).
// This lab mocks it as an always-positive stub with an optional
// force-decline override, per
// docs/ARCHITECTURE-phase3-corrections.md section 3.
package verify

import (
	"sync"
	"time"
)

// Decision is a single merchant's verification outcome, matching
// VerifyDecisionSnapshotResponseDto's field names verbatim (data.dto.ts,
// prisma/schema.prisma:629-644 for the enum values themselves).
// ManualOverrideDecision is never set by this lab -- it stays the zero
// value so its `omitempty` JSON tag (applied at the wire boundary, not
// here) omits it, matching the real always-null-until-a-human-intervenes
// shape.
type Decision struct {
	MerchantApplicationID  int
	OverallDecision        string
	ManualOverrideDecision string
	AmlDecision            string
	AmlCompanyDecision     string
	RiskDecision           string
	RiskCompanyDecision    string
	RiskScore              int
	RiskClassification     string
	CompletedAt            string
}

// positiveDecision returns the real "approved" defaults, verbatim from
// prisma/schema.prisma:629-638,640-644.
func positiveDecision(id int) Decision {
	return Decision{
		MerchantApplicationID: id,
		OverallDecision:       "APPROVED",
		AmlDecision:           "APPROVED",
		AmlCompanyDecision:    "APPROVED",
		RiskDecision:          "LOW",
		RiskCompanyDecision:   "LOW",
		RiskScore:             5,
		RiskClassification:    "LOW",
	}
}

// declineDecision is what a configured VERIFY_FORCE_DECLINE_IDS entry
// produces instead.
func declineDecision(id int) Decision {
	return Decision{
		MerchantApplicationID: id,
		OverallDecision:       "DECLINE",
		AmlDecision:           "DECLINED",
		AmlCompanyDecision:    "DECLINED",
		RiskDecision:          "HIGH",
		RiskCompanyDecision:   "HIGH",
		RiskScore:             95,
		RiskClassification:    "HIGH",
	}
}

// Engine owns the verification-decision lifecycle: a merchant's decision is
// not readable via Get until `delay` has elapsed after Trigger, mirroring
// the real service's asynchronous decisioning step (and internal/b4b's
// Engine.Submit/process shape: a goroutine sleeps then records, Trigger
// never blocks the caller).
type Engine struct {
	mu           sync.Mutex
	decisions    map[int]*Decision
	delay        time.Duration
	forceDecline map[int]bool
}

// NewEngine builds an Engine. forceDecline is the set of
// merchantApplicationIds (VERIFY_FORCE_DECLINE_IDS) that resolve to a
// decline decision instead of the default approval.
func NewEngine(delay time.Duration, forceDecline map[int]bool) *Engine {
	return &Engine{
		decisions:    map[int]*Decision{},
		delay:        delay,
		forceDecline: forceDecline,
	}
}

// Trigger starts deciding id and returns immediately with the decision that
// will be recorded -- the caller (the HTTP layer) uses this to build its
// synchronous 202 response body, even though Get is what a poller actually
// consults. The decision itself is recorded, with CompletedAt set, only
// after `delay` elapses in a background goroutine; before then Get reports
// not-found, matching the real service's "404 until ready" shape.
func (e *Engine) Trigger(id int) *Decision {
	var d Decision
	if e.forceDecline[id] {
		d = declineDecision(id)
	} else {
		d = positiveDecision(id)
	}

	go func() {
		if e.delay > 0 {
			time.Sleep(e.delay)
		}
		d.CompletedAt = time.Now().UTC().Format(time.RFC3339)
		e.mu.Lock()
		e.decisions[id] = &d
		e.mu.Unlock()
	}()

	result := d
	return &result
}

// Get returns the recorded decision for id, or (nil, false) if it was never
// triggered or the trigger's delay has not yet elapsed.
func (e *Engine) Get(id int) (*Decision, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.decisions[id]
	if !ok {
		return nil, false
	}
	cp := *d
	return &cp, true
}
