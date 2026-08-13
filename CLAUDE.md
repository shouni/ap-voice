# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

**AP Voice** is a Go service (Cloud Run + Cloud Tasks) that turns a document (web article or GCS
object) into a narrated WAV. It reads source text, has Gemini generate a structured narration
script (JSON, speaker/style/text per line), synthesizes that script into a WAV via a VOICEVOX
engine, and writes both the WAV and the script to local disk or GCS.

Module name: `ap-voice` (Go 1.26). One image, deployed as two Cloud Run services (`ap-voice`
public / `ap-voice-worker` private) selected by `SERVER_ROLE`, the same shape as
`ap-comp`/`ap-mv`/`ap-story`.

## Commands

```bash
go build ./...
go vet ./...
gofmt -l .                                          # CI fails on any output
go test ./...
go test ./internal/pipeline/ -run TestName -v       # a single test
go run .                                            # start the server (SERVER_ROLE required)
```

There is no Makefile. `.github/workflows/ci.yml` runs on pushes and PRs to `main`/`develop` in
three jobs: build + `go vet` + `gofmt -l` + `go test -race -cover`, then `golangci-lint`
(config in `.golangci.yml`), then `govulncheck`.

## Required environment

- `SERVER_ROLE` — **required**, one of `web` / `worker` / `both` (`both` is for local
  development). Parsed by `gcp-kit/serverrole` in `Config.normalize`; an empty or unknown value
  fails startup rather than defaulting, because treating unset as `both` would restore the
  worker routes on the public service the moment one env var went missing. It selects which
  dependency graph `builder` assembles and which routes `server.setupRoutes` registers.
- `GEMINI_MODELS` — required. Comma-separated; the first entry is the default model, used when a
  request's `ai_model` is empty (`GenerateRunner.modelFor`). There is deliberately **no default
  model in the code**: model IDs age on Google's release schedule, not this repo's, so a default
  would keep an outdated model in use unnoticed. `ValidateEssentialConfig` fails startup when it
  is empty — do not reintroduce a fallback. Plural matches the fleet convention, where
  `ap-infra`'s `shared_models.tf` is the single source of the spelling.
- `GCP_PROJECT_ID` — required. **Gemini is called via Vertex AI only**; there is no
  `GEMINI_API_KEY` path. On Cloud Run the runtime SA's `roles/aiplatform.user` authenticates, so
  shipping a key would hand out access to a secret nothing reads — and Cloud Run resolves secret
  envs at startup, so an unused one cannot be dropped without a redeploy. Local runs need ADC
  (`gcloud auth application-default login`).
