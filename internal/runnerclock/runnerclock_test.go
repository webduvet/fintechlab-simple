package runnerclock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// TestNowIsShiftedByTheOffset: Now is the wall clock plus the runner's
// offset, and a zero offset is the wall clock.
func TestNowIsShiftedByTheOffset(t *testing.T) {
	defer Set(0)
	if d := Now().Sub(time.Now()); d > time.Second || d < -time.Second {
		t.Fatalf("with no offset Now is %s from the wall clock", d)
	}
	Set(26 * time.Hour)
	if d := Now().Sub(time.Now()); d < 26*time.Hour-time.Second || d > 26*time.Hour+time.Second {
		t.Fatalf("with a 26h offset Now is %s from the wall clock", d)
	}
}

// TestFollowTracksTheRunnerAndSurvivesItGoingAway: a move on the runner is
// picked up on the next poll, and while the runner does not answer the last
// offset it gave stands rather than snapping back to the real clock.
func TestFollowTracksTheRunnerAndSurvivesItGoingAway(t *testing.T) {
	defer Set(0)
	var offsetMs atomic.Int64
	var down atomic.Bool
	offsetMs.Store(3_600_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"offset_ms":` + strconv.FormatInt(offsetMs.Load(), 10) + `,"mode":"pinned"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Follow(ctx, srv.URL, 10*time.Millisecond)

	waitFor := func(want time.Duration) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for Offset() != want {
			if time.Now().After(deadline) {
				t.Fatalf("offset = %s, want %s", Offset(), want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor(time.Hour)
	offsetMs.Store(-2 * 86_400_000)
	waitFor(-48 * time.Hour)

	down.Store(true)
	time.Sleep(50 * time.Millisecond)
	if Offset() != -48*time.Hour {
		t.Fatalf("offset = %s while the runner is down, want the last one it gave", Offset())
	}
	down.Store(false)
	offsetMs.Store(0)
	waitFor(0)
}

func TestFetchRefusesAnAnswerWithoutAnOffset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"mode":"real"}`))
	}))
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("an answer with no offset_ms was taken as an offset")
	}
}

// TestWaitFollowsTheRunnerBothWays: a wait for a daily 08:30 wakes as soon
// as the runner is moved past it, and one planned while the runner was days
// ahead re-plans to the earlier slot when the runner is moved back.
func TestWaitFollowsTheRunnerBothWays(t *testing.T) {
	defer Set(0)
	defer func(old time.Duration) { sleepSlice = old }(sleepSlice)
	sleepSlice = 10 * time.Millisecond
	daily := func(now time.Time) time.Time {
		at := time.Date(now.Year(), now.Month(), now.Day(), 8, 30, 0, 0, time.UTC)
		if !at.After(now) {
			at = at.AddDate(0, 0, 1)
		}
		return at
	}
	wait := func() <-chan time.Time {
		done := make(chan time.Time, 1)
		go func() { done <- Wait(daily) }()
		return done
	}

	// Forward: the next 08:30 is up to a day away; jump past it.
	Set(0)
	done := wait()
	time.Sleep(30 * time.Millisecond)
	planned := daily(Now())
	Set(planned.Sub(time.Now()) + time.Minute)
	select {
	case got := <-done:
		if !got.Equal(planned) {
			t.Errorf("woke for %s, want %s", got, planned)
		}
	case <-time.After(time.Second):
		t.Fatal("still waiting after the runner moved past the slot")
	}

	// Back: planned two days ahead, then the clock returns; the wait must
	// re-plan to the nearer slot rather than sit out two days.
	Set(48 * time.Hour)
	done = wait()
	time.Sleep(30 * time.Millisecond)
	Set(0)
	time.Sleep(30 * time.Millisecond)
	nearer := daily(Now())
	Set(nearer.Sub(time.Now()) + time.Second)
	select {
	case got := <-done:
		if !got.Equal(nearer) {
			t.Errorf("woke for %s, want the re-planned %s", got, nearer)
		}
	case <-time.After(time.Second):
		t.Fatal("never woke: the wait kept the slot planned two days ahead")
	}
}
