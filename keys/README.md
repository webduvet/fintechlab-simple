# keys/

Every key and certificate the lab generates, one subdirectory per owner.
All of it is lab-only and fake: gitignored, never real key material, and
readable by every container (see [docs/security/ca-and-tls.md](../docs/security/ca-and-tls.md)
for why the permissions are loose).

Nothing here is written by hand. `make up` / `make start` create what is
missing and leave what exists alone, so a platform that copied a key keeps
working across restarts. Containers see this directory at `/keys`, with the
same layout.

## Somewhere else

This directory is only the default. Set `LAB_KEYS_DIR` and the lab
generates into that path instead:

```sh
export LAB_KEYS_DIR=~/lab-keys
make up
```

A platform that reads the lab's keys from disk is pointed at the same path.
The subdirectory names below are the ones buddy's local runner reads, and
its default is this directory (`~/gh/fintechlab-simple/keys`), so it needs
one variable only when you move them:

```sh
export FINTECH_SIM_LAB=~/lab-keys   # buddy: setup.sh, init/run-banking-circle.sh
```

Nothing in a key depends on where it is stored, so moving the directory
(`mv keys ~/lab-keys`, then set `LAB_KEYS_DIR`) needs no regeneration.

In the pod deployment the same tree lives in the `fintechlab-keys` volume
instead ([docs/deploy-pod.md](../docs/deploy-pod.md)).

## What is here

| Path | Made by | What a platform under test needs from it |
| --- | --- | --- |
| `certs/ca.pem` | `ca/generate.sh` | trust it (`NODE_EXTRA_CA_CERTS`) to reach Banking Circle over TLS |
| `certs/client.pem`, `certs/client-key.pem` | `ca/generate.sh` | the mTLS client certificate Banking Circle requires |
| `certs/ca-key.pem`, `certs/<server>*.pem` | `ca/generate.sh` | nothing — the CA's own key and the servers' certificates |
| `b4b-keys/private.pem` | the b4b service, first start | signs the RS512 bearer token B4B checks |
| `b4b-keys/public.pem` | the b4b service | what B4B verifies with |
| `wlsftp-keys/worldline_private.asc` | the worldline service, first start | decrypts the settlement files (the lab encrypts to its own key) |
| `wlsftp-keys/worldline_public.asc` | the worldline service | verifies the files' signatures |
| `wlsftp-keys/host_key.pub` | the worldline service | pins the SFTP server's host key |
| `wlsftp-keys/host_key` | the worldline service | nothing — the SFTP server's private key |

A platform that does not read from disk can take all of it from the
console instead: each vendor card's **Connect your platform** panel offers
the client-side files above and an `.env` with them inlined (all vendors at
once from Configuration → Connect your platform).

## New keys

```sh
make keys-regenerate                  # asks first; CONFIRM=yes for scripts
```

It stops the lab, empties the three subdirectories, issues a new PKI and
starts the lab again, where b4b and worldline generate new keypairs. Every
copy of the old keys a platform holds stops working at that moment: restart
the platform's runner, and download its `.env` again if it uses one. The pod
equivalent is `make pod-up REGENERATE_KEYS=true`.
