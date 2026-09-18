# Banking Circle notifications: subscriptions, batching, retries

## What was wrong before

The subscription model here was a three-field struct — `{url, active,
version}` — and delivery was one notification per POST with a generic
`1s,2s,4s` backoff. None of that is what a client integrates against.
Concretely, a client written against the old simulator would meet all of
these for the first time in production:

| Real behaviour | Old simulator |
| --- | --- |
| Every mutation needs `If-Match: {rowVersion}`; a mismatch is 412 | No concurrency control at all |
| `maxNotificationsPerMessage` (5–1000) changes the wire shape | Always one notification per message |
| Each subscription carries its own 32-char encryption key | One key for the whole server |
| Notifications route by subscribed event type and target | Fan-out to every active subscription |
| Eleven retries, then Banking Circle deactivates the subscription | Four attempts, then give up quietly and stay subscribed |
| Undelivered notifications are retained and redelivered on reactivation | Dropped |
| `status` is a 0/1/2/4 enum; the key reads back as `*Hidden*` | Boolean `active`; no key |

The fan-out one is the quiet killer: it made every subscription's event and
target configuration decorative, so a client with a completely broken
subscription looked exactly like a working one.

## Subscriptions

`POST /api/v1/notificationselfservice/subscription` requires `endpoint`,
`encryptionKey` and `status`. The key must be exactly 32 characters — the
webhook cipher uses its raw UTF-8 bytes as an AES-256 key, so anything else
cannot work, and it is rejected at create time rather than at the first
delivery. Endpoints are unique across subscriptions. `maxNotificationsPerMessage`
outside 5–1000 is rejected rather than clamped: a client asking for 2000
has misunderstood something, and silently giving it 1000 hides that.

Every mutating call — `PUT /subscription/{id}`, `/activate`,
`/deactivate`, `DELETE`, `PUT /subscriptionEvent/{id}/targets`,
`DELETE /subscriptionEvent/{id}` — requires `If-Match` holding the record's
current `rowVersion`. An absent header is a rejection, not a bypass. The
`rowVersion` also advances on delivery retries, so an in-flight `PUT` can
be invalidated by a retry it knows nothing about — which is the case a
client most needs to have been forced to handle.

## Routing

A notification reaches a subscription only if that subscription is active
and has an active event whose `eventType` matches and whose targets include
the payment's account or owning company. An event with no targets is
subscription-wide.

Target types are `0` Account, `1` Company, `2` CompanyGroup.

Most payment events nest their detail under `payment`. **`PaymentStatus`**
and **`AgencyBankingWhitelistResult`** use **`payload`** instead. A parser
written against only one of them silently drops the other, so the
simulator emits both correctly (`UsesPayloadProperty`).

## Batching

Each subscription has its own queue. A message goes out when the queue
reaches `maxNotificationsPerMessage`, or when `batch_flush_interval`
elapses with a partial batch — without that second condition a subscription
with a batch size of 1000 would never deliver anything until a thousand
payments happened.

One queue per subscription, not one global queue: batch size is
per-subscription, and a slow subscriber must not hold up a fast one.

## The retry schedule

Successful delivery is any `2xx`. Anything else — 500, refused connection,
timeout — starts the schedule:

| Retry | Delay after the previous attempt | Email |
| --- | --- | --- |
| 1 | 15s | |
| 2 | 30s | |
| 3 | 1m | |
| 4 | 10m | |
| 5 | 30m | |
| 6 | 1h | |
| 7 | 2h | warning |
| 8 | 6h | |
| 9 | 12h | |
| 10 | 24h | warning |
| 11 | 48h | deactivation |

After the final failure the subscription is **deactivated**, a deactivation
email is sent, and the undelivered notifications are **retained**.
Reactivating with `PUT /subscription/{id}/activate` redelivers them; events
that occurred while the subscription was down are not backfilled. That
distinction is the difference between a correct recovery procedure and a
lost day of payments.

The retry guide documents 11 retries while the OpenAPI `statusMessage` text
says 10. 11 is the schedule of record here; the discrepancy is recorded
rather than silently resolved.

## Making two days watchable

`config/banking-circle.json` holds the table, so it stays readable as
documentation. `BC_TIME_SCALE` divides every delay in it:

- `1` — real timing. The schedule reaches deactivation 48 hours after the
  first failure.
- `86400` (the compose default) — the whole eleven-step story plays out in
  about four seconds.

The step count, the email placement and the shape do not change with the
scale, and those are what a client is actually being tested against. The
`banking-circle-retry-deactivation` harness scenario runs the entire
schedule, the deactivation, the email and the redelivery in six seconds.

## Emails

Warning and deactivation emails are recorded rather than sent, and readable
at `GET /sim/emails`. A warning nobody can see is not a simulation of a
warning.

## mTLS

`BC_MTLS` selects `off`, `optional` or `require` (default). Real
integrations vary — some present a client certificate, some do not — so
forcing it on makes the lab unusable for the ones that do not, and leaving
it out makes it untestable for the ones that do. `off` still serves HTTPS;
it drops the client-certificate requirement, not the transport security.
Bearer auth applies in every mode, so turning mTLS off never makes the API
unauthenticated.

## Lab-only endpoints

Everything under `/sim` is this lab's, not Banking Circle's:

| Endpoint | Purpose |
| --- | --- |
| `GET /sim/emails` | The warning/deactivation emails the schedule emitted |
| `POST /sim/subscription/{id}/notifications?count=N` | Queue N notifications *without* flushing, so batching is observable |
| `GET /sim/subscription/{id}/pending` | How many notifications are held for redelivery |

`clienttest` is deliberately left alone: it is a real endpoint whose job is
to answer "is my endpoint reachable" *now*, so it flushes immediately, and
bending it to take a count would be inventing vendor behaviour.
