package uuidx

import "testing"

// TestDeriveMatchesRFC4122 pins one value against the published algorithm.
// The point of using real UUIDv5 rather than an invented hash-to-hex scheme
// is that anything else implementing RFC 4122 reaches the same answer, so
// an id found in a database can be checked without this package. A test
// that only compared Derive to itself would not defend that property.
func TestDeriveMatchesRFC4122(t *testing.T) {
	// The canonical example from RFC 4122's own namespace: DNS namespace,
	// name "www.example.org".
	const dns = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	const want = "74738ff5-5367-5958-9aee-98fffdcd1876"
	if got := Derive(dns, "www.example.org"); got != want {
		t.Fatalf("Derive(DNS, www.example.org) = %s, want %s", got, want)
	}
}

func TestDeriveIsStableAndDistinct(t *testing.T) {
	a := Derive(NamespaceLab, "merchant-1")
	if b := Derive(NamespaceLab, "merchant-1"); a != b {
		t.Errorf("not stable: %s vs %s", a, b)
	}
	if c := Derive(NamespaceLab, "merchant-2"); c == a {
		t.Errorf("distinct names collided on %s", a)
	}
	if !Valid(a) {
		t.Errorf("Derive produced %q, which does not validate", a)
	}
	// Version 5 and the RFC 4122 variant, in the canonical positions.
	if a[14] != '5' {
		t.Errorf("version nibble = %c, want 5", a[14])
	}
	if a[19] != '8' && a[19] != '9' && a[19] != 'a' && a[19] != 'b' {
		t.Errorf("variant nibble = %c, want one of 8/9/a/b", a[19])
	}
}

func TestValid(t *testing.T) {
	for _, s := range []string{
		"00000000-0000-4000-8000-000000000978",
		"74738ff5-5367-5958-9aee-98fffdcd1876",
	} {
		if !Valid(s) {
			t.Errorf("Valid(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"",
		"bc_acc_sga_eur",
		"00000000-0000-4000-8000-00000000097",   // short
		"00000000-0000-4000-8000-0000000009781", // long
		"00000000000040008000000000000978",      // unhyphenated
		"00000000-0000-4000-8000-00000000097g",  // not hex
		"00000000-0000-4000-8000-000000000978 ", // trailing space
		"0000000000004000-8000-000000000978--",  // hyphens misplaced
		// Upper case is legal per the RFC but is not what these APIs emit,
		// and accepting it would give one account two spellings.
		"74738FF5-5367-5958-9AEE-98FFFDCD1876",
	} {
		if Valid(s) {
			t.Errorf("Valid(%q) = true, want false", s)
		}
	}
}

func TestDerivePanicsOnABadNamespace(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("a non-UUID namespace must panic: it would silently mint a whole parallel id space")
		}
	}()
	Derive("not-a-namespace", "x")
}
