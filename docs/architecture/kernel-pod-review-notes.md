# kernel-pod-review vs kernels v1 — align / drift

Branch: `kernel-pod-review` @ e90bb989 (2026-09-09)
Contract: Avram `docs/fintech/simulation/kernels.md` (v1 accepted)

## Aligns (strong)

1. **Kernel / pod / recipe split** matches the doctrine: brand-generic jobs in `core/`, vendor vocabulary only in `recipes/`, one `cmd/pod` binary.
2. **YAML recipes** compose kernels (`pod.v1` + `kernel.v1`); wiring is routing not money judgement; Signals cannot be emitted from recipes.
3. **InfinitePay pod set** ships: `acquirer-edge`, `gateway-facade`, `b4b-oversight` (`pod-emi-oversight`), `bank-rails`.
4. **Hard money/control rule** is structural: no `ledger` ⇒ control plane. Oversight and gateway have no ledger (D1 enforced in code + tests).
5. **Bus grammar** (Command / Fact / Signal) with Signal never sole booking authority — matches “webhook ACK ≠ book”.
6. **sanctions ⊥ kyc** as two registered kinds sharing one engine (`gate`) — correct model, shared code.
7. **Boot phases**, ports vs bus participants, `GET /pod` live schematic, conservation invariant on ledger.
8. Docs explicitly defer to Avram for intent; record gaps honestly.

## Drifts / gaps

1. **Dual stack (main UX drift Andrej called out)**  
   Console **Services** = standalone `cmd/*` vendor sims (worldline, b4b, banking-circle, aci, …).  
   Console **Pods** = composable recipes.  
   Docs say pods run *alongside* standalones (`make up` unchanged; pods behind compose profile).  
   Conceptually mocked vendors *are* pods; today the primary “services” UI still presents the monoliths. Pods feel like a second architecture, not the label for the mocks.

2. **13 / 18 kernels built**  
   Missing: `auth-decision`, `capture-presentment`, `scheme-message-codec`, `fraud-scorer`, `fx-rate-book`.  
   Documented as deliberate (principle 7). Deep crypto/JWT/SFTP/Bambora still live in standalones — right homes named (`scheme-message-codec`, transport ports) but not moved.

3. **`pod-ledger-recon` (addressed)**  
   Platform MoR recipe now ships at `recipes/ledger-recon/`. Customer/virtual books only; hints vs Processed evidence split in routes; booking wiring gated on `Processed`. Standalone `settlement` / `receiver` remain until harness parity.

4. **ACI / Worldline depth**  
   Gateway + acquirer *pods* exist as thin compositions; protocol-accurate behaviour still in `cmd/aci`, `cmd/worldline`. Recipes are not yet the runtime that `make harness` proves against.

5. **Naming noise**  
   Recipe dir `b4b-oversight` vs pod name `pod-emi-oversight` is fine; console Services still say “B4B Payments” without pointing at the pod recipe.

## Improvement ideas (ordered)

A. **UI labelling (high value, low risk)**  
   - Rename or subtitle **Services** → “Standalone stack” / “Process catalogue”.  
   - Make **Pods** the primary “mocked vendors (composed of kernels)” view.  
   - On each vendor service card, link “Composable twin: `recipes/…`” when a recipe exists.  
   - Optional: badge pod cards with Worldline / ACI / B4B / BC swap-for names from `simulates:`.

B. **Compose story**  
   - Document a `make up-pods` (or profile-default later) path where harness hits pods.  
   - Don’t delete standalones until a pod passes the same harness assertions for that hop.

C. **Port protocol accuracy into kernels next**  
   - Promote `scheme-message-codec` for AES-GCM / Bambora shapes.  
   - JWT mode on `http-ingress` or thin codec.  
   - Keep SFTP+PGP as a **port/transport**, not a kernel truth.

D. **Optional fold**  
   - Hold `capture-presentment` deferred until issuer pod (as branch docs suggest).  
   - Add `fraud-scorer` when Oversight recipe needs TM ≠ sanctions.

E. **`pod-ledger-recon` shipped**  
   - Thin recipe from existing kernels; deepen protocol later, do not invent new kernels for labelling.

## Implement now?

Confident for **A** (UI clarify + cross-links) on this branch without boiling the ocean.  
Not confident to replace `cmd/worldline|b4b|bankingcircle|aci` with pods in one pass — that needs harness parity first.
