# Allowlists

The notifier **rejects** destinations that are not on `WEBHOOK_ALLOWLIST`. The check runs on enqueue, on `POST /subscriptions`, and again on each delivery (`internal/allowlist`). An off-list URL never gets an HTTP POST.

Compose default:

```text
receiver,receiver:8443,localhost,127.0.0.1
```

That is a **local policy**: compose DNS names and loopback. It is the opposite of a production bank webhook policy.

## Local policy vs real public CIDRs

| Lab | Production-shaped (still not this repo) |
| --- | --- |
| Hostname `receiver` on a user-defined network | Explicit public egress CIDRs published by the PSP |
| `127.0.0.1` so host-run tests work | No loopback, no link-local, no metadata IPs |
| Easy to add `10.0.0.0/8` for a kind cluster | Change-controlled list; deny by default |
| HTTP or HTTPS | HTTPS + possibly mTLS |

`file://`, userinfo in URLs, empty hosts, and unknown hostnames are rejected.

If you point `WEBHOOK_URL` at `https://evil.example/hook`, enqueue returns 403. That is the demo.
