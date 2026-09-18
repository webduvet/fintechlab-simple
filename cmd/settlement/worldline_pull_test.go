package main

import (
	"sync"
	"testing"
)

// A settlement file processed twice pays every merchant in it twice, so
// the claim has to be atomic: the check and the mark must happen under one
// lock, not on either side of a multi-second download and payout run.
func TestClaimLetsExactlyOneCallerTakeAFile(t *testing.T) {
	p := &worldlinePuller{seen: map[string]bool{}}

	const callers = 50
	var wins int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if p.claim("20260906010414_Worldline_Settlement_ER_EUR.csv.pgp") {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d callers claimed the same settlement file; every one of them would have paid it out", wins)
	}
}

func TestClaimTreatsDistinctFilesIndependently(t *testing.T) {
	p := &worldlinePuller{seen: map[string]bool{}}
	if !p.claim("morning.csv.pgp") {
		t.Fatal("first claim of a fresh file was refused")
	}
	if !p.claim("afternoon.csv.pgp") {
		t.Fatal("claiming a different file was refused")
	}
	if p.claim("morning.csv.pgp") {
		t.Fatal("a file was claimed twice")
	}
}

// Files already in the archive were taken delivery of on an earlier run.
// Re-processing them after a restart is the same double payout by another
// route.
func TestAlreadyArchivedFilesAreNotClaimedAgain(t *testing.T) {
	p := &worldlinePuller{seen: map[string]bool{"seen-before.csv.pgp": true}}
	if p.claim("seen-before.csv.pgp") {
		t.Fatal("a file left over from a previous run was claimed again")
	}
}
