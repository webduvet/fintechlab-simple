// Package console backs the lab's control panel: the catalogue of
// simulated services, their live health, the sim banks, and the merchant
// registry a demo needs before any of the money flow means anything.
//
// It holds no vendor contract of its own. Everything real-shaped lives in
// the vendor services (worldline, b4b, banking-circle, aci); the console
// only reads and drives them over exactly the same HTTP surface a human
// with curl would use. Nothing here is on the critical path of a scenario,
// which is the point: if the console were the only way to make something
// happen, that thing would not be testable headlessly.
package console

import (
	"os"
	"strings"
)

// Kind is which half of the lab a service belongs to. The distinction is
// the one docs/catalogue.md draws: a vendor simulation is the deliverable,
// scaffolding exists only so the harness can prove a hop is connected.
type Kind string

const (
	KindVendor     Kind = "vendor"
	KindPlatform   Kind = "platform"
	KindSupporting Kind = "supporting"
)

// Endpoint is one route worth showing a human. Not exhaustive -- the
// catalogue is a map, not a substitute for the OpenAPI the real vendors
// publish.
type Endpoint struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Note   string `json:"note,omitempty"`
}

// Service is one entry in the catalogue. BaseURL is where *this process*
// reaches it, which differs between a containerized console (compose
// service names) and one run from a shell (published 127.0.0.1 ports) --
// hence Browse, the URL that works from the operator's browser instead.
type Service struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Kind       Kind       `json:"kind"`
	Summary    string     `json:"summary"`
	BaseURL    string     `json:"base_url"`
	Browse     string     `json:"browse_url"`
	HealthPath string     `json:"health_path"`
	Ports      []string   `json:"ports"`
	Transport  string     `json:"transport"`
	Auth       string     `json:"auth"`
	SwapFor    string     `json:"swap_for"`
	Docs       string     `json:"docs,omitempty"`
	PodRecipe  string     `json:"pod_recipe,omitempty"` // recipes/<dir> composable twin, if any
	Endpoints  []Endpoint `json:"endpoints,omitempty"`
}

// Catalogue is the ordered list of services, vendors first.
type Catalogue struct {
	Services []Service `json:"services"`
}

// Get returns the service with the given id.
func (c *Catalogue) Get(id string) (Service, bool) {
	for _, s := range c.Services {
		if s.ID == id {
			return s, true
		}
	}
	return Service{}, false
}

