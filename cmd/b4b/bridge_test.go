package main

import (
	"testing"

	"github.com/webduvet/fintechlab-simple/internal/b4b"
)

// TestTheBridgeSaysWhereTheMoneyIsGoing: Banking Circle can only pass a
// payout on to the beneficiary's own bank if B4B tells it which account —
// the creditor account the payout named, or the beneficiary's registered
// one. An IBAN goes as an IBAN; an account number and sort code as those.
func TestTheBridgeSaysWhereTheMoneyIsGoing(t *testing.T) {
	a := &app{beneficiaries: b4b.NewBeneficiaryStore(func() string { return "ben_1" })}
	ben, err := a.beneficiaries.Register(b4b.RegisterParams{
		ExternalRef: "70000001", AccountName: "Quiet Coffee", AccountNumber: "12345678", FinancialInstitution: "SC112233",
	})
	if err != nil {
		t.Fatal(err)
	}

	named := &b4b.Payment{BeneficiaryID: ben.ID, CreditorAccount: b4b.AccountRef{Account: "GB00 SIM0 0000 0000 0001"}, CreditorName: "Alice"}
	if got := a.bridgeBody(named, "bcp_1"); got["iban"] != "GB00SIM0000000000001" || got["holder"] != "Alice" || got["accountNumber"] != nil {
		t.Errorf("a payout naming an IBAN: %v", got)
	}

	registered := &b4b.Payment{BeneficiaryID: ben.ID}
	got := a.bridgeBody(registered, "bcp_2")
	if got["accountNumber"] != "12345678" || got["financialInstitution"] != "SC112233" || got["holder"] != "Quiet Coffee" || got["iban"] != nil {
		t.Errorf("a payout to a registered beneficiary: %v", got)
	}
	if got["paymentId"] != "bcp_2" {
		t.Errorf("paymentId = %v", got["paymentId"])
	}
}
