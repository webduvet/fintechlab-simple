package bankingcircle

import (
	"testing"
)

// projectedRow is bc_p_1 (processed) as a request with sel would see it,
// found by whichever of its identifiers the selection left in.
func projectedRow(t *testing.T, sel PropertySelection) map[string]any {
	t.Helper()
	for _, row := range sel.Project(IntradayReconciliation(samplePayments(), today())) {
		if row["paymentId"] == "bc_p_1" || row["userReferenceNumber"] == "stl-1" {
			return row
		}
	}
	t.Fatal("bc_p_1 not in the report")
	return nil
}

// TestDefaultPropertiesLeaveTheSweepFieldsNull: paymentId, processedTimestamp
// and return are not in the reference's default list, so a request that
// does not ask for them gets them as null — exactly the production failure
// a client that forgot PropertiesIncluded would hit.
func TestDefaultPropertiesLeaveTheSweepFieldsNull(t *testing.T) {
	row := projectedRow(t, PropertySelection{})
	for _, key := range []string{"paymentId", "processedTimestamp", "return", "latestStatusChangedTimestamp", "clientOrderId"} {
		if v, ok := row[key]; !ok || v != nil {
			t.Errorf("%s = %v (present %v), want null", key, v, ok)
		}
	}
	for _, key := range []string{"account", "accountCurrency", "transactionAmount", "reportDate", "userReferenceNumber"} {
		if row[key] == nil {
			t.Errorf("%s is null, want the default property set", key)
		}
	}
}

// TestPropertiesIncludedSelectsExactlyThose: the list replaces the default
// one, and names match whatever their case or surrounding spaces.
func TestPropertiesIncludedSelectsExactlyThose(t *testing.T) {
	sel := PropertySelection{Included: SplitProperties("PaymentId, processedtimestamp,RETURN")}
	row := projectedRow(t, sel)
	if row["paymentId"] != "bc_p_1" {
		t.Errorf("paymentId = %v, want bc_p_1", row["paymentId"])
	}
	if row["processedTimestamp"] == nil {
		t.Error("processedTimestamp is null for a processed payment")
	}
	for _, key := range []string{"account", "transactionAmount", "userReferenceNumber"} {
		if row[key] != nil {
			t.Errorf("%s = %v, want null: it was not asked for", key, row[key])
		}
	}
}

// TestPropertiesExcluded: an empty list means every property, a non-empty
// one every property but those.
func TestPropertiesExcluded(t *testing.T) {
	all := projectedRow(t, PropertySelection{ExcludedSent: true})
	for _, key := range []string{"paymentId", "processedTimestamp", "account", "transactionAmount"} {
		if all[key] == nil {
			t.Errorf("empty PropertiesExcluded: %s is null, want every property", key)
		}
	}

	row := projectedRow(t, PropertySelection{Excluded: []string{"Account"}, ExcludedSent: true})
	if row["account"] != nil {
		t.Errorf("account = %v, want null: it was excluded", row["account"])
	}
	if row["paymentId"] != "bc_p_1" {
		t.Errorf("paymentId = %v, want bc_p_1", row["paymentId"])
	}
}

// TestPropertiesIncludedWinsOverExcluded: the reference leaves both at once
// undefined; the lab lets the inclusion list decide.
func TestPropertiesIncludedWinsOverExcluded(t *testing.T) {
	row := projectedRow(t, PropertySelection{
		Included:     []string{"PaymentId"},
		Excluded:     []string{"PaymentId"},
		ExcludedSent: true,
	})
	if row["paymentId"] != "bc_p_1" {
		t.Errorf("paymentId = %v, want bc_p_1", row["paymentId"])
	}
	if row["account"] != nil {
		t.Errorf("account = %v, want null", row["account"])
	}
}
