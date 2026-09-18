package settlement

import "encoding/xml"

// reportXML mirrors Report field-for-field with the camelCase element
// names in ARCHITECTURE-settlement-report.md's XML example.
type reportXML struct {
	XMLName                  xml.Name `xml:"report"`
	MerchantID               string   `xml:"merchantId"`
	TransactionsDate         string   `xml:"transactionsDate"`
	Currency                 string   `xml:"currency"`
	SettlementState          string   `xml:"settlementState"`
	SettlementDate           string   `xml:"settlementDate"`
	ApprovedTransactionCount int      `xml:"approvedTransactionCount"`
	DeclinedTransactionCount int      `xml:"declinedTransactionCount"`
	SaleAmountTotal          int64    `xml:"saleAmountTotal"`
	ReturnedAmountTotal      int64    `xml:"returnedAmountTotal"`
	ChargebacksCount         int      `xml:"chargebacksCount"`
	ChargebacksAmountTotal   int64    `xml:"chargebacksAmountTotal"`
	DiscountRateFeeTotal     int64    `xml:"discountRateFeeTotal"`
	ChargebackFeeTotal       int64    `xml:"chargebackFeeTotal"`
	ReservesHeld             int64    `xml:"reservesHeld"`
	ReservesReleased         int64    `xml:"reservesReleased"`
	ReservesForward          int64    `xml:"reservesForward"`
	SettlementNetAmount      int64    `xml:"settlementNetAmount"`
}

func toReportXML(r *Report) reportXML {
	return reportXML{
		MerchantID:               r.MerchantID,
		TransactionsDate:         r.TransactionsDate,
		Currency:                 r.Currency,
		SettlementState:          r.SettlementState,
		SettlementDate:           r.SettlementDate,
		ApprovedTransactionCount: r.ApprovedTransactionCount,
		DeclinedTransactionCount: r.DeclinedTransactionCount,
		SaleAmountTotal:          r.SaleAmountTotal,
		ReturnedAmountTotal:      r.ReturnedAmountTotal,
		ChargebacksCount:         r.ChargebacksCount,
		ChargebacksAmountTotal:   r.ChargebacksAmountTotal,
		DiscountRateFeeTotal:     r.DiscountRateFeeTotal,
		ChargebackFeeTotal:       r.ChargebackFeeTotal,
		ReservesHeld:             r.ReservesHeld,
		ReservesReleased:         r.ReservesReleased,
		ReservesForward:          r.ReservesForward,
		SettlementNetAmount:      r.SettlementNetAmount,
	}
}

// settlementReportXML is the document root: <settlementReport> with one
// <report> child per report.
type settlementReportXML struct {
	XMLName xml.Name    `xml:"settlementReport"`
	Reports []reportXML `xml:"report"`
}

// ToXML formats a single report as the documented
// <settlementReport><report>...</report></settlementReport> XML shape,
// UTF-8 encoded with an XML declaration.
func ToXML(r *Report) ([]byte, error) {
	return ToXMLAll([]*Report{r})
}

// ToXMLAll formats one <settlementReport> root containing one <report>
// child per report. Used by the multi-record GET
// /reports/settlement?format=xml response; ToXML (single report) is the
// documented per-file staging entry point.
func ToXMLAll(reports []*Report) ([]byte, error) {
	doc := settlementReportXML{Reports: make([]reportXML, 0, len(reports))}
	for _, r := range reports {
		doc.Reports = append(doc.Reports, toReportXML(r))
	}
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(xml.Header)+len(body)+1)
	out = append(out, []byte(xml.Header)...)
	out = append(out, body...)
	out = append(out, '\n')
	return out, nil
}
