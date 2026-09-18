package b4b

import (
	"errors"

	"github.com/webduvet/fintechlab-simple/internal/allowlist"
)

// ValidateCallbackURL enforces B4B's webhook destination policy at payment
// creation time. Unlike every other webhook sender in this lab, B4B's
// callback_url is caller-supplied per payment, not a fixed config value --
// this is a real, intentional rejection path (400 at creation), not
// something silently deferred to delivery time.
func ValidateCallbackURL(list *allowlist.List, callbackURL string) error {
	if callbackURL == "" {
		return errors.New("b4b: callback_url required")
	}
	return list.Allowed(callbackURL)
}
