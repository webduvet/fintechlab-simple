package bankingcircle

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

// recorder stands in for the encrypted HTTP delivery, so the batching and
// retry logic can be tested without a network, a cipher, or a two-day wait.
type recorder struct {
	mu       sync.Mutex
	sent     []Envelope
	failFor  int // fail this many deliveries before succeeding
	attempts int
}

func (r *recorder) send(_ *Subscription, env Envelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts++
	if r.attempts <= r.failFor {
		return errors.New("destination returned 500")
	}
	r.sent = append(r.sent, env)
	return nil
}

func (r *recorder) envelopes() []Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Envelope, len(r.sent))
	copy(out, r.sent)
	return out
}

func (r *recorder) attemptCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts
}

func fastConfig(retries int) DeliveryConfig {
	sched := make([]RetryStep, retries)
	for i := range sched {
		sched[i] = RetryStep{After: Duration(time.Millisecond)}
	}
	if retries > 0 {
		sched[retries-1].Email = "deactivation"
	}
	return DeliveryConfig{
		Schedule:                          sched,
		TimeScale:                         1,
		DeactivateAfterRetries:            retries,
		BatchFlushInterval:                Duration(20 * time.Millisecond),
		DefaultMaxNotificationsPerMessage: DefaultNotificationsPerMessage,
		RequestTimeout:                    Duration(time.Second),
	}
}

