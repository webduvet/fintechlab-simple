package console

import (
	"strings"
	"testing"
)

func TestGeneratedIdentityIsDeterministic(t *testing.T) {
	// Same id, same everything: a screenshot, a reloaded registry and a
	// test fixture all have to agree, which a random source cannot do.
	for _, id := range []string{"out_0001", "mer_0042", ""} {
		if addressFor(id, "GB") != addressFor(id, "GB") {
			t.Errorf("addressFor(%q) not deterministic", id)
		}
		if midFor(id) != midFor(id) {
			t.Errorf("midFor(%q) not deterministic", id)
		}
	}
	if midFor("out_0001") == midFor("out_0002") {
		t.Fatal("two outlets got the same MID")
	}
}

func TestAddressLandsInTheRequestedCountry(t *testing.T) {
	a := addressFor("out_0007", "NL")
	if a.Country != "NL" || a.City != "Amsterdam" {
		t.Fatalf("address = %+v, want a Dutch one", a)
	}
	// An unknown country is not stored as junk -- one is generated.
	b := addressFor("out_0007", "ZZ")
	if normCountry(b.Country) == "" {
		t.Fatalf("country %q is not one the generator knows", b.Country)
	}
}

func TestGeneratedDataIsObviouslyFake(t *testing.T) {
	if !strings.HasPrefix(fakeAccountNumber("out_1"), "GB00SIM") {
		t.Error("account numbers must keep the lab's obviously-fake GB00SIM shape")
	}
	if !strings.HasPrefix(midFor("out_1"), "WL") {
		t.Error("a MID must not be mistakable for an IBAN")
	}
	// RFC 2606 reserves .test, so a generated address can never be mailed.
	if !strings.HasSuffix(emailFor("Quiet Coffee Ltd"), ".test") {
		t.Error("generated emails must be unroutable")
	}
	if emailFor("") == "" {
		t.Error("an unnamed merchant still needs an address")
	}
}

func TestCurrencyFollowsCountry(t *testing.T) {
	if currencyFor("GB") != "GBP" || currencyFor("DE") != "EUR" {
		t.Fatal("currency does not follow country")
	}
	if currencyFor("ZZ") != "EUR" {
		t.Fatal("an unknown country needs a usable fallback")
	}
}

func TestCountriesPicklistMatchesValidation(t *testing.T) {
	for _, c := range Countries() {
		if normCountry(c["code"]) == "" {
			t.Errorf("picklist offers %q, which normCountry rejects", c["code"])
		}
	}
}
