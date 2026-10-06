package console

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Batches: many merchants in one call.
//
// One merchant at a time is right for a demo and wrong for a load: a
// settlement file paying three hundred outlets is a different test from one
// paying five. A batch is a set of merchants made together — names,
// countries, trades and outlet counts drawn from a seed — so the lab can
// have a realistic-looking population in one click, and lose it again in
// another.
//
// "Randomised" here is the design system's kind (contract 8): derived, not
// random. The batch's seed decides every name, country and outlet count, and
// the answer reports it, so the same seed makes the same population again —
// with new ids, and so new MIDs, because those derive from the ids. With no
// seed given, the seed is derived from the batch's own id.

// Batch is one set of merchants made together.
type Batch struct {
	ID         string    `json:"id"`
	Seed       int64     `json:"seed"`
	Count      int       `json:"count"`
	MaxOutlets int       `json:"max_outlets"`
	Country    string    `json:"country,omitempty"` // "" is a mix
	CreatedAt  time.Time `json:"created_at"`
}

// BatchParams is what the batch form collects.
type BatchParams struct {
	// Count is how many merchants, 1 to MaxBatch.
	Count int `json:"count"`
	// MaxOutlets caps each merchant's outlets, 1 to 10; most get one.
	MaxOutlets int `json:"max_outlets"`
	// Country puts every merchant in one country; empty mixes them.
	Country string `json:"country"`
	// Seed makes the same population again; nil derives one.
	Seed      *int64 `json:"seed"`
	PartnerID string `json:"partner_id"`
}

// MaxBatch is the most merchants one batch makes: enough for a settlement
// file worth the name, few enough that the Merchants view still draws.
const MaxBatch = 500

var (
	batchStems = []string{
		"Copperleaf", "Northgate", "Bramble", "Saltmarsh", "Kestrel", "Juniper", "Harbour", "Lantern",
		"Meadow", "Oakridge", "Pebble", "Quayside", "Rowan", "Silverbirch", "Thistle", "Underwood",
		"Vale", "Willow", "Yarrow", "Ashby", "Beacon", "Cobble", "Driftwood", "Elmstead",
		"Foxglove", "Granite", "Hazel", "Ironbridge", "Larch", "Millbrook", "Nettle", "Orchard",
	}
	// batchTrades pairs a shopfront word with the MCC that trade files under.
	batchTrades = []struct{ Word, MCC string }{
		{"Bakery", "5462"}, {"Coffee", "5814"}, {"Kitchen", "5812"}, {"Grocers", "5411"},
		{"Outfitters", "5691"}, {"Books", "5942"}, {"Cycles", "5940"}, {"Florist", "5992"},
		{"Hardware", "5251"}, {"Electronics", "5732"}, {"Guesthouse", "7011"}, {"Cabs", "4121"},
		{"Pharmacy", "5912"}, {"Barbers", "7230"}, {"Gallery", "5971"}, {"Toys", "5945"},
	}
	// legalSuffix is the company form a merchant in that country files as.
	legalSuffix = map[string]string{
		"GB": "Ltd", "IE": "Ltd", "NL": "B.V.", "DE": "GmbH", "FR": "SAS", "ES": "S.L.", "SE": "AB", "PL": "Sp. z o.o.",
	}
)

// batchDraw is the stream of numbers merchant i of a batch is made from.
func batchDraw(seedValue int64, i int) func(int) int {
	return seed(fmt.Sprintf("batch:%d:%d", seedValue, i))
}

// seedFor derives a seed from an id, for a batch asked for without one.
func seedFor(id string) int64 {
	sum := sha256.Sum256([]byte("batch-seed:" + id))
	return int64(binary.BigEndian.Uint32(sum[:4]) % 1_000_000)
}

// outletCountFor: most merchants trade from one place, some from two, a
// few from more — capped at max.
func outletCountFor(n func(int) int, max int) int {
	k := 1
	switch r := n(100); {
	case r >= 92:
		k = 3 + n(8) // 3..10
	case r >= 65:
		k = 2
	}
	if k > max {
		k = max
	}
	return k
}

