# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

**AP Voice** is a Go service (Cloud Run + Cloud Tasks) that turns a document (web article or GCS
object) into a narrated WAV. It reads source text, has Gemini generate a structured narration
script (JSON: a title plus speaker/style/text per line), and synthesizes that script into a WAV
via a VOICEVOX engine.

**Those two halves are separate jobs.** `generate` stops at the script; the operator reads it in
the web UI and then triggers `synthesize`, which is the only command that produces audio. The
reason is that a script is an output *and* an input: fixing one line's reading should not cost a
regeneration that rewrites every other line, nor minutes of synthesis on a draft that is about to
change.

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
(config in `.golangci.yml`), then `govulncheck`. `cloudbuild.yaml` builds the image with BuildKit
caching and deploys it to **both** Cloud Run services; `Dockerfile` produces a `scratch` image
holding only the static binary, certs and zoneinfo — the prompts, `speakers.json` and the kagome
dictionary are all compiled in (~54 MB), so nothing else needs to be copied.

## Environment

`README.md` has the full table with defaults. Listed here are only the ones carrying a rule that
is easy to break by editing.

- `SERVER_ROLE` — **required**, one of `web` / `worker` / `both` (`both` is for local
  development). Parsed by `gcp-kit/serverrole` in `Config.normalize`; an empty or unknown value
  fails startup rather than defaulting, because treating unset as `both` would restore the
  worker routes on the public service the moment one env var went missing. It selects which
  dependency graph `builder` assembles and which routes `server.setupRoutes` registers.
- `GEMINI_MODELS` — required. Comma-separated; the first entry is the default model, used when a
  request's `ai_model` is empty (`ScriptStep.modelFor`). There is deliberately **no default
  model in the code**: model IDs age on Google's release schedule, not this repo's, so a default
  would keep an outdated model in use unnoticed. `ValidateEssentialConfig` fails startup when it
  is empty — do not reintroduce a fallback. Plural matches the fleet convention, where
  `ap-infra`'s `shared_models.tf` is the single source of the spelling.
- `GCP_PROJECT_ID` — required. **Gemini is called via Vertex AI only**; there is no
  `GEMINI_API_KEY` path. On Cloud Run the runtime SA's `roles/aiplatform.user` authenticates, so
  shipping a key would hand out access to a secret nothing reads — and Cloud Run resolves secret
  envs at startup, so an unused one cannot be dropped without a redeploy. Local runs need ADC
  (`gcloud auth application-default login`).
