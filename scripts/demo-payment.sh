#!/bin/sh
# Happy-path: seed is already in the bank. Create a payment, poll receiver
# until the connected webhook arrives, print the event.
set -eu

API="${PAYMENT_API_URL:-http://127.0.0.1:8080}"
RECV="${RECEIVER_URL:-https://127.0.0.1:8443}"
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
CA="${CA_FILE:-$ROOT/certs/ca.pem}"
KEY="demo-$(date +%s)-$$"

if [ ! -f "$CA" ]; then
  echo "missing $CA — run make up first (or make certs)" >&2
  exit 1
fi

echo "== POST /payments (Idempotency-Key: $KEY)"
PAY=$(curl -sS -X POST "$API/payments" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $KEY" \
  -d '{"debtorAccountId":"acc_alice","creditorIban":"GB00SIM0000000000003","amount":"25.00","currency":"EUR","reference":"demo-invoice-1"}')
echo "$PAY"
echo

PAY_ID=$(printf '%s' "$PAY" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("id") or (d.get("payment") or {}).get("id",""))')
if [ -z "$PAY_ID" ]; then
  echo "payment-api did not return an id" >&2
  exit 1
fi

echo "== GET /payments/$PAY_ID"
curl -sS "$API/payments/$PAY_ID"
echo
echo

echo "== poll receiver GET /events until payment $PAY_ID arrives"
i=0
EVENTS=""
while [ "$i" -lt 30 ]; do
  EVENTS=$(curl -sS --cacert "$CA" "$RECV/events")
  HIT=$(printf '%s' "$EVENTS" | python3 -c "
import json,sys
d=json.load(sys.stdin)
want=sys.argv[1]
for e in d.get('events') or []:
    body=e.get('body') or {}
    if isinstance(body, str):
        try:
            body=json.loads(body)
        except Exception:
            body={}
    data = body.get('data') if isinstance(body, dict) else {}
    if not isinstance(data, dict):
        data = {}
    pid = e.get('paymentId') or body.get('paymentId') or data.get('id')
    if pid == want:
        print(json.dumps(e, indent=2))
        break
" "$PAY_ID")
  if [ -n "$HIT" ]; then
    echo "webhook received:"
    echo "$HIT"
    exit 0
  fi
  i=$((i + 1))
  sleep 1
done

echo "timed out waiting for webhook. last /events:" >&2
echo "$EVENTS" >&2
exit 1

