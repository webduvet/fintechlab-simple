package sftpgateway

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDropInboundWritesPathAndContent(t *testing.T) {
	c := &Channel{BaseDir: t.TempDir()}
	path, err := c.DropInbound(CategoryOnboarding, "GB00SIM0000000000003", "contract.pdf", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(c.BaseDir, "inbound", "onboarding", "GB00SIM0000000000003", "contract.pdf")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
}

func TestDropInboundRejectsBadCategoryExtensionAndPath(t *testing.T) {
	c := &Channel{BaseDir: t.TempDir()}
	if _, err := c.DropInbound("bogus", "m1", "a.pdf", []byte("x")); err == nil {
		t.Fatal("expected reject for unknown category")
	}
	if _, err := c.DropInbound(CategoryCorrections, "m1", "a.pdf", []byte("x")); err == nil {
		t.Fatal("expected reject: corrections does not accept pdf")
	}
	if _, err := c.DropInbound(CategoryOnboarding, "m1", "../etc/passwd", []byte("x")); err == nil {
		t.Fatal("expected reject for path-traversal filename")
	}
	if _, err := c.DropInbound(CategoryOnboarding, "m1", "big.csv", make([]byte, MaxInboundFileSize+1)); err == nil {
		t.Fatal("expected reject for oversized file")
	}
}

func TestListInboundReturnsOnboardingUploads(t *testing.T) {
	c := &Channel{BaseDir: t.TempDir()}
	if _, err := c.DropInbound(CategoryOnboarding, "m1", "a.pdf", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DropInbound(CategoryOnboarding, "m1", "b.csv", []byte("22")); err != nil {
		t.Fatal(err)
	}
	// A different merchant's upload must not leak into m1's listing.
	if _, err := c.DropInbound(CategoryOnboarding, "m2", "c.xml", []byte("3")); err != nil {
		t.Fatal(err)
	}
	infos, err := c.ListInbound(CategoryOnboarding, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("len(infos) = %d, want 2", len(infos))
	}
	names := map[string]bool{}
	for _, fi := range infos {
		names[fi.Name()] = true
	}
	if !names["a.pdf"] || !names["b.csv"] {
		t.Fatalf("unexpected listing: %v", names)
	}
}

func TestListInboundEmptyForUnknownMerchantIsNotError(t *testing.T) {
	c := &Channel{BaseDir: t.TempDir()}
	infos, err := c.ListInbound(CategoryCorrections, "no-such-merchant")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Fatalf("len(infos) = %d, want 0", len(infos))
	}
}

func writeOutbound(t *testing.T, c *Channel, merchantID, category, name, content string) {
	t.Helper()
	dir := filepath.Join(c.BaseDir, "outbound", merchantID, category)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListOutboundReturnsBothCategories(t *testing.T) {
	c := &Channel{BaseDir: t.TempDir()}
	writeOutbound(t, c, "m1", CategoryDaily, "m1_2026-09-03_EUR_WX.csv", "wx")
	writeOutbound(t, c, "m1", CategoryFinancial, "m1_2026-09-03_financial.csv", "fin")

	infos, err := c.ListOutbound("m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("len(infos) = %d, want 2", len(infos))
	}
}

func TestListOutboundCategoryScopesToOneReportType(t *testing.T) {
	c := &Channel{BaseDir: t.TempDir()}
	writeOutbound(t, c, "m1", CategoryDaily, "m1_2026-09-03_EUR_WX.csv", "wx")
	writeOutbound(t, c, "m1", CategoryFinancial, "m1_2026-09-03_financial.csv", "fin")

	infos, err := c.ListOutboundCategory("m1", CategoryDaily)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name() != "m1_2026-09-03_EUR_WX.csv" {
		t.Fatalf("unexpected daily listing: %v", infos)
	}
	if _, err := c.ListOutboundCategory("m1", "bogus"); err == nil {
		t.Fatal("expected reject for unknown outbound category")
	}
}

func TestReadOutboundRetrievesContent(t *testing.T) {
	c := &Channel{BaseDir: t.TempDir()}
	writeOutbound(t, c, "m1", CategoryDaily, "file.csv", "settlement-data")

	got, err := c.ReadOutbound("m1", "daily/file.csv")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "settlement-data" {
		t.Fatalf("content = %q", got)
	}
	if _, err := c.ReadOutbound("m1", "../../../etc/passwd"); err == nil {
		t.Fatal("expected reject for path-traversal relative path")
	}
	if _, err := c.ReadOutbound("m1", "shared/x.pdf"); err == nil {
		t.Fatal("expected reject for non daily/financial category")
	}
}

func TestArchiveOutboundMovesFileByYearMonthAndListArchiveFindsIt(t *testing.T) {
	c := &Channel{BaseDir: t.TempDir()}
	writeOutbound(t, c, "m1", CategoryDaily, "file.csv", "settlement-data")

	dest, err := c.ArchiveOutbound("m1", "daily/file.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("archived file missing at %s: %v", dest, err)
	}
	if _, err := os.Stat(filepath.Join(c.BaseDir, "outbound", "m1", "daily", "file.csv")); err == nil {
		t.Fatal("original outbound file should have been moved, not copied")
	}

	now := time.Now().UTC()
	year := fmt.Sprintf("%04d", now.Year())
	month := fmt.Sprintf("%02d", now.Month())
	infos, err := c.ListArchive(year, month)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name() != "file.csv" {
		t.Fatalf("ListArchive(%s, %s) = %v, want [file.csv]", year, month, infos)
	}
}

func TestConfigLoadUpdateRoundTrip(t *testing.T) {
	c := &Channel{BaseDir: t.TempDir()}
	if _, err := c.GetConfig(); err == nil {
		t.Fatal("expected error reading config before it has ever been written")
	}
	want := &Config{
		MerchantID:          "GB00SIM0000000000003",
		SFTPDirectory:       "/sftp/outbound/GB00SIM0000000000003",
		ReportTypes:         []string{"WX", "Financial"},
		ReportFormats:       []string{"CSV", "XML", "ASCII"},
		CutOffTime:          "00:00 CET",
		RemittanceFrequency: "daily",
		SFTPDirectoryCount:  3,
	}
	if err := c.UpdateConfig(want); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.MerchantID != want.MerchantID || got.SFTPDirectory != want.SFTPDirectory ||
		got.CutOffTime != want.CutOffTime || got.RemittanceFrequency != want.RemittanceFrequency ||
		got.SFTPDirectoryCount != want.SFTPDirectoryCount || len(got.ReportTypes) != 2 || len(got.ReportFormats) != 3 {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}

	// Update again: the file must reflect the new value, not silently keep
	// the old one.
	want.RemittanceFrequency = "weekly"
	if err := c.UpdateConfig(want); err != nil {
		t.Fatal(err)
	}
	got, err = c.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.RemittanceFrequency != "weekly" {
		t.Fatalf("RemittanceFrequency = %q, want weekly", got.RemittanceFrequency)
	}
}

func TestDirectoryStructureMatchesWorldlineConvention(t *testing.T) {
	c := &Channel{BaseDir: t.TempDir()}
	path, err := c.DropInbound(CategoryCorrections, "m1", "fix.xml", []byte("<x/>"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(c.BaseDir, "inbound", "corrections", "m1", "fix.xml")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

// TestMerchantIDPathTraversalRejected guards against a real reported gap:
// a %2f-encoded URL path segment (e.g. "..%2f..%2f..%2fetc") reaches
// net/http's {merchant_id} wildcard as a literal string containing "/" and
// ".." *without* triggering ServeMux's built-in ".." redirect (that
// redirect only fires for an unescaped ".." segment in the raw request
// path). Every Channel method that takes merchantID must treat it as an
// untrusted single path component, not just filenames/relativePaths.
func TestMerchantIDPathTraversalRejected(t *testing.T) {
	c := &Channel{BaseDir: t.TempDir()}
	poisoned := []string{"../../../etc", "foo/../../bar", "foo/bar", "..", ".", ""}

	for _, id := range poisoned {
		if _, err := c.DropInbound(CategoryOnboarding, id, "x.pdf", []byte("x")); err == nil {
			t.Fatalf("DropInbound(%q): expected reject", id)
		}
		if _, err := c.ListInbound(CategoryOnboarding, id); err == nil {
			t.Fatalf("ListInbound(%q): expected reject", id)
		}
		if _, err := c.ListOutbound(id); err == nil {
			t.Fatalf("ListOutbound(%q): expected reject", id)
		}
		if _, err := c.ListOutboundCategory(id, CategoryDaily); err == nil {
			t.Fatalf("ListOutboundCategory(%q): expected reject", id)
		}
		if _, err := c.ReadOutbound(id, "daily/x.csv"); err == nil {
			t.Fatalf("ReadOutbound(%q): expected reject", id)
		}
		if _, err := c.ArchiveOutbound(id, "daily/x.csv"); err == nil {
			t.Fatalf("ArchiveOutbound(%q): expected reject", id)
		}
	}

	// Confirm the escape would actually have landed outside BaseDir had it
	// not been rejected: BaseDir/inbound/onboarding/../../../etc cleans to
	// a path outside BaseDir entirely.
	base := t.TempDir()
	escaped := filepath.Join(base, "inbound", "onboarding", "../../../etc")
	if strings.HasPrefix(filepath.Clean(escaped), base) {
		t.Fatal("test setup invalid: expected escaped path to leave BaseDir")
	}
}
