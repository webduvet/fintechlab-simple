// Package labswitch is the lab-only switch that disconnects a stand-in.
//
// The stand-ins — the settlement service, the webhook receiver — played the
// platform's part before a real platform plugged in. Once one has, a
// stand-in still pulling Worldline's files, or still the target of a
// vendor's webhooks, is traffic the developer did not ask for: payouts at
// B4B nobody made, batches in Banking Circle's log for a listener that is
// not theirs. So each wiring that a stand-in owns carries one of these, and
// the console turns it off and on (docs/plugins.md, Stand-ins).
//
// A switch only stops what the service does on its own — a timer, a
// delivery to a fixed target. A request someone makes explicitly still
// works: turning a stand-in off must not make its buttons lie.
package labswitch

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/webduvet/fintechlab-simple/internal/httputilx"
)

// Switch is one connection a stand-in owns, on unless turned off.
type Switch struct {
	name   string // the service, for the log
	what   string // what the switch stops, in a sentence
	target string // where the wiring goes, when it is a delivery

	mu        sync.RWMutex
	connected bool
	changedAt time.Time
	reason    string
}

// New is a switch in the given starting state. what is the sentence the
// console shows: what stops when this is off.
func New(name, what string, connected bool) *Switch {
	return &Switch{name: name, what: what, connected: connected}
}

// FromEnv reads the starting state from an env value: "false", "0", "off"
// or "no" start it disconnected; anything else — including unset — on.
func FromEnv(name, what, value string) *Switch {
	on := true
	switch value {
	case "false", "0", "off", "no":
		on = false
	default:
		if b, err := strconv.ParseBool(value); err == nil {
			on = b
		}
	}
	return New(name, what, on)
}

// Target records where the wiring delivers to, so the console can tell
// whose listener it is. It returns s for chaining at construction.
func (s *Switch) Target(url string) *Switch {
	s.target = url
	return s
}

// Connected reports whether the wiring is live.
func (s *Switch) Connected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

// Set turns it on or off.
func (s *Switch) Set(on bool, reason string) {
	s.mu.Lock()
	changed := s.connected != on
	s.connected, s.reason, s.changedAt = on, reason, time.Now().UTC()
	s.mu.Unlock()
	if changed {
		state := "disconnected"
		if on {
			state = "connected"
		}
		log.Printf("%s: %s — %s (%s)", s.name, state, s.what, reason)
	}
}

// State is the wire shape of GET and POST on the switch's path.
type State struct {
	Connected bool   `json:"connected"`
	What      string `json:"what"`
	Target    string `json:"target,omitempty"`
	Reason    string `json:"reason,omitempty"`
	ChangedAt string `json:"changed_at,omitempty"`
}

func (s *Switch) state() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := State{Connected: s.connected, What: s.what, Target: s.target, Reason: s.reason}
	if !s.changedAt.IsZero() {
		st.ChangedAt = s.changedAt.Format(time.RFC3339)
	}
	return st
}

// Routes serves GET and POST on path. POST takes {"connected": bool,
// "reason": "…"}.
func (s *Switch) Routes(mux *http.ServeMux, path string) {
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		httputilx.WriteJSON(w, 200, s.state())
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Connected *bool  `json:"connected"`
			Reason    string `json:"reason"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || body.Connected == nil {
			httputilx.Error(w, 400, `want {"connected": true|false}`)
			return
		}
		reason := body.Reason
		if reason == "" {
			reason = "set over " + path
		}
		s.Set(*body.Connected, reason)
		httputilx.WriteJSON(w, 200, s.state())
	})
}
