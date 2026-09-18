# Webhooks

## Why a ping is not a connected system

`GET /health` on the receiver proves the process is up. It does **not** prove that:

1. the payment API moved the ledger,
2. the notifier accepted an enqueue for *that* payment id,
3. the POST carried an HMAC over the body,
4. the receiver stored the event.

`make demo-payment` waits for step 4. If you only curl `/health`, you have a ping.

## Delivery

Notifier `POST /enqueue` returns 202 and runs attempts in a goroutine.

- Header `X-Sim-Signature: sha256=<hex>` over `timestamp + "." + body`
- Header `X-Sim-Timestamp` (unix seconds)
- Header `X-Sim-Event-Id`

Receiver returns **2xx** only after HMAC verifies and the event is stored. Anything else is retried.

## Retries

Default lab backoffs: `1s,2s,4s` (env `RETRY_BACKOFF`). A longer demo profile is `15s,30s,1m`. After `MAX_ATTEMPTS` (default 4) failures the delivery is `failed` and the subscription is marked `failed`. Further enqueues then 409.

2xx stops the loop. Non-2xx retries, including 4xx, so a broken receiver is visible in `GET /deliveries`.