- `CLOUD_TASKS_QUEUE_ID` / `WORKER_URL` / `TASK_CALLER_SERVICE_ACCOUNT_EMAIL` and the OAuth set
  (`GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `SESSION_SECRET`, `SESSION_ENCRYPT_KEY`,
  `ALLOWED_EMAILS`/`ALLOWED_DOMAINS`) — **required for `web`/`both`**, and not read by `worker`.
  The caller SA is the one Cloud Tasks is told to mint an OIDC token *as*; the worker's
  `ALLOWED_TASK_SERVICE_ACCOUNTS` is the receiving end of the same pair. `SESSION_ENCRYPT_KEY`
  must be 16/24/32 bytes (AES) and `SESSION_SECRET` at least 16 — the first is checked in
  `validateWebConfig`, the second by `gcp-kit/auth` when the handler is built.
- `GCP_LOCATION_ID` — the **Cloud Tasks queue region** (`asia-northeast1`), *not* the Vertex AI
  endpoint. Vertex is pinned to `global` in `adapters.defaultVertexLocationID`, the same split
  ap-comp and ap-story make; feeding the queue region to Vertex points it at an endpoint that
  does not exist.
- `TASK_AUDIENCE_URL` / `ALLOWED_TASK_SERVICE_ACCOUNTS` — **required for `worker`/`both`**, not
  read at all by `web`. The audience is the worker's own URL; the allowlist holds the *caller's*
  SA (on a split deployment that is the **web** SA, not the worker's own). Both must be present
  or `auth.TaskVerifier` is fail-closed, so `BuildHandlers` refuses to start rather than let
  every task 401. `TASK_AUDIENCE_URL` falls back to `SERVICE_URL` when unset.
- `VOICEVOX_MAX_PARALLEL_SEGMENTS` / `VOICEVOX_SEGMENT_RATE_LIMIT` / `VOICEVOX_SEGMENT_TIMEOUT` —
  optional; `8`, `500ms`, `120s`. Throughput is `min(1/rate, parallel ÷ time-per-segment)`, and
  **measurement says the second term is the binding one**: a 12-segment job took 50s at 0.24
  segments/sec while the limiter allowed 2.0/sec, so the rate limit never came into play and is
  not a usable knob today. CPU did not saturate either (2.31 of 5 allocated vCPU, memory 0.94 of
  4 GiB), so the limit sits inside the engine and **neither raising nor lowering the parallelism
  has a predictable effect — measure before changing it.** What parallelism does cost is engine
  memory, and note that the peak does not grow with script length: the in-flight count is capped,
  so a 200-line script has the same memory peak as a 12-line one and only runs longer. They are
  env vars rather than constants because the engine's size is decided in `ap-infra`, not here, so
  throttling to fit it should not need a rebuild. Note this is **unrelated** to Cloud Run's
  `max_instance_request_concurrency = 1`, which counts *jobs* per instance, not segments per job.
  go-voicevox logs per-segment avg/min/max at the end of each batch — read that before touching
  any of the three.
- `PIPELINE_TIMEOUT` / `TASK_DISPATCH_DEADLINE` — optional; default to `25m` and `30m`. These are
  the top two rungs of the fleet's timeout ladder
  (`PIPELINE_TIMEOUT` < dispatch deadline <= Cloud Run timeout). **The smallest wins**, so the
  point of the app's own limit is to give up *before* Cloud Tasks does — otherwise the process is
  SIGTERMed mid-job and the failure notification never fires, and with `max_attempts = 1` the job
  is simply lost. `ValidateEssentialConfig` rejects a configuration that inverts them.
  `Pipeline.Execute` applies the limit, and deliberately keeps a **separate, un-cancelled context
  for notifications** — reusing the timed-out one would silence the very notification the ladder
  exists to deliver (`TestPipelineExecute_TimesOutAndStillNotifies`). That test also pins the
  error chain: the failure handed to the notifier must still satisfy
  `errors.Is(err, context.DeadlineExceeded)`, because `SlackAdapter` gives a timeout its own
  heading and guidance. One `%v` where a `%w` belongs, anywhere between the engine and the
  pipeline, silently turns a timeout back into an ordinary failure.

Per-run values (command, job ID, input, output, mode, model, script) are **not** environment
variables — they are a `domain.Request`, built by the web form or posted as the JSON body of a
Cloud Tasks request.

## Architecture

A small, strictly-layered dependency-injection pipeline. `README.md` has the mermaid sequence
diagram for the full call graph; the summary here is the mental model to keep while editing.

```
main.go         logger setup -> config.LoadConfig -> ValidateEssentialConfig -> server.Run
  -> internal/server    chi router + graceful shutdown; routes are registered per role
       -> .../handlers        web face: form, history, detail, synthesize, delete, audio
  -> internal/builder   wires everything together (DI root, no business logic)
       -> internal/app        Container: Config, RemoteIO, HTTPClient, Notifier, Pipeline, Speakers
       -> internal/pipeline   orchestrates resolve script -> publish -> notify
            ScriptStep (Gemini) and PublishStep (synthesis + upload)
                 -> internal/adapters   wrappers over external libraries (Gemini, VOICEVOX, Slack, prompts)
       -> internal/repository  reads back what was written (history list, stored script, delete)
       -> internal/domain      ports (Pipeline, Voice, Notifier, ScriptStore, TaskQueue),
                               models (Request, Script, ScriptLine) and StorageLayout
assets/         embedded prompts, speakers.json, HTML templates and static files (go:embed)
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
- **The web role never runs a job — it enqueues and it reads.** `handlers.Handler` validates the
  form with the same `Request.Validate` the worker runs and hands it to `domain.TaskQueue`; it
  never waits for synthesis, which takes minutes. It does read GCS directly for the history
  screens (`internal/repository`), which is why the web SA needs `objectUser` and not just
  enqueue rights. The form's mode list is read from the embedded prompts, so a mode on screen is
  always a mode the worker can render. `Auth`/`Web` are a pair in `AppHandlers.Validate` for the
  same reason `TaskAuth`/`Worker` are — a missing `Auth` would publish the form unauthenticated.
- **Both `Middleware` and `CSRFContextMiddleware` are required on the authenticated group.** The
  first checks the token, the second mints it; registering only the first means no form ever
  carries a valid token and every POST is rejected. Every `method="post"` needs the
  `csrf_token` hidden field — `templates_test.go` counts them, since a missing one looks like a
  perfectly normal page until someone submits it.
- **`internal/repository` serves the history screens** — `List`, `Load`, `HasAudio`, and
  `Delete`, which removes the whole job prefix rather than a fixed list of names. `List` sorts
  and truncates **before** filling in titles, so a page of 50 costs 50 object reads no matter how
  many jobs exist; doing it the other way round made every page view scale with the bucket.
  A script that will not parse leaves the job listed under its ID, so a broken job can still be
  deleted. `HasAudio` asks storage whether the object exists rather than searching a listing —
  the listing is capped, so any job past the cap reported no audio and lost its player.
- **Templates are only evaluated at request time.** A renamed view field still compiles, so
  `internal/server/handlers/templates_test.go` renders every screen with the real view structs
  (a `map` would turn a missing key into `<no value>` and pass).
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
  so failure paths do not have to remember to notify. **There are two outcomes, not three** — the
  sibling apps notify a "skipped" case for input that has not changed, and ap-voice carried the
  whole path (port method, pipeline helper, Slack title) with nothing able to trigger it. Note
  `deadcode` reports such a path as live, because its tests count as callers.
- **Script generation and synthesis are separate commands**, and `Pipeline.Execute` branches on
  `Command` twice — once in `resolveScript`, once in `publish`:
  - `generate` — reads the source, calls Gemini, and stops at `PublishStep.PublishScript`, which
    writes the script only. **It produces no audio and returns no signed URL**, deliberately:
    signing does not check that the object exists, so signing a WAV that was never made hands out
    a 404 link in the Slack notification.
  - `synthesize` — never touches Gemini. It uses `Request.Script` when present and otherwise
    loads the stored script by `JobID` (`domain.ScriptStore`). The web face always takes the
    second path: **the script is not carried in the task payload**, because a long one can reach
    Cloud Tasks' 1MB limit. `PublishStep.Run` writes the WAV *and* rewrites the script, so an
    edited script cannot drift from the audio that was actually spoken.

  `Request.Command` has **no default**: an empty command is an error, because silently treating it
  as `generate` would discard a caller's `script` and bill them for generation. `Request.Validate`
  lives in `domain` so the web form can reuse it, and runs before anything external is touched
  (`TestPipelineExecute_InvalidRequest`).
- **`domain.StorageLayout` owns every object name**, and artifacts live under one prefix per job
  (`voice/<jobID>/audio.wav`, `.../audio.json`). Callers never choose paths — the web form has no
  output field, and `Handler.Enqueue` derives the URI from the job ID. That is what lets
  `repository` list and delete a job without knowing what it contains. The script's location is
  the audio name with a swapped extension, and **`ScriptURIFor` is the only place that rule
  lives** — the writer derives it from the audio URI, the reader from the job ID, and both go
  through it. They used to compute it separately, which would have saved to a path nothing reads
  if either changed (`TestScriptPathAndScriptURIForAgree`).
- **A signed URL is only ever a bonus.** It is generated when the RemoteIO's `URLSigner` is
  non-nil (GCS); local output never gets one and that is a soft failure (logged, not returned).
- **Prompt modes are file-driven.** `assets/assets.go` embeds `prompts/*.md` and
  `go-prompt-kit` keys them by filename, so **dropping in `assets/prompts/<mode>.md` adds a
  `mode` with no code change** (the directory already says they are prompts, so filenames carry
  no prefix — same as the sibling apps). Each file opens with a YAML front matter block
  (`label` / `direction` / `use_when`) that supplies the form's option text and the description
  under the select — the same arrangement as ap-comp, so **the explanation lives next to the
  prompt it explains** rather than in a list the form owns. `assets/modes.go` splits it:
  `LoadModes` reads the metadata, and **`LoadPrompts` returns the body only** — leaving the front
  matter in would slip YAML into the top of the instruction text, and the run would still
  succeed, so nothing would flag it. A prompt with no front matter still appears, labelled by its
  key. The mode string travels from `Request.Mode` straight to
  `PromptAdapter.Generate` and is never validated against a list. The one exception is `promo`,
  named in `adapters/prompt.go` because it is the only mode whose *input type* differs: it reads
  ap-comp's `recipe.json` and decodes it into a `music.Recipe` before rendering.
- **`assets/speakers.json` is the speaker vocabulary**, and it is this app's file, not
  go-voicevox's. It is the engine's `/speakers` response saved verbatim (pretty-printed so engine
  updates produce a readable diff); refresh it with the curl in `assets/assets.go`. `builder`
  turns it into a `speaker.Registry` before opening any connection, and the same registry feeds
  both the Gemini schema and `voicevox.New`. **Style IDs in that file are never used** — go-voicevox
  re-reads them from the live engine, since they shift between engine builds.
- **AI output is schema-constrained.** `ScriptStep` calls Gemini with
  `ResponseMIMEType: application/json` and a `ResponseSchema` built once at construction from the
  registry (`internal/pipeline/schema.go`), then unmarshals straight into a `domain.Script` —
  an object (`{title, lines}`), not a bare array, so the history list has something to show
  besides job IDs. The title comes from the same call; there is no second request for it.
  `speaker` and `style` are **independent enums**, so the schema cannot express "this speaker only
  has these styles" — an impossible pairing is not rejected, `getStyleID` quietly falls back to
  that speaker's default and the instruction is ignored. Per-speaker and per-mode constraints
  therefore live in the prompt text, and **every prompt currently pins `style` to `"ノーマル"`**,
  so the enum's width does not matter in practice. If you change `ScriptLine`'s fields, update the
  schema in lockstep.
- **The prompts carry all the expressive work.** With one fixed style, nothing about the audio
  varies except the words, so each prompt says so and asks for short sentences, restatement and
  questions instead. They also spell out what a TTS script needs and a written one does not:
  katakana for acronyms, no symbols or bullet lists, unambiguous numbers. If you loosen the style
  rule, drop the compensating instructions with it — otherwise the two pull against each other.
- **There is no `direction` field.** It was an emotion tag for downstream video production that
  nothing ever read — not the engine, not any sibling app. Styles now carry the emotion and
  actually change the audio, so the tag was removed rather than left as a field the AI spends
  tokens filling in.
- **Config and per-run values are separate types.** `config.Config` holds only what the deployment
  target decides, read from the environment with `caarlos0/env` struct tags and grouped into
  sub-structs (`Server`, `Tasks`, `GCP`, `AI`, `Voicevox`, `Notification`, `HTTP`) the way the
  sibling apps group theirs. `LoadConfig` → `normalize()` (parses `SERVER_ROLE`, trims, dedupes
  lists, fills defaults) → `ValidateEssentialConfig()`, whose worker-only checks are skipped for
  `web`.

## Notable external dependencies

First-party (`github.com/shouni/*`):

- `go-gemini-client` — Gemini/Vertex AI client (structured JSON generation)
- `go-voicevox` — parallel VOICEVOX synthesis wrapper. It parses `/speakers` but ships no roster;
  the vocabulary is this repo's `assets/speakers.json` (see above). Throughput comes from
  `config.Voicevox`, not from constants here.
- `go-web-reader` — reads `https://` and `gs://` input sources transparently
- `go-remote-io` — local/GCS read/write + signed URL abstraction (`remoteio.Bundle`,
  `remoteio.Writer`, `remoteio.URLSigner`, `remoteio.IOFactory`)
- `go-prompt-kit` — loads and renders the embedded prompt templates
- `go-http-kit` — HTTP client with retries; note `builder` passes
  `WithSkipNetworkValidation(true)`, which disables the SSRF guard for the whole client
- `go-notify` — Slack message assembly (`notify.Pipeline`, `notify.Body`); `adapters/slack.go`
  only decides *what* to say
- `go-utils/jobid` — issues and validates job IDs. **Never sort job IDs lexically** — the prefix
  outranks the timestamp; use `jobid.SortKey`.
- `gcp-kit` — `serverrole` (role vocabulary), `worker` (Cloud Tasks target handler), `auth`
  (OAuth login, CSRF, Cloud Tasks OIDC verification), `tasks` (enqueue), `cloudlog`
  (Cloud Logging format + trace correlation)

Third-party: `go-chi/chi` (routing), `caarlos0/env` (environment → config struct),
`gopkg.in/yaml.v3` (prompt front matter).

When touching adapter code the actual behavior often lives in these modules rather than in this
repo — check `go.mod` for pinned versions before assuming a signature.
