# EveryAPI Edge Agent

The supplier-side daemon for the EveryAPI BYO-GPU marketplace. Runs
on your GPU machine, connects out to the EveryAPI gateway over a
reverse WebSocket, and serves inference requests through local Ollama,
Diffusers, and Kokoro runtimes. No port forwarding, no public IP, no domain
needed — your machine just needs outbound HTTPS to api.everyapi.ai.

## Five-minute onboarding

1. **Get an account.** Sign up at https://everyapi.ai and turn on
   marketplace selling in `/profile`.

2. **Register a node.** From the dashboard go to
   **My channels → New edge node**. Give it a memorable name and
   click create. You'll see a one-time **registration token** —
   copy it (we never show it again).

3. **Run the installer.** On macOS or Linux:

   ```bash
   curl -fsSL https://dl.everyapi.ai/edge/install.sh | bash -s -- \
     --node-id 5 --token edgert_...
   ```

   On Windows PowerShell:

   ```powershell
   git clone https://github.com/everyapi-ai/everyapi-edge $HOME/everyapi-edge
   & $HOME/everyapi-edge/install.ps1 -NodeId 5 -Token edgert_...
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

6. **Open Edge Control Room.** Visit `http://<node-LAN-IP>:8421` from any device on the same trusted LAN (or `http://127.0.0.1:8421` on the node itself), then enter the pairing token printed by the installer. From there you can download and remove further models, watch active load, inspect recent redacted traffic, and read the local agent log — no container commands required. It reuses the same memory budget the installer probed, so its one-click model choices fit the machine.

   The income card is deliberately receipt-based: it shows only earnings the
   gateway has already settled for this node (the latest 200 receipts are
   replayed after reconnect). It is not an estimate and it is not the seller
   account's total withdrawable balance.

## Hardware

The default `docker-compose.yml` ships an NVIDIA configuration
(needs a recent driver + `nvidia-container-toolkit` on the host).
Two GPU variants are provided alongside it:

Before running a Compose file without the installer, copy `.env.example` to `.env` and generate a 32-byte pairing token. On macOS or Linux:

```bash
openssl rand -hex 32
```

On Windows PowerShell, use the platform CSPRNG:

```powershell
$rng = [Security.Cryptography.RandomNumberGenerator]::Create(); $bytes = New-Object byte[] 32; $rng.GetBytes($bytes); $rng.Dispose(); -join ($bytes | ForEach-Object { $_.ToString('x2') })
```

Paste the output after `EVERYAPI_CONSOLE_TOKEN=`. Compose intentionally refuses to start with an empty pairing token. Keep this value private; it grants browser access to the local Control Room.

| File                          | When to use                         |
|-------------------------------|-------------------------------------|
| `docker-compose.yml`          | NVIDIA — most common case           |
| `docker-compose.rocm.yml`     | AMD Radeon Instinct / RX 7000/6000 with ROCm 5.7+ installed |
| `docker-compose.macos.yml`    | Apple Silicon (M1/M2/M3/M4) — native Ollama + Diffusers + Kokoro MPS |
| `docker-compose.windows.yml`  | Windows 10/11, Docker Desktop WSL2, NVIDIA GPU |

Pick by filename:

```bash
docker compose -f docker-compose.rocm.yml up -d     # AMD
docker compose -f docker-compose.macos.yml up -d    # macOS
docker compose -f docker-compose.windows.yml up -d  # Windows NVIDIA
```

The macOS variant runs the agent in Docker but runs Ollama and the accelerated image, speech, transcription, video, and rerank runtimes natively because Metal/MPS is not available through a Linux container. The installer creates isolated arm64 Python environments, registers those runtimes with launchd, and verifies every configured local runtime before reporting success.

The agent's `OLLAMA_URL` resolves to `host.docker.internal:11434`
in that file, which Docker Desktop / OrbStack / Colima all expose
on macOS by default.

### Upgrade an installer-managed node

Run the same installer command again on the supplier host:

```bash
curl -fsSL https://dl.everyapi.ai/edge/install.sh | bash
```

The installer verifies the existing checkout, reuses its node ID, gateway,
name, supported operator settings, and persisted Ed25519 identity, then
refreshes the bundle and host hardware metadata. It ignores any stale consumed
registration token, does not require a new one, and leaves the existing model
library in place. On Apple Silicon this host-side step is what records unified
physical memory and `darwin/arm64`; the Linux agent container cannot infer
either host value accurately from its own runtime.

