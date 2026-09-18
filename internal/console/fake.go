package console

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

// Fake identity data for merchants and outlets.
//
// Derived from the record's own id rather than drawn from a random source,
// for the same reason internal/b4b's LookupBeneficiary is: the same id must
// produce the same address every time, so a screenshot, a test fixture and
// a re-read of the registry all agree. Every value is obviously synthetic
// -- GB00SIM IBANs, .test domains, streets nobody can post to.

// countries the generator knows how to dress a merchant in. Currency is
// the one a merchant in that country would most likely settle in, which is
// what the settlement file is cut per.
var countries = []struct {
	Code, Name, Currency, City, PostFmt string
}{
	{"GB", "United Kingdom", "GBP", "London", "%s%d %d%s%s"},
	{"NL", "Netherlands", "EUR", "Amsterdam", "%d%d%d%d %s%s"},
	{"DE", "Germany", "EUR", "Berlin", "%d%d%d%d%d"},
	{"FR", "France", "EUR", "Lyon", "%d%d%d%d%d"},
	{"IE", "Ireland", "EUR", "Dublin", "D%d%d"},
	{"ES", "Spain", "EUR", "Madrid", "%d%d%d%d%d"},
	{"SE", "Sweden", "SEK", "Stockholm", "%d%d%d %d%d"},
	{"PL", "Poland", "PLN", "Warsaw", "%d%d-%d%d%d"},
}

var streets = []string{
	"Simulator Way", "Fixture Street", "Ledger Lane", "Sandbox Road",
	"Mockingbird Close", "Settlement Square", "Harness Hill", "Payout Parade",
}

// Countries is the picklist the create-merchant form offers.
func Countries() []map[string]string {
	out := make([]map[string]string, 0, len(countries))
	for _, c := range countries {
		out = append(out, map[string]string{"code": c.Code, "name": c.Name, "currency": c.Currency})
	}
	return out
}

// normCountry upper-cases and validates an ISO alpha-2 code against the
// list above, returning "" for anything unknown so the caller can fall
// back to a generated one rather than storing junk.
func normCountry(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, c := range countries {
		if c.Code == code {
			return code
		}
	}
	return ""
}

// seed turns an id into a stream of small numbers.
func seed(id string) func(mod int) int {
	sum := sha256.Sum256([]byte(id))
	i := 0
	return func(mod int) int {
		if mod <= 0 {
			return 0
		}
		// 4 bytes per draw, wrapping around the digest. A merchant needs a
		// handful of numbers, not a CSPRNG.
		off := (i * 4) % (len(sum) - 4)
		i++
		return int(binary.BigEndian.Uint32(sum[off:off+4]) % uint32(mod))
	}
}

func countryFor(id string) string {
	return countries[seed(id+":country")(len(countries))].Code
}

func currencyFor(code string) string {
	for _, c := range countries {
		if c.Code == code {
			return c.Currency
		}
	}
	return "EUR"
}

func cityFor(code string) string {
	for _, c := range countries {
		if c.Code == code {
			return c.City
		}
	}
	return "Simville"
}

// addressFor builds a postal address in the merchant's own country.
func addressFor(id, country string) Address {
	if normCountry(country) == "" {
		country = countryFor(id)
	}
	n := seed(id + ":address")
	return Address{
		Line1:    fmt.Sprintf("%d %s", 1+n(240), streets[n(len(streets))]),
		City:     cityFor(country),
		PostCode: postCodeFor(id, country),
		Country:  country,
	}
}

// postCodeFor is shaped-not-valid on purpose: it should read as the right
// country at a glance and fail any real validator, so nobody mistakes lab
// data for something postable.
func postCodeFor(id, country string) string {
	n := seed(id + ":post")
	switch country {
	case "GB":
		return fmt.Sprintf("SM%d %dSM", 1+n(19), n(10))
	case "NL":
		return fmt.Sprintf("%04d SM", 1000+n(8999))
	case "IE":
		return fmt.Sprintf("D%02d SIM%d", n(24), n(10))
	case "PL":
		return fmt.Sprintf("%02d-%03d", n(99), n(999))
	case "SE":
		return fmt.Sprintf("%03d %02d", 100+n(899), n(99))
	default:
		return fmt.Sprintf("%05d", 10000+n(89999))
	}
}

// midFor is the Worldline submerchant id. Prefixed WL and clearly not an
// IBAN: the MID and the account an outlet is paid into are different
// things, and conflating them is how a payout goes to the wrong place.
func midFor(id string) string {
	n := seed(id + ":mid")
	return fmt.Sprintf("WL%012d", n(999999999))
}

func terminalFor(id string) string {
	n := seed(id + ":terminal")
	return fmt.Sprintf("TERM%06d", n(999999))
}

// fakeAccountNumber matches cmd/bank's obviously-fake GB00SIM shape.
func fakeAccountNumber(id string) string {
	n := seed(id + ":account")
	return fmt.Sprintf("GB00SIM%013d", n(9999999999))
}

func sortCodeFor(id string) string {
	n := seed(id + ":fi")
	return fmt.Sprintf("SC%06d", n(999999))
}

func mccFor(id string) string {
	mccs := []string{"5411", "5812", "5691", "7011", "5999", "4121", "5732"}
	return mccs[seed(id+":mcc")(len(mccs))]
}

// emailFor derives a .test address -- reserved by RFC 2606, so it can
// never resolve and can never be mailed by accident.
func emailFor(name string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		}
		return -1
	}, name)
	if slug == "" {
		slug = "merchant"
	}
	if len(slug) > 24 {
		slug = slug[:24]
	}
	return "ops@" + slug + ".test"
}
