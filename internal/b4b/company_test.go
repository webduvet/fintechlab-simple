package b4b

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/runnerclock"
)

func gbCompany() CompanyParams {
	return CompanyParams{
		ExternalRef: "mer_0001",
		LegalName:   "Harness Merchant Ltd",
		TradingName: "Harness",
		CompanyType: "kyb",
		Address: &Address{
			AddressLine1: "1 Simulation Way",
			City:         "London",
			PostalCode:   "EC1A 1AA",
			Country:      "GB",
		},
	}
}

func TestCreateCompanyRequiresLegalNameAndAddress(t *testing.T) {
	d := NewDirectory(nil)
	cases := map[string]CompanyParams{
		"no legal name": {Address: gbCompany().Address},
		"no address":    {LegalName: "Acme Ltd"},
		"address with no country": {LegalName: "Acme Ltd", Address: &Address{
			AddressLine1: "1 Simulation Way", City: "London", PostalCode: "EC1A 1AA",
		}},
		"three-letter country": {LegalName: "Acme Ltd", Address: &Address{
			AddressLine1: "1 Simulation Way", City: "London", PostalCode: "EC1A 1AA", Country: "GBR",
		}},
		"five-digit mcc": func() CompanyParams {
			p := gbCompany()
			p.MerchantCategoryCode = "59999"
			return p
		}(),
	}
	for name, p := range cases {
		if _, err := d.CreateCompany(p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

// A repeated external_ref is the signal a reconciler reads as "I already
// sent this". Creating a second company under it would make that signal
// unavailable and leave two companies nobody can choose between.
func TestCreateCompanyRefusesDuplicateExternalRef(t *testing.T) {
	d := NewDirectory(nil)
	first, err := d.CreateCompany(gbCompany())
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = d.CreateCompany(gbCompany())
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second create err = %v, want ErrDuplicate", err)
	}
	if !strings.Contains(err.Error(), first.ID) {
		t.Errorf("duplicate error should name the existing company %s, got %q", first.ID, err)
	}
	if got := d.Companies(); len(got) != 1 {
		t.Errorf("companies = %d, want 1", len(got))
	}
}

// The registered office arrives inline on the create. A client that then
// reads the addresses back and finds none would reasonably conclude it had
// to post it again.
func TestCreateCompanyMaterializesRegisteredOffice(t *testing.T) {
	d := NewDirectory(nil)
	c, err := d.CreateCompany(gbCompany())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	addrs, err := d.Addresses(c.ID)
	if err != nil {
		t.Fatalf("addresses: %v", err)
	}
	if len(addrs) != 1 || addrs[0].Type != "registered" || addrs[0].City != "London" {
		t.Fatalf("addresses = %+v, want one registered London address", addrs)
	}
}

// Resolving by the caller's own reference matters because that is the only
// identifier a client has before it has stored the one B4B minted.
func TestCompanyResolvesByExternalRef(t *testing.T) {
	d := NewDirectory(nil)
	c, err := d.CreateCompany(gbCompany())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	byRef, err := d.Company("mer_0001")
	if err != nil {
		t.Fatalf("by external_ref: %v", err)
	}
	if byRef.ID != c.ID {
		t.Fatalf("by external_ref = %s, want %s", byRef.ID, c.ID)
	}
	if _, err := d.Company("cmp_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown company err = %v, want ErrNotFound", err)
	}
}

// This is the non-GB boarding failure, reproduced: the same payload that
// boards a GB merchant with the waiver set is refused without it.
func TestPersonIdentityDocumentRequiredUnlessWaived(t *testing.T) {
	person := PersonParams{FirstName: "Ada", LastName: "Lovelace", DateOfBirth: "1815-12-10"}

	d := NewDirectory(nil)
	strict, err := d.CreateCompany(gbCompany())
	if err != nil {
		t.Fatalf("create strict company: %v", err)
	}
	_, err = d.AddPerson(strict.ID, person)
	if !errors.Is(err, ErrRequirementUnmet) {
		t.Fatalf("person with no identity document: err = %v, want ErrRequirementUnmet", err)
	}
	for _, field := range []string{"document_type", "document_number", "document_country"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("error should name the missing field %s, got %q", field, err)
		}
	}

	waivedParams := gbCompany()
	waivedParams.ExternalRef = "mer_0002"
	waivedParams.PersonDocumentsWaived = true
	waived, err := d.CreateCompany(waivedParams)
	if err != nil {
		t.Fatalf("create waived company: %v", err)
	}
	if _, err := d.AddPerson(waived.ID, person); err != nil {
		t.Fatalf("same person against a waived company: %v", err)
	}

	documented := person
	documented.DocumentType, documented.DocumentNumber, documented.DocumentCountry = "passport", "P123456", "IE"
	if _, err := d.AddPerson(strict.ID, documented); err != nil {
		t.Fatalf("person with an identity document: %v", err)
	}
}