// DefaultCatalogue describes the lab as compose runs it. Each BaseURL can
// be overridden with CONSOLE_URL_<ID>, upper-cased with dashes as
// underscores (CONSOLE_URL_BANKING_CIRCLE), which is what the compose file
// sets so the console reaches peers by service name.
//
// A single source of truth was tempting here -- generating this from
// compose.yml -- but compose is not always what is running (a service may
// be run from a shell, or pointed at a real vendor host), so the catalogue
// describes the *shape* and the environment says where it currently lives.
func DefaultCatalogue() *Catalogue {
	svcs := []Service{
		{
			ID: "worldline", Name: "Worldline", Kind: KindVendor,
			PodRecipe: "acquirer-edge",
			Summary:    "The acquirer. Holds acquired card transactions per submerchant (MID), cuts the daily Bambora settlement file in two slots, publishes it PGP-encrypted over real SSH/SFTP, and wires the lump sum to the safeguarding account.",
			BaseURL:    "http://127.0.0.1:8084",
			HealthPath: "/health",
			Ports:      []string{"8084/http", "2222/ssh"},
			Transport:  "HTTP + real SSH/SFTP with PGP",
			Auth:       "SFTP password or public key; optional host-key pinning",
			SwapFor:    "Point WORLDLINE_SFTP_HOST and the credentials at real Worldline.",
			Docs:       "docs/ARCHITECTURE-worldline-acquirer.md",
			Endpoints: []Endpoint{
				{"POST", "/sim/transactions", "seed acquired card transactions"},
				{"GET", "/sim/transactions", "what the acquirer holds"},
				{"POST", "/sim/settlement-cycle/run?slot=morning|afternoon", "cut and publish a file now"},
				{"GET", "/sim/settlement-cycle/runs", "cycle history"},
				{"GET", "/config", "SFT file-exchange channel config"},
			},
		},
		{
			ID: "b4b", Name: "B4B Payments", Kind: KindVendor,
			PodRecipe: "b4b-oversight",
			Summary:    "Oversight API, both halves: boarding a company (people, documents, the create-once extended profile, vIBANs) and paying it (beneficiary registration with pass/review/fail sanctions, the gates, the payout lifecycle and its callbacks, and the bridge into Banking Circle once approved).",
			BaseURL:    "http://127.0.0.1:8086",
			HealthPath: "/health",
			Ports:      []string{"8086/http"},
			Transport:  "HTTP",
			Auth:       "Bearer RS512 JWT (aud b4b-payments)",
			SwapFor:    "Point B4B_URL at the real Oversight API.",
			Docs:       "docs/ARCHITECTURE-b4b-oversight.md",
			Endpoints: []Endpoint{
				{"POST", "/oversight/v1/companies", "board a company (422 on a repeated external_ref)"},
				{"GET", "/oversight/v1/companies/{id}", "by id or external_ref"},
				{"POST", "/oversight/v1/companies/{id}/addresses", ""},
				{"POST", "/oversight/v1/companies/{id}/people", "natural persons and legal entities"},
				{"POST", "/oversight/v1/uploads", "documents: accepted, measured, discarded"},
				{"POST", "/oversight/v1/companies/{id}/extended", "the regulatory profile -- once, ever"},
				{"GET", "/oversight/v1/companies/{id}/extended", "read this before re-posting one"},
				{"POST", "/oversight/v1/companies/{id}/vibans", "stable per currency"},
				{"POST", "/oversight/v1/beneficiaries", "register a payee"},
				{"GET", "/oversight/v1/beneficiaries/{id}", ""},
				{"POST", "/oversight/v1/payments", "pay out (all gates apply)"},
				{"GET", "/oversight/v1/payments/{id}", "recovery read for a missed callback"},
				{"PUT", "/sim/beneficiaries/{id}/sanctions", "lab-only: move a sanctions status"},
				{"PUT", "/sim/beneficiaries/{id}/status", "lab-only: active/disabled"},
				{"PUT", "/sim/people/{id}/sanctions", "lab-only: sanctions and PEP move separately"},
				{"PUT", "/sim/companies/{id}/sanctions", "lab-only"},
			},
		},
		{
			ID: "banking-circle", Name: "Banking Circle", Kind: KindVendor,
			PodRecipe: "bank-rails",
			Summary:    "Connect API: the safeguarding-account ledger, the full notification self-service surface (subscriptions, If-Match, per-subscription keys, batching, event targets) and the eleven-step retry schedule ending in auto-deactivation.",
			BaseURL:    "https://127.0.0.1:8085",
			HealthPath: "/health",
			Ports:      []string{"8085/https+mtls", "8095/http (lab bridge)"},
			Transport:  "HTTPS, mTLS per BC_MTLS",
			Auth:       "Basic -> Bearer exchange",
			SwapFor:    "Point at the real Connect API.",
			Docs:       "docs/ARCHITECTURE-banking-circle-webhooks.md",
			Endpoints: []Endpoint{
				{"GET", "/api/v1/authorizations/authorize", "Basic -> Bearer"},
				{"GET", "/api/v1/accounts/{id}/balances", ""},
				{"GET|POST", "/api/v1/notificationselfservice/subscription", ""},
				{"POST", "/api/v1/notificationselfservice/clienttest/{id}", "flush a test notification"},
				{"GET", "/sim/emails", "warning + deactivation emails"},
				{"GET", "/sim/delivery-config", "the retry schedule it is actually running"},
			},
		},
		{
			ID: "aci", Name: "ACI", Kind: KindVendor,
			PodRecipe: "gateway-facade",
			Summary:    "Online card-payment gateway. A sender, not a callee: it emits an AES-256-GCM encrypted webhook that stands in for \"a card payment just happened\".",
			BaseURL:    "http://127.0.0.1:8087",
			HealthPath: "/health",
			Ports:      []string{"8087/http"},
			Transport:  "HTTP out (webhook)",
			Auth:       "shared webhook secret (hex, 32 raw bytes)",
			SwapFor:    "It is the sender -- point ACI_WEBHOOK_TARGET_URL at your own listener.",
			Endpoints: []Endpoint{
				{"POST", "/internal/simulate-payment", "emit a card-payment webhook"},
				{"GET", "/internal/sent", "what it delivered"},
			},
		},
		{
			ID: "verify", Name: "Merchant verification", Kind: KindSupporting,
			Summary:    "Stub verification/compliance service gating a payout. Always approves unless an id is force-declined.",
			BaseURL:    "http://127.0.0.1:8088",
			HealthPath: "/api/v1/verification/health",
			Ports:      []string{"8088/http"},
			Transport:  "HTTP",
			Auth:       "x-internal-api-key",
			SwapFor:    "A real verification vendor.",
			Endpoints: []Endpoint{
				{"POST", "/api/v1/verification/verify/aml-decision", "trigger"},
				{"GET", "/api/v1/verification/verify/data/verify-decision/{id}", "read the decision"},
			},
		},
		{
			ID: "settlement", Name: "Settlement (platform stand-in)", Kind: KindPlatform,
			Summary:    "Scaffolding for your platform: pulls Worldline's file over real SFTP+PGP, decrypts, parses, splits it per MID and pays each outlet through B4B.",
			BaseURL:    "http://127.0.0.1:8083",
			HealthPath: "/health",
			Ports:      []string{"8083/http"},
			Transport:  "HTTP",
			Auth:       "none (lab)",
			SwapFor:    "Replace it with your own platform.",
			Endpoints: []Endpoint{
				{"GET", "/worldline/files", "what it has taken delivery of"},
				{"POST", "/worldline/pull", "pull now"},
				{"GET", "/reports/settlement", ""},
			},
		},
		{
			ID: "receiver", Name: "Webhook receiver (platform stand-in)", Kind: KindPlatform,
			Summary:    "Stand-in for your own listener: verifies the HMAC scheme, and captures raw bodies for wire formats it holds no key for.",
			BaseURL:    "https://127.0.0.1:8443",
			HealthPath: "/health",
			Ports:      []string{"8443/https"},
			Transport:  "HTTPS",
			Auth:       "HMAC on /webhooks; none on /raw-events",
			SwapFor:    "Point the vendors' webhook URLs at your own listener.",
			Endpoints: []Endpoint{
				{"GET", "/events", "verified webhooks"},
				{"GET", "/raw-events", "raw captures (base64 bodies)"},
			},
		},
		{
			ID: "payment-api", Name: "Payment API", Kind: KindPlatform,
			Summary: "Generic payment facade with Idempotency-Key. Scaffolding, not vendor-shaped.",
			BaseURL: "http://127.0.0.1:8080", HealthPath: "/health",
			Ports: []string{"8080/http"}, Transport: "HTTP", Auth: "none (lab)",
			SwapFor: "-- generic shape, not a vendor.",
			Endpoints: []Endpoint{
				{"POST", "/payments", ""}, {"GET", "/payments", ""},
			},
		},
		{
			ID: "bank", Name: "Core ledger", Kind: KindPlatform,
			Summary: "In-memory ledger with obviously fake IBANs. The sim bank behind the Banks view.",
			BaseURL: "http://127.0.0.1:8081", HealthPath: "/health",
			Ports: []string{"8081/http"}, Transport: "HTTP", Auth: "none (lab)",
			SwapFor: "-- generic shape, not a vendor.",
			Endpoints: []Endpoint{
				{"GET", "/accounts", ""}, {"POST", "/accounts", ""},
				{"POST", "/transfers", ""}, {"GET", "/ledger", ""},
			},
		},
		{
			ID: "notifier", Name: "Notifier", Kind: KindPlatform,
			Summary: "Signed webhook worker with retries and an allowlist.",
			BaseURL: "http://127.0.0.1:8082", HealthPath: "/health",
			Ports: []string{"8082/http"}, Transport: "HTTP", Auth: "HMAC out",
			SwapFor: "-- generic shape, not a vendor.",
			Endpoints: []Endpoint{
				{"GET", "/subscriptions", ""}, {"GET", "/deliveries", ""},
			},
		},
	}
	for i := range svcs {
		if v := os.Getenv(envKeyFor(svcs[i].ID)); v != "" {
			svcs[i].BaseURL = v
		}
		svcs[i].Browse = browseURL(svcs[i])
	}
	return &Catalogue{Services: svcs}
}

// envKeyFor is the per-service base-URL override, e.g. banking-circle ->
// CONSOLE_URL_BANKING_CIRCLE.
func envKeyFor(id string) string {
	return "CONSOLE_URL_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_"))
}

// browseURL is the link handed to the operator's browser. Inside compose
// the console reaches peers as http://bank:8081, which means nothing on
// the host, so the published port is reconstructed from the catalogue's
// own port list. CONSOLE_BROWSE_HOST names the host those ports are
// published on (127.0.0.1 unless the lab runs somewhere else).
func browseURL(s Service) string {
	host := os.Getenv("CONSOLE_BROWSE_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	scheme := "http"
	port := ""
	for _, p := range s.Ports {
		name, kind, _ := strings.Cut(p, "/")
		if strings.HasPrefix(kind, "http") {
			port = name
			if strings.HasPrefix(kind, "https") {
				scheme = "https"
			}
			break
		}
	}
	if port == "" {
		return ""
	}
	return scheme + "://" + host + ":" + port
}
