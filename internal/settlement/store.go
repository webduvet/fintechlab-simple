package settlement

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/runnerclock"
)

// SettlementRecord is the persisted, live view of one settlement: the
// report snapshot plus its current state-machine position. Reason and
// ReplacementID are populated by Transition (EventFail / EventSupersede
// respectively) so a manual review has the "why" alongside the "what".
// PayoutID / PayoutState are populated by cmd/settlement after a successful
// EventSettle transition submits the payout to B4B (see
// docs/ARCHITECTURE-vendor-corrections.md Addendum section A -- settlement
// calls B4B, not Banking Circle directly; PayoutState tracks B4B's own
// status string, e.g. "B4BAccepted" through "B4BTMApproved"/"B4BFailed") --
// both fields are additions beyond the reconciliation-api.md snippet.
type SettlementRecord struct {
	ID               string          `json:"id"`
	MerchantID       string          `json:"merchant_id"`
	TransactionsDate string          `json:"transactions_date"`
	Currency         string          `json:"currency"`
	State            SettlementState `json:"settlement_state"`
	SettlementDate   string          `json:"settlement_date"`
	Report           *Report         `json:"report,omitempty"` // populated when settled
	Reason           string          `json:"reason,omitempty"`
	ReplacementID    string          `json:"replacement_id,omitempty"`
	PayoutID         string          `json:"payout_id,omitempty"`
	PayoutState      string          `json:"payout_state,omitempty"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
}

// Store is a persisted, in-memory settlement record store: sync.Mutex
// guarding maps, matching cmd/bank's store shape. Path, when non-empty,
// makes every mutating method save to disk immediately ("saves on each
// mutation" per ARCHITECTURE-reconciliation-api.md); it is set by
// cmd/settlement after Load, not by NewStore, so tests can use a Store
// with no disk footprint at all.
type Store struct {
	mu         sync.Mutex
	records    map[string]*SettlementRecord
	byMerchant map[string][]string
	byDate     map[string][]string
	Path       string
}

// NewStore returns an empty Store ready for Create/Load.
func NewStore() *Store {
	return &Store{
		records:    map[string]*SettlementRecord{},
		byMerchant: map[string][]string{},
		byDate:     map[string][]string{},
	}
}

// Create inserts a new settlement record. The caller assigns ID (and every
// other field) before calling Create; Create rejects a blank or duplicate ID.
func (s *Store) Create(r *SettlementRecord) error {
	if r == nil || r.ID == "" {
		return fmt.Errorf("settlement: record ID required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[r.ID]; exists {
		return fmt.Errorf("settlement: record %s already exists", r.ID)
	}
	now := nowRFC3339()
	if r.CreatedAt == "" {
		r.CreatedAt = now
	}
	if r.UpdatedAt == "" {
		r.UpdatedAt = now
	}
	s.records[r.ID] = r
	s.byMerchant[r.MerchantID] = append(s.byMerchant[r.MerchantID], r.ID)
	s.byDate[r.TransactionsDate] = append(s.byDate[r.TransactionsDate], r.ID)
	s.saveLocked()
	return nil
}

// UpdateState sets a record's state directly, bypassing transition
// validation (Transition is the validated path; this is the low-level
// setter it and callers needing an unconditional override use).
func (s *Store) UpdateState(id string, state SettlementState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return fmt.Errorf("settlement: record %s not found", id)
	}
	rec.State = state
	rec.UpdatedAt = nowRFC3339()
	s.saveLocked()
	return nil
}

// SetReport attaches a generated Report to a record (populated once
// settlement math has run, before the Processing -> Settled transition).
func (s *Store) SetReport(id string, r *Report) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return fmt.Errorf("settlement: record %s not found", id)
	}
	rec.Report = r
	rec.UpdatedAt = nowRFC3339()
	s.saveLocked()
	return nil
}

// SetPayout records the outcome of the best-effort Banking Circle payout
// submission triggered by a Processing -> Settled transition. payoutID may
// be empty (e.g. on submission failure, where payoutState is
// "submission_failed").
func (s *Store) SetPayout(id, payoutID, payoutState string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return fmt.Errorf("settlement: record %s not found", id)
	}
	rec.PayoutID = payoutID
	rec.PayoutState = payoutState
	rec.UpdatedAt = nowRFC3339()
	s.saveLocked()
	return nil
}

// Query filters records whose TransactionsDate falls within
// [fromDate, toDate] (inclusive, "YYYY-MM-DD" lexical compare) and, when
// merchantID is non-empty, whose MerchantID matches. Results are sorted by
// TransactionsDate then ID for deterministic output.
func (s *Store) Query(fromDate, toDate, merchantID string) []*SettlementRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*SettlementRecord, 0)
	for _, rec := range s.records {
		if rec.TransactionsDate < fromDate || rec.TransactionsDate > toDate {
			continue
		}
		if merchantID != "" && rec.MerchantID != merchantID {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TransactionsDate != out[j].TransactionsDate {
			return out[i].TransactionsDate < out[j].TransactionsDate
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns a single record by ID.
func (s *Store) Get(id string) (*SettlementRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return nil, fmt.Errorf("settlement: record %s not found", id)
	}
	return rec, nil
}

// List returns every record, sorted by ID for deterministic output.
func (s *Store) List() []*SettlementRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*SettlementRecord, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Save persists every record to path as a JSON array, creating path's
// parent directory if missing.
func (s *Store) Save(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveToLocked(path)
}

// saveLocked persists to s.Path when set. Called by every mutating method
// while s.mu is already held.
func (s *Store) saveLocked() {
	if s.Path == "" {
		return
	}
	if err := s.saveToLocked(s.Path); err != nil {
		fmt.Fprintf(os.Stderr, "settlement: save %s: %v\n", s.Path, err)
	}
}

func (s *Store) saveToLocked(path string) error {
	out := make([]*SettlementRecord, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}

// Load replaces the store's contents with records read from path. A
// missing file is not an error (first run has nothing to load yet).
func (s *Store) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var records []*SettlementRecord
	if len(data) > 0 {
		if err := json.Unmarshal(data, &records); err != nil {
			return fmt.Errorf("settlement: load %s: %w", path, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = map[string]*SettlementRecord{}
	s.byMerchant = map[string][]string{}
	s.byDate = map[string][]string{}
	for _, rec := range records {
		s.records[rec.ID] = rec
		s.byMerchant[rec.MerchantID] = append(s.byMerchant[rec.MerchantID], rec.ID)
		s.byDate[rec.TransactionsDate] = append(s.byDate[rec.TransactionsDate], rec.ID)
	}
	return nil
}

func nowRFC3339() string {
	return runnerclock.Now().Format(time.RFC3339)
}
