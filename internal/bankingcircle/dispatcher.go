package bankingcircle

import (
	"sync"
	"time"
)

// Dispatcher batches notifications per subscription and delivers them on
// the configured retry schedule, deactivating a subscription that never
// acknowledges.
//
// One queue per subscription, not one global queue: batch size is a
// per-subscription setting, and a slow subscriber must not hold up a fast
// one. Delivery itself is injected (Send) so the whole thing is testable
// without HTTP, encryption or a network.
type Dispatcher struct {
	cfg   DeliveryConfig
	subs  *SubscriptionStore
	mail  *MailBox
	logf  func(format string, args ...any)
	sleep func(time.Duration)

	// Send delivers one encrypted batch to a subscription and reports
	// whether it was acknowledged with a 2xx. Anything else -- a 500, a
	// refused connection, a timeout -- is a failure that starts the retry
	// schedule.
	Send func(sub *Subscription, env Envelope) error

	mu     sync.Mutex
	queues map[string]*queue
	// pending holds notifications for subscriptions that were deactivated
	// before they could be delivered. They are retained and redelivered on
	// reactivation, which is what the API documents; events that occur
	// while deactivated are not queued here at all.
	pending map[string][]Notification
	wg      sync.WaitGroup
}

type queue struct {
	items []Notification
	timer *time.Timer
}

// NewDispatcher builds a Dispatcher. logf may be nil.
func NewDispatcher(cfg DeliveryConfig, subs *SubscriptionStore, mail *MailBox, logf func(string, ...any)) *Dispatcher {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if mail == nil {
		mail = &MailBox{}
	}
	return &Dispatcher{
		cfg:     cfg,
		subs:    subs,
		mail:    mail,
		logf:    logf,
		sleep:   time.Sleep,
		queues:  map[string]*queue{},
		pending: map[string][]Notification{},
	}
}

// Enqueue adds n to sub's batch, flushing immediately if the batch is now
// full and otherwise arming the flush timer.
func (d *Dispatcher) Enqueue(sub *Subscription, n Notification) {
	size := sub.MaxNotificationsPerMessage
	if size <= 0 {
		size = d.cfg.DefaultMaxNotificationsPerMessage
	}
	if size <= 0 {
		size = DefaultNotificationsPerMessage
	}

	d.mu.Lock()
	q, ok := d.queues[sub.ID]
	if !ok {
		q = &queue{}
		d.queues[sub.ID] = q
	}
	q.items = append(q.items, n)

	if len(q.items) >= size {
		batch := q.items
		q.items = nil
		if q.timer != nil {
			q.timer.Stop()
			q.timer = nil
		}
		d.mu.Unlock()
		d.deliver(sub, batch)
		return
	}

	if q.timer == nil {
		subID := sub.ID
		q.timer = time.AfterFunc(time.Duration(d.cfg.BatchFlushInterval), func() {
			d.flush(subID)
		})
	}
	d.mu.Unlock()
}

// Flush sends whatever is queued for a subscription right now, without
// waiting for the timer. Tests and the clienttest endpoint want this;
// nothing in the production path needs it.
func (d *Dispatcher) Flush(subscriptionID string) { d.flush(subscriptionID) }

func (d *Dispatcher) flush(subscriptionID string) {
	d.mu.Lock()
	q, ok := d.queues[subscriptionID]
	if !ok || len(q.items) == 0 {
		if ok && q.timer != nil {
			q.timer.Stop()
			q.timer = nil
		}
		d.mu.Unlock()
		return
	}
	batch := q.items
	q.items = nil
	if q.timer != nil {
		q.timer.Stop()
		q.timer = nil
	}
	d.mu.Unlock()

	sub, err := d.subs.Get(subscriptionID)
	if err != nil {
		d.logf("banking-circle: dropping %d notification(s) for deleted subscription %s", len(batch), subscriptionID)
		return
	}
	d.deliver(sub, batch)
}

