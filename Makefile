# fintechlab-simple — local compose lab
SHELL := /bin/sh

COMPOSE ?= $(shell if docker compose version >/dev/null 2>&1; then echo "docker compose"; \
	elif podman compose version >/dev/null 2>&1; then echo "podman compose"; \
	elif command -v docker-compose >/dev/null 2>&1; then echo "docker-compose"; \
	else echo "docker compose"; fi)

# Plain single-container engine (not compose) for `make harness-docker`,
# which attaches one container to the already-running compose network
# instead of being a compose service itself — compose "profiles" would be
# the normal way to keep a batch job out of `make up`, but podman-compose
# (a real target here) does not implement profiles at all, so a harness
# compose service would either always run on `up` or need profiles it
# can't see. A plain `run --network` sidesteps that entirely.
ENGINE ?= $(shell if docker version >/dev/null 2>&1; then echo docker; \
	elif podman version >/dev/null 2>&1; then echo podman; \
	else echo docker; fi)

# podman-compose 1.x does not implement Compose "profiles".
COMPOSE_IS_PODMAN := $(findstring podman,$(COMPOSE))

.PHONY: help certs sftp-dirs key-dirs console-dir up start down logs test vet fmt-check demo-payment console harness harness-docker tidy rebuild

help:
	@echo "make up             generate certs if needed, compose up"
	@echo "make console        open the control panel (http://127.0.0.1:8090)"
	@echo "make demo-payment   create a payment and wait for the webhook"
	@echo "make harness        run the full scenario harness locally (go run)"
	@echo "make harness-docker build+run the harness as one container against the compose network"
	@echo "make test           unit tests + vet + gofmt check (no Docker required)"
	@echo "make start          bring the lab back up without rebuilding"
	@echo "make down           compose down"
	@echo "make rebuild        no-cache rebuild of the console image"
	@echo "                    if Pods breaks after pull (/recipes missing): make rebuild && make up"

certs:
	@mkdir -p certs
	@CERTS_DIR=$$(pwd)/certs ./ca/generate.sh

# Host directories shared into settlement/worldline as bind mounts (so a
# human can `ls sftp/out` directly). Pre-created here, world-writable: the
# two containers run as different non-root UIDs (65532) that don't own
# whatever UID podman/docker would otherwise auto-create these as, and
# unlike certs/ these hold no secrets — same fake-and-obvious-lab tradeoff
# as ca/generate.sh's cert permissions (see docs/security/ca-and-tls.md).
# Non-recursive: subdirectories/files the containers create underneath are
# their own responsibility (see internal/sftp's Stage(), which already
# creates 0o777) — recursing here would try to chmod paths a container UID
# owns, which the host user can't do and isn't root, failing the whole
# target on every `make up` after the first.
sftp-dirs:
	@mkdir -p sftp/out sftp/staging sftp/outbound sftp/inbound sftp/config sftp/archive
	@chmod 777 sftp sftp/out sftp/staging sftp/outbound sftp/inbound sftp/config sftp/archive

# Same rationale as sftp-dirs, for the two new generated-keypair directories
# (docs/ARCHITECTURE-vendor-corrections.md Addendum sections D/E): b4b
# generates its JWT keypair into b4b-keys, settlement reads it back to sign
# outgoing calls; worldline generates its SSH host key + PGP keypair into
# wlsftp-keys, and settlement and the harness read the PGP private key back
# to decrypt what they download over the real SFTP+PGP channel. World-writable, non-secret
# (lab-only, gitignored, never real key material) -- same tradeoff as certs.
key-dirs:
	@mkdir -p b4b-keys wlsftp-keys
	@chmod 777 b4b-keys wlsftp-keys

# The console's merchant registry. A host bind mount rather than a named
# volume (unlike settlement-data) because merchants you create in the UI
# are lab data a human wants to read, diff and delete -- `cat
# console-data/registry.json` is the whole state. Same world-writable
# tradeoff as the directories above: the container runs as UID 65532.
console-dir:
	@mkdir -p console-data
	@chmod 777 console-data

# B4B's own state: every company it has boarded, its people, their extended
# profile, the beneficiaries and the payments. Same bind-mount-and-777
# tradeoff as console-data, and for the same reason -- `cat
# b4b-data/state.json` is the whole of what the payout rail remembers, and
# a boarded merchant that vanishes on restart makes the rail untestable.
b4b-dir:
	@mkdir -p b4b-data
	@chmod 777 b4b-data

