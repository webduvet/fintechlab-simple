package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/webduvet/fintechlab-simple/internal/activity"
	"github.com/webduvet/fintechlab-simple/internal/bankingcircle"
)

// What the console's two Banking Circle panels say.
//
// Both summarisers read the caller's own request rather than this
// service's interpretation of it, so a payment refused before it reached
// the ledger still appears with the amount that was attempted. A panel
// that only showed accepted payments would be a panel that goes quiet
// exactly when something is wrong.

func summarizeBridgePayment(c *activity.Call) (string, map[string]string) {
	amount := strings.TrimSpace(c.JSONField("amount") + " " + c.JSONField("currency"))
	holder := c.JSONField("holder")
	summary := fmt.Sprintf("payout %s from %s", amount, c.JSONField("accountId"))
	if holder != "" {
		summary += " to " + holder
	}
	return summary, nonEmpty(map[string]string{
		"payment_id":   c.JSONField("paymentId"),
		"external_ref": c.JSONField("externalRef"),
		"account":      c.JSONField("accountId"),
		"beneficiary":  c.JSONField("iban"),
		"holder":       holder,
		"amount":       amount,
	})
}

func summarizeIncoming(c *activity.Call) (string, map[string]string) {
	amount := strings.TrimSpace(c.JSONField("amount") + " " + c.JSONField("currency"))
	return fmt.Sprintf("funds in: %s to the safeguarding account", amount),
		nonEmpty(map[string]string{
			"amount":    amount,
			"reference": c.JSONField("reference"),
		})
}

func nonEmpty(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// recordNotification logs one delivery attempt of an encrypted batch.
//
// The batch is the unit, not the event: the vendor's own contract is that
// notifications are delivered in batches of up to five, and an operator
// asking "did the webhook fire" is asking about the POST. The event types
// inside it are what makes the line worth reading, so they are named.
func (a *app) recordNotification(sub *bankingcircle.Subscription, env bankingcircle.Envelope, status int, err error) {
	types := map[string]int{}
	order := []string{}
	for _, n := range env.Notifications {
		if _, seen := types[n.NotificationType]; !seen {
			order = append(order, n.NotificationType)
		}
		types[n.NotificationType]++
	}
	kinds := make([]string, 0, len(order))
	for _, k := range order {
		if types[k] > 1 {
			kinds = append(kinds, fmt.Sprintf("%s ×%d", k, types[k]))
			continue
		}
		kinds = append(kinds, k)
	}

	detail := nonEmpty(map[string]string{
		"subscription": sub.ID,
		"endpoint":     sub.Endpoint,
		"events":       strconv.Itoa(len(env.Notifications)),
		"http":         statusText(status),
	})
	summary := fmt.Sprintf("batch of %d to %s — %s", len(env.Notifications), sub.Endpoint, strings.Join(kinds, ", "))
	ev := activity.Event{Op: "notification", Peer: sub.Endpoint, Summary: summary, Detail: detail, Status: activity.StatusOK}
	if err != nil {
		// The client's own refusal, verbatim: this is the line that says
		// whether the platform is listening, which is the whole question.
		ev.Status = activity.StatusBad
		ev.Summary = summary + " — " + err.Error()
		ev.Detail["error"] = err.Error()
	}
	a.notifLog.Record(ev)
}

func statusText(status int) string {
	if status == 0 {
		return ""
	}
	return strconv.Itoa(status)
}
