package main

import (
	"regexp"
	"testing"
)

// The platform's payment-callback schema declares the id a uuid and answers
// 422 to anything else, so every callback for a payment whose id is not one is
// undeliverable — and the payout sits at SUBMITTED forever with no error
// anywhere near the settlement code. The format is contract, not cosmetics.
var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestPaymentIDIsUUIDv4(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := paymentID()
		if !uuidV4.MatchString(id) {
			t.Fatalf("paymentID() = %q, which a client validating a uuid would refuse", id)
		}
		if seen[id] {
			t.Fatalf("paymentID() repeated %q; two payments sharing an id cross their callbacks", id)
		}
		seen[id] = true
	}
}
