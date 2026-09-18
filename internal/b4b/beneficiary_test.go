package b4b

import (
	"errors"
	"testing"
)

func TestLookupBeneficiaryDeterministic(t *testing.T) {
	a1 := LookupBeneficiary("ben_merchant42")
	a2 := LookupBeneficiary("ben_merchant42")
	if *a1 != *a2 {
		t.Fatalf("LookupBeneficiary not deterministic: %+v vs %+v", a1, a2)
	}
	if a1.SanctionsStatus != SanctionsPass {
		t.Fatalf("sanctions_status = %q, want pass", a1.SanctionsStatus)
	}
	if a1.ID != "ben_merchant42" {
		t.Fatalf("id = %q, want ben_merchant42", a1.ID)
	}
	if a1.AccountName == "" || a1.AccountNumber == "" || a1.FinancialInstitution == "" {
		t.Fatalf("auto-vivified fields must not be empty: %+v", a1)
	}
}

func TestLookupBeneficiaryVariesByID(t *testing.T) {
	a := LookupBeneficiary("ben_merchant1")
	b := LookupBeneficiary("ben_merchant2")
	if a.AccountNumber == b.AccountNumber {
		t.Fatalf("account_number collided for different ids: %q", a.AccountNumber)
	}
	if a.FinancialInstitution == b.FinancialInstitution {
		t.Fatalf("financial_institution collided for different ids: %q", a.FinancialInstitution)
	}
}

func TestLookupBeneficiaryNeverFails(t *testing.T) {
	for _, id := range []string{"", "ben_x", "🤖", "a very long beneficiary id with spaces"} {
		b := LookupBeneficiary(id)
		if b == nil {
			t.Fatalf("LookupBeneficiary(%q) returned nil", id)
		}
		if b.SanctionsStatus != SanctionsPass {
			t.Fatalf("LookupBeneficiary(%q) sanctions_status = %q", id, b.SanctionsStatus)
		}
	}
}

func TestRegisterValidatesRequiredFields(t *testing.T) {
	s := NewBeneficiaryStore(nil)
	cases := map[string]RegisterParams{
		"no account name":   {AccountNumber: "12345678", FinancialInstitution: "SC112233"},
		"no account number": {AccountName: "Acme Ltd", FinancialInstitution: "SC112233"},
		"no institution":    {AccountName: "Acme Ltd", AccountNumber: "12345678"},
		"bad sanctions status": {
			AccountName: "Acme Ltd", AccountNumber: "12345678",
			FinancialInstitution: "SC112233", SanctionsStatus: "CLEAR",
		},
	}
	for name, p := range cases {
		if _, err := s.Register(p); err == nil {
			t.Errorf("Register(%s) succeeded, want an error", name)
		}
	}
}

