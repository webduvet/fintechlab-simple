package console

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"
)

// TestABatchIsDerivedFromItsSeed: the same seed makes the same population —
// names, countries, outlet counts — so a batch in a screenshot can be made
// again. The ids, and so the MIDs, are new each time.
func TestABatchIsDerivedFromItsSeed(t *testing.T) {
	seed := int64(42)
	shape := func() ([]string, *Batch) {
		r, _ := NewRegistry("")
		b, ms, err := r.AddBatch(BatchParams{Count: 25, MaxOutlets: 4, Seed: &seed})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range ms {
			out = append(out, m.LegalName+"|"+m.Country+"|"+m.Currency+"|"+strconv.Itoa(len(m.Outlets)))
			if m.Batch != b.ID || len(m.Outlets) < 1 || len(m.Outlets) > 4 {
				t.Fatalf("merchant %+v: batch %s, %d outlets", m, b.ID, len(m.Outlets))
			}
		}
		return out, b
	}
	a, b1 := shape()
	b, _ := shape()
	if len(a) != 25 || b1.Seed != 42 {
		t.Fatalf("made %d, seed %d", len(a), b1.Seed)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("merchant %d differs between two batches with one seed: %q vs %q", i, a[i], b[i])
		}
	}
	names := map[string]bool{}
	for _, x := range a {
		if names[x] {
			t.Fatalf("two merchants in one batch share a name: %q", x)
		}
		names[x] = true
	}
}

func TestABatchIsBoundedAndCountryIsChecked(t *testing.T) {
	r, _ := NewRegistry("")
	for _, p := range []BatchParams{{Count: 0}, {Count: MaxBatch + 1}, {Count: 2, MaxOutlets: 11}, {Count: 2, Country: "XX"}} {
		if _, _, err := r.AddBatch(p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v, want invalid", p, err)
		}
	}
	// A mix stays in the currencies the lab settles end to end.
	_, mixed, err := r.AddBatch(BatchParams{Count: 60})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mixed {
		if m.Currency != "EUR" && m.Currency != "GBP" {
			t.Errorf("%s in a mix settles in %s, which Banking Circle holds no safeguarding account for", m.LegalName, m.Currency)
		}
	}
	_, ms, err := r.AddBatch(BatchParams{Count: 5, Country: "ie"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Country != "IE" || m.Currency != "EUR" {
			t.Errorf("%s is %s/%s, want IE/EUR", m.LegalName, m.Country, m.Currency)
		}
	}
}

// TestABatchSurvivesARestartAndGoesInOneDelete.
func TestABatchSurvivesARestartAndGoesInOneDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	r, _ := NewRegistry(path)
	single, _ := r.AddMerchant(NewMerchantParams{LegalName: "Kept Ltd"})
	b, _, err := r.AddBatch(BatchParams{Count: 7})
	if err != nil {
		t.Fatal(err)
	}

	again, err := NewRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if bs := again.Batches(); len(bs) != 1 || bs[0].ID != b.ID || bs[0].Seed != b.Seed {
		t.Fatalf("batches after reload = %+v", bs)
	}
	ids, _ := again.BatchMerchantIDs(b.ID)
	if len(ids) != 7 {
		t.Fatalf("batch has %d merchants after reload", len(ids))
	}
	n, err := again.DeleteBatch(b.ID)
	if err != nil || n != 7 {
		t.Fatalf("delete batch = %d, %v", n, err)
	}
	if ms := again.Merchants(); len(ms) != 1 || ms[0].ID != single.ID {
		t.Fatalf("after deleting the batch: %+v", ms)
	}
	if _, err := again.DeleteBatch(b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
}
