# Fake bank

In-process ledger. Seeded on start:

| id | IBAN | holder | opening |
| --- | --- | --- | --- |
| `acc_alice` | `GB00SIM0000000000001` | Alice Simulator | 10000.00 EUR |
| `acc_bob` | `GB00SIM0000000000002` | Bob Simulator | 500.00 EUR |
| `acc_merchant` | `GB00SIM0000000000003` | Merchant Simulator | 0.00 EUR |

IBANs are **obviously fake**. They are not valid modulo-97 IBANs and must not be sent to any real rail.

## API

- `GET /health`
- `GET /accounts` / `GET /accounts/{id}`
- `POST /accounts` `{holder, currency, openingBalance}`
- `POST /transfers` `{fromAccountId, toIban|toAccountId, amount, currency, reference, paymentId}`
- `GET /ledger`

Amounts are decimal strings (`"25.00"`), stored as integer cents. Insufficient funds → 409. Restart wipes state.
