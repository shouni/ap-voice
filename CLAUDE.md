# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

**AP Voice** is a Go service (Cloud Run + Cloud Tasks) that turns a document (web article or GCS object) into a narrated WAV audio file. It reads source text, has Gemini generate a structured narration script (JSON, speaker/style/text per line), then synthesizes that script into a WAV via a VOICEVOX engine, uploading the result (and the script) to local disk or GCS.

Module name: `ap-voice` (Go 1.26). One image, deployed as two Cloud Run services (`ap-voice` public / `ap-voice-worker` private) selected by `SERVER_ROLE`, the same shape as `ap-comp`/`ap-mv`/`ap-story`. It used to be a cobra CLI (`ap-voice generate`); `cmd/` is gone and `main.go` starts the HTTP server.

## Commands

```bash
go build ./...          # build everything
go run .                 # start the server (needs the env below; SERVER_ROLE is mandatory)
go test ./...            # run all tests
go test ./internal/pipeline/... -run TestName -v   # run a single test
go vet ./...
```

There is no Makefile/CI config in the repo — the commands above are the whole workflow.

### Required environment

- `SERVER_ROLE` — **required**, one of `web` / `worker` / `both` (`both` is for local
  development). Parsed by `gcp-kit/serverrole` in `Config.normalize`; an empty or unknown value
  fails startup rather than defaulting, because treating unset as `both` would restore the
  worker routes on the public service the moment one env var went missing. It selects which
  dependency graph `builder` assembles and which routes `server.setupRoutes` registers.

- `GEMINI_MODELS` — required. Comma-separated; the first entry is the default model, used when
  a request's `ai_model` is empty (`GenerateRunner.modelFor`). There is deliberately **no
  default model in the code**: model IDs age on Google's release schedule, not this repo's, so a
  default would keep an outdated model in use unnoticed. `Config.ValidateEssentialConfig` fails
  startup when it is empty — do not reintroduce a fallback. Plural matches the fleet convention
  (`ap-comp`/`ap-mv`/`ap-story`), where `ap-infra`'s `shared_models.tf` is the single source of
  the spelling.
- `TASK_AUDIENCE_URL` / `ALLOWED_TASK_SERVICE_ACCOUNTS` — **required for `worker`/`both`**, and
  not read at all by `web`. The audience is the worker's own URL; the allowlist holds the
  *caller's* SA (on a split deployment that is the **web** SA, not the worker's own). Both must
  be present or `auth.TaskVerifier` is fail-closed, so `BuildHandlers` refuses to start rather
  than let every task 401. `TASK_AUDIENCE_URL` falls back to `SERVICE_URL` when unset.
- `GCP_PROJECT_ID` — required. **Gemini is called via Vertex AI only**; there is no
  `GEMINI_API_KEY` path. On Cloud Run the runtime SA's `roles/aiplatform.user` authenticates,
  so shipping a key would hand out access to a secret nothing reads — and Cloud Run resolves
  secret envs at startup, so an unused one cannot be dropped without a redeploy. Local runs
  need ADC (`gcloud auth application-default login`).
- `GCP_LOCATION_ID` — optional, defaults to `global` (ap-voice's Gemini calls have always used
  `global`; the rest of the fleet passes `asia-northeast1`)
