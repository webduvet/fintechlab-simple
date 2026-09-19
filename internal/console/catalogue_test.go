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

// TestLocalRunnerCardIsDroppedWhenSwitchedOff. The lab has to stand alone:
// most of the time there is no platform runner, and a permanently red row
// for something nobody started teaches an operator to ignore red.
func TestLocalRunnerCardIsDroppedWhenSwitchedOff(t *testing.T) {
	if _, ok := DefaultCatalogue().Get("local-runner"); !ok {
		t.Fatal("the runner card should be present by default")
	}
	t.Setenv("CONSOLE_LOCAL_RUNNER", "off")
	cat := DefaultCatalogue()
	if _, ok := cat.Get("local-runner"); ok {
		t.Error("CONSOLE_LOCAL_RUNNER=off must drop the card")
	}
	// And drop only that one.
	for _, id := range []string{"worldline", "b4b", "banking-circle"} {
		if _, ok := cat.Get(id); !ok {
			t.Errorf("%s went missing with it", id)
		}
	}
}

// TestLocalRunnerIsReachableByEnv, because it runs on the host while the
// console runs in a container, where "127.0.0.1" means the console itself.
func TestLocalRunnerIsReachableByEnv(t *testing.T) {
	t.Setenv("CONSOLE_URL_LOCAL_RUNNER", "http://host.containers.internal:3109")
	s, ok := DefaultCatalogue().Get("local-runner")
	if !ok {
		t.Fatal("no runner card")
	}
	if s.BaseURL != "http://host.containers.internal:3109" {
		t.Errorf("BaseURL = %q, want the override", s.BaseURL)
	}
	if s.Activity == "" {
		t.Error("the runner keeps a log of its runs; the card must declare it")
	}
}
