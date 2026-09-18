package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/webduvet/fintechlab-simple/internal/bankingcircle"
	"github.com/webduvet/fintechlab-simple/internal/httputilx"
)

// HTTP handlers for the notification self-service API.
//
// Every mutation carries an If-Match header holding the record's current
// rowVersion, and a mismatch is a 412. That is not decoration: it is how
// the real API stops two callers clobbering each other, and a client that
// has not been made to send it will fail on its first concurrent update in
// production. The rowVersion also changes on each delivery retry, so an
// in-flight PUT can be invalidated by a retry it knows nothing about --
// which is exactly the case a client needs to have been forced to handle.

// ifMatch reads the concurrency token off the request.
func ifMatch(r *http.Request) string { return r.Header.Get("If-Match") }

// writeSubscriptionError maps store errors onto the documented statuses.
func writeSubscriptionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, bankingcircle.ErrSubscriptionNotFound):
		httputilx.Error(w, 404, "subscription not found")
	case errors.Is(err, bankingcircle.ErrRowVersionMismatch):
		httputilx.Error(w, 412, "If-Match does not match the current rowVersion; re-read the subscription and retry")
	case errors.Is(err, bankingcircle.ErrDuplicateEndpoint):
		httputilx.Error(w, 409, err.Error())
	case errors.Is(err, bankingcircle.ErrDuplicateEvent):
		httputilx.Error(w, 409, err.Error())
	default:
		httputilx.Error(w, 400, err.Error())
	}
}

type createSubscriptionReq struct {
	Endpoint      string  `json:"endpoint"`
	EncryptionKey string  `json:"encryptionKey"`
	Status        *int    `json:"status"`
	Email         string  `json:"email"`
	MaxPerMessage *int    `json:"maxNotificationsPerMessage"`
	MTLSEnabled   *bool   `json:"mtlsEnabled"`
	LegacyURL     *string `json:"url"`
}

func (a *app) createSubscription(w http.ResponseWriter, r *http.Request) {
	var req createSubscriptionReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	// The old lab shape was {"url": "..."} with no key and no status. It
	// is accepted here so an existing script keeps working, but it is
	// mapped onto the real fields rather than kept as a parallel model --
	// there is one subscription type, and it is the documented one.
	if req.Endpoint == "" && req.LegacyURL != nil {
		req.Endpoint = *req.LegacyURL
	}
	if req.EncryptionKey == "" {
		req.EncryptionKey = string(a.notifKey)
	}
	status := bankingcircle.StatusActive
	if req.Status != nil {
		status = bankingcircle.SubscriptionStatus(*req.Status)
	}
	if err := a.list.Allowed(req.Endpoint); err != nil {
		httputilx.Error(w, 400, "endpoint rejected by allowlist: "+err.Error())
		return
	}
	params := bankingcircle.CreateParams{
		Endpoint:      req.Endpoint,
		EncryptionKey: req.EncryptionKey,
		Status:        status,
		Email:         req.Email,
	}
	if req.MaxPerMessage != nil {
		params.MaxNotificationsPerMessage = *req.MaxPerMessage
	}
	if req.MTLSEnabled != nil {
		params.MTLSEnabled = *req.MTLSEnabled
	}
	sub, err := a.subs.Create(params)
	if err != nil {
		writeSubscriptionError(w, err)
		return
	}
	httputilx.WriteJSON(w, 200, sub.Public())
}

func (a *app) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "PageNumber", 1)
	size := queryInt(r, "PageSize", 50)
	subs, total := a.subs.List(page, size)
	out := make([]bankingcircle.PublicSubscription, 0, len(subs))
	for _, s := range subs {
		out = append(out, s.Public())
	}
	httputilx.WriteJSON(w, 200, map[string]any{
		"result":     out,
		"pageInfo":   map[string]int{"pageNumber": page, "pageSize": size, "totalItems": total},
		"totalItems": total,
	})
}

func (a *app) getSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := a.subs.Get(r.PathValue("id"))
	if err != nil {
		writeSubscriptionError(w, err)
		return
	}
	httputilx.WriteJSON(w, 200, sub.Public())
}

type updateSubscriptionReq struct {
	Endpoint      *string `json:"endpoint"`
	EncryptionKey *string `json:"encryptionKey"`
	Email         *string `json:"email"`
	MaxPerMessage *int    `json:"maxNotificationsPerMessage"`
	MTLSEnabled   *bool   `json:"mtlsEnabled"`
}

func (a *app) updateSubscription(w http.ResponseWriter, r *http.Request) {
	var req updateSubscriptionReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	if req.Endpoint != nil {
		if err := a.list.Allowed(*req.Endpoint); err != nil {
			httputilx.Error(w, 400, "endpoint rejected by allowlist: "+err.Error())
			return
		}
	}
	sub, err := a.subs.Update(r.PathValue("id"), ifMatch(r), bankingcircle.UpdateParams{
		Endpoint:                   req.Endpoint,
		EncryptionKey:              req.EncryptionKey,
		Email:                      req.Email,
		MaxNotificationsPerMessage: req.MaxPerMessage,
		MTLSEnabled:                req.MTLSEnabled,
	})
	if err != nil {
		writeSubscriptionError(w, err)
		return
	}
	httputilx.WriteJSON(w, 200, sub.Public())
}

func (a *app) activateSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := a.subs.SetActive(r.PathValue("id"), ifMatch(r), true)
	if err != nil {
		writeSubscriptionError(w, err)
		return
	}
	// Notifications held from before the subscription went down are
	// delivered now. Events that happened while it was down are not
	// backfilled -- they were never queued.
	redelivered := a.dispatch.Redeliver(sub)
	httputilx.WriteJSON(w, 200, map[string]any{
		"subscription":             sub.Public(),
		"redeliveredNotifications": redelivered,
	})
}

