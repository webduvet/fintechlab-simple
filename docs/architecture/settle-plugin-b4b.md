# Settle plug-in → b4b-oversight (first slice)

Goal: lightyear / merchant settle can auth to and call `pod-emi-oversight`
(`recipes/b4b-oversight`) the same way it calls standalone `cmd/b4b`.

## Landed in this slice

| Piece | Where |
| --- | --- |
| `GET /oversight/v1/beneficiaries/{id}` | `kernels/api.yaml` → `identity-party-store` (404 on unknown; no auto-vivify) |
| RS512 JWT auth (`aud: b4b-payments`, kid `b4b-mock-1`) | `http-ingress` mode `jwt-rs512` + `secrets` PEM handle |
| Payment-status callback scaffolding | `callbacks` subscription → `http://127.0.0.1:8443/api/v1/b4b/callback/payments`, envelope `case-status` (`{id,status,payload?}`), signing `none` |
| Twin doc `:9185` drift | `banking-circle-twin.md` — plain listener is introspection-only after PR #4 |

Lab JWT material: throwaway public PEM is the `api.jwt_public_pem` default in
`kernels/secrets.yaml` (`*.pem` is gitignored). Override with
`B4B_JWT_PUBLIC_KEY_PATH` to the shared `b4b-keys` public half in compose.

## Remaining (out of scope here)

- Full Worldline SFTP depth on the acquirer pod
- Verify pod parity with standalone `cmd/verify`
- `subscriptionEvent` / PUT activate on notification self-service (rails)
- `/internal` balances shim if a client still needs the old path
- Per-payment `callback_url` override (recipe destination is fixed; real API
  prefers the client-account endpoint)
- `banking_circle_api_response.paymentId` on the terminal callback + live
  handoff body mapping onto bank-rails `/internal/handoff`
- Persistent boarding state across pod restart (still in-memory)

## Smoke-test

```bash
# from repo root
make pod-check
go test ./core/kernels/httpingress/ ./core/pod/ -count=1
make pod POD=recipes/b4b-oversight   # listen :9086

# Mint a token the same way settlement does (kid b4b-mock-1, aud b4b-payments)
# via internal/b4b.SignBearerToken against the private key matching the
# recipe default public PEM (see core/pod/recipe_test.go), then:

curl -s -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:9086/oversight/v1/beneficiaries/unknown   # expect 404

curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  http://127.0.0.1:9086/oversight/v1/beneficiaries \
  -d '{"external_ref":"ben-1","name":"SIM PAYEE","account_name":"SIM PAYEE","account_number":"IE29AIBK93115212345678","financial_institution":"AIBKIE2D"}'
```
