package settlement

import (
	"encoding/xml"
	"strings"
	"testing"
)

const xmlHeaderPrefix = `<?xml version="1.0" encoding="UTF-8"?>`

func TestToXMLStructure(t *testing.T) {
	data, err := ToXML(sampleReport())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), xmlHeaderPrefix) {
		t.Fatalf("missing XML declaration: %q", data[:min(60, len(data))])
	}
	var doc settlementReportXML
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Reports) != 1 {
		t.Fatalf("expected 1 <report>, got %d", len(doc.Reports))
	}
	got := doc.Reports[0]
	if got.MerchantID != "GB00SIM0000000000003" || got.Currency != "EUR" || got.SettlementNetAmount != 133250 {
		t.Fatalf("round-tripped report mismatch: %+v", got)
	}
	if !strings.Contains(string(data), "<settlementReport>") || !strings.Contains(string(data), "<report>") {
		t.Fatalf("missing expected elements: %s", data)
	}
}

func TestToXMLAllMultipleReports(t *testing.T) {
	r2 := sampleReport()
	r2.MerchantID = "GB00SIM0000000000004"
	data, err := ToXMLAll([]*Report{sampleReport(), r2})
	if err != nil {
		t.Fatal(err)
	}
	var doc settlementReportXML
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Reports) != 2 {
		t.Fatalf("expected 2 <report> children, got %d", len(doc.Reports))
	}
	if doc.Reports[0].MerchantID != "GB00SIM0000000000003" || doc.Reports[1].MerchantID != "GB00SIM0000000000004" {
		t.Fatalf("reports out of order: %+v", doc.Reports)
	}
}
