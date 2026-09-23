// Package runnerclock is the lab's shared business clock: wall time moved
// by the offset the platform's local runner is on.
//
// The runner can put the platform on another day — pinned to an instant,
// walked back to the last business day, or advanced an hour to let a sweep
// fire. A vendor that stamped its bookings with the real clock would then
// disagree with the platform about what day it is: a payout made "on
// Monday" by the platform would book on the vendor's Sunday. So a vendor
// stamps anything the bank would date — bookings, notification timestamps,
// balance dates, report dates — from this clock instead.
//
// Only business time moves. Token lifetimes, retry backoff, delivery
// windows and timeouts measure elapsed time and stay on the real clock,
// the same split the runner's own clock shim makes: it shifts Date by a
// constant, so elapsed time is unchanged.
//
// With no runner to follow the offset is zero and Now is the wall clock,
// so a vendor that follows nobody behaves exactly as before.
package runnerclock

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

var offset atomic.Int64 // nanoseconds

// Now is the wall clock moved by the runner's current offset, in UTC.
func Now() time.Time {
	return time.Now().Add(time.Duration(offset.Load())).UTC()
}

// Offset is the runner's current offset from the wall clock.
func Offset() time.Duration {
	return time.Duration(offset.Load())
}

// Set moves the clock to d from the wall clock. Follow calls it; tests may
// too, and must put it back.
func Set(d time.Duration) {
	offset.Store(int64(d))
}

// runnerClock is the part of the runner's GET /sim/clock this reads.
type runnerClock struct {
	OffsetMs *float64 `json:"offset_ms"`
}

// Fetch reads the runner's current offset from its clock endpoint
// (GET {url}, e.g. http://host.containers.internal:3109/sim/clock).
func Fetch(ctx context.Context, client *http.Client, url string) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("runner clock %s: HTTP %d", url, resp.StatusCode)
	}
	var got runnerClock
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		return 0, fmt.Errorf("runner clock %s: %w", url, err)
	}
	if got.OffsetMs == nil {
		return 0, fmt.Errorf("runner clock %s: no offset_ms", url)
	}
	return time.Duration(*got.OffsetMs * float64(time.Millisecond)), nil
}

// Follow polls the runner's clock every interval until ctx ends, moving Now
// with it. The runner itself picks a new offset up within a second, so a
// one-second poll keeps the two within a second of each other.
//
// While the runner cannot be reached the last offset it gave stands — a
// runner restarting is not the platform going back to the real clock — and
// that is logged once, when it starts and when it ends, rather than every
// second.
func Follow(ctx context.Context, url string, interval time.Duration) {
	client := &http.Client{Timeout: 2 * time.Second}
	reachable := true
	poll := func() {
		d, err := Fetch(ctx, client, url)
		if err != nil {
			if reachable {
				log.Printf("runnerclock: cannot read %s (%v); keeping offset %s", url, err, Offset())
				reachable = false
			}
			return
		}
		if !reachable {
			log.Printf("runnerclock: %s answering again", url)
			reachable = true
		}
		if d != Offset() {
			Set(d)
			log.Printf("runnerclock: now %s (%s from real)", Now().Format(time.RFC3339), d)
		}
	}
	poll()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}

// FollowEnv follows the runner's clock in the background when
// RUNNER_CLOCK_URL is set (compose points every vendor at the runner's
// GET /sim/clock), and leaves the wall clock in charge when it is not.
func FollowEnv(ctx context.Context, service string) {
	url := strings.TrimSpace(os.Getenv("RUNNER_CLOCK_URL"))
	if url == "" {
		return
	}
	log.Printf("%s: following the runner's clock at %s", service, url)
	go Follow(ctx, url, time.Second)
}

// sleepSlice is how often Wait re-reads the clock.
var sleepSlice = 30 * time.Second

// Wait blocks until the runner's clock reaches the next scheduled time and
// returns it. next says, for any "now", when the schedule fires next.
//
// It re-reads the clock at least every 30 seconds and re-plans each time:
//   - moved forward past the planned time, it returns — the platform's day
//     has reached that point, so whatever was scheduled for it happens;
//   - moved back, it takes whatever next now says, which may be earlier —
//     otherwise a schedule planned while the clock was two days ahead would
//     sit out two real days, skipping every slot in between.
func Wait(next func(now time.Time) time.Time) time.Time {
	at := next(Now())
	for {
		now := Now()
		if !now.Before(at) {
			return at
		}
		if again := next(now); again.Before(at) {
			at = again
		}
		rem := at.Sub(now)
		if rem > sleepSlice {
			rem = sleepSlice
		}
		time.Sleep(rem)
	}
}