The supported image matrix is macOS Apple Silicon/MPS, Linux NVIDIA/CUDA,
Linux AMD/ROCm, and Windows NVIDIA through Docker Desktop WSL2. CPU-only and
Windows DirectML image nodes are rejected explicitly instead of being shown as
image-capable. DirectML can be added later when its PyTorch/Diffusers version
contract supports the selected pipelines reliably.

## Image generation

The image runtime advertises
`Efficient-Large-Model/Sana_600M_1024px_diffusers`, a small Sana 0.6B
text-to-image model suitable for 16 GB-class machines, and keeps the existing
allow-listed Qwen image editors. Buyer requests use the OpenAI-compatible
`POST /v1/images/generations` endpoint. Models download into
`$HOME/.everyapi/edge/images` during runtime startup. Installation can take
several minutes, and the node does not advertise Sana until loading succeeds.

ComfyUI is optional and is not bundled. Diffusers provides the stable generation and editing API; the isolated render adapter can additionally execute operator-installed, read-only ComfyUI workflow templates without exposing arbitrary workflow JSON or the host Docker socket.

## Text APIs

Completion-capable Ollama models are exposed through `POST /v1/chat/completions`, `POST /v1/completions`, and `POST /v1/responses`. Ollama remains stateless, so the gateway implements `previous_response_id` itself: stored response context is AES-256-GCM encrypted, isolated by authenticated user and organization, bounded to 1 MiB, and expires after seven days. Responses storage follows the API default; send `store=false` to disable it. `DELETE /v1/responses/{response_id}` removes owned state, and `POST /v1/responses/compact` asks the selected local model to produce a constrained compact context that replaces the prior state chain.

## Speech

The speech runtime advertises `hexgrad/Kokoro-82M` and serves the OpenAI-compatible `POST /v1/audio/speech` endpoint. Weights and voices are about 330 MB and download into `$HOME/.everyapi/edge/speech` at startup; the node does not advertise the model until every voice it offers is warm, so no buyer request ever waits on a download. Peak VRAM stays under 1 GB, so the speech container shares the card with Ollama and Diffusers rather than needing one of its own.

Buyers can request `mp3`, `wav`, `flac`, or raw `pcm`, and the six stock OpenAI voice names map onto Kokoro voices. English (`af_*`, `am_*`, `bf_*`, `bm_*`) and Mandarin (`zf_*`, `zm_*`) voices are advertised; Kokoro's other locales need grapheme-to-phoneme dependencies that are not in the image yet.

Speech ships in the NVIDIA, ROCm, Windows, and Apple Silicon bundles. On macOS the installer runs Kokoro in a dedicated arm64 Python environment under launchd and the Dockerized agent reaches it through `host.docker.internal:8189`.

The separate Whisper runtime serves `POST /v1/audio/transcriptions` and `POST /v1/audio/translations`. It enforces bounded uploads and duration, preloads the selected model before advertising either capability, and supports cancellation when the buyer disconnects.

## Video and render

Video generation uses the asynchronous `POST /v1/videos` contract with polling, cancellation, restart recovery, bounded output storage, and a node-pinned gateway task record. Render jobs use `POST /v1/render/jobs` and can select only operator-installed workflow templates with typed parameters; buyers cannot submit raw ComfyUI graphs.

## Rerank

The bundled cross-encoder runtime serves `POST /v1/rerank` with `BAAI/bge-reranker-v2-m3`. It accepts at most 100 bounded documents, preloads the revision-pinned model before publishing `text.rerank` as ready, batches scoring, and runs through an admission pool independent from text generation. NVIDIA, ROCm, Windows, and native Apple MPS installers use the same API contract.

## Security model

- The agent generates an Ed25519 keypair on first run and stores
  it at `./data/agent/identity.json` (mode 0600). The private key
  never leaves your machine. The gateway only ever sees your
  public key and signatures.

- The registration token is one-shot. After your first successful
  connect, even the gateway can't reuse it. Subsequent reconnects
  use a fresh server-issued challenge that you sign with the
  identity from step 1.