func TestPersonLegalEntityNeedsLegalNameNotDocuments(t *testing.T) {
	d := NewDirectory(nil)
	c, err := d.CreateCompany(gbCompany())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := d.AddPerson(c.ID, PersonParams{EntityType: EntityLegalEntity}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("legal entity with no legal_name: err = %v, want ErrInvalid", err)
	}
	entity, err := d.AddPerson(c.ID, PersonParams{EntityType: EntityLegalEntity, LegalName: "Holdco Ltd"})
	if err != nil {
		t.Fatalf("legal entity: %v", err)
	}
	if entity.SanctionsStatus != SanctionsPass || entity.PepSanctionsStatus != SanctionsPass {
		t.Fatalf("a new entity should screen clean, got %+v", entity)
	}
	if _, err := d.AddPerson(c.ID, PersonParams{EntityType: "robot", LegalName: "X"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown entity_type: err = %v, want ErrInvalid", err)
	}
}

// The two screening statuses move independently, because a PEP flag with no
// sanctions hit is exactly the case a client reading only the first gets
// wrong.
func TestSetPersonSanctionsMovesEachStatusIndependently(t *testing.T) {
	d := NewDirectory(nil)
	p := gbCompany()
	p.PersonDocumentsWaived = true
	c, _ := d.CreateCompany(p)
	person, err := d.AddPerson(c.ID, PersonParams{FirstName: "Ada", LastName: "Lovelace", DateOfBirth: "1815-12-10"})
	if err != nil {
		t.Fatalf("add person: %v", err)
	}
	got, changed, err := d.SetPersonSanctions(person.ID, "", SanctionsReview)
	if err != nil || !changed {
		t.Fatalf("set pep only: err=%v changed=%t", err, changed)
	}
	if got.PepSanctionsStatus != SanctionsReview || got.SanctionsStatus != SanctionsPass {
		t.Fatalf("pep-only move changed the wrong field: %+v", got)
	}
	if _, _, err := d.SetPersonSanctions(person.ID, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty move: err = %v, want ErrInvalid", err)
	}
	if _, _, err := d.SetPersonSanctions(person.ID, "CLEAR", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uppercase status: err = %v, want ErrInvalid", err)
	}
}

// Create-once, with no update endpoint. A boarding chain that re-runs after
// a later step failed hits this, and a client that treats it as fatal never
// finishes boarding anybody.
func TestExtendedProfileCanOnlyBeCreatedOnce(t *testing.T) {
	d := NewDirectory(nil)
	p := gbCompany()
	p.CompanyDocumentsWaived = true
	c, _ := d.CreateCompany(p)

	first, err := d.CreateExtended(c.ID, ExtendedParams{RegisteredCompanyNo: "12345678"})
	if err != nil {
		t.Fatalf("first extended: %v", err)
	}
	_, err = d.CreateExtended(c.ID, ExtendedParams{RegisteredCompanyNo: "12345678"})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second extended: err = %v, want ErrDuplicate", err)
	}
	if !strings.Contains(err.Error(), first.ID) {
		t.Errorf("the refusal should name the profile that exists (%s), so a client can read it: %q", first.ID, err)
	}
	got, err := d.Extended(c.ID)
	if err != nil || got.ID != first.ID {
		t.Fatalf("read back = %+v, %v; want %s", got, err, first.ID)
	}
}

