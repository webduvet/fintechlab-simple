package settlement

import "testing"

func newScheduled(t *testing.T, s *Store, id string) *SettlementRecord {
	t.Helper()
	rec := &SettlementRecord{ID: id, MerchantID: "GB00SIM0000000000003", TransactionsDate: "2026-09-03", Currency: "EUR", State: StateScheduled}
	if err := s.Create(rec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return rec
}

func TestValidTransitionsTable(t *testing.T) {
	cases := []struct {
		name  string
		from  SettlementState
		event TransitionEvent
		to    SettlementState
		meta  []string
		setup func(*Store, *SettlementRecord)
	}{
		{name: "scheduled to processing", from: StateScheduled, event: EventProcess, to: StateProcessing},
		{name: "processing to settled", from: StateProcessing, event: EventSettle, to: StateSettled,
			setup: func(s *Store, r *SettlementRecord) { _ = s.SetReport(r.ID, &Report{MerchantID: r.MerchantID}) }},
		{name: "processing to failed", from: StateProcessing, event: EventFail, to: StateFailed, meta: []string{"card network outage"}},
		{name: "failed to scheduled", from: StateFailed, event: EventRetry, to: StateScheduled},
		{name: "settled to superseded", from: StateSettled, event: EventSupersede, to: StateSuperseded, meta: []string{"set_replacement01"},
			setup: func(s *Store, r *SettlementRecord) { _ = s.SetReport(r.ID, &Report{MerchantID: r.MerchantID}) }},
		{name: "superseded to scheduled", from: StateSuperseded, event: EventReactivate, to: StateScheduled,
			setup: func(s *Store, r *SettlementRecord) { _ = s.SetReport(r.ID, &Report{MerchantID: r.MerchantID}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			rec := newScheduled(t, s, "set_"+tc.name)
			if err := s.UpdateState(rec.ID, tc.from); err != nil {
				t.Fatalf("UpdateState: %v", err)
			}
			if tc.setup != nil {
				tc.setup(s, rec)
			}
			result, err := s.Transition(rec.ID, tc.event, tc.meta...)
			if err != nil {
				t.Fatalf("Transition: %v", err)
			}
			if !result.Success || result.ToState != tc.to || result.FromState != tc.from {
				t.Fatalf("got %+v, want to=%s from=%s success=true", result, tc.to, tc.from)
			}
			got, err := s.Get(rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.to {
				t.Fatalf("stored state = %s, want %s", got.State, tc.to)
			}
		})
	}
}

func TestInvalidTransitionsTable(t *testing.T) {
	cases := []struct {
		name  string
		from  SettlementState
		event TransitionEvent
	}{
		{"scheduled cannot settle directly", StateScheduled, EventSettle},
		{"scheduled cannot fail directly", StateScheduled, EventFail},
		{"processing cannot retry", StateProcessing, EventRetry},
		{"failed cannot process", StateFailed, EventProcess},
		{"settled cannot process again", StateSettled, EventProcess},
		{"superseded cannot settle", StateSuperseded, EventSettle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			rec := newScheduled(t, s, "set_"+tc.name)
			if err := s.UpdateState(rec.ID, tc.from); err != nil {
				t.Fatal(err)
			}
			result, err := s.Transition(rec.ID, tc.event)
			if err == nil {
				t.Fatal("expected error for invalid transition")
			}
			if result.Success {
				t.Fatal("expected Success=false")
			}
		})
	}
}

func TestTransitionRecordNotFound(t *testing.T) {
	s := NewStore()
	_, err := s.Transition("set_missing", EventProcess)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSettleWithoutReportData(t *testing.T) {
	s := NewStore()
	rec := newScheduled(t, s, "set_no_report")
	if err := s.UpdateState(rec.ID, StateProcessing); err != nil {
		t.Fatal(err)
	}
	result, err := s.Transition(rec.ID, EventSettle)
	if err == nil {
		t.Fatal("expected error settling without report data")
	}
	if result.Success {
		t.Fatal("expected Success=false")
	}
}

func TestFailWithoutReason(t *testing.T) {
	s := NewStore()
	rec := newScheduled(t, s, "set_no_reason")
	if err := s.UpdateState(rec.ID, StateProcessing); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ID, EventFail); err == nil {
		t.Fatal("expected error failing without reason")
	}
}

func TestSupersedeWithoutReplacement(t *testing.T) {
	s := NewStore()
	rec := newScheduled(t, s, "set_no_replacement")
	if err := s.UpdateState(rec.ID, StateProcessing); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReport(rec.ID, &Report{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ID, EventSettle); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ID, EventSupersede); err == nil {
		t.Fatal("expected error superseding without replacement ID")
	}
}

func TestValidTransitionsFunc(t *testing.T) {
	cases := []struct {
		state SettlementState
		want  []SettlementState
	}{
		{StateScheduled, []SettlementState{StateProcessing}},
		{StateFailed, []SettlementState{StateScheduled}},
		{StateSuperseded, []SettlementState{StateScheduled}},
	}
	for _, tc := range cases {
		got := ValidTransitions(tc.state)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %v want %v", tc.state, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: got %v want %v", tc.state, got, tc.want)
			}
		}
	}
	// Processing fans out to two next states; order isn't significant.
	proc := ValidTransitions(StateProcessing)
	if len(proc) != 2 {
		t.Fatalf("Processing: got %v, want 2 entries", proc)
	}
}

func TestCanTransition(t *testing.T) {
	if !CanTransition(StateScheduled, StateProcessing) {
		t.Fatal("Scheduled -> Processing should be valid")
	}
	if CanTransition(StateScheduled, StateSettled) {
		t.Fatal("Scheduled -> Settled should be invalid")
	}
	if CanTransition(StateSettled, StateScheduled) {
		t.Fatal("Settled -> Scheduled should be invalid")
	}
}

func TestLifecycleGenerateProcessSettle(t *testing.T) {
	s := NewStore()
	rec := newScheduled(t, s, "set_lifecycle")
	if _, err := s.Transition(rec.ID, EventProcess); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReport(rec.ID, &Report{MerchantID: rec.MerchantID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ID, EventSettle); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(rec.ID)
	if got.State != StateSettled {
		t.Fatalf("state = %s, want Settled", got.State)
	}
}

func TestLifecycleFailRetry(t *testing.T) {
	s := NewStore()
	rec := newScheduled(t, s, "set_fail_retry")
	if _, err := s.Transition(rec.ID, EventProcess); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ID, EventFail, "insufficient ledger data"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ID, EventRetry); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(rec.ID)
	if got.State != StateScheduled {
		t.Fatalf("state = %s, want Scheduled", got.State)
	}
	if got.Reason != "insufficient ledger data" {
		t.Fatalf("Reason = %q, not preserved", got.Reason)
	}
}

func TestLifecycleSettleSupersede(t *testing.T) {
	s := NewStore()
	rec := newScheduled(t, s, "set_settle_supersede")
	if _, err := s.Transition(rec.ID, EventProcess); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReport(rec.ID, &Report{MerchantID: rec.MerchantID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ID, EventSettle); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(rec.ID, EventSupersede, "set_replacement02"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(rec.ID)
	if got.State != StateSuperseded {
		t.Fatalf("state = %s, want Superseded", got.State)
	}
	if got.ReplacementID != "set_replacement02" {
		t.Fatalf("ReplacementID = %q, not preserved", got.ReplacementID)
	}
}
