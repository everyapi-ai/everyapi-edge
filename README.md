# EveryAPI Edge Agent

The supplier-side daemon for the EveryAPI BYO-GPU marketplace. Runs
on your GPU machine, connects out to the EveryAPI gateway over a
reverse WebSocket, and serves inference requests by forwarding them
to a local Ollama. No port forwarding, no public IP, no domain
needed — your machine just needs outbound HTTPS to api.everyapi.ai.

## Five-minute onboarding

1. **Get an account.** Sign up at https://everyapi.ai and turn on
   marketplace selling in `/profile`.

2. **Register a node.** From the dashboard go to
   **My channels → New edge node**. Give it a memorable name and
   click create. You'll see a one-time **registration token** —
   copy it (we never show it again).

3. **Run the bundle.** On the machine with the GPU:

   ```bash
   git clone https://github.com/everyapi-ai/everyapi-edge
   cd everyapi-edge
   cp .env.example .env
   # fill in EVERYAPI_NODE_ID + EVERYAPI_REGISTRATION_TOKEN (from step 2)
   docker compose up -d
   ```

4. **Watch the dashboard.** Within ~30 seconds the node row flips
   to `online`. From this point, any buyer routing through your
   channel sends traffic to your GPU.

5. **The installer selects and verifies a model for you.** It probes
   accelerator memory and free disk space, picks a conservative Qwen
   model, pulls it, then runs one short local inference request before
   reporting success. If the first automatic choice cannot run, it
   tries smaller models. To select a particular model yourself, add
   `--model qwen2.5:7b` to the installer command.

   The agent reconnects after model setup, so the gateway receives the
   model list automatically. Do not restart it merely to publish a
   model: the installer already handles the authenticated reconnect.

6. **Open Edge Control Room.** Visit http://127.0.0.1:8421 and
   enter `EVERYAPI_CONSOLE_TOKEN` from `.env`. If you left it blank,
   the agent creates a persistent token at
   `./data/agent/console.token` on first start. From there you can
   download and remove further models, watch active load, inspect recent
   redacted traffic, and read the local agent log — no container
   commands required. It reuses the same memory budget the installer
   probed, so its one-click model choices fit the machine.

   The income card is deliberately receipt-based: it shows only earnings the
   gateway has already settled for this node (the latest 200 receipts are
   replayed after reconnect). It is not an estimate and it is not the seller
   account's total withdrawable balance.

## Hardware

The default `docker-compose.yml` ships an NVIDIA configuration
(needs a recent driver + `nvidia-container-toolkit` on the host).
Two GPU variants are provided alongside it:

| File                          | When to use                         |
|-------------------------------|-------------------------------------|
| `docker-compose.yml`          | NVIDIA — most common case           |
| `docker-compose.rocm.yml`     | AMD Radeon Instinct / RX 7000/6000 with ROCm 5.7+ installed |
| `docker-compose.macos.yml`    | Apple Silicon (M1/M2/M3/M4) — runs ollama natively for Metal |

Pick by filename:

```bash
docker compose -f docker-compose.rocm.yml up -d     # AMD
docker compose -f docker-compose.macos.yml up -d    # macOS
```

The macOS variant runs the agent in docker but runs Ollama natively
(Metal acceleration isn't available through the docker container).
The installer installs Ollama with Homebrew when needed, starts it,
then verifies the selected model. If Homebrew is unavailable, it
stops with the official Ollama download link rather than starting an
Edge node that cannot serve requests.

The agent's `OLLAMA_URL` resolves to `host.docker.internal:11434`
in that file, which Docker Desktop / OrbStack / Colima all expose
on macOS by default.

CPU-only nodes WILL run — the agent connects fine and Ollama
serves from CPU. Throughput will be too low to be commercially
useful for chat workloads, but embeddings can work.

## Security model

- The agent generates an Ed25519 keypair on first run and stores
  it at `./data/agent/identity.json` (mode 0600). The private key
  never leaves your machine. The gateway only ever sees your
  public key and signatures.

- The registration token is one-shot. After your first successful
  connect, even the gateway can't reuse it. Subsequent reconnects
  use a fresh server-issued challenge that you sign with the
  identity from step 1.

- Inference traffic is an outbound WebSocket to api.everyapi.ai.
  The Control Room is published only as `127.0.0.1:8421` and also
  requires a 32+ character local console token. Do not change the
  Compose port binding to a public interface.

- Traffic history keeps only model, endpoint, timing, token counts,
  and a node-scoped opaque customer label. Prompts, responses, API
  keys, email addresses, and internal user IDs are never stored in
  the agent.

- The agent enforces a path whitelist on inbound requests. Even
  if the gateway were compromised, it could not coerce your
  machine into POST'ing to arbitrary local URLs — only the
  OpenAI-compatible /v1/* paths Ollama exposes are accepted.

## Troubleshooting

**Node stays offline after `docker compose up`** — check
`docker compose logs agent`. The most common failures are
`EVERYAPI_NODE_ID` mismatch (copy-paste error from the dashboard)
or an expired registration token (the dashboard's token field
clears after ~24h; re-create the node row).

**"registration token not recognised"** — you tried to reuse a
token. Delete the node from the dashboard, create a new one, copy
the fresh token into `.env`, `docker compose restart agent`.

**GPU not detected** — `docker run --rm --gpus all
nvidia/cuda:12.0.0-base nvidia-smi` is the canonical check. If
that doesn't work, the bundle won't either; fix the host's
nvidia-container-toolkit before debugging the agent.

**Identity loss** — if `./data/agent/identity.json` gets deleted,
the gateway no longer recognises your machine's pubkey. Delete the
node from the dashboard, register a new one. (We don't support
"rebind to existing node id" yet because the threat model treats
identity loss as equivalent to "machine was compromised.")

## What does the agent NOT do?

- It does not phone home with your IP, your username, or anything
  beyond what's in `.env` (gateway URL, node id, supplier-declared
  metadata) plus liveness heartbeats with GPU utilisation.

- It does not auto-update. We don't push image updates without
  your explicit `docker compose pull && docker compose up -d`.

- It does not run arbitrary code. The path whitelist above is
  enforced inside the agent binary, not inside ollama.

## Building the Control Room UI

Building the agent needs **only the Go toolchain**:

```bash
go build ./...
```

That works because the Control Room's compiled UI is committed at
`internal/console/web/index.html` and embedded with `//go:embed`. The
`go:embed` directive requires the asset to exist at compile time, so the build
output is a checked-in artifact rather than something produced on demand.

Its source is the Vite app in `console-web/` (React 19, TanStack Router +
Query, Tailwind v4, Zustand, Zod). Touch anything under `console-web/src/` and
you must rebuild the bundle in the same commit:

```bash
cd console-web
bun install
bun run build       # writes ../internal/console/web/index.html
bun run typecheck
```

CI rejects a pull request whose committed bundle does not match its source, so
a forgotten rebuild fails the build instead of silently shipping the old UI.

For UI work, `bun run dev` (port 5175) proxies `/api` to a locally running
agent on `127.0.0.1:8421`, so the console can be iterated without recompiling
Go. Point it elsewhere with `EDGE_CONSOLE_TARGET`.

The whole app is inlined into that one HTML file — no external script, style or
font requests. The Control Room is meant to work on a machine with no Internet
access beyond the gateway, and a single file keeps the committed artifact
reviewable.

## License + source

Apache 2.0. Full source at
https://github.com/everyapi-ai/everyapi-edge. Issues + PRs welcome.