func (a *app) deactivateSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := a.subs.SetActive(r.PathValue("id"), ifMatch(r), false)
	if err != nil {
		writeSubscriptionError(w, err)
		return
	}
	httputilx.WriteJSON(w, 200, sub.Public())
}

func (a *app) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	if err := a.subs.Delete(r.PathValue("id"), ifMatch(r)); err != nil {
		writeSubscriptionError(w, err)
		return
	}
	w.WriteHeader(200)
}

type createSubscriptionEventReq struct {
	SubscriptionID string   `json:"subscriptionId"`
	EventType      string   `json:"eventType"`
	TargetType     *int     `json:"targetType"`
	TargetIDs      []string `json:"targetIds"`
}

func (a *app) createSubscriptionEvent(w http.ResponseWriter, r *http.Request) {
	var req createSubscriptionEventReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	if req.TargetType == nil {
		httputilx.Error(w, 400, "targetType is required")
		return
	}
	ev, err := a.subs.AddEvent(req.SubscriptionID, req.EventType,
		bankingcircle.TargetType(*req.TargetType), req.TargetIDs)
	if err != nil {
		writeSubscriptionError(w, err)
		return
	}
	httputilx.WriteJSON(w, 200, ev)
}

type replaceTargetsReq struct {
	TargetType *int     `json:"targetType"`
	TargetIDs  []string `json:"targetIds"`
}

func (a *app) replaceSubscriptionEventTargets(w http.ResponseWriter, r *http.Request) {
	var req replaceTargetsReq
	if err := httputilx.ReadJSON(r, &req); err != nil {
		httputilx.Error(w, 400, err.Error())
		return
	}
	if req.TargetType == nil {
		httputilx.Error(w, 400, "targetType is required")
		return
	}
	ev, err := a.subs.ReplaceTargets(r.PathValue("id"), ifMatch(r),
		bankingcircle.TargetType(*req.TargetType), req.TargetIDs)
	if err != nil {
		writeSubscriptionError(w, err)
		return
	}
	// The V2 response renders status as a string rather than the numeric
	// enum -- a real inconsistency in the API, reproduced rather than
	// tidied away, because a client has to cope with both.
	httputilx.WriteJSON(w, 200, map[string]any{
		"id":                             ev.ID,
		"eventType":                      ev.EventType,
		"subscriptionId":                 ev.SubscriptionID,
		"status":                         statusWord(ev.Status),
		"rowVersion":                     ev.RowVersion,
		"subscriptionEventTargetDetails": ev.Targets,
	})
}

func (a *app) deleteSubscriptionEvent(w http.ResponseWriter, r *http.Request) {
	if err := a.subs.DeleteEvent(r.PathValue("id"), ifMatch(r)); err != nil {
		writeSubscriptionError(w, err)
		return
	}
	w.WriteHeader(200)
}

func statusWord(s bankingcircle.SubscriptionStatus) string {
	switch s {
	case bankingcircle.StatusActive:
		return "Active"
	case bankingcircle.StatusInactive:
		return "Inactive"
	case bankingcircle.StatusRetired:
		return "Retired"
	}
	return "None"
}

// listEmails exposes the warning and deactivation emails the retry
// schedule emits. Lab-only: a warning nobody can see is not a simulation
// of a warning.
func (a *app) listEmails(w http.ResponseWriter, _ *http.Request) {
	emails := a.mail.All()
	httputilx.WriteJSON(w, 200, map[string]any{"count": len(emails), "emails": emails})
}

// queryIntStrict parses a query parameter, ignoring anything unparseable.
func queryIntStrict(r *http.Request, key string, def int) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// enqueueSyntheticNotifications is a lab-only test hook: it queues n
// notifications for a subscription *without* flushing, so a caller can
// watch batching actually happen.
//
// It is deliberately not folded into clienttest. clienttest is a real
// endpoint and flushes immediately, because its job is to answer "is my
// endpoint reachable" now; bending it to take a count would be inventing
// vendor behaviour. This lives under /sim, where everything is obviously
// this lab's own.
func (a *app) enqueueSyntheticNotifications(w http.ResponseWriter, r *http.Request) {
	sub, err := a.subs.Get(r.PathValue("subscriptionId"))
	if err != nil {
		writeSubscriptionError(w, err)
		return
	}
	count := queryIntStrict(r, "count", 1)
	if count < 1 || count > bankingcircle.MaxNotificationsPerMessage {
		httputilx.Error(w, 400, "count must be between 1 and "+strconv.Itoa(bankingcircle.MaxNotificationsPerMessage))
		return
	}
	eventType := r.URL.Query().Get("eventType")
	if eventType == "" {
		eventType = string(bankingcircle.NotificationOutgoingPaymentProcessed)
	}
	for i := range count {
		rec := bankingcircle.Recipient{Subscription: sub, Event: syntheticEvent(sub, eventType)}
		a.dispatch.Enqueue(sub, newNotification(rec, eventType, map[string]any{
			"paymentId": fmt.Sprintf("sim_%s_%d", sub.ID, i),
			"status":    eventType,
		}))
	}
	if r.URL.Query().Get("flush") == "true" {
		a.dispatch.Flush(sub.ID)
	}
	httputilx.WriteJSON(w, 202, map[string]any{
		"enqueued":                   count,
		"subscriptionId":             sub.ID,
		"maxNotificationsPerMessage": sub.MaxNotificationsPerMessage,
	})
}

// pendingNotifications reports how many notifications are being held for
// redelivery because the subscription was deactivated before they landed.
func (a *app) pendingNotifications(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("subscriptionId")
	httputilx.WriteJSON(w, 200, map[string]any{
		"subscriptionId": id,
		"pending":        a.dispatch.Pending(id),
	})
}
