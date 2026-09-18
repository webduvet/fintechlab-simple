// Package money stores demo amounts as integer minor units.
package money

import (
	"fmt"
	"strconv"
	"strings"
)

func Parse(s string) (int64, error) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	whole, frac, ok := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("money: %q", s)
	}
	var cents int64
	if ok {
		if len(frac) == 0 {
			frac = "00"
		}
		if len(frac) == 1 {
			frac += "0"
		}
		if len(frac) != 2 {
			return 0, fmt.Errorf("money: expected 2 decimal places, got %q", s)
		}
		cents, err = strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("money: %q", s)
		}
	}
	v := w*100 + cents
	if neg {
		v = -v
	}
	return v, nil
}

func Format(v int64) string {
	neg := ""
	if v < 0 {
		neg = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", neg, v/100, v%100)
}
