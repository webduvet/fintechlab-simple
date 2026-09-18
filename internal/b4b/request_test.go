package b4b

import (
	"testing"

	"github.com/webduvet/fintechlab-simple/internal/allowlist"
)

func TestValidateCallbackURLAllowed(t *testing.T) {
	list, err := allowlist.Parse("settlement,settlement:8083,localhost,127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCallbackURL(list, "http://settlement:8083/internal/b4b-webhook"); err != nil {
		t.Fatalf("expected allowed destination to pass, got %v", err)
	}
}

func TestValidateCallbackURLRejectsDisallowedHost(t *testing.T) {
	list, err := allowlist.Parse("settlement,settlement:8083,localhost,127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCallbackURL(list, "http://evil.example.com/steal"); err == nil {
		t.Fatal("expected disallowed destination to be rejected")
	}
}

func TestValidateCallbackURLRejectsEmpty(t *testing.T) {
	list, err := allowlist.Parse("settlement,settlement:8083,localhost,127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCallbackURL(list, ""); err == nil {
		t.Fatal("expected empty callback_url to be rejected")
	}
}