- `HTTP_TIMEOUT` — optional, defaults to `60s`
- `VOICEVOX_API_URL` — VOICEVOX engine endpoint. Optional: unset falls back to
  `http://localhost:50021` (go-voicevox's default, with a warning), which is what both local
  runs and a Cloud Run sidecar want. Env-only by design — there is no flag, because this is a
  value the deployment target decides, not one that changes per run. It reaches the engine via
  `config.Config.VoicevoxAPIURL` → `builder.buildPublishRunner` → `adapters.NewVoiceAdapter`;
  before that chain existed the adapter passed `""` unconditionally and the env var was
  documented but never read.
- `SERVICE_URL` / `PORT` — optional; default to `http://localhost:8080` and `8080`
- `GOOGLE_APPLICATION_CREDENTIALS` — only if reading/writing `gs://` URIs
- `SLACK_WEBHOOK_URL` — optional; if unset, notifications are a no-op

Per-run values (input, output, mode, model) are **not** environment variables — they arrive as
the JSON body of a Cloud Tasks request and are decoded into a `domain.Request`.

## Architecture

This is a small, strictly-layered dependency-injection pipeline. Read `README.md`'s mermaid sequence diagram for the full call graph — the summary here is the mental model to keep while editing:

```
main.go         logger setup -> config.LoadConfig -> ValidateEssentialConfig -> server.Run
  -> internal/server    chi router + graceful shutdown; routes are registered per role
  -> internal/builder   wires everything together (DI root, no business logic)
       -> internal/app        Container struct: holds Config, RemoteIO, HTTPClient, Notifier, Pipeline
       -> internal/pipeline   orchestrates generate -> publish -> notify
            -> internal/runner     GenerateRunner (script gen) and PublishRunner (voice + upload) — the actual use cases
                 -> internal/adapters   concrete implementations wrapping external libraries (Gemini, VOICEVOX, Slack, prompts)
       -> internal/domain      interfaces/ports (Pipeline, Voice, Notifier) and models (Request, ScriptLine) — no implementation, no external deps
assets/         embedded prompt templates (prompt_solo.md, prompt_dialogue.md, prompt_duet.md) loaded via go:embed
```

Key invariants:

- **A role never means touching the router.** `BuildHandlers` leaves the handlers a role does
  not serve as nil, and `setupRoutes` guards each route group on nil, so `SERVER_ROLE=web`
  simply has no `/tasks/generate` — it 404s rather than existing unprotected.
  `AppHandlers.Validate` rejects the half-built case (`TaskAuth` set without `Worker`, or vice
  versa) at startup, because the router would otherwise turn a DI mistake into a silent 404 that
  looks identical to a config mistake. `router_test.go` pins all three states.
- **The worker handler is `gcp-kit/worker`, not hand-written.** `worker.NewHandler[domain.Request]`
  takes anything with `Execute(ctx, T) error`, which `pipeline.Pipeline` already satisfied — so
  body-size limits, JSON decoding, Cloud Tasks retry metadata, and `ErrPermanent` (2xx to stop a
  doomed retry) come from the kit. `domain.Request` doubles as the task payload; its `json` tags
  are the wire contract with whatever enqueues it.
- **Only the worker builds the pipeline** (`BuildContainer`). The web role skips it, so the
  public service holds no Gemini client and never opens a VOICEVOX connection — `voicevox.New`
  calls `/speakers` at construction, so building it on the public side would make every cold
  start wait on the engine.

- **`internal/domain` is dependency-free** — it defines ports (interfaces) and plain data models only. Adapters implement these ports; runners/pipeline depend only on the interfaces, never on concrete adapter types. Preserve this direction when adding features — new external integrations become a new adapter + port, not a change to `domain`.
- **`internal/builder` is the only place that constructs concrete adapters.** `BuildContainer` builds GCS storage → RemoteIO → HTTP client → Notifier → Pipeline (generator + publisher), tracking every opened resource in a `[]io.Closer` so a partial failure during construction cleans up everything already opened. If you add a new external resource here, register it the same way. `app.RemoteIO` is a type alias for `remoteio.Bundle`: the struct and its assembly function used to be duplicated in every app, so go-remote-io took them over — `remoteio.NewBundle(factory)` replaces the local `buildRemoteIO`, and `rio.Writer` / `rio.Signer` read the same as before (`rio.Reader` is now built too, which the previous local version skipped).
- **Pipeline.Execute is the only orchestration point**: generate script → error if empty → publish (WAV + script upload + optional signed URL) → notify success/failure. Notification always fires from a single `defer` in `Execute`, so failure paths don't need to remember to notify.
- **PublishRunner writes two artifacts per run**: the WAV via `Voice.UploadWav`, then a companion file via `Voice.UploadScript` (VoiceAdapter writes this as `<output-basename>.json`, not `.txt` despite older docs/comments — check `internal/adapters/voice.go` if this matters for a change). A signed URL is generated only if the RemoteIO's `URLSigner` is non-nil (GCS); local output never gets a signed URL and that's treated as a soft failure (logged, not returned as an error).
- **AI output is schema-constrained**: `GenerateRunner` calls Gemini with `ResponseMIMEType: application/json` and an explicit `ResponseSchema` (see `internal/runner/schema.go`), then unmarshals directly into `[]domain.ScriptLine`. The schema hardcodes `allowedSpeakers`/`allowedStyles`/`allowedDirections` enums (e.g. speakers are just `ずんだもん`/`めたん`) — these must stay in sync with whatever VOICEVOX speakers/styles are actually available, and any new speaker/style needs an update here, not just in the prompt templates. If you change `ScriptLine`'s fields, update the schema in lockstep.
- **Config and per-run values are separate types.** `config.Config` holds only what the
  deployment target decides (project, models, engine URL, timeouts, webhook) and is read from
  the environment with `caarlos0/env` struct tags, grouped into sub-structs (`GCP`, `AI`,
  `Voicevox`, `Notification`, `HTTP`) the same way `ap-comp`/`ap-mv`/`ap-story` group theirs.
  Values that change per run — input, output, mode, model — arrive as a `domain.Request` in the
  task body. Putting both in one struct is what the old flat `Config` + `FillDefaults` did, and
  it made per-run values look like they came from the environment. `LoadConfig` → `normalize()`
  (parses `SERVER_ROLE`, trims, dedupes lists, fills defaults) → `ValidateEssentialConfig()`,
  whose worker-only checks are skipped for `web`.

## Notable external dependencies (all first-party `github.com/shouni/*` libraries)

- `go-gemini-client` — Gemini/Vertex AI client (structured JSON generation)
- `go-voicevox` — parallel VOICEVOX synthesis engine wrapper; tuned via `defaultMaxParallelSegments`/`defaultSegmentRateLimit`/`defaultSegmentTimeout` in `internal/adapters/voice.go`
- `go-web-reader` — reads `https://` and `gs://` input sources transparently
- `go-remote-io` — local/GCS write + signed URL abstraction (`remoteio.Writer`, `remoteio.URLSigner`, `remoteio.IOFactory`)
- `go-prompt-kit` — loads/renders the embedded prompt templates in `assets/prompts/`
- `gcp-kit` — `serverrole` (SERVER_ROLE vocabulary), `worker` (Cloud Tasks target handler),
  `auth` (Cloud Tasks OIDC verification), `cloudlog` (Cloud Logging format + trace correlation)

When touching adapter code, the actual behavior often lives in these external modules rather than in this repo — check `go.mod` for pinned versions before assuming a signature.
