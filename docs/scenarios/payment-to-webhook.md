# Scenario: payment → webhook

Happy path you can run after `make up`.

## 1. Seed (already there)

Alice `GB00SIM0000000000001` has 10000.00 EUR. Merchant `GB00SIM0000000000003` has 0.

```bash
curl -s http://127.0.0.1:8081/accounts | python3 -m json.tool
```

## 2. Create a payment

```bash
curl -s -X POST http://127.0.0.1:8080/payments \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: tutorial-1' \
  -d '{
    "debtorAccountId": "acc_alice",
    "creditorIban": "GB00SIM0000000000003",
    "amount": "25.00",
    "currency": "EUR",
    "reference": "invoice-42"
  }'
```

Replay the same `Idempotency-Key` — you get the same payment id, no second debit.

## 3. Ledger moved

```bash
curl -s http://127.0.0.1:8081/accounts/acc_alice
curl -s http://127.0.0.1:8081/ledger
```

Alice is down 25.00; merchant is up 25.00.

## 4. Webhook stored

```bash
curl -s --cacert certs/ca.pem https://127.0.0.1:8443/events
```

Look for `"paymentId": "pay_…"` matching step 2. Or run `make demo-payment`, which polls until it appears.

## 5. What you just proved

Accept on the payment API is not decorative: it called the bank, then the notifier, then a TLS+HMAC POST landed in the receiver. That is the whole lab.
