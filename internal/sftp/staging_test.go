package sftp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/settlement"
)

func testReport() *settlement.Report {
	return &settlement.Report{
		MerchantID:          "GB00SIM0000000000003",
		TransactionsDate:    "2026-09-03",
		Currency:            "EUR",
		SettlementState:     "Scheduled",
		SettlementDate:      "2026-09-04",
		SaleAmountTotal:     15000,
		SettlementNetAmount: 15000,
	}
}

func TestStageWritesDirectoryStructureAndContent(t *testing.T) {
	dir := t.TempDir()
	s := &Stager{OutDir: dir}
	rel, err := s.Stage(testReport(), "csv")
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	wantRel := filepath.Join("GB00SIM0000000000003", "GB00SIM0000000000003_2026-09-03_EUR_WX.csv")
	if rel != wantRel {
		t.Fatalf("rel = %q, want %q", rel, wantRel)
	}
	full := filepath.Join(dir, rel)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("file not written at %s: %v", full, err)
	}
	want, _ := settlement.ToCSV(testReport())
	if string(data) != string(want) {
		t.Fatalf("content mismatch:\ngot:  %s\nwant: %s", data, want)
	}
}

func TestStageAllThreeFormats(t *testing.T) {
	dir := t.TempDir()
	s := &Stager{OutDir: dir}
	for _, format := range []string{"csv", "xml", "json"} {
		if _, err := s.Stage(testReport(), format); err != nil {
			t.Fatalf("Stage(%s): %v", format, err)
		}
	}
	files, err := s.ListOut()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 staged files, got %d", len(files))
	}
}

func TestStageUnsupportedFormat(t *testing.T) {
	s := &Stager{OutDir: t.TempDir()}
	if _, err := s.Stage(testReport(), "pdf"); err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

// TestStageRejectsPathTraversalMerchantID is a regression test: a
// merchant_id that reaches Stage containing ".." must be rejected before
// any filesystem path is constructed, not silently resolved -- otherwise
// filepath.Join(OutDir, merchantID, filename) can escape OutDir entirely
// (OutDir is absolute, so Clean happily walks ".." past it).
func TestStageRejectsPathTraversalMerchantID(t *testing.T) {
	dir := t.TempDir()
	s := &Stager{OutDir: dir}
	r := testReport()
	r.MerchantID = "../../../etc"
	if _, err := s.Stage(r, "csv"); err == nil {
		t.Fatal("expected Stage to reject a path-traversal merchant ID")
	}
	// Nothing should have been written anywhere, in OutDir or outside it.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("Stage wrote files despite rejecting the merchant ID: %v", entries)
	}
}

func TestListOutSortedByModTimeDescending(t *testing.T) {
	dir := t.TempDir()
	s := &Stager{OutDir: dir}
	r1 := testReport()
	r1.MerchantID = "m1"
	if _, err := s.Stage(r1, "csv"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	r2 := testReport()
	r2.MerchantID = "m2"
	if _, err := s.Stage(r2, "csv"); err != nil {
		t.Fatal(err)
	}
	files, err := s.ListOut()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if !files[0].ModTime().After(files[1].ModTime()) && files[0].ModTime() != files[1].ModTime() {
		t.Fatalf("not sorted newest-first: %v then %v", files[0].ModTime(), files[1].ModTime())
	}
}

func TestListOutOnMissingDirIsEmptyNotError(t *testing.T) {
	s := &Stager{OutDir: filepath.Join(t.TempDir(), "does-not-exist")}
	files, err := s.ListOut()
	if err != nil {
		t.Fatalf("ListOut on missing dir: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected empty, got %d", len(files))
	}
}

func TestReadOutRetrievesStagedFile(t *testing.T) {
	dir := t.TempDir()
	s := &Stager{OutDir: dir}
	rel, err := s.Stage(testReport(), "json")
	if err != nil {
		t.Fatal(err)
	}
	data, err := s.ReadOut(rel)
	if err != nil {
		t.Fatalf("ReadOut: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty content")
	}
}

func TestReadOutRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	// Plant a file outside OutDir to prove it is unreachable.
	outsideDir := filepath.Dir(dir)
	secret := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("do not read me"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(secret)

	s := &Stager{OutDir: dir}
	if _, err := s.ReadOut("../secret.txt"); err == nil {
		t.Fatal("expected ReadOut to reject a traversal path")
	}
	if _, err := s.ReadOut("../../../../../../etc/passwd"); err == nil {
		t.Fatal("expected ReadOut to reject a deep traversal path")
	}
}

func TestReadOutMissingFile(t *testing.T) {
	s := &Stager{OutDir: t.TempDir()}
	if _, err := s.ReadOut("nope/nope.csv"); err == nil {
		t.Fatal("expected error for missing file")
	}
}
