# Local CA and TLS

`ca/generate.sh` (and the **ca** compose service) writes:

| File | Role |
| --- | --- |
| `certs/ca.pem` | Trust anchor for notifier, banking-circle, and `curl --cacert` |
| `certs/ca-key.pem` | Lab-only CA key — **do not reuse** outside this directory |
| `certs/receiver.pem` / `receiver-key.pem` | Server cert for the receiver (`DNS:receiver`, `DNS:localhost`, `DNS:receiver.local`, `DNS:receiver.fintechlab-simple.test`, `IP:127.0.0.1`) |
| `certs/banking-circle.pem` / `banking-circle-key.pem` | Server cert for the Banking Circle mock's mTLS listener (`DNS:banking-circle`, `DNS:localhost`, `DNS:banking-circle.fintechlab-simple.test`, `IP:127.0.0.1`) |
| `certs/client.pem` / `client-key.pem` | Client cert every caller presents to Banking Circle's mTLS listener — **not optional**: real Banking Circle requires one from every caller, and this mock enforces it the same way (`tls.RequireAndVerifyClientCert`) |

Receiver speaks HTTPS only. Banking Circle's `:8085` listener requires
**both** a trusted client certificate at the TLS handshake **and** a
Bearer token on every route after `authorize` — mTLS is not a substitute
for the auth flow, it's an additional layer real Banking Circle also
enforces. Its `:8095` listener (the B4B bridge and the harness's other
lab-only test hooks) is deliberately plain HTTP, no TLS, no auth — see
`docs/ARCHITECTURE-vendor-corrections.md` Addendum section C for why the
two can't share one listener. Notifier and the harness both load
`CA_FILE=/certs/ca.pem` so they will not accept a random public cert for
`receiver`/`banking-circle`.

Every other service in this lab (`bank`, `payment-api`, `settlement`,
`worldline`'s HTTP side, `b4b`, `aci`, `verify`) is plain HTTP — matching
how buddy's own real equivalents actually run in local dev too (their auth
is JWT/shared-secret/payload-encryption, not transport-level; see
`docs/ARCHITECTURE-phase3-corrections.md`'s "Existing Patterns Followed").
`worldline`'s SFTP side (`:2222`) uses SSH's own transport security
(host-key fingerprint pinning, not a certificate) — see
`docs/ARCHITECTURE-worldline-sftp-channel.md`.

This is **not** public PKI. Browsers will warn. That is expected.

Private keys stay under `certs/` (gitignored). Never paste them into
tickets.

See [../local-domains.md](../local-domains.md) for mapping these
certificates' `.test` SANs to `/etc/hosts` entries, so a real buddy service
running on the host can point at this lab using config shaped like a real
vendor endpoint instead of bare `localhost:<port>`.
