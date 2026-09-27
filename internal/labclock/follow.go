// Package labclock is the lab's shared business clock: wall time moved by
// one offset that the lab's clock service owns.
//
// The clock can put the whole lab on another day — pinned to an instant,
// walked back to the last business day, or advanced an hour to let a sweep
// fire. A vendor that stamped its bookings with the real clock would then
// disagree with the platform about what day it is: a payout made "on
// Monday" by the platform would book on the vendor's Sunday. So a vendor
// stamps anything the bank would date — bookings, notification timestamps,
// balance dates, report dates — from this clock instead, and the platform
// under test follows the same service (see docs/plugins.md).
//
// Only business time moves. Token lifetimes, retry backoff, delivery
// windows and timeouts measure elapsed time and stay on the real clock. What
// is shared is an offset, not a time: each follower adds it to its own wall
// clock, so only a change has to travel, and a follower a continent away is
// as right as one on the same host.
//
// This file is the follower side. server.go is the clock service itself.
// With no clock to follow the offset is zero and Now is the wall clock, so
// a vendor that follows nobody behaves exactly as before.
package labclock

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

// Now is the wall clock moved by the lab clock's current offset, in UTC.
func Now() time.Time {
	return time.Now().Add(time.Duration(offset.Load())).UTC()
}

// Offset is the lab clock's current offset from the wall clock.
func Offset() time.Duration {
	return time.Duration(offset.Load())
}

// Set moves the clock to d from the wall clock. Follow calls it; tests may
// too, and must put it back.
func Set(d time.Duration) {
	offset.Store(int64(d))
}

// wireClock is the part of the clock service's GET /clock this reads.
type wireClock struct {
	OffsetMs *float64 `json:"offset_ms"`
}

// Fetch reads the current offset from the clock service
// (GET {url}, e.g. http://clock:8096/clock).
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
		return 0, fmt.Errorf("lab clock %s: HTTP %d", url, resp.StatusCode)
	}
	var got wireClock
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		return 0, fmt.Errorf("lab clock %s: %w", url, err)
	}
	if got.OffsetMs == nil {
		return 0, fmt.Errorf("lab clock %s: no offset_ms", url)
	}
	return time.Duration(*got.OffsetMs * float64(time.Millisecond)), nil
}

// Follow polls the clock service every interval until ctx ends, moving Now
// with it. A one-second poll keeps every follower within a second of a
// change.
//
// While the clock cannot be reached the last offset it gave stands — the
// clock service restarting is not the lab going back to the real clock —
// and that is logged once, when it starts and when it ends, rather than
// every second.
func Follow(ctx context.Context, url string, interval time.Duration) {
	client := &http.Client{Timeout: 2 * time.Second}
	reachable := true
	poll := func() {
		d, err := Fetch(ctx, client, url)
		if err != nil {
			if reachable {
				log.Printf("labclock: cannot read %s (%v); keeping offset %s", url, err, Offset())
				reachable = false
			}
			return
		}
		if !reachable {
			log.Printf("labclock: %s answering again", url)
			reachable = true
		}
		if d != Offset() {
			Set(d)
			log.Printf("labclock: now %s (%s from real)", Now().Format(time.RFC3339), d)
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

// FollowEnv follows the lab clock in the background when LAB_CLOCK_URL is
// set (compose points every vendor at the clock service's GET /clock), and
// leaves the wall clock in charge when it is not.
func FollowEnv(ctx context.Context, service string) {
	url := strings.TrimSpace(os.Getenv("LAB_CLOCK_URL"))
	if url == "" {
		return
	}
	log.Printf("%s: following the lab clock at %s", service, url)
	go Follow(ctx, url, time.Second)
}

// sleepSlice is how often Wait re-reads the clock.
var sleepSlice = 5 * time.Second

// Wait blocks until the lab clock reaches the next scheduled time and
// returns it. next says, for any "now", when the schedule fires next.
//
// It re-reads the clock at least every 5 seconds and re-plans each time —
// a clock moved and moved back within that window can go unnoticed:
//   - moved forward past the planned time, it returns — the lab's day
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