func TestExtendedProfileDocumentRules(t *testing.T) {
	d := NewDirectory(nil)
	c, _ := d.CreateCompany(gbCompany()) // no waiver

	if _, err := d.CreateExtended(c.ID, ExtendedParams{RegisteredCompanyNo: "12345678"}); !errors.Is(err, ErrRequirementUnmet) {
		t.Fatalf("unwaived profile with no documents: err = %v, want ErrRequirementUnmet", err)
	}
	if _, err := d.CreateExtended(c.ID, ExtendedParams{Documents: []string{"doc_x"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("profile with no registered_company_no: err = %v, want ErrInvalid", err)
	}
	// A document id nobody uploaded is a caller error, not a silent
	// dangling reference: the two-step upload flow only means anything if
	// the second step checks the first happened.
	_, err := d.CreateExtended(c.ID, ExtendedParams{RegisteredCompanyNo: "12345678", Documents: []string{"doc_never"}})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "doc_never") {
		t.Fatalf("unknown document id: err = %v, want ErrInvalid naming doc_never", err)
	}

	doc := d.AddDocument(DocumentParams{DocumentType: "business_registry_extract", FileName: "extract.pdf", Size: 12})
	if _, err := d.CreateExtended(c.ID, ExtendedParams{RegisteredCompanyNo: "12345678", Documents: []string{doc.ID}}); err != nil {
		t.Fatalf("profile with an uploaded document: %v", err)
	}
}

// Provisioning, not an append-only log: a retry must not leave a company
// with two accounts in the same currency.
func TestVibanIsStablePerCurrency(t *testing.T) {
	d := NewDirectory(nil)
	c, _ := d.CreateCompany(gbCompany())

	gbp, err := d.AddViban(c.ID, "gbp")
	if err != nil {
		t.Fatalf("add viban: %v", err)
	}
	if gbp.Currency != "GBP" {
		t.Errorf("currency = %q, want GBP (normalized)", gbp.Currency)
	}
	if !strings.HasPrefix(gbp.IBAN, "GB00SIM") {
		t.Errorf("iban %q must be obviously fake (GB00SIM...)", gbp.IBAN)
	}
	again, _ := d.AddViban(c.ID, "GBP")
	if again.ID != gbp.ID || again.IBAN != gbp.IBAN {
		t.Fatalf("re-registering GBP minted a second account: %s vs %s", again.ID, gbp.ID)
	}
	eur, _ := d.AddViban(c.ID, "EUR")
	if eur.IBAN == gbp.IBAN {
		t.Fatalf("EUR and GBP share an account number %q", eur.IBAN)
	}
	if _, err := d.AddViban(c.ID, "POUNDS"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad currency: err = %v, want ErrInvalid", err)
	}
	list, _ := d.Vibans(c.ID)
	if len(list) != 2 {
		t.Fatalf("vibans = %d, want 2", len(list))
	}
}

func TestDirectoryOnChangeFiresForEveryMutation(t *testing.T) {
	d := NewDirectory(nil)
	changes := 0
	d.OnChange = func() { changes++ }

	c, err := d.CreateCompany(gbCompany())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := d.AddAddress(c.ID, AddressParams{Type: "trading", Address: *gbCompany().Address}); err != nil {
		t.Fatalf("add address: %v", err)
	}
	d.AddDocument(DocumentParams{FileName: "x.pdf"})
	if _, err := d.AddViban(c.ID, "EUR"); err != nil {
		t.Fatalf("add viban: %v", err)
	}
	if changes != 4 {
		t.Fatalf("OnChange fired %d times, want 4 (company, address, document, viban)", changes)
	}
	// A refused create must not fire it: persisting after a no-op write is
	// how a state file grows entries nothing asked for.
	before := changes
	if _, err := d.CreateCompany(gbCompany()); err == nil {
		t.Fatal("expected the duplicate create to fail")
	}
	if changes != before {
		t.Fatalf("OnChange fired on a refused create")
	}
}

func TestUnknownCompanyIsNotFoundEverywhere(t *testing.T) {
	d := NewDirectory(nil)
	if _, err := d.AddAddress("cmp_nope", AddressParams{Type: "trading", Address: *gbCompany().Address}); !errors.Is(err, ErrNotFound) {
		t.Errorf("AddAddress err = %v, want ErrNotFound", err)
	}
	if _, err := d.AddPerson("cmp_nope", PersonParams{FirstName: "A", LastName: "B", DateOfBirth: "2000-01-01"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("AddPerson err = %v, want ErrNotFound", err)
	}
	if _, err := d.CreateExtended("cmp_nope", ExtendedParams{RegisteredCompanyNo: "1"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("CreateExtended err = %v, want ErrNotFound", err)
	}
	if _, err := d.AddViban("cmp_nope", "EUR"); !errors.Is(err, ErrNotFound) {
		t.Errorf("AddViban err = %v, want ErrNotFound", err)
	}
	if _, err := d.Person("per_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Person err = %v, want ErrNotFound", err)
	}
	if _, err := d.Document("doc_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Document err = %v, want ErrNotFound", err)
	}
}

// TestRecordsAreStampedOnTheRunnersClock: with the platform's clock moved,
// a company is created on the platform's day, not the wall clock's.
func TestRecordsAreStampedOnTheRunnersClock(t *testing.T) {
	runnerclock.Set(72 * time.Hour)
	defer runnerclock.Set(0)
	c, err := NewDirectory(nil).CreateCompany(gbCompany())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.CreatedAt[:10], runnerclock.Now().Format("2006-01-02"); got != want {
		t.Errorf("createdAt %s, want the runner's day %s", c.CreatedAt, want)
	}
}