// AddBatch makes a batch of merchants and their outlets, saved once.
func (r *Registry) AddBatch(p BatchParams) (*Batch, []Merchant, error) {
	if p.Count < 1 || p.Count > MaxBatch {
		return nil, nil, fmt.Errorf("%w: count must be between 1 and %d", ErrInvalid, MaxBatch)
	}
	if p.MaxOutlets == 0 {
		p.MaxOutlets = 3
	}
	if p.MaxOutlets < 1 || p.MaxOutlets > 10 {
		return nil, nil, fmt.Errorf("%w: max_outlets must be between 1 and 10", ErrInvalid)
	}
	// A mix draws from the countries the lab settles end to end: Banking
	// Circle holds safeguarding accounts in EUR and GBP only, so a PLN or
	// SEK merchant in a mix would be a payout that cannot leave. Asked for
	// by name, any country is made.
	var settling []string
	for _, c := range countries {
		if c.Currency == "EUR" || c.Currency == "GBP" {
			settling = append(settling, c.Code)
		}
	}
	country := normCountry(p.Country)
	if strings.TrimSpace(p.Country) != "" && country == "" {
		return nil, nil, fmt.Errorf("%w: country %q is not one the lab dresses merchants in", ErrInvalid, p.Country)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.PartnerID != "" {
		if _, ok := r.partners[p.PartnerID]; !ok {
			return nil, nil, fmt.Errorf("%w: partner %s", ErrNotFound, p.PartnerID)
		}
	}
	b := &Batch{ID: r.next("bat"), Count: p.Count, MaxOutlets: p.MaxOutlets, Country: country, CreatedAt: time.Now().UTC()}
	if p.Seed != nil {
		b.Seed = *p.Seed
	} else {
		b.Seed = seedFor(b.ID)
	}

	taken := map[string]bool{}
	for _, m := range r.merchants {
		taken[strings.ToLower(displayName(m))] = true
	}
	made := make([]Merchant, 0, p.Count)
	for i := 0; i < p.Count; i++ {
		n := batchDraw(b.Seed, i)
		trade := batchTrades[n(len(batchTrades))]
		name := batchStems[n(len(batchStems))] + " " + trade.Word
		// Unique across the registry, so the view never shows two
		// merchants nobody can tell apart.
		for k := 2; taken[strings.ToLower(name)]; k++ {
			name = fmt.Sprintf("%s %s %d", batchStems[n(len(batchStems))], trade.Word, k)
		}
		taken[strings.ToLower(name)] = true
		c := country
		if c == "" {
			c = settling[n(len(settling))]
		}
		id := r.next("mer")
		m := &Merchant{
			ID:          id,
			PartnerID:   p.PartnerID,
			Batch:       b.ID,
			LegalName:   name + " " + legalSuffix[c],
			TradingName: name,
			Country:     c,
			Currency:    currencyFor(c),
			MCC:         trade.MCC,
			Email:       emailFor(name),
			Status:      "active",
			CreatedAt:   b.CreatedAt,
			Address:     addressFor(id, c),
		}
		for k := outletCountFor(n, p.MaxOutlets); k > 0; k-- {
			m.Outlets = append(m.Outlets, r.newOutletLocked(m, ""))
		}
		r.merchants[m.ID] = m
		made = append(made, *m)
	}
	r.batches[b.ID] = b
	return b, made, r.save()
}

// DeleteBatch removes a batch and every merchant in it, and says how many.
// Like DeleteMerchant, nothing already registered at a vendor is unwound.
func (r *Registry) DeleteBatch(id string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.batches[id]; !ok {
		return 0, fmt.Errorf("%w: batch %s", ErrNotFound, id)
	}
	n := 0
	for mid, m := range r.merchants {
		if m.Batch == id {
			delete(r.merchants, mid)
			n++
		}
	}
	delete(r.batches, id)
	return n, r.save()
}

// Batches returns every batch, oldest first.
func (r *Registry) Batches() []Batch {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Batch, 0, len(r.batches))
	for _, b := range r.batches {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// BatchMerchantIDs lists a batch's merchants, id-ordered.
func (r *Registry) BatchMerchantIDs(id string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.batches[id]; !ok {
		return nil, fmt.Errorf("%w: batch %s", ErrNotFound, id)
	}
	var out []string
	for _, m := range r.merchants {
		if m.Batch == id {
			out = append(out, m.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}

// TradeAmountFor is a merchant's typical card payment, in cents, derived
// from its id — so a batch's trading is not every outlet taking the same
// 25.00 all day.
func TradeAmountFor(merchantID string) int64 {
	return int64(300 + seed(merchantID+":ticket")(14700)) // 3.00 .. 150.00
}