// deliver runs one batch through the retry schedule in its own goroutine,
// so a subscriber that takes two simulated days to fail never blocks
// payment processing.
func (d *Dispatcher) deliver(sub *Subscription, batch []Notification) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.deliverSync(sub, batch)
	}()
}

// deliverSync is deliver's blocking form, used by Wait-based tests.
func (d *Dispatcher) deliverSync(sub *Subscription, batch []Notification) {
	env := Envelope{Notifications: batch}

	err := d.Send(sub, env)
	if err == nil {
		d.logf("banking-circle: delivered %d notification(s) to %s on the first attempt", len(batch), sub.Endpoint)
		return
	}
	d.logf("banking-circle: initial delivery to %s failed: %v", sub.Endpoint, err)

	for retry := 1; ; retry++ {
		wait, ok := d.cfg.Wait(retry)
		if !ok {
			break
		}
		d.sleep(wait)

		// A subscription deleted mid-schedule has nothing left to retry
		// against.
		if _, gerr := d.subs.Get(sub.ID); gerr != nil {
			d.logf("banking-circle: subscription %s disappeared mid-retry, abandoning %d notification(s)", sub.ID, len(batch))
			return
		}

		if kind := d.cfg.EmailFor(retry); kind != "" {
			d.sendEmail(sub, kind, retry)
		}

		err = d.Send(sub, env)
		if err == nil {
			d.logf("banking-circle: delivered %d notification(s) to %s on retry %d", len(batch), sub.Endpoint, retry)
			return
		}
		d.logf("banking-circle: retry %d to %s failed: %v", retry, sub.Endpoint, err)
	}

	// Schedule exhausted. Deactivate, and hold on to the notifications:
	// they are delivered when the subscription is reactivated. Discarding
	// them here is how a client loses a day of payments and never learns
	// which ones.
	d.retain(sub.ID, batch)
	if _, derr := d.subs.Deactivate(sub.ID,
		"Subscription deactivated after the final unsuccessful delivery retry"); derr != nil {
		d.logf("banking-circle: deactivate %s: %v", sub.ID, derr)
	}
	d.logf("banking-circle: subscription %s (%s) deactivated after %d failed retries; %d notification(s) retained for redelivery",
		sub.ID, sub.Endpoint, d.cfg.DeactivateAfterRetries, len(batch))
}

func (d *Dispatcher) sendEmail(sub *Subscription, kind string, attempt int) {
	subject := "Webhook delivery is failing"
	if kind == "deactivation" {
		subject = "Webhook subscription deactivated"
	}
	to := sub.Email
	if to == "" {
		to = "(no email configured)"
	}
	d.mail.Send(Email{
		To:             to,
		Kind:           kind,
		SubscriptionID: sub.ID,
		Endpoint:       sub.Endpoint,
		Subject:        subject,
		Attempt:        attempt,
	})
	d.logf("banking-circle: %s email for subscription %s to %s (retry %d)", kind, sub.ID, to, attempt)
}

func (d *Dispatcher) retain(subscriptionID string, batch []Notification) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending[subscriptionID] = append(d.pending[subscriptionID], batch...)
}

// Pending reports how many notifications are held for redelivery.
func (d *Dispatcher) Pending(subscriptionID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pending[subscriptionID])
}

// Redeliver re-queues everything retained for a subscription. Call it on
// reactivation.
func (d *Dispatcher) Redeliver(sub *Subscription) int {
	d.mu.Lock()
	held := d.pending[sub.ID]
	delete(d.pending, sub.ID)
	d.mu.Unlock()
	if len(held) == 0 {
		return 0
	}
	d.logf("banking-circle: redelivering %d retained notification(s) to %s", len(held), sub.Endpoint)
	for _, n := range held {
		d.Enqueue(sub, n)
	}
	return len(held)
}

// Wait blocks until every in-flight delivery has finished. For tests.
func (d *Dispatcher) Wait() { d.wg.Wait() }
