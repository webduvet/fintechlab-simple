package console

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Stand-ins.
//
// Before a real platform plugged into the lab, a few of the lab's own
// services played its part: the settlement service pulled Worldline's
// files and paid outlets through B4B, and the webhook receiver was the
// listener Banking Circle and ACI delivered to. They were the stepping
// stone the vendors were built against, and they are still how a vendor's
// outbound half is proved connected with no platform running.
//
// Once a platform is plugged in they are noise — payouts at B4B nobody
// made, deliveries to a listener that is not the developer's — so each can
// be disconnected (its vendor wiring stopped, see internal/labswitch) and
// hidden (its card and diagram box gone). Connected is read live from the
// services that own the wiring and never stored here; shown is the
// console's own, because only the console draws cards.

// StandInPref is what a plugin's descriptor, or a console button, asks of
// one stand-in. A nil field is "leave it as it is".
type StandInPref struct {
	Connected *bool `json:"connected,omitempty"`
	Shown     *bool `json:"shown,omitempty"`
}

// StandInInfo marks a catalogue service as a stand-in, and says how the
// console is showing it and who decided.
type StandInInfo struct {
	Shown bool      `json:"shown"`
	SetBy string    `json:"set_by,omitempty"`
	SetAt time.Time `json:"set_at,omitempty"`
}

// ErrNotStandIn is a request about a service that is not one.
var ErrNotStandIn = errors.New("not a stand-in")

// standInLocked is the live state of a stand-in, creating it from the
// catalogue's marker on first use. Callers hold c.mu.
func (c *Catalogue) standInLocked(id string) *StandInInfo {
	if st, ok := c.standIns[id]; ok {
		return st
	}
	for _, s := range c.Services {
		if s.ID == id && s.StandIn != nil {
			if c.standIns == nil {
				c.standIns = map[string]*StandInInfo{}
			}
			st := *s.StandIn
			c.standIns[id] = &st
			return &st
		}
	}
	return nil
}

// overlayLocked puts the live stand-in state on a copy of s.
func (c *Catalogue) overlayLocked(s Service) Service {
	if s.StandIn == nil {
		return s
	}
	if st := c.standInLocked(s.ID); st != nil {
		cp := *st
		s.StandIn = &cp
	}
	return s
}

// StandInIDs lists the catalogue's stand-ins, in catalogue order.
func (c *Catalogue) StandInIDs() []string {
	var out []string
	for _, s := range c.Services {
		if s.StandIn != nil {
			out = append(out, s.ID)
		}
	}
	return out
}

// IsStandIn reports whether id is one of the catalogue's stand-ins.
func (c *Catalogue) IsStandIn(id string) bool {
	for _, s := range c.Services {
		if s.ID == id {
			return s.StandIn != nil
		}
	}
	return false
}

// Hidden reports whether a stand-in's card is hidden.
func (c *Catalogue) Hidden(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.standInLocked(id)
	return st != nil && !st.Shown
}

// ShowStandIn shows or hides a stand-in's card, recording who asked.
func (c *Catalogue) ShowStandIn(id string, shown bool, by string) error {
	if !c.IsStandIn(id) {
		return fmt.Errorf("%w: %q", ErrNotStandIn, id)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.standInLocked(id)
	st.Shown, st.SetBy, st.SetAt = shown, by, c.clockNow().UTC()
	return nil
}

// StandInChanged reports whether a plugin's stand_ins differ from what was
// last applied for it, and records them as applied. A plugin renews every
// ten seconds with the same descriptor; applying its preferences on every
// renewal would undo a console button ten seconds after it was pressed.
func (c *Catalogue) StandInChanged(pluginID string, prefs map[string]StandInPref) bool {
	if len(prefs) == 0 {
		return false
	}
	// Map keys marshal sorted, so equal preferences compare equal.
	b, _ := json.Marshal(prefs)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.appliedStandIns == nil {
		c.appliedStandIns = map[string]string{}
	}
	if c.appliedStandIns[pluginID] == string(b) {
		return false
	}
	c.appliedStandIns[pluginID] = string(b)
	return true
}

// validStandIns refuses a preference for anything that is not a stand-in:
// a plugin cannot hide a vendor.
func (c *Catalogue) validStandIns(prefs map[string]StandInPref) error {
	var bad []string
	for id := range prefs {
		if !c.IsStandIn(id) {
			bad = append(bad, id)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("%w: stand_ins names %q; the lab's stand-ins are %q", ErrBadPlugin, bad, c.StandInIDs())
}
