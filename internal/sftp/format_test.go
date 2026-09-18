package sftp

import "testing"

func TestParseFilenameValid(t *testing.T) {
	cases := []struct {
		name                               string
		merchantID, date, currency, format string
	}{
		{"GB00SIM0000000000003_2026-09-03_EUR_WX.csv", "GB00SIM0000000000003", "2026-09-03", "EUR", "csv"},
		{"GB00SIM0000000000003_2026-09-03_EUR_WX.xml", "GB00SIM0000000000003", "2026-09-03", "EUR", "xml"},
		{"GB00SIM0000000000003_2026-09-03_EUR_WX.json", "GB00SIM0000000000003", "2026-09-03", "EUR", "json"},
		// merchant IDs elsewhere in this lab embed underscores; must not
		// get swallowed by the date/currency fields.
		{"acc_merchant_2026-01-15_USD_WX.csv", "acc_merchant", "2026-01-15", "USD", "csv"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merchantID, date, currency, format, err := ParseFilename(tc.name)
			if err != nil {
				t.Fatalf("ParseFilename(%q): %v", tc.name, err)
			}
			if merchantID != tc.merchantID || date != tc.date || currency != tc.currency || format != tc.format {
				t.Fatalf("got (%q,%q,%q,%q), want (%q,%q,%q,%q)",
					merchantID, date, currency, format, tc.merchantID, tc.date, tc.currency, tc.format)
			}
			if err := ValidateFilename(tc.name); err != nil {
				t.Fatalf("ValidateFilename(%q): %v", tc.name, err)
			}
		})
	}
}

func TestParseFilenameInvalid(t *testing.T) {
	bad := []string{
		"",
		"no_structure_at_all.csv",
		"GB00SIM0000000000003_2026-09-03_EUR_WX.pdf",        // unsupported format
		"GB00SIM0000000000003_2026-9-3_EUR_WX.csv",          // date not zero-padded
		"GB00SIM0000000000003_2026-09-03_EURO_WX.csv",       // currency not 3 letters
		"GB00SIM0000000000003_2026-09-03_EUR.csv",           // missing _WX
		"../../../etc_2026-09-03_EUR_WX.csv",                // embeds a traversal-shaped id (still rejected: contains "/")
		"GB00SIM0000000000003_2026-09-03_EUR_WX.csv/../etc", // trailing traversal
	}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			if _, _, _, _, err := ParseFilename(name); err == nil {
				t.Fatalf("ParseFilename(%q): expected error", name)
			}
			if err := ValidateFilename(name); err == nil {
				t.Fatalf("ValidateFilename(%q): expected error", name)
			}
		})
	}
}

func TestValidatePathSegment(t *testing.T) {
	valid := []string{"GB00SIM0000000000003", "acc_merchant", "m1"}
	for _, s := range valid {
		if err := ValidatePathSegment(s); err != nil {
			t.Fatalf("ValidatePathSegment(%q): unexpected error: %v", s, err)
		}
	}

	invalid := []string{"", ".", "..", "../../../etc", "foo/../../bar", "a/b", "a\\b", "/etc/passwd"}
	for _, s := range invalid {
		if err := ValidatePathSegment(s); err == nil {
			t.Fatalf("ValidatePathSegment(%q): expected error", s)
		}
	}
}
