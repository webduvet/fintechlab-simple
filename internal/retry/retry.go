// Package retry implements short demo backoffs for webhook delivery.
package retry

import (
	"fmt"
	"strings"
	"time"
)

// DefaultBackoffs are short on purpose so the lab is watchable.
// A production-shaped profile might use 15s, 30s, 1m — still not "real SLA".
var DefaultBackoffs = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// ParseBackoffs reads "1s,2s,4s" or "15s,30s,1m".
func ParseBackoffs(spec string) ([]time.Duration, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return append([]time.Duration(nil), DefaultBackoffs...), nil
	}
	var out []time.Duration
	for _, p := range strings.Split(spec, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		d, err := time.ParseDuration(p)
		if err != nil {
			return nil, fmt.Errorf("retry: backoff %q: %w", p, err)
		}
		if d < 0 {
			return nil, fmt.Errorf("retry: backoff %q is negative", p)
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return append([]time.Duration(nil), DefaultBackoffs...), nil
	}
	return out, nil
}

// SleepBeforeAttempt returns the wait before attempt n (1-based).
// Attempt 1 is immediate (0). After that, backoffs[n-2], last value held.
func SleepBeforeAttempt(n int, backoffs []time.Duration) time.Duration {
	if n <= 1 || len(backoffs) == 0 {
		return 0
	}
	i := n - 2
	if i >= len(backoffs) {
		return backoffs[len(backoffs)-1]
	}
	return backoffs[i]
}

// ShouldRetry reports whether a delivery HTTP status should be retried.
// 2xx is success. Lab policy: retry anything that is not 2xx so a down
// receiver is visible in the retry log.
func ShouldRetry(status int, attempt, maxAttempts int) bool {
	if status >= 200 && status <= 299 {
		return false
	}
	return attempt < maxAttempts
}

// Exhausted is true when the last attempt finished without 2xx.
func Exhausted(attempt, maxAttempts int) bool {
	return attempt >= maxAttempts
}
