# Pointing settle's Banking Circle client at this lab

Verified against `apps/banking-circle/src/modules/auth/bc-api.client.ts`
with a Node client built the same way — base64 PEMs into an
`https.Agent{cert, key, passphrase}`, Basic auth to authorize, Bearer for
everything after. No change to that client was needed.

## Configuration

```dotenv
BC_AUTH_BASE_URL=https://localhost:8085
BC_API_BASE_URL=https://localhost:8085
BC_API_M2M_USERNAME=sim
BC_API_M2M_PASSWORD=sim-bc-dev-only
BC_API_CLIENT_CERT_PEM_BASE64=$(base64 -w0 certs/client.pem)
BC_API_CLIENT_KEY_PEM_BASE64=$(base64 -w0 certs/client-key.pem)
# The lab's client key is not encrypted, so leave the passphrase unset.
# BC_API_CLIENT_KEY_PASSPHRASE=
```

Run `make certs` first; `certs/` is generated and gitignored.

## The one thing that is not an env var

`bc-api.client.ts` builds its agent with `cert`, `key` and `passphrase` and
**no `ca:`**, so Node validates the lab's certificate against its own trust
store and rejects it. Nothing in the client's configuration can fix that —
it needs the CA at the process level:

```bash
NODE_EXTRA_CA_CERTS=/path/to/certs/ca.pem
```

Without it the failure is `UNABLE_TO_VERIFY_LEAF_SIGNATURE`, which reads
like a lab problem and is not one.

Use `localhost`, not `127.0.0.1`: the lab's certificate carries
`banking-circle` and `localhost` as subject alternative names, and an IP
address is not one of them.

## What was checked

| Configuration | Result |
| --- | --- |
| mTLS + CA trusted | authorize 200, reports 200 |
| No client certificate | refused — `ERR_SSL_TLSV13_ALERT_CERTIFICATE_REQUIRED` |
| Client certificate, CA not trusted | refused — `UNABLE_TO_VERIFY_LEAF_SIGNATURE` |

The second row is the one worth keeping honest. `BC_MTLS` defaults to
`require`, and a lab that accepted a connection without a client
certificate would let an integration pass locally in a configuration that
fails against the real thing. If you need to take the client certificate
out of the picture while debugging something else, set `BC_MTLS=optional`
deliberately rather than discovering it by accident — bearer auth still
applies in every mode, so that never makes the API unauthenticated.

## Token

`GET /api/v1/authorizations/authorize` answers
`{access_token, expires_in, token_type}`. `expires_in` is a number here;
the client accepts `string | number`, so both are safe. The lab's tokens
last an hour rather than the documented 300 seconds, which means a local
run will not exercise the client's refresh path. Set a shorter TTL if that
is what you are testing.