func TestRegisteredBeneficiaryDefaultsToPassAndIsReadBack(t *testing.T) {
	s := NewBeneficiaryStore(nil)
	ben, err := s.Register(RegisterParams{
		ExternalRef:          "merchant-42",
		AccountName:          "Acme Ltd",
		AccountNumber:        "12345678",
		FinancialInstitution: "SC112233",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ben.SanctionsStatus != SanctionsPass {
		t.Fatalf("sanctions_status = %q, want %q", ben.SanctionsStatus, SanctionsPass)
	}
	got := s.Get(ben.ID)
	if got.AccountNumber != "12345678" || got.AccountName != "Acme Ltd" {
		t.Fatalf("registered beneficiary read back as %+v", got)
	}
	// An unknown id still auto-vivifies, so a caller that never registered
	// anything keeps working.
	if auto := s.Get("ben_never_registered"); auto.AccountNumber == "" {
		t.Fatal("an unknown id did not auto-vivify")
	}
}

func TestARegisteredBeneficiaryIsReachableByTheCallersOwnReference(t *testing.T) {
	s := NewBeneficiaryStore(nil)
	ben, err := s.Register(RegisterParams{
		ExternalRef: "WL000000000042", AccountName: "Acme Ltd",
		AccountNumber: "12345678", FinancialInstitution: "SC112233",
	})
	if err != nil {
		t.Fatal(err)
	}
	// B4B mints the id, so a platform that keys payouts on its own
	// reference has only that to look up by. Without this it would reach
	// an auto-vivified record carrying account details nobody registered,
	// and pay against those.
	byRef := s.Get("WL000000000042")
	if byRef.ID != ben.ID || byRef.AccountNumber != "12345678" {
		t.Fatalf("lookup by external_ref returned %+v, want the registered record", byRef)
	}
	// And a status moved by reference moves the registered record, not a
	// second one that nothing reads.
	if _, changed, err := s.SetSanctions("WL000000000042", SanctionsFail); err != nil || !changed {
		t.Fatalf("SetSanctions by ref: changed=%v err=%v", changed, err)
	}
	if s.Get(ben.ID).SanctionsStatus != SanctionsFail {
		t.Fatal("the status change did not reach the record found by id")
	}
	// Re-registering the same reference corrects it rather than forking a
	// second payee.
	again, err := s.Register(RegisterParams{
		ExternalRef: "WL000000000042", AccountName: "Acme Ltd",
		AccountNumber: "87654321", FinancialInstitution: "SC112233",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get("WL000000000042").AccountNumber != "87654321" || again.ID == "" {
		t.Fatal("re-registering a reference did not correct the record it resolves to")
	}
}

func TestSetSanctionsReportsWhetherItChanged(t *testing.T) {
	s := NewBeneficiaryStore(nil)
	ben, err := s.Register(RegisterParams{
		AccountName: "Acme Ltd", AccountNumber: "12345678", FinancialInstitution: "SC112233",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Callbacks fire on a change, not on every write, so writing the same
	// value again must not report one.
	if _, changed, err := s.SetSanctions(ben.ID, SanctionsPass); err != nil || changed {
		t.Fatalf("re-setting the same status reported changed=%v err=%v", changed, err)
	}
	updated, changed, err := s.SetSanctions(ben.ID, SanctionsFail)
	if err != nil || !changed {
		t.Fatalf("moving pass -> fail reported changed=%v err=%v", changed, err)
	}
	if updated.SanctionsStatus != SanctionsFail {
		t.Fatalf("status = %q, want %q", updated.SanctionsStatus, SanctionsFail)
	}
	// The change sticks: a status can move at any time under continuous
	// screening, so a beneficiary that paid out yesterday may not today.
	if s.Get(ben.ID).SanctionsStatus != SanctionsFail {
		t.Fatal("the status change did not persist")
	}
	if _, _, err := s.SetSanctions(ben.ID, "CLEAR"); err == nil {
		t.Fatal("an invalid sanctions status was accepted")
	}
}

func TestCheckCreditorRejectsMismatchedDetails(t *testing.T) {
	ben := &Beneficiary{
		AccountName:          "Acme Ltd",
		AccountNumber:        "12345678",
		FinancialInstitution: "SC112233",
	}
	if err := ben.CheckCreditor("12345678", "SC112233", "Acme Ltd"); err != nil {
		t.Fatalf("matching details were rejected: %v", err)
	}
	// Case is not the difference that matters.
	if err := ben.CheckCreditor("12345678", "sc112233", "acme ltd"); err != nil {
		t.Fatalf("case-different but matching details were rejected: %v", err)
	}
	// An omitted field is not a mismatch -- the check is on what was sent.
	if err := ben.CheckCreditor("12345678", "", ""); err != nil {
		t.Fatalf("omitted optional creditor fields were rejected: %v", err)
	}
	for name, args := range map[string][3]string{
		"wrong account":     {"87654321", "SC112233", "Acme Ltd"},
		"wrong institution": {"12345678", "SC999999", "Acme Ltd"},
		"wrong name":        {"12345678", "SC112233", "Someone Else"},
	} {
		err := ben.CheckCreditor(args[0], args[1], args[2])
		if err == nil {
			t.Errorf("CheckCreditor(%s) succeeded; accepting a mismatch is how money reaches the wrong account", name)
			continue
		}
		if !errors.Is(err, ErrCreditorMismatch) {
			t.Errorf("CheckCreditor(%s) returned %v, want ErrCreditorMismatch", name, err)
		}
	}
}

// A disabled payee screens clean and still cannot be paid. A client gating
// only on sanctions_status passes every check it makes and is refused
// anyway, which is why the two are separate here.
func TestBeneficiaryStatusIsSeparateFromSanctions(t *testing.T) {
	s := NewBeneficiaryStore(nil)
	ben, err := s.Register(RegisterParams{
		AccountName: "Acme Ltd", AccountNumber: "12345678", FinancialInstitution: "SC112233",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if ben.Status != StatusActive {
		t.Fatalf("a new beneficiary is %q, want active", ben.Status)
	}
	disabled, changed, err := s.SetStatus(ben.ID, StatusDisabled)
	if err != nil || !changed {
		t.Fatalf("disable: err=%v changed=%t", err, changed)
	}
	if disabled.DisabledAt == "" {
		t.Error("disabling should record when")
	}
	if disabled.SanctionsStatus != SanctionsPass {
		t.Errorf("disabling moved the sanctions status to %q; the two are independent", disabled.SanctionsStatus)
	}
	reenabled, _, err := s.SetStatus(ben.ID, StatusActive)
	if err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if reenabled.DisabledAt != "" {
		t.Error("re-enabling should clear disabled_at")
	}
	if _, _, err := s.SetStatus(ben.ID, "suspended"); err == nil {
		t.Error("an unknown status should be refused")
	}
}

// Whether the real API refuses this is unconfirmed, so the check has to be
// exactly this: a rule a caller can turn on, and silence when it is off.
func TestCheckCompanyOnlyBindsWhenBothSidesNameOne(t *testing.T) {
	owned := &Beneficiary{ID: "ben_1", CompanyID: "cmp_merchant"}
	if err := owned.CheckCompany("cmp_merchant"); err != nil {
		t.Errorf("same company: %v", err)
	}
	if err := owned.CheckCompany(""); err != nil {
		t.Errorf("a payment naming no company must not be refused: %v", err)
	}
	if err := owned.CheckCompany("cmp_platform"); !errors.Is(err, ErrCompanyMismatch) {
		t.Errorf("different company: err = %v, want ErrCompanyMismatch", err)
	}
	unowned := &Beneficiary{ID: "ben_2"}
	if err := unowned.CheckCompany("cmp_platform"); err != nil {
		t.Errorf("an auto-vivified payee belongs to nobody and is payable by anyone: %v", err)
	}
}

func TestBeneficiaryStoreOnChangeFires(t *testing.T) {
	s := NewBeneficiaryStore(nil)
	changes := 0
	s.OnChange = func() { changes++ }
	if _, err := s.Register(RegisterParams{
		AccountName: "Acme Ltd", AccountNumber: "12345678", FinancialInstitution: "SC112233",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, err := s.SetSanctions("ben_whatever", SanctionsFail); err != nil {
		t.Fatalf("set sanctions: %v", err)
	}
	if _, _, err := s.SetStatus("ben_whatever", StatusDisabled); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if changes != 3 {
		t.Fatalf("OnChange fired %d times, want 3", changes)
	}
}
