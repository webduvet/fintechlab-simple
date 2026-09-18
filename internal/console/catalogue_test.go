package console

import (
	"strings"
	"testing"
)

func TestCatalogueCoversTheVendorsAndLabelsTheSides(t *testing.T) {
	c := DefaultCatalogue()
	// The vendor/scaffolding line is the one docs/catalogue.md draws, and
	// the console is where most people will meet it.
	for _, id := range []string{"worldline", "b4b", "banking-circle", "aci"} {
		s, ok := c.Get(id)
		if !ok {
			t.Fatalf("%s missing from the catalogue", id)
		}
		if s.Kind != KindVendor {
			t.Errorf("%s kind = %q, want vendor", id, s.Kind)
		}
		if s.SwapFor == "" {
			t.Errorf("%s does not say how to point it at the real thing", id)
		}
	}
	for _, id := range []string{"settlement", "receiver", "bank"} {
		s, _ := c.Get(id)
		if s.Kind != KindPlatform {
			t.Errorf("%s kind = %q, want platform", id, s.Kind)
		}
	}
	if _, ok := c.Get("nope"); ok {
		t.Fatal("Get invented a service")
	}
}

func TestBaseURLIsOverridableAndBrowseURLStaysOnTheHost(t *testing.T) {
	t.Setenv("CONSOLE_URL_BANKING_CIRCLE", "https://banking-circle:8085")
	t.Setenv("CONSOLE_BROWSE_HOST", "127.0.0.1")
	s, _ := DefaultCatalogue().Get("banking-circle")
	if s.BaseURL != "https://banking-circle:8085" {
		t.Fatalf("base_url = %q, override ignored", s.BaseURL)
	}
	// Inside compose the console reaches peers by service name, which
	// means nothing in the operator's browser -- the link has to be the
	// published port instead.
	if s.Browse != "https://127.0.0.1:8085" {
		t.Fatalf("browse_url = %q, want the published host port", s.Browse)
	}
}

func TestBrowseURLIsEmptyForAServiceWithNoHTTPPort(t *testing.T) {
	if got := browseURL(Service{Ports: []string{"2222/ssh"}}); got != "" {
		t.Fatalf("browse_url = %q for an SSH-only service, want empty", got)
	}
}

func TestEnvKeyShape(t *testing.T) {
	if got := envKeyFor("banking-circle"); got != "CONSOLE_URL_BANKING_CIRCLE" {
		t.Fatalf("envKeyFor = %q", got)
	}
	for _, s := range DefaultCatalogue().Services {
		if strings.Contains(envKeyFor(s.ID), "-") {
			t.Errorf("%s yields an env key with a dash in it", s.ID)
		}
	}
}
