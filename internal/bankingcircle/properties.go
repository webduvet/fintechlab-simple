package bankingcircle

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Which properties a reconciliation row carries is the caller's choice,
// made with the PropertiesIncluded / PropertiesExcluded query parameters.
// The rules are Banking Circle's, from the reference for
// GET /api/v1/reports/intraday-reconciliation-paged-report:
//
//   - neither parameter: the default list below;
//   - PropertiesIncluded: exactly the listed properties;
//   - PropertiesExcluded: every property except the listed ones, so an empty
//     PropertiesExcluded asks for all of them.
//
// This matters because `paymentId`, `processedTimestamp` and `return` are
// not in the default list. A client that forgets to ask for PaymentId gets
// no paymentId from the real bank, and must get none here either, or the
// lab passes a sweep that cannot match a single payment in production.
//
// A property left out is sent as null, not dropped: the reference's example
// response for a default request shows the non-default properties
// (`paymentId`, `processedTimestamp`, `return`, ...) as null. The reference
// does not say what happens when both parameters are sent; here
// PropertiesIncluded wins. Names match case-insensitively, and an unknown
// name selects nothing.

// defaultProperties is the reference's "If neither PropertiesIncluded nor
// PropertiesExcluded are specified" list, lower-cased.
var defaultProperties = propertySet([]string{
	"PIdChannelUser", "PTxnDate", "ReportDate", "CustomerId", "Account",
	"AccountCurrency", "DebitAmount", "CreditAmount", "ValueDate",
	"InstructedAmount", "InstructedAmountCurrency", "TransactionAmount",
	"TransactionAmountCurrency", "ExchangeRate", "DebtorBankCode",
	"DebtorAccount", "DebtorLine1", "DebtorLine2", "DebtorLine3",
	"DebtorLine4", "BeneficiaryBankCode", "BeneficiaryAccount",
	"BeneficiaryLine1", "BeneficiaryLine2", "BeneficiaryLine3",
	"BeneficiaryLine4", "PaymentReferenceNumber", "FileReferenceNumber",
	"UserReferenceNumber", "PaymentDetails1", "PaymentDetails2",
	"PaymentDetails3", "PaymentDetails4", "CreditDebitIndicator",
	"BankTrnsCodeDomain", "BankTrnsCodeFamily", "CreatedAt",
})

// PropertySelection is what a report request asked for.
type PropertySelection struct {
	// Included is PropertiesIncluded (or the deprecated IncludeProperties).
	Included []string
	// Excluded is PropertiesExcluded (or the deprecated ExcludeProperties).
	Excluded []string
	// ExcludedSent is true when the excluded parameter was present at all,
	// even empty: an empty exclusion list means "every property".
	ExcludedSent bool
}

// includes reports whether a row's JSON key is selected. The JSON keys are
// the property names with a lower-case first letter, so one case-insensitive
// comparison serves both.
func (s PropertySelection) includes(key string) bool {
	key = strings.ToLower(key)
	switch {
	case len(s.Included) > 0:
		return propertySet(s.Included)[key]
	case s.ExcludedSent:
		return !propertySet(s.Excluded)[key]
	default:
		return defaultProperties[key]
	}
}

// Project renders rows with only the selected properties set; every other
// property is null.
func (s PropertySelection) Project(rows []ReconciliationRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		fields := rowFields(row)
		for key := range fields {
			if !s.includes(key) {
				fields[key] = nil
			}
		}
		out = append(out, fields)
	}
	return out
}

// rowFields turns a row into its wire form as a map, through the same JSON
// tags the row is otherwise sent with, so the two can never drift apart.
func rowFields(row ReconciliationRow) map[string]any {
	raw, err := json.Marshal(row)
	if err != nil {
		panic(err) // a struct of strings, floats and bools always marshals
	}
	fields := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep amounts exactly as the row had them
	if err := dec.Decode(&fields); err != nil {
		panic(err)
	}
	return fields
}

// SplitProperties parses a comma separated property list, the reference's
// format for both parameters.
func SplitProperties(list string) []string {
	var out []string
	for _, name := range strings.Split(list, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func propertySet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[strings.ToLower(strings.TrimSpace(name))] = true
	}
	return set
}