func testSubscription(t *testing.T, store *SubscriptionStore, endpoint string, batch int) *Subscription {
	t.Helper()
	sub, err := store.Create(CreateParams{
		Endpoint:                   endpoint,
		EncryptionKey:              "0123456789abcdef0123456789abcdef",
		Status:                     StatusActive,
		Email:                      "alerts@example.com",
		MaxNotificationsPerMessage: batch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddEvent(sub.ID, string(NotificationOutgoingPaymentProcessed), TargetCompany, nil); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestDispatcherSendsOneMessageOncePerBatchSize(t *testing.T) {
	store := NewSubscriptionStore(nil)
	sub := testSubscription(t, store, "https://example.test/hook", 5)
	rec := &recorder{}
	// A long flush interval, so anything that goes out did so because the
	// batch filled up, not because a timer fired.
	cfg := fastConfig(1)
	cfg.BatchFlushInterval = Duration(time.Hour)
	d := NewDispatcher(cfg, store, nil, t.Logf)
	d.Send = rec.send

	for i := range 5 {
		d.Enqueue(sub, Notification{EventID: "evt", NotificationType: "OutgoingPaymentProcessed", Timestamp: string(rune('a' + i))})
	}
	d.Wait()

	envs := rec.envelopes()
	if len(envs) != 1 {
		t.Fatalf("delivered %d messages, want 1 -- five notifications with a batch size of five is one message", len(envs))
	}
	if len(envs[0].Notifications) != 5 {
		t.Fatalf("message carried %d notifications, want all 5", len(envs[0].Notifications))
	}
}

func TestDispatcherHoldsAPartialBatchUntilTheFlushInterval(t *testing.T) {
	store := NewSubscriptionStore(nil)
	sub := testSubscription(t, store, "https://example.test/hook", 1000)
	rec := &recorder{}
	cfg := fastConfig(1)
	cfg.BatchFlushInterval = Duration(30 * time.Millisecond)
	d := NewDispatcher(cfg, store, nil, t.Logf)
	d.Send = rec.send

	d.Enqueue(sub, Notification{EventID: "evt1"})
	d.Enqueue(sub, Notification{EventID: "evt2"})

	// Nothing yet: the batch is nowhere near 1000.
	if got := len(rec.envelopes()); got != 0 {
		t.Fatalf("delivered %d messages immediately; a partial batch must wait", got)
	}
	// Without a flush interval a subscription with a 1000-notification
	// batch size would never deliver anything until a thousand payments
	// happened.
	time.Sleep(80 * time.Millisecond)
	d.Wait()

	envs := rec.envelopes()
	if len(envs) != 1 {
		t.Fatalf("delivered %d messages after the flush interval, want 1", len(envs))
	}
	if len(envs[0].Notifications) != 2 {
		t.Fatalf("flushed %d notifications, want the 2 that were queued", len(envs[0].Notifications))
	}
}

func TestDispatcherRetriesOnTheScheduleThenSucceeds(t *testing.T) {
	store := NewSubscriptionStore(nil)
	sub := testSubscription(t, store, "https://example.test/hook", 5)
	rec := &recorder{failFor: 3}
	d := NewDispatcher(fastConfig(11), store, nil, t.Logf)
	d.Send = rec.send

	d.Enqueue(sub, Notification{EventID: "evt1"})
	d.Flush(sub.ID)
	d.Wait()

	if rec.attemptCount() != 4 {
		t.Fatalf("made %d attempts, want 4 (one initial + three retries)", rec.attemptCount())
	}
	if len(rec.envelopes()) != 1 {
		t.Fatalf("delivered %d messages, want the one that eventually succeeded", len(rec.envelopes()))
	}
	// A subscription that recovers must stay subscribed.
	fresh, err := store.Get(sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh.IsActive {
		t.Fatal("subscription was deactivated even though delivery eventually succeeded")
	}
}

func TestDispatcherDeactivatesAfterTheFinalRetryAndRetainsTheNotifications(t *testing.T) {
	store := NewSubscriptionStore(nil)
	sub := testSubscription(t, store, "https://example.test/hook", 5)
	rec := &recorder{failFor: 1000} // never succeeds
	mail := &MailBox{}
	d := NewDispatcher(fastConfig(11), store, mail, t.Logf)
	d.Send = rec.send

	d.Enqueue(sub, Notification{EventID: "evt1"})
	d.Enqueue(sub, Notification{EventID: "evt2"})
	d.Flush(sub.ID)
	d.Wait()

	// One initial attempt plus eleven retries: the documented schedule.
	if rec.attemptCount() != 12 {
		t.Fatalf("made %d attempts, want 12 (one initial + eleven retries)", rec.attemptCount())
	}

	fresh, err := store.Get(sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.IsActive || fresh.Status != StatusInactive {
		t.Fatalf("subscription still active after the schedule was exhausted: isActive=%v status=%d", fresh.IsActive, fresh.Status)
	}
	if fresh.StatusMessage == "" {
		t.Fatal("no statusMessage explaining the deactivation; a client has no way to find out why it stopped")
	}
	// Its events must go inactive with it, or a subsequent event would
	// look deliverable.
	for _, e := range fresh.Events {
		if e.IsActive {
			t.Fatalf("event %s still active on a deactivated subscription", e.EventType)
		}
	}

	// The undelivered notifications are retained, not dropped. Dropping
	// them is how a client loses a day of payments and never learns which.
	if got := d.Pending(sub.ID); got != 2 {
		t.Fatalf("retained %d notifications, want the 2 that never landed", got)
	}

	var sawDeactivation bool
	for _, e := range mail.All() {
		if e.Kind == "deactivation" {
			sawDeactivation = true
			if e.To != "alerts@example.com" {
				t.Fatalf("deactivation email went to %q", e.To)
			}
		}
	}
	if !sawDeactivation {
		t.Fatal("no deactivation email was sent")
	}
}

func TestDispatcherRedeliversRetainedNotificationsOnReactivation(t *testing.T) {
	store := NewSubscriptionStore(nil)
	sub := testSubscription(t, store, "https://example.test/hook", 5)
	rec := &recorder{failFor: 1000}
	d := NewDispatcher(fastConfig(2), store, nil, t.Logf)
	d.Send = rec.send

	d.Enqueue(sub, Notification{EventID: "evt1"})
	d.Flush(sub.ID)
	d.Wait()

	if d.Pending(sub.ID) != 1 {
		t.Fatalf("expected the failed notification to be retained, pending=%d", d.Pending(sub.ID))
	}

	// The endpoint comes back.
	rec.mu.Lock()
	rec.failFor = 0
	rec.attempts = 0
	rec.mu.Unlock()

	fresh, err := store.Get(sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	reactivated, err := store.SetActive(sub.ID, fresh.RowVersion, true)
	if err != nil {
		t.Fatal(err)
	}
	if n := d.Redeliver(reactivated); n != 1 {
		t.Fatalf("redelivered %d notifications, want 1", n)
	}
	d.Flush(sub.ID)
	d.Wait()

	envs := rec.envelopes()
	if len(envs) != 1 || len(envs[0].Notifications) != 1 || envs[0].Notifications[0].EventID != "evt1" {
		t.Fatalf("redelivery did not carry the retained notification: %+v", envs)
	}
	if d.Pending(sub.ID) != 0 {
		t.Fatal("notifications are still pending after a successful redelivery")
	}
}

func TestTimeScaleCompressesTheRealSchedule(t *testing.T) {
	cfg := DefaultDeliveryConfig()
	// The real schedule's last step is a 48-hour wait. Nobody is going to
	// sit through that to watch a deactivation, which is the whole reason
	// the scale factor exists.
	if got, ok := cfg.Wait(11); !ok || got != 48*time.Hour {
		t.Fatalf("unscaled retry 11 = %v (ok=%v), want 48h", got, ok)
	}
	cfg.TimeScale = 3600 // an hour becomes a second
	got, ok := cfg.Wait(11)
	if !ok || got != 48*time.Second {
		t.Fatalf("scaled retry 11 = %v (ok=%v), want 48s", got, ok)
	}
	// Compression must not change the shape: same number of steps, same
	// emails in the same places.
	if len(cfg.Schedule) != 11 {
		t.Fatalf("schedule has %d steps, want the documented 11", len(cfg.Schedule))
	}
	if cfg.EmailFor(7) != "warning" || cfg.EmailFor(10) != "warning" || cfg.EmailFor(11) != "deactivation" {
		t.Fatalf("emails landed on the wrong steps: 7=%q 10=%q 11=%q",
			cfg.EmailFor(7), cfg.EmailFor(10), cfg.EmailFor(11))
	}
	if _, ok := cfg.Wait(12); ok {
		t.Fatal("there is a twelfth retry; the schedule must end at 11")
	}
}

func TestLoadDeliveryConfigMergesOverTheDefaults(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bc.json"
	// A file that sets only the time scale must keep the real schedule --
	// not silently end up with an empty one and no retries at all.
	if err := os.WriteFile(path, []byte(`{"time_scale": 3600}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDeliveryConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Schedule) != 11 {
		t.Fatalf("schedule has %d steps after a partial config, want the documented 11", len(cfg.Schedule))
	}
	if cfg.TimeScale != 3600 {
		t.Fatalf("time_scale = %v, want 3600", cfg.TimeScale)
	}
	if got, _ := cfg.Wait(1); got != 15*time.Second/3600 {
		t.Fatalf("first retry = %v, want the 15s step scaled by 3600", got)
	}

	// Durations read as strings, because this file is edited by hand.
	if err := os.WriteFile(path, []byte(`{"retry_schedule":[{"after":"250ms","email":"deactivation"}],"batch_flush_interval":"1s"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadDeliveryConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Schedule) != 1 || time.Duration(cfg.Schedule[0].After) != 250*time.Millisecond {
		t.Fatalf("schedule = %+v, want a single 250ms step", cfg.Schedule)
	}
	// deactivate_after_retries must fall back to the schedule length, or
	// the default of 11 would index past a one-step table.
	if cfg.DeactivateAfterRetries != 1 {
		t.Fatalf("deactivate_after_retries = %d, want it clamped to the 1-step schedule", cfg.DeactivateAfterRetries)
	}
	if time.Duration(cfg.BatchFlushInterval) != time.Second {
		t.Fatalf("batch_flush_interval = %v, want 1s", time.Duration(cfg.BatchFlushInterval))
	}

	// A missing file is not an error: the defaults are the real schedule.
	cfg, err = LoadDeliveryConfig(dir + "/absent.json")
	if err != nil {
		t.Fatalf("missing config file returned an error: %v", err)
	}
	if len(cfg.Schedule) != 11 || cfg.TimeScale != 1 {
		t.Fatalf("missing config did not fall back to the real unscaled schedule: %+v", cfg)
	}
}

func TestShippedConfigFileParses(t *testing.T) {
	// The file in config/ is what the compose service mounts. If it does
	// not parse, the service silently runs the unscaled 48-hour schedule
	// and the lab looks broken for reasons nobody will guess.
	cfg, err := LoadDeliveryConfig("../../config/banking-circle.json")
	if err != nil {
		t.Fatalf("config/banking-circle.json does not parse: %v", err)
	}
	if len(cfg.Schedule) != 11 {
		t.Fatalf("shipped config has %d retry steps, want 11", len(cfg.Schedule))
	}
	if cfg.TimeScale <= 1 {
		t.Fatalf("shipped config time_scale = %v; it exists to compress the schedule", cfg.TimeScale)
	}
	if cfg.EmailFor(11) != "deactivation" {
		t.Fatal("shipped config does not send a deactivation email on the final retry")
	}
}
