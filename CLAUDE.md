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

- `GEMINI_API_KEY` or `GCP_PROJECT_ID` (one required — direct Gemini API key vs. Vertex AI via project ID)
- `VOICEVOX_API_URL` — VOICEVOX engine endpoint (e.g. `http://localhost:50021`)
- `GOOGLE_APPLICATION_CREDENTIALS` — only if reading/writing `gs://` URIs
- `SLACK_WEBHOOK_URL` — optional; if unset, notifications are a no-op

`--input`/`-i` is a required flag; `--output`/`-o` has no default and errors at runtime if omitted.

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
- **`internal/builder` is the only place that constructs concrete adapters.** `BuildContainer` builds GCS storage → RemoteIO → HTTP client → Notifier → Pipeline (generator + publisher), tracking every opened resource in a `[]io.Closer` so a partial failure during construction cleans up everything already opened. If you add a new external resource here, register it the same way.
- **Pipeline.Execute is the only orchestration point**: generate script → error if empty → publish (WAV + script upload + optional signed URL) → notify success/failure. Notification always fires from a single `defer` in `Execute`, so failure paths don't need to remember to notify.
- **PublishRunner writes two artifacts per run**: the WAV via `Voice.UploadWav`, then a companion file via `Voice.UploadScript` (VoiceAdapter writes this as `<output-basename>.json`, not `.txt` despite older docs/comments — check `internal/adapters/voice.go` if this matters for a change). A signed URL is generated only if the RemoteIO's `URLSigner` is non-nil (GCS); local output never gets a signed URL and that's treated as a soft failure (logged, not returned as an error).
- **AI output is schema-constrained**: `GenerateRunner` calls Gemini with `ResponseMIMEType: application/json` and an explicit `ResponseSchema` (see `internal/runner/schema.go`), then unmarshals directly into `[]domain.ScriptLine`. The schema hardcodes `allowedSpeakers`/`allowedStyles`/`allowedDirections` enums (e.g. speakers are just `ずんだもん`/`めたん`) — these must stay in sync with whatever VOICEVOX speakers/styles are actually available, and any new speaker/style needs an update here, not just in the prompt templates. If you change `ScriptLine`'s fields, update the schema in lockstep.
- **Config resolution order**: CLI flags (cobra, in `opts`) are the base; `initAppPreRunE` (`cmd/root.go`) fills any still-empty fields from environment variables via `config.LoadConfig()` + `FillDefaults`, then `Normalize()` trims whitespace. Flags always win over env vars.
- **`shouni/clibase`** is this project's shared CLI bootstrap library (external module) — `cmd.Execute()` just declares the app name, persistent flags, pre-run hook, and subcommands; clibase handles the actual cobra `Execute()` call and shared init (logging etc.).

## Notable external dependencies (all first-party `github.com/shouni/*` libraries)

- `go-gemini-client` — Gemini/Vertex AI client (structured JSON generation)
- `go-voicevox` — parallel VOICEVOX synthesis engine wrapper; tuned via `defaultMaxParallelSegments`/`defaultSegmentRateLimit`/`defaultSegmentTimeout` in `internal/adapters/voice.go`
- `go-web-reader` — reads `https://` and `gs://` input sources transparently
- `go-remote-io` — local/GCS write + signed URL abstraction (`remoteio.Writer`, `remoteio.URLSigner`, `remoteio.IOFactory`)
- `go-prompt-kit` — loads/renders the embedded prompt templates in `assets/prompts/`
- `clibase` — shared CLI bootstrap (see above)

When touching adapter code, the actual behavior often lives in these external modules rather than in this repo — check `go.mod` for pinned versions before assuming a signature.