# Why `up` tears down first under podman.
#
# podman-compose 1.x does not replace a container when its image changes:
# `up --build` builds a fresh binary and then keeps serving the old one. The
# failure is silent and it points somewhere else — a request body or a
# config field looks wrong when the truth is that the parser is older than
# the thing it is parsing.
#
# --force-recreate is not the fix. It removes and creates in one pass and
# races itself ("container name is already in use"), and it cannot replace a
# service others depend on ("has dependent containers which must be removed
# before it"). Both failures are noisy and partial, which is worse than
# either working or not.
#
# So: build, down, up. It costs a few seconds — the image layers are cached,
# only the containers are replaced — and it always runs what you just built.
# Named volumes survive `down`, so settlement-data and the console registry
# are not lost.
ifeq ($(COMPOSE_IS_PODMAN),podman)
COMPOSE_UP = $(COMPOSE) build && $(COMPOSE) down && $(COMPOSE) up -d
else
COMPOSE_UP = $(COMPOSE) up -d --build
endif

up: certs sftp-dirs key-dirs console-dir b4b-dir
	$(COMPOSE_UP)
	@echo "lab is up. payment-api :8080  bank :8081  notifier :8082  receiver :8443"
	@echo "         settlement :8083  worldline :8084 (+ SFTP+PGP :2222)  banking-circle :8085 (+ internal :8095)  b4b :8086  aci :8087  verify :8088"
	@echo ""
	@echo "control panel: http://127.0.0.1:8090   (make console)"
	@echo "next: make demo-payment  (or: make harness)"


# The console embeds a Go binary and its web assets. --no-cache is for when
# a warm layer cache means `up --build` quietly reuses the old one.
rebuild:
	$(COMPOSE) build --no-cache console
	@echo "rebuilt console (no cache). next: make up"

# start is `up` without the build step: it still replaces the containers,
# so the lab reaches a known state, but it does not recompile anything. Use
# it when you have changed no Go code and just want the stack back.
#
# It tears down first for the same reason `up` does — `up -d` against a
# stack that is already running reports "container already exists" once per
# service and changes nothing, which reads like a failure and is not.
ifeq ($(COMPOSE_IS_PODMAN),podman)
COMPOSE_START = $(COMPOSE) down && $(COMPOSE) up -d
else
COMPOSE_START = $(COMPOSE) up -d
endif

start: certs sftp-dirs key-dirs console-dir b4b-dir
	$(COMPOSE_START)
	@echo "lab restarted from the images already built. Use 'make up' after a code change."

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f --tail=100

test: vet fmt-check
	go test ./...

vet:
	go vet ./...

fmt-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:"; echo "$$unformatted"; exit 1; \
	fi

tidy:
	go mod tidy

demo-payment:
	@./scripts/demo-payment.sh

# Best-effort open; prints the URL either way, since a headless or remote
# box has no browser to hand it to.
console:
	@echo "http://127.0.0.1:8090"
	@(xdg-open http://127.0.0.1:8090 >/dev/null 2>&1 || open http://127.0.0.1:8090 >/dev/null 2>&1 || true)

# --- pods ---------------------------------------------------------------
# The kernel/pod architecture (docs/kernels.md). One binary boots any
# recipe, so these targets take a recipe directory rather than a service
# name -- POD=recipes/<name>.

POD ?= recipes/bank-rails





harness:
	@go run ./cmd/harness

harness-docker:
	$(ENGINE) build -f docker/Dockerfile.harness -t fintechlab-simple_harness .
	$(ENGINE) run --rm --network fintechlab-simple_default \
		-e PAYMENT_API_URL=http://payment-api:8080 \
		-e BANK_URL=http://bank:8081 \
		-e NOTIFIER_URL=http://notifier:8082 \
		-e RECEIVER_URL=https://receiver:8443 \
		-e RECEIVER_DELIVERY_URL=https://receiver:8443 \
		-e SETTLEMENT_URL=http://settlement:8083 \
		-e WORLDLINE_URL=http://worldline:8084 \
		-e BANKING_CIRCLE_URL=https://pod-bank-rails:9085 \
		-e BANKING_CIRCLE_INTERNAL_URL=https://pod-bank-rails:9085 \
		-e BANKING_CIRCLE_TARGET=pod \
		-e ACI_URL=http://aci:8087 \
		-e B4B_URL=http://b4b:8086 \
		-e B4B_JWT_PRIVATE_KEY_PATH=/b4b-keys/private.pem \
		-e B4B_JWT_KEY_ID=b4b-mock-1 \
		-e CA_FILE=/certs/ca.pem \
		-e CLIENT_CERT=/certs/client.pem \
		-e CLIENT_KEY=/certs/client-key.pem \
		-e WORLDLINE_SFTP_HOST=worldline \
		-e WORLDLINE_SFTP_PASSWORD=sim-sftp-dev-only \
		-e WORLDLINE_PGP_PRIVATE_KEY_PATH=/wlsftp-keys/worldline_private.asc \
		-v $$(pwd)/certs:/certs:ro \
		-v $$(pwd)/wlsftp-keys:/wlsftp-keys:ro \
		-v $$(pwd)/b4b-keys:/b4b-keys:ro \
		fintechlab-simple_harness