- `TASK_AUDIENCE_URL` / `ALLOWED_TASK_SERVICE_ACCOUNTS` — **required for `worker`/`both`**, not
  read at all by `web`. The audience is the worker's own URL; the allowlist holds the *caller's*
  SA (on a split deployment that is the **web** SA, not the worker's own). Both must be present
  or `auth.TaskVerifier` is fail-closed, so `BuildHandlers` refuses to start rather than let
  every task 401. `TASK_AUDIENCE_URL` falls back to `SERVICE_URL` when unset.
- `VOICEVOX_API_URL` — optional; unset falls back to `http://localhost:50021` (go-voicevox's
  default, with a warning), which is what both local runs and a Cloud Run sidecar want.
- `GCP_LOCATION_ID` — optional, defaults to `global` (ap-voice's Gemini calls have always used
  `global`; the rest of the fleet passes `asia-northeast1`).
- `SERVICE_URL` / `PORT` / `HTTP_TIMEOUT` — optional; default to `http://localhost:8080`, `8080`
  and `60s`.
- `SLACK_WEBHOOK_URL` — optional; if unset, notifications are a no-op.
- `GOOGLE_APPLICATION_CREDENTIALS` — only if reading/writing `gs://` URIs.

Per-run values (command, input, output, mode, model, script) are **not** environment variables —
they arrive as the JSON body of a Cloud Tasks request and are decoded into a `domain.Request`.

## Architecture

A small, strictly-layered dependency-injection pipeline. `README.md` has the mermaid sequence
diagram for the full call graph; the summary here is the mental model to keep while editing.

```
main.go         logger setup -> config.LoadConfig -> ValidateEssentialConfig -> server.Run
  -> internal/server    chi router + graceful shutdown; routes are registered per role
  -> internal/builder   wires everything together (DI root, no business logic)
       -> internal/app        Container: Config, RemoteIO, HTTPClient, Notifier, Pipeline
       -> internal/pipeline   orchestrates resolve script -> publish -> notify
            -> internal/runner     GenerateRunner (script gen) and PublishRunner (voice + upload)
                 -> internal/adapters   wrappers over external libraries (Gemini, VOICEVOX, Slack, prompts)
       -> internal/domain      ports (Pipeline, Voice, Notifier) and models (Request, ScriptLine)
assets/         embedded prompt templates loaded via go:embed
```

### Key invariants

- **`internal/domain` is dependency-free** — ports (interfaces) and plain data models only.
  Adapters implement these ports; runners and pipeline depend only on the interfaces, never on
  concrete adapter types. New external integrations become a new adapter + port, not a change to
  `domain`.
- **`internal/builder` is the only place that constructs concrete adapters.** `BuildContainer`
  builds GCS storage → RemoteIO → HTTP client → Notifier → Pipeline, tracking every opened
  resource in a `[]io.Closer` so a partial failure during construction cleans up what was already
  opened. Register new external resources the same way. `app.RemoteIO` is a type alias for
  `remoteio.Bundle` — go-remote-io owns the struct and its assembly (`remoteio.NewBundle`).
- **Only the worker builds the pipeline.** The web role skips it, so the public service holds no
  Gemini client and never opens a VOICEVOX connection — `voicevox.New` calls `/speakers` at
  construction, so building it on the public side would make every cold start wait on the engine.
- **A new role never means touching the router.** `BuildHandlers` leaves the handlers a role does
  not serve as nil and `setupRoutes` guards each route group on nil, so `SERVER_ROLE=web` simply
  has no `/tasks/generate` — it 404s rather than existing unprotected. `AppHandlers.Validate`
  rejects the half-built case (`TaskAuth` without `Worker`, or vice versa) at startup, because the
  router would otherwise turn a DI mistake into a silent 404 indistinguishable from a config
  mistake. `router_test.go` pins all three states.
- **The worker handler is `gcp-kit/worker`, not hand-written.** `worker.NewHandler[domain.Request]`
  takes anything with `Execute(ctx, T) error`, which `pipeline.Pipeline` already satisfied, so
  JSON decoding, body-size limits and Cloud Tasks retry metadata come from the kit.
  `domain.Request` doubles as the task payload; its `json` tags are the wire contract with
  whatever enqueues it.
- **`Pipeline.Execute` is the only orchestration point**: validate → resolve script → publish
  (WAV + script upload + optional signed URL) → notify. Notification fires from a single `defer`,
  so failure paths do not have to remember to notify.
- **Script generation and synthesis are separate commands**, and only `resolveScript` differs:
  `generate` reads the source and calls Gemini, `synthesize` takes `Request.Script` as-is and
  never touches Gemini. The script is an *output and an input* — `PublishRunner` writes it as
  `<output>.json` next to the WAV, and fixing one line's reading or speaker should not mean
  paying for a regeneration that rewrites every other line. `Request.Command` has **no default**:
  an empty command is an error, because silently treating it as `generate` would discard a
  caller's `script` and bill them for generation. `Request.Validate` lives in `domain` so the web
  form can reuse it, and runs before anything external is touched
  (`TestPipelineExecute_InvalidRequest`).
- **`PublishRunner` writes two artifacts per run**: the WAV via `Voice.UploadWav`, then the script
  as `<output-basename>.json` via `Voice.UploadScript`. A signed URL is generated only when the
  RemoteIO's `URLSigner` is non-nil (GCS); local output never gets one and that is a soft failure
  (logged, not returned).
- **Prompt modes are file-driven.** `assets/assets.go` embeds `prompts/prompt_*.md` and
  `go-prompt-kit` keys them by the part after `prompt_`, so **dropping in
  `assets/prompts/prompt_<mode>.md` adds a `mode` with no code change**. The mode string travels
  from `Request.Mode` straight to `PromptAdapter.Generate` and is never validated against a list.
- **AI output is schema-constrained, and the speaker vocabulary is not owned here.**
  `GenerateRunner` calls Gemini with `ResponseMIMEType: application/json` and an explicit
  `ResponseSchema` (`internal/runner/schema.go`), then unmarshals straight into
  `[]domain.ScriptLine`. `allowedSpeakers`/`allowedStyles` are **derived at init from
  `go-voicevox/speaker`** (`SupportedSpeakerNames()` / `SupportedStyleNames()`) precisely so the
  enums cannot drift from what the engine will accept — **adding a speaker or style is a change to
  go-voicevox, not to this file**. Only `allowedDirections` is defined locally, because
  `direction` is a video-production tag with no VOICEVOX counterpart. Which speaker/style pairing
  suits which mode is left to the prompt text, not the schema. If you change `ScriptLine`'s
  fields, update the schema in lockstep.
- **Config and per-run values are separate types.** `config.Config` holds only what the deployment
  target decides, read from the environment with `caarlos0/env` struct tags and grouped into
  sub-structs (`Server`, `Tasks`, `GCP`, `AI`, `Voicevox`, `Notification`, `HTTP`) the way the
  sibling apps group theirs. `LoadConfig` → `normalize()` (parses `SERVER_ROLE`, trims, dedupes
  lists, fills defaults) → `ValidateEssentialConfig()`, whose worker-only checks are skipped for
  `web`.

## Notable external dependencies

First-party (`github.com/shouni/*`):

- `go-gemini-client` — Gemini/Vertex AI client (structured JSON generation)
- `go-voicevox` — parallel VOICEVOX synthesis wrapper, **and the source of the supported speaker
  and style vocabulary**; tuned via `defaultMaxParallelSegments`/`defaultSegmentRateLimit`/
  `defaultSegmentTimeout` in `internal/adapters/voice.go`
- `go-web-reader` — reads `https://` and `gs://` input sources transparently
- `go-remote-io` — local/GCS write + signed URL abstraction (`remoteio.Bundle`, `remoteio.Writer`,
  `remoteio.URLSigner`, `remoteio.IOFactory`)
- `go-prompt-kit` — loads and renders the embedded prompt templates
- `go-http-kit` — HTTP client with retries; note `builder` passes
  `WithSkipNetworkValidation(true)`, which disables the SSRF guard for the whole client
- `gcp-kit` — `serverrole` (role vocabulary), `worker` (Cloud Tasks target handler), `auth`
  (Cloud Tasks OIDC verification), `cloudlog` (Cloud Logging format + trace correlation)

Third-party: `go-chi/chi` (routing), `caarlos0/env` (environment → config struct).

When touching adapter code the actual behavior often lives in these modules rather than in this
repo — check `go.mod` for pinned versions before assuming a signature.
