# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

**AP Voice** is a Go CLI that turns a document (web article or GCS object) into a narrated WAV audio file. It reads source text, has Gemini generate a structured narration script (JSON, speaker/style/text per line), then synthesizes that script into a WAV via a VOICEVOX engine, uploading the result (and the script) to local disk or GCS.

Module name: `ap-voice` (Go 1.26). Single binary, single command: `ap-voice generate`.

## Commands

```bash
go build ./...          # build everything
go run . generate -i <input> -o <output> [-m dialogue|solo|duet] [-g gemini-model]
go test ./...            # run all tests
go test ./internal/pipeline/... -run TestName -v   # run a single test
go vet ./...
```

There is no Makefile/CI config in the repo — the commands above are the whole workflow.

### Required environment for running `generate`

- `GEMINI_MODELS` — required. Comma-separated; the first entry is the default model, and
  `--model`/`-g` overrides it per run. There is deliberately **no default model in the code**:
  model IDs age on Google's release schedule, not this repo's, so a default would keep an
  outdated model in use unnoticed. `Config.ValidateEssentialConfig` (called from the root
  `PreRunE`) fails startup when it is empty — do not reintroduce a fallback. Plural matches the
  fleet convention (`ap-comp`/`ap-mv`/`ap-story`), where `ap-infra`'s `shared_models.tf` is the
  single source of the spelling.
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
- `GOOGLE_APPLICATION_CREDENTIALS` — only if reading/writing `gs://` URIs
- `SLACK_WEBHOOK_URL` — optional; if unset, notifications are a no-op

`--input`/`-i` is a required flag; `--output`/`-o` has no default and errors at runtime if omitted. `--model`/`-g` is optional — `initAppPreRunE` fills it from `GEMINI_MODELS[0]` when unset, and `ValidateEssentialConfig` has already failed startup if that list is empty.

## Architecture

This is a small, strictly-layered dependency-injection pipeline. Read `README.md`'s mermaid sequence diagram for the full call graph — the summary here is the mental model to keep while editing:

```
cmd/            Cobra command definitions + flag parsing (root.go, generate.go)
  -> internal/builder   wires everything together (DI root, no business logic)
       -> internal/app        Container struct: holds Config, RemoteIO, HTTPClient, Notifier, Pipeline
       -> internal/pipeline   orchestrates generate -> publish -> notify
            -> internal/runner     GenerateRunner (script gen) and PublishRunner (voice + upload) — the actual use cases
                 -> internal/adapters   concrete implementations wrapping external libraries (Gemini, VOICEVOX, Slack, prompts)
       -> internal/domain      interfaces/ports (Pipeline, Voice, Notifier) and models (Request, ScriptLine) — no implementation, no external deps
assets/         embedded prompt templates (prompt_solo.md, prompt_dialogue.md, prompt_duet.md) loaded via go:embed
```

Key invariants:

- **`internal/domain` is dependency-free** — it defines ports (interfaces) and plain data models only. Adapters implement these ports; runners/pipeline depend only on the interfaces, never on concrete adapter types. Preserve this direction when adding features — new external integrations become a new adapter + port, not a change to `domain`.
- **`internal/builder` is the only place that constructs concrete adapters.** `BuildContainer` builds GCS storage → RemoteIO → HTTP client → Notifier → Pipeline (generator + publisher), tracking every opened resource in a `[]io.Closer` so a partial failure during construction cleans up everything already opened. If you add a new external resource here, register it the same way. `app.RemoteIO` is a type alias for `remoteio.Bundle`: the struct and its assembly function used to be duplicated in every app, so go-remote-io took them over — `remoteio.NewBundle(factory)` replaces the local `buildRemoteIO`, and `rio.Writer` / `rio.Signer` read the same as before (`rio.Reader` is now built too, which the previous local version skipped).
- **Pipeline.Execute is the only orchestration point**: generate script → error if empty → publish (WAV + script upload + optional signed URL) → notify success/failure. Notification always fires from a single `defer` in `Execute`, so failure paths don't need to remember to notify.
- **PublishRunner writes two artifacts per run**: the WAV via `Voice.UploadWav`, then a companion file via `Voice.UploadScript` (VoiceAdapter writes this as `<output-basename>.json`, not `.txt` despite older docs/comments — check `internal/adapters/voice.go` if this matters for a change). A signed URL is generated only if the RemoteIO's `URLSigner` is non-nil (GCS); local output never gets a signed URL and that's treated as a soft failure (logged, not returned as an error).
- **AI output is schema-constrained**: `GenerateRunner` calls Gemini with `ResponseMIMEType: application/json` and an explicit `ResponseSchema` (see `internal/runner/schema.go`), then unmarshals directly into `[]domain.ScriptLine`. The schema hardcodes `allowedSpeakers`/`allowedStyles`/`allowedDirections` enums (e.g. speakers are just `ずんだもん`/`めたん`) — these must stay in sync with whatever VOICEVOX speakers/styles are actually available, and any new speaker/style needs an update here, not just in the prompt templates. If you change `ScriptLine`'s fields, update the schema in lockstep.
- **Config and per-run values are separate types.** `config.Config` holds only what the
  deployment target decides (project, models, engine URL, timeouts, webhook) and is read from
  the environment with `caarlos0/env` struct tags, grouped into sub-structs (`GCP`, `AI`,
  `Voicevox`, `Notification`, `HTTP`) the same way `ap-comp`/`ap-mv`/`ap-story` group theirs.
  Values that change per run — input, output, mode, model override — live in `cmd`'s unexported
  `options` struct and reach the pipeline as a `domain.Request`. Putting both in one struct is
  what the old flat `Config` + `FillDefaults` did, and it made per-run values look like they
  came from the environment. `LoadConfig` → `normalize()` (trims, dedupes lists, fills
  defaults) → `ValidateEssentialConfig()`; `--model`/`-g` beats `GEMINI_MODELS[0]`.
- **`shouni/clibase`** is this project's shared CLI bootstrap library (external module) — `cmd.Execute()` just declares the app name, persistent flags, pre-run hook, and subcommands; clibase handles the actual cobra `Execute()` call and shared init (logging etc.).

## Notable external dependencies (all first-party `github.com/shouni/*` libraries)

- `go-gemini-client` — Gemini/Vertex AI client (structured JSON generation)
- `go-voicevox` — parallel VOICEVOX synthesis engine wrapper; tuned via `defaultMaxParallelSegments`/`defaultSegmentRateLimit`/`defaultSegmentTimeout` in `internal/adapters/voice.go`
- `go-web-reader` — reads `https://` and `gs://` input sources transparently
- `go-remote-io` — local/GCS write + signed URL abstraction (`remoteio.Writer`, `remoteio.URLSigner`, `remoteio.IOFactory`)
- `go-prompt-kit` — loads/renders the embedded prompt templates in `assets/prompts/`
- `clibase` — shared CLI bootstrap (see above)

When touching adapter code, the actual behavior often lives in these external modules rather than in this repo — check `go.mod` for pinned versions before assuming a signature.
