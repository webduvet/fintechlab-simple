# Working in this repo

Read [docs/index.md](docs/index.md) for what the lab is and
[docs/principles.md](docs/principles.md) for the sim/prod line and the
no-vendor-secrets rule. [docs/structure/README.md](docs/structure/README.md)
explains how a service's code is split across `cmd/`, `internal/`,
`docker/`, `compose.yml`, `config/` and `docs/`.

## One architecture

This tree is the **standalone vendor simulations** only: `cmd/<service>`
plus `internal/<service>`, one process per simulated third party. They hold
the deep protocol work — RS512 JWT, AES-256-GCM webhook envelopes, real
SFTP+PGP, the Bambora file format — and that is the point of them.

The kernel/pod/recipe experiment lives on its own branches and is parked.
Do not add `core/`, `recipes/` or a pod runtime here. If a simulation needs
new behaviour, it goes in that vendor's own package.

## What this exists to do

Run the settle path end to end against mocked third parties, so the real
platform can be pointed at it locally:

    worldline SFTP → settlement file → per-outlet payout → b4b (regulatory,
    positive) → banking-circle (funds, batched webhooks, intraday recon) →
    business bank

Every verification-style service answers positively. Failure injection is a
later concern; a happy path that actually connects is the deliverable.

## User interfaces

**Before writing or changing any UI, read
[docs/design-system.md](docs/design-system.md) and follow it.** It is the
source of truth for tokens, layout, components, behaviour contracts and copy
voice; `cmd/console/web/app.css` is one instance of it, not the definition.
If the two disagree, the document wins — change the document first, then
bring the implementations to it.
