package retry

import (
	"testing"
	"time"
)

func TestParseBackoffs(t *testing.T) {
	got, err := ParseBackoffs("15s,30s,1m")
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{15 * time.Second, 30 * time.Second, time.Minute}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseDefault(t *testing.T) {
	got, err := ParseBackoffs("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != time.Second {
		t.Fatalf("default: %v", got)
	}
}

func TestSleepBeforeAttempt(t *testing.T) {
	b := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if d := SleepBeforeAttempt(1, b); d != 0 {
		t.Fatalf("attempt 1: %v", d)
	}
	if d := SleepBeforeAttempt(2, b); d != time.Second {
		t.Fatalf("attempt 2: %v", d)
	}
	if d := SleepBeforeAttempt(3, b); d != 2*time.Second {
		t.Fatalf("attempt 3: %v", d)
	}
	if d := SleepBeforeAttempt(4, b); d != 4*time.Second {
		t.Fatalf("attempt 4: %v", d)
	}
	if d := SleepBeforeAttempt(9, b); d != 4*time.Second {
		t.Fatalf("hold last: %v", d)
	}
}

func TestShouldRetry(t *testing.T) {
	if ShouldRetry(200, 1, 4) {
		t.Fatal("2xx must not retry")
	}
	if ShouldRetry(204, 1, 4) {
		t.Fatal("204 must not retry")
	}
	if !ShouldRetry(500, 1, 4) {
		t.Fatal("500 should retry")
	}
	if !ShouldRetry(0, 2, 4) {
		t.Fatal("transport fail should retry")
	}
	if ShouldRetry(503, 4, 4) {
		t.Fatal("last attempt must not schedule another")
	}
}

func TestExhausted(t *testing.T) {
	if Exhausted(3, 4) {
		t.Fatal("not yet")
	}
	if !Exhausted(4, 4) {
		t.Fatal("should be exhausted")
	}
}
