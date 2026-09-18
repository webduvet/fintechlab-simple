package settlement

import (
	"path/filepath"
	"testing"
)

func TestCreateRejectsBlankAndDuplicateID(t *testing.T) {
	s := NewStore()
	if err := s.Create(&SettlementRecord{}); err == nil {
		t.Fatal("expected error for blank ID")
	}
	rec := &SettlementRecord{ID: "set_dup", MerchantID: "m1", TransactionsDate: "2026-09-01"}
	if err := s.Create(rec); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(&SettlementRecord{ID: "set_dup"}); err == nil {
		t.Fatal("expected error for duplicate ID")
	}
}

func seedRecords(t *testing.T, s *Store) {
	t.Helper()
	records := []*SettlementRecord{
		{ID: "set_1", MerchantID: "m1", TransactionsDate: "2026-09-01", Currency: "EUR", State: StateScheduled},
		{ID: "set_2", MerchantID: "m1", TransactionsDate: "2026-09-03", Currency: "EUR", State: StateSettled},
		{ID: "set_3", MerchantID: "m2", TransactionsDate: "2026-09-02", Currency: "USD", State: StateScheduled},
	}
	for _, r := range records {
		if err := s.Create(r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueryDateAndMerchantFiltering(t *testing.T) {
	s := NewStore()
	seedRecords(t, s)

	all := s.Query("2026-09-01", "2026-09-03", "")
	if len(all) != 3 {
		t.Fatalf("all: got %d, want 3", len(all))
	}

	narrow := s.Query("2026-09-02", "2026-09-03", "")
	if len(narrow) != 2 || narrow[0].ID != "set_3" || narrow[1].ID != "set_2" {
		t.Fatalf("narrow range: got %v", ids(narrow))
	}

	byMerchant := s.Query("2026-09-01", "2026-09-03", "m1")
	if len(byMerchant) != 2 {
		t.Fatalf("by merchant: got %d, want 2", len(byMerchant))
	}
	for _, r := range byMerchant {
		if r.MerchantID != "m1" {
			t.Fatalf("leaked record for %s", r.MerchantID)
		}
	}

	none := s.Query("2026-01-01", "2026-01-02", "")
	if len(none) != 0 {
		t.Fatalf("out of range: got %d, want 0", len(none))
	}
}

func ids(rs []*SettlementRecord) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := NewStore()
	seedRecords(t, s)
	if err := s.SetReport("set_2", &Report{MerchantID: "m1", SaleAmountTotal: 5000}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "nested", "settlements.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded := NewStore()
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := loaded.List()
	if len(got) != 3 {
		t.Fatalf("loaded %d records, want 3", len(got))
	}
	rec, err := loaded.Get("set_2")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Report == nil || rec.Report.SaleAmountTotal != 5000 {
		t.Fatalf("report not round-tripped: %+v", rec.Report)
	}
	if rec.State != StateSettled {
		t.Fatalf("state not preserved: %s", rec.State)
	}
}

func TestLoadMissingFileIsNotError(t *testing.T) {
	s := NewStore()
	if err := s.Load(filepath.Join(t.TempDir(), "nope.json")); err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatal("expected empty store")
	}
}

func TestAutoSaveOnMutationWhenPathSet(t *testing.T) {
	s := NewStore()
	s.Path = filepath.Join(t.TempDir(), "auto", "settlements.json")
	rec := &SettlementRecord{ID: "set_auto", MerchantID: "m1", TransactionsDate: "2026-09-01"}
	if err := s.Create(rec); err != nil {
		t.Fatal(err)
	}
	reloaded := NewStore()
	if err := reloaded.Load(s.Path); err != nil {
		t.Fatalf("Load after autosave: %v", err)
	}
	if _, err := reloaded.Get("set_auto"); err != nil {
		t.Fatalf("autosave did not persist: %v", err)
	}
}
