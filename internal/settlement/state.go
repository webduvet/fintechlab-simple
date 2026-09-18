// Package settlement models the Worldline-shaped daily settlement lifecycle:
// report generation, a state machine governing each settlement record, and
// the reconciliation query store. See docs/ARCHITECTURE-settlement-*.md.
package settlement

import "fmt"

// SettlementState is the lifecycle stage of a single settlement record.
//
// Defined once here (both ARCHITECTURE-reconciliation-api.md and
// ARCHITECTURE-settlement-state-machine.md give this type; the latter's
// version is authoritative because it also has StateSuperseded, which the
// former's snippet omits).
type SettlementState string

const (
	StateScheduled  SettlementState = "Scheduled"  // Report generated, awaiting processing
	StateProcessing SettlementState = "Processing" // Processing in progress (e.g., fee calculation, reserve hold)
	StateSettled    SettlementState = "Settled"    // Settlement complete, funds credited
	StateFailed     SettlementState = "Failed"     // Settlement failed, needs manual review
	StateSuperseded SettlementState = "Superseded" // Replaced by a later settlement (e.g., correction)
)

// TransitionEvent names a state-machine trigger.
type TransitionEvent string

const (
	EventProcess    TransitionEvent = "process"    // Scheduled -> Processing
	EventSettle     TransitionEvent = "settle"     // Processing -> Settled
	EventFail       TransitionEvent = "fail"       // Processing -> Failed
	EventRetry      TransitionEvent = "retry"      // Failed -> Scheduled
	EventSupersede  TransitionEvent = "supersede"  // Settled -> Superseded
	EventReactivate TransitionEvent = "reactivate" // Superseded -> Scheduled
)

// transitions is the full state graph: current state -> event -> next state.
var transitions = map[SettlementState]map[TransitionEvent]SettlementState{
	StateScheduled: {
		EventProcess: StateProcessing,
	},
	StateProcessing: {
		EventSettle: StateSettled,
		EventFail:   StateFailed,
	},
	StateFailed: {
		EventRetry: StateScheduled,
	},
	StateSettled: {
		EventSupersede: StateSuperseded,
	},
	StateSuperseded: {
		EventReactivate: StateScheduled,
	},
}

// eventTarget names the state each event nominally aims for, independent of
// whether the current state actually permits it. Used only to report a
// meaningful "to" state in invalid-transition errors.
var eventTarget = map[TransitionEvent]SettlementState{
	EventProcess:    StateProcessing,
	EventSettle:     StateSettled,
	EventFail:       StateFailed,
	EventRetry:      StateScheduled,
	EventSupersede:  StateSuperseded,
	EventReactivate: StateScheduled,
}

// TransitionResult describes the outcome of a state transition.
type TransitionResult struct {
	RecordID  string          `json:"record_id"`
	FromState SettlementState `json:"from_state"`
	ToState   SettlementState `json:"to_state"`
	Success   bool            `json:"success"`
	Error     string          `json:"error,omitempty"`
}

// ValidTransitions returns the set of allowed next states for a given
// current state.
func ValidTransitions(current SettlementState) []SettlementState {
	events := transitions[current]
	out := make([]SettlementState, 0, len(events))
	for _, to := range events {
		out = append(out, to)
	}
	return out
}

// CanTransition returns true if the transition from -> to is valid.
func CanTransition(from, to SettlementState) bool {
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// Transition attempts to move a settlement record from its current state to
// a new state via event. meta carries event-specific data: for EventFail it
// is the required failure reason, for EventSupersede the required
// replacement record ID. Both are stored on the record (Reason /
// ReplacementID) when present. See internal/settlement/store.go for the
// SettlementRecord this operates on.
func (s *Store) Transition(id string, event TransitionEvent, meta ...string) (TransitionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[id]
	if !ok {
		err := fmt.Errorf("settlement: record %s not found", id)
		return TransitionResult{RecordID: id, Success: false, Error: err.Error()}, err
	}

	from := rec.State
	to, valid := transitions[from][event]
	if !valid {
		want := eventTarget[event]
		err := fmt.Errorf("settlement: invalid transition from %s to %s", from, want)
		return TransitionResult{RecordID: id, FromState: from, ToState: want, Success: false, Error: err.Error()}, err
	}

	var reason string
	if len(meta) > 0 {
		reason = meta[0]
	}
	switch event {
	case EventSettle:
		if rec.Report == nil {
			err := fmt.Errorf("settlement: cannot settle without report data")
			return TransitionResult{RecordID: id, FromState: from, ToState: to, Success: false, Error: err.Error()}, err
		}
	case EventFail:
		if reason == "" {
			err := fmt.Errorf("settlement: fail requires a reason")
			return TransitionResult{RecordID: id, FromState: from, ToState: to, Success: false, Error: err.Error()}, err
		}
		rec.Reason = reason
	case EventSupersede:
		if reason == "" {
			err := fmt.Errorf("settlement: supersede requires replacement record ID")
			return TransitionResult{RecordID: id, FromState: from, ToState: to, Success: false, Error: err.Error()}, err
		}
		rec.ReplacementID = reason
	}

	rec.State = to
	rec.UpdatedAt = nowRFC3339()
	s.saveLocked()
	return TransitionResult{RecordID: id, FromState: from, ToState: to, Success: true}, nil
}
