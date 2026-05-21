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

5. **Pull the models you want to serve.** Inside the running
   container:

   ```bash
   docker compose exec ollama ollama pull llama3.1:8b
   docker compose exec ollama ollama pull qwen2.5:14b
   # ...whatever fits your VRAM
   ```

   The agent reports the model list to the gateway on every
   reconnect, so the dashboard learns about new models the next
   time the agent reconnects (or immediately on `docker compose
   restart agent`).

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

The macOS variant runs the agent in docker but expects ollama to
be installed natively (Metal acceleration isn't available through
the docker container). One-time setup on the Mac:

```bash
brew install ollama
brew services start ollama
```

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

- No port is exposed publicly. All traffic is the agent's
  outbound WebSocket to api.everyapi.ai.

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

## License + source

Apache 2.0. Full source at
https://github.com/everyapi-ai/everyapi-edge (mirrored from the
EveryAPI monorepo's `clients/edge/`). Issues + PRs welcome.