- Inference traffic is an outbound WebSocket to api.everyapi.ai. The Control Room is published on the node's LAN interface as `:8421` and requires the installer-generated pairing token from remote browsers. Pairing prevents unauthorized browser actions, but the connection still uses plain HTTP: put the node only on a trusted LAN and never expose port 8421 to the Internet. Set `EVERYAPI_CONSOLE_PORT` to use another host port.

- Traffic history keeps only model, endpoint, timing, token counts,
  and a node-scoped opaque customer label. Prompts, responses, API
  keys, email addresses, and internal user IDs are never stored in
  the agent.

- The agent enforces a path-to-runtime whitelist on inbound requests. Even if the gateway were compromised, it could not coerce your machine into sending requests to arbitrary local URLs; only the explicit text, image, speech, transcription, video, render, and rerank runtime paths are accepted.

- Speech voices are allow-listed. Kokoro loads a voice by fetching
  `voices/<name>.pt` from its model repository, so passing the buyer's
  string through would let a request choose what your node downloads.

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

**A runtime stays `warming` forever** — `diffusers`, `speech`,
`transcription`, `video` and `rerank` fetch their weights from
Hugging Face on first start, so on a network that cannot reach
`huggingface.co` the container comes up, serves `/health`, and
sits at `{"status":"warming"}` indefinitely. The real cause is
buried in `docker compose logs <service>` as
`[Errno 101] Network is unreachable`, and nothing in the health
response points at it. Set a mirror in `.env` and restart the
runtime:

```
HF_ENDPOINT=https://hf-mirror.com
```

Every runtime service already passes `HF_ENDPOINT` through, so one
line covers all of them. A node whose models are already in the
mounted cache keeps working without it, which is why this only
shows up on a fresh node or a newly added runtime.

**Identity loss** — if `./data/agent/identity.json` gets deleted, choose **Recover identity** for the existing node in the seller dashboard and run the displayed command on that node within 15 minutes. The one-time recovery token authorizes the installer to create a new local identity while preserving the node, channel, models, and history; once the new identity connects, the old private key is rejected. If the recovery connection fails, the installer restores the previous local identity backup when one exists.

## What does the agent NOT do?

- It does not phone home with your IP, your username, or anything
  beyond what's in `.env` (gateway URL, node id, supplier-declared
  metadata) plus liveness heartbeats with GPU utilisation.

- It does not update silently by default. A seller or authorized platform operator can explicitly choose **Update** in the Edge Control Room or App Dashboard, or the seller can opt this node into automatic agent updates from the Control Room's Agent version settings. Automatic updates start only after the node has connected successfully, wait a random 0–30 minutes before the first check to spread fleet load, and check every 24 hours after that. Disabling the setting stops future scheduled checks; it does not interrupt an update that is already downloading.
  When an update changes host-detected metadata such as Apple Silicon unified
  memory, rerun the installer command above once so the host configuration is
  regenerated; no node re-registration is required.
  The agent then resolves only the official latest stable `edge-v*` release,
  selects the fixed asset for its OS/architecture, verifies it against the
  release's SHA-256 checksum file, and restarts itself. The gateway cannot send
  an arbitrary URL, version, shell command, or downgrade request.

  Existing installations created before Control Room pairing must first generate a token with the platform command above and add it as `EVERYAPI_CONSOLE_TOKEN=<generated token>` in `.env`; then run `docker compose pull && docker compose up -d` once to gain remote-update support.
  After that, the verified binary is stored beside the persistent agent
  identity, so it survives container restarts. A candidate is promoted only
  after it reconnects to the gateway; if it exits before that first successful
  connection, the image's bundled agent rolls it back on the next restart.
  This action updates the Edge Agent only. Model runtimes remain pinned to the Compose images selected by the operator and are upgraded with the normal `docker compose pull && docker compose up -d` workflow; the Control Room never receives access to the host Docker socket.

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
Go. To inspect the latest development UI from another device on the same
trusted LAN, use `bun run dev -- --host 0.0.0.0`, then open
`http://<node-LAN-IP>:5175`. Point the proxy elsewhere with
`EDGE_CONSOLE_TARGET`.

The whole app is inlined into that one HTML file — no external script, style or
font requests. The Control Room is meant to work on a machine with no Internet
access beyond the gateway, and a single file keeps the committed artifact
reviewable.

## License + source

Apache 2.0. Full source at
https://github.com/everyapi-ai/everyapi-edge. Issues + PRs welcome.
