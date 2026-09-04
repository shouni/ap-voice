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
public / `ap-voice-worker` private) selected by `SERVER_ROLE`, the same shape as the siblings.

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
  development). Parsed by `go-serve-kit/serverrole` in `Config.normalize`; an empty or unknown value
  fails startup rather than defaulting, because treating unset as `both` would restore the
  worker routes on the public service the moment one env var went missing. It selects which
  dependency graph `builder` assembles and which routes `server.registerRoutes` registers.
- `GEMINI_MODELS` — required. Comma-separated; the first entry is the default model, used when a
  request's `ai_model` is empty (`ScriptStep.modelFor`). There is deliberately **no default
  model in the code**: model IDs age on Google's release schedule, not this repo's, so a default
  would keep an outdated model in use unnoticed. `ValidateEssentialConfig` fails startup when it
  is empty — do not reintroduce a fallback. Plural matches the fleet convention; the spelling
  comes from the env var alone, never from a constant here.
- `GCP_PROJECT_ID` — required. **Gemini is called via Vertex AI only**; there is no
  `GEMINI_API_KEY` path. On Cloud Run the runtime SA's `roles/aiplatform.user` authenticates, so
  shipping a key would hand out access to a secret nothing reads — and Cloud Run resolves secret
  envs at startup, so an unused one cannot be dropped without a redeploy. Local runs need ADC
  (`gcloud auth application-default login`).
- `CLOUD_TASKS_QUEUE_ID` / `WORKER_URL` / `TASK_CALLER_SERVICE_ACCOUNT_EMAIL` and the OAuth set
  (`GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `ALLOWED_EMAILS`/`ALLOWED_DOMAINS`) —
  **required for `web`/`both`**, and not read by `worker`. The caller SA is the one Cloud Tasks
  is told to mint an OIDC token *as*; the worker's `ALLOWED_TASK_SERVICE_ACCOUNTS` is the
  receiving end of the same pair.
- **There are no session keys.** The session lives in Firestore and the cookie carries an opaque
  ID, so nothing is signed or encrypted client-side. `SESSION_FIRESTORE_DATABASE` (default
  `sessions`) must name a **different database from the job-status one** — a database name is an
  identifier and cannot be changed later, so sharing leaves one of the two named after the wrong
  thing. The infrastructure owes this database a TTL policy on `expiresAt`; unlike a cookie, a
  stored session does not expire itself.
- `GCP_LOCATION_ID` — the **Cloud Tasks queue region** (`asia-northeast1`), *not* the Vertex AI
  endpoint. Vertex is pinned to `global` in `adapters.defaultVertexLocationID`, the same split the
  siblings make; feeding the queue region to Vertex points it at an endpoint that does not exist.
- `TASK_AUDIENCE_URL` / `ALLOWED_TASK_SERVICE_ACCOUNTS` — **required for `worker`/`both`**, not
  read at all by `web`. The audience is the worker's own URL; the allowlist holds the *caller's*
  SA (on a split deployment that is the **web** SA, not the worker's own). Both must be present
  or `auth.TaskVerifier` is fail-closed, so `BuildHandlers` refuses to start rather than let
  every task 401. `TASK_AUDIENCE_URL` falls back to `SERVICE_URL` when unset.
- `VOICEVOX_MAX_PARALLEL_SEGMENTS` / `VOICEVOX_SEGMENT_RATE_LIMIT` / `VOICEVOX_SEGMENT_TIMEOUT` —
  optional; `4`, `100ms`, `120s`. Throughput is `min(1/rate, parallel ÷ time-per-segment)`, and
  **measurement says the second term is the binding one**: a 12-segment job took 50s at 0.24
  segments/sec while the limiter (then 500ms) allowed 2.0/sec, so the rate limit never came into
  play in that job. **It is not free while it fails to bind, though** — the limiter's burst is 1,
  so `(parallel - 1) x rate` lands on the head of every batch: the old 500ms cost 3.5s at the
  parallelism of the time (8), and would still cost 1.5s at today's 4. And "it never binds"
  turned out to hold only for long scripts. 90 days of the
  worker's own batch logs (29 batches, `segment_duration_*` against the elapsed time) put a
  segment anywhere between 1.0s and 35.3s; **13 of the 29 contained a segment under the 4s
  threshold and 4 averaged under it** — all of them 4-segment jobs that took 4.5-6.4s wall clock
  for 2.0-3.2s of synthesis. It was cut to `100ms` for that reason, and the gain lands on short
  scripts, where the stagger was the larger half of the job. Keep it small enough to be invisible: its only job is to
  avoid opening every connection at once, and lengthening it also widens the window in which a
  `PIPELINE_TIMEOUT` cancellation lands on segments that never started (go-voicevox counts those
  as failures, by design). CPU did not saturate either (2.31 of 5 allocated vCPU, memory 0.94 of
  4 GiB), so the limit sits inside the engine and **neither raising nor lowering the parallelism
  has a predictable effect — measure before changing it.** **That has since been measured, and
  parallelism was the knob that moved.** Per-segment progress logs carry both a rune count and a
  duration, so cost per character can be read off at each effective concurrency: ~4 gives
  233 ms/char (17.2 chars/sec), 8 gives 430 ms/char (18.6 chars/sec) — **throughput flat, latency
  doubled**, with 430 ≈ 233 x 2 matching the 8/4 ratio, and CPU p99 at 3.7 of 5 vCPU (the engine
  holds 4 of those). Synthesis is CPU-bound, so 4 vCPU saturate at 4 in flight; the default is now
  `4`. The low-concurrency side is only 7 samples (progress logs fire every 5th segment plus the
  last) and those small batches may have caught `startup_cpu_boost`, which would flatter them —
  that bias runs against the change, so if elapsed times worsen, put `8` back through the env and
  measure again. **The env var exists for that reversal, not for infra to tune**: the deployment sets
  none of these three, deliberately — infra owns the engine's size, the app owns how to feed it.
  What parallelism does cost is engine
  memory, and note that the peak does not grow with script length: the in-flight count is capped,
  so a 200-line script has the same memory peak as a 12-line one and only runs longer. They are
  env vars rather than constants because the engine's size is decided by the
  deployment, not here, so
  throttling to fit it should not need a rebuild. Note this is **unrelated** to Cloud Run's
  `max_instance_request_concurrency = 1`, which counts *jobs* per instance, not segments per job.
  go-voicevox logs per-segment avg/min/max at the end of each batch — read that before touching
  any of the three.
- `PIPELINE_TIMEOUT` / `TASK_DISPATCH_DEADLINE` — optional; default to `25m` and `30m`. These are
  the top two rungs of the fleet's timeout ladder
  (`PIPELINE_TIMEOUT` < dispatch deadline <= Cloud Run timeout). **The smallest wins**, so the
  point of the app's own limit is to give up *before* Cloud Tasks does — otherwise the process is
  SIGTERMed mid-job and the failure notification never fires, and the record stays at `running`,
  so the job is simply lost. `ValidateEssentialConfig` rejects a configuration that inverts them.
  `worker.Lifecycle.Timeout` applies the limit to the run alone, and the kit calls `Finish`
  (record + notify) on a **separate, un-cancelled context** — reusing the timed-out one would
  silence the very notification the ladder exists to deliver
  (`TestPipelineExecute_TimesOutAndStillNotifies`). That test also pins the
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
       -> .../handlers        web face: form, history, detail, regenerate, delete, audio, api
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
  builds GCS storage → Store → HTTP client → Notifier → Pipeline, tracking every opened
  resource in a `[]io.Closer` so a partial failure during construction cleans up what was already
  opened. Register new external resources the same way. The container holds both `Storage`
  (the `remoteio.Factory`, which owns the client lifetime and is what goes into `Closers`) and
  `Store` (the read/write/sign window taken from it); go-web-reader wants the factory itself,
  everything else wants the store.
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
- **Job state lives in `go-job-firestore`**, unlike the siblings: `jobfirestore.Status` written to
  Firestore `ap-voice/<jobID>`, **outside the job's GCS prefix**. Deleting a job's objects no longer
  takes its state with it, so `Repository.Delete` removes the document explicitly; if that fails the
  delete still succeeds and the orphan is logged. The record also carries `mode`, which
  **nothing else preserves**: a finished script only reveals the speaker line-up, so a one-speaker
  script cannot be told apart as `tech_solo` or `tech_howto`, and revising a mode's length or tone
  from real output needs to know which mode
  produced what. `synthesize` carries no mode (the script already exists), so
  `JobStatus.CarryFrom` brings it forward along with the artifact URIs — **one method for every
  value a later write cannot re-derive**, since the web face and the pipeline both rebuild the
  record from scratch and used to carry it in two separate places. `domain.NewJobStatus` is the
  other half — the rebuild itself — so a value worth keeping is added once rather than in both
  faces. The record also holds `input_uri` and `ai_model`, and that is what makes **"regenerate
  from the same input"** possible: a job that failed before writing a script has no artifact
  naming what it read, so without them the URL survives only in the operator's memory. The redo
  keeps the job ID (a redo does not deserve a second row in the history), and rewriting the record
  to `queued` is what carries it past the re-run guard.
  The web face records `queued`, the pipeline `running` / `succeeded` / `failed`.
  **The queued write happens before the enqueue** — Cloud Tasks arrives in tens of milliseconds
  and the worker reads state before it works, so the reverse order lets a stale record overwrite
  a live one; ap-story hit exactly this. That ordering is also what makes the re-run guard safe:
  **`Execute` returns early when the job already reads `succeeded`**, and since every enqueue path
  rewrites the record to `queued` first, a second command on the same job ID (the `generate` →
  `synthesize` flow, which deliberately reuses it) arrives as `queued` and runs. Only a
  redelivery, which never touches a handler, still reads `succeeded`. Without the guard an
  at-least-once redelivery re-ran the whole synthesis — minutes of VOICEVOX work for audio that
  already existed. The guard sits **above the deferred failure recorder** on purpose: an
  unreadable status returns from there, and running the defer would write `failed` over a record
  that may say `succeeded`, disarming the guard for the next delivery. Failure is recorded on the
  un-cancelled notification context for the same reason the notification is: a timed-out context
  records nothing.
  `Repository` satisfies `jobfirestore.StatusStore`, which is why the store is assembled in one
  place. `List` takes `jobfirestore.ListOption` and does not invent a filter vocabulary of its own;
  `?state=` on the history screen is `WithState` with the recorded spelling, and an unknown value
  is a 400 rather than a silent full listing (a typo answering "no failures" is worse than an
  error). **The filtered query needs a composite index** (`state` asc + `queued_at` desc) that the
  deployment owns, so an environment without it fails only when the filter is used.
  **Listing reads no artifacts at all**: title, audio presence and creation time all come
  out of the record, so a page costs one query plus a count instead of a full bucket scan.
  `script_test.go` keeps the bucket empty and still expects a page — without that, "just in case"
  reads of the artifacts creep back.
- **One resource, one route.** `auth.Protected(m2m, session)` tries an OIDC bearer first and falls
  back to session + CSRF, and `handlers/job.go` then picks the representation from `Accept`.
  Handlers that used to exist twice — once rendering a template, once writing JSON — are merged there;
  keeping two meant a fix landed on one side and the two answers drifted.
- **The job is the only primary resource, and there is no `/api/` prefix** (the URL naming
  convention in public-docs). `/jobs/{jobID}` names a job from enqueue to delete: the browser
  gets the detail screen, `Accept: application/json` gets the status record, so the poller and
  the page read the same thing and nobody switches URLs on state. Where a person and a machine
  send different bodies to the same action — `POST /jobs` (form tabs vs JSON), `POST
  /jobs/{jobID}/synthesize` (edited rows to save first vs nothing, meaning "the stored script") —
  one handler dispatches on `Content-Type` (`JobCreate`, `Synthesize` in `handlers/job_create.go`
  and `handlers/job_script.go`). Enqueues and actions answer `202` with `Location: /jobs/{jobID}`.
  Delete is `DELETE` for both readers; the page sends it from `App.deleteResource` because a form
  cannot, and a second `POST …/delete` for the screen would have split the authorization and log
  entry points. The reading check is `POST /reading/preview` and hangs off no job (it sends what
  is in the table, not the stored script). A browser sends `X-CSRF-Token` on those calls, which
  `gcp-kit/auth` accepts alongside the form field; a machine on a Bearer never reaches that check.
  The pre-convention paths (`POST /`, `/history/*`, `/api/*`, `/preview-reading`) are gone; the
  MCP server calls `/jobs` directly. `ALLOWED_M2M_SERVICE_ACCOUNTS` is optional;
  unset, verification always fails and everything falls through to the session, so the failure
  mode of forgetting it is "the agent gets redirected to login" (logged by `auth.Protected` as a
  config error). **`/speakers` exists because the styles are per speaker** — 春日部つむぎ has one
  and ずんだもん has eight, an impossible pair is rejected on save, and a client with no way to
  read the list can only guess. `PUT /jobs/{id}/script` saves without synthesizing, since an agent
  may revise several times before spending the minutes once; the browser folds the two into one
  button because a person editing has already decided. Both go through `validateScript` — one
  loose path is enough to store a pair that silently becomes the speaker's default at synthesis.
- **`/modes` lists, `/modes/{mode}` shows one**, the split the siblings use. The index carries only
  front matter and **assembles no prompts**: the eight bodies come to more than 10k characters,
  so building them for a page that may only be scanned is waste, and a reader — or an MCP client
  — that wants the index should not pay for the bodies. The detail assembles through the same
  builder the worker uses, partials expanded, so the page cannot describe something different
  from what Gemini receives. It feeds a placeholder input and retries with a sample recipe when a
  mode refuses plain text, which avoids naming the recipe-input mode a second time; a key that is
  not in the list is a 404 rather than an assembly error.
- **The detail screen edits the script, it does not just show it.** That is what makes the
  two-command split pay: the reason given for it is fixing a reading without regenerating, and
  until the form existed there was no way to fix anything, so review could only choose between
  synthesizing and deleting. Saving and synthesizing are one button — with the script editable,
  a re-synthesis that skips the save would only ever reproduce the same audio.
  **The edited script is not put in the task.** `synthesizeFromForm` writes it to GCS and enqueues the
  job ID alone (the reason is under `synthesize` below), so the worker keeps using the
  stored-script path it already had. Speaker and style come from the registry and are re-checked
  on submit: the browser offers only valid pairs, but the form accepts anything, and an
  impossible pair survives every later check (see the response schema below).
  Rows can be added, moved and removed, since the speaking order *is* the script; the row cap
  travels to the page as `data-max-lines` rather than being written into the JS a second time.
  The screen also calls `/reading/preview` and, while a job is queued or running, polls
  `GET /jobs/{jobID}` as JSON and reloads when the state changes — both are the server's answers, not a second rendering.
- **`internal/repository` serves the history screens** — `List`, `Load`, `SaveScript`, `HasAudio`,
  `Get`/`Save` (the job record) and `Delete`, which removes the whole job prefix rather than a
  fixed list of names. A record with no title yet leaves the job listed under its ID, so a job
  that failed before naming anything can
  still be deleted — and until recently *only in principle*: **a job with no objects at all could
  not be deleted from anywhere.** `Delete` refused an empty prefix with "not found", the detail
  screen (which holds the only delete button) 502'd because there was no script to read, and the
  record therefore sat in the listing forever. `Delete` now removes the record alone when the
  prefix is empty, and reserves `ErrJobNotFound` for a job that has neither objects nor a record —
  the two have to stay apart, or a 404 becomes indistinguishable from tidying up a failed job.
  `Load` draws the same line with `ErrScriptNotFound`: it re-asks storage whether the object
  exists rather than reading the backend's error value (the spelling differs per backend), so a
  missing script and an unreadable one land on the state page and a 502 respectively.
  `List` carries `State` and `Error` for the same reason the badge needs them: the artifacts alone
  cannot separate "still running" from "finished with a script" from "failed". They come out of
  the record the listing already reads, so the page costs nothing extra.
  **`HasAudio` deliberately disagrees with the listing**: the listing trusts the
  recorded `audio_uri`, while the detail screen asks storage whether the object is really there.
  A record that outlived its object would otherwise offer a player that 404s.
- **The detail screen opens for a job with no script.** It is the entry point to a job *and* the
  only place delete lives, so refusing to render it is what stranded failed jobs in the listing.
  Without a script it shows the recorded state and error and offers delete alone; the state alert
  is shown even when a script exists, because a failed `synthesize` leaves the script behind and
  its artifacts look exactly like a job that stopped at `generate`. The recorded error reaches a
  person here and in the Slack notification, nowhere else.
- **A page's scripts are declared in Go, not in the template.** `handlers.pageScripts` maps the
  template name to its JS, `renderTemplate` puts the list on the view and `layout.html` renders it
  with `defer`; `app.js` (what every page needs — currently the `data-confirm` guard) is loaded by
  the layout itself. This is the shape the siblings use, and file names follow theirs
  (`job_status.js` is the same file in three services). Because the paths now live in Go rather
  than in a `{{ define "scripts" }}` block, the assets guard cannot see them —
  `TestPageScriptsExist` and `TestRenderTemplateLoadsThePageScripts` take that job over: one
  checks the files exist, the other that the table, the view and the layout are still connected.
  A missing script is invisible otherwise, since the page still renders.
- **Templates are only evaluated at request time.** A renamed view field still compiles, so
  `internal/server/handlers/templates_test.go` renders every screen with the real view structs
  (a `map` would turn a missing key into `<no value>` and pass).
- **A new role never means touching the router.** `BuildHandlers` leaves the handlers a role does
  not serve as nil and `registerRoutes` guards each route group on nil, so `SERVER_ROLE=web` simply
  has no `/tasks/generate` — it 404s rather than existing unprotected. `AppHandlers.Validate`
  rejects the half-built case (`TaskAuth` without `Worker`, or vice versa) at startup, because the
  router would otherwise turn a DI mistake into a silent 404 indistinguishable from a config
  mistake. `router_test.go` pins all three states.
- **The worker handler is `gcp-kit/worker`, not hand-written.** `worker.NewHandler[domain.Request]`
  takes anything with `Execute(ctx, T) error`, which `pipeline.Runner` satisfies by delegating to
  `worker.Lifecycle`, so
  JSON decoding, body-size limits and Cloud Tasks retry metadata come from the kit.
  `domain.Request` doubles as the task payload; its `json` tags are the wire contract with
  whatever enqueues it.
- **`Runner.lifecycle` is the only orchestration point**, and the order is the kit's, not ours
  (public-docs worker convention): `Begin` (re-run guard + `running`) → `Validate` (a failure is
  `Permanent`) → `Run` (resolve script → publish: WAV + script upload + optional signed URL) →
  `Finish` (record → notify, on a detached context, for success and failure alike; a panic
  arrives as `worker.ErrPanicked`). Nothing here recovers, detaches or times out by hand. **There are two outcomes, not three** — the
  sibling apps notify a "skipped" case for input that has not changed, and ap-voice carried the
  whole path (port method, pipeline helper, Slack title) with nothing able to trigger it. Note
  `deadcode` reports such a path as live, because its tests count as callers.
- **Script generation and synthesis are separate commands**, and the mapping from `Command` to
  steps lives in one place, `DefaultPlanner.Plan` (`planner.go`): the script's origin is the only
  difference (`ScriptStep` generates, `LoadScriptStep` reads), and `PublishStep` is shared:
  - `generate` — reads the source, calls Gemini, and stops at `PublishStep.PublishScript`, which
    writes the script only. **It produces no audio and returns no signed URL**, deliberately:
    signing does not check that the object exists, so signing a WAV that was never made hands out
    a 404 link in the Slack notification.
  - `generate_and_synthesize` — the same as `generate` followed by `synthesize`, for when there
    is nothing to fix. **It needs no new step**: the planner gives it `ScriptStep` + `PublishStep`
    like `generate`, and `PublishStep` treats anything that is not `generate` as going all the
    way to audio, so the third value lands on the wanted side of both.
  - `synthesize` — never touches Gemini. It loads the stored script by `JobID`
    (`domain.ScriptStore`), and that is the only way in: **the script is not carried in the task
    payload**, because a long one can reach Cloud Tasks' 1MB limit. `Request` used to have a
    `Script` field that the load step preferred when set, and once every enqueue path saved the
    script and passed the ID alone — including the two that accept a caller's own script, the
    `POST /jobs` JSON body and the form's script tab — nothing could set it. It went the way the
    "skipped" notification did, and for the same reason: tests were its only callers.
    `PublishStep.Run` rewrites the script *and* writes the WAV, so an
    edited script cannot drift from the audio that was actually spoken. **The script goes
    first**: in the combined command it exists only in memory until then, so writing audio first
    would lose a generated script to a synthesis timeout, leaving nothing to retry from.

  `Request.Command` has **no default**: an empty command is an error, because silently treating it
  as `generate` would discard the `script` a caller put in the `POST /jobs` body and bill them for
  generation. `Request.Validate`
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
  a genre prefix so the list groups itself: `tech_*`, `news_*`, `story_*`, `music_*`).
  Each file opens with a YAML front matter block
  (`order` / `label` / `direction` / `use_when`) that supplies the form's option text and the
  description under the select — the same arrangement the siblings use, so **the explanation lives next
  to the prompt it explains** rather than in a list the form owns. `order` (numbered in tens, so a
  mode can be inserted without renumbering) decides the option order, and it lives in front matter
  rather than in a numeric filename prefix **because the filename is the mode key**: it is the
  value carried in `Request.Mode`, the `/modes/{mode}` URL and what `list_voice_modes` hands an
  agent, so a rename for presentation's sake would strand every stored job and outside caller.
  A mode with no `order` sorts last (key order breaks ties), the same shape of fallback as an
  absent `label`. `assets/modes.go` splits it:
  `LoadModes` reads the metadata, and **`LoadPrompts` returns the body only** — leaving the front
  matter in would slip YAML into the top of the instruction text, and the run would still
  succeed, so nothing would flag it. A prompt with no front matter still appears, labelled by its
  key. **Files beginning with `_` are partials, not modes** — `_writing` (how to spell a line so
  the engine speaks it), `_clarity` (how to write a line the ear can follow), `_length`, `_title`
  and `_input` — so the eight prompts state only what is particular to them. `_writing` and
  `_clarity` are deliberately two files: one is about notation, the other about comprehension, and
  a mode can want the first without the second. **`_clarity` is included by six modes, not eight.**
  `story_reading` leaves it out because rewriting a demonstrative or spelling out a term is exactly
  the rewriting a reading must not do, and `comedy_manzai` because its "言い換えて紛れを避ける" rule
  is the opposite of what a homophone gag needs — both carry their own listener rules instead. `go-prompt-kit` already excludes that prefix from `Build`, and `LoadModes`
  filters on `prompts.DefaultPartialPrefix` so the same rule is not written twice; `LoadPrompts`
  keeps them, since the bodies reference them.
  The mode string travels from `Request.Mode` straight to
  `PromptAdapter.Generate` and is never validated against a list. The one mode whose *input type*
  differs — `music_promo`, which reads a `recipe.json` and decodes it into
  genai-kit's `music.Recipe` before rendering — **is not named in code either**:
  `NewPromptAdapter` collects
  the recipe modes from the same `input: "recipe"` front matter the form's tabs read, so the
  answer to "which modes take a recipe" does not live in two places.
  **No mode name appears anywhere in Go code**, which is what keeps adding one to a file drop.
  `TestPromptAdapterBuildsEveryMode` assembles every mode through the real builder, since a
  mistyped `{{template "_clarity" .}}` compiles fine and only fails when someone picks that mode.
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
  has these styles" — an impossible pairing is not rejected, synthesis quietly falls back to
  that speaker's default and the instruction is ignored. Per-speaker and per-mode constraints
  therefore live in the prompt text, and **every prompt currently pins `style` to `"ノーマル"`**,
  so the enum's width does not matter in practice. If you change `ScriptLine`'s fields, update the
  schema in lockstep.
- **The prompts carry all the expressive work.** With one fixed style, nothing about the audio
  varies except the words, so `_writing` says exactly that and asks for short sentences,
  restatement and reordering instead. It also spells out what a TTS script needs and a written one
  does not: katakana for acronyms, no symbols or bullet lists, unambiguous numbers, kana for a word
  with two readings. If you loosen the style rule, drop the compensating instructions with it —
  otherwise the two pull against each other.
- **`_clarity` is about the listener, not the engine.** A line the engine pronounces correctly can
  still be unfollowable: a demonstrative pointing three sentences back, a dropped subject, a term
  explained later, a homophone. **None of that shows up in the audio as a defect** — it sounds
  fine and simply fails to land — so it has to be instructed rather than caught. The rules assume
  the listener cannot rewind, which is the one thing separating this from ordinary editing.
  **Every clarity rule needs a counterweight, or it buys legibility with content.** The first
  version ended the "open every term" rule with "a term you cannot open is better left out", and
  one run on the same source article as an earlier one showed the price: distinct technical terms
  fell from 36 to 16, SSRF became "server attacks", "the SDK's types" became "dedicated tools",
  and the twelve-panels-redraw-panel-three example vanished. Facing "open every term" against "one
  idea per line", the model dropped the names rather than explain them — and kept every topic, so
  the script covered the same ground at half the resolution. Hence two rules that read as
  redundant but are not: **opening a term is name-plus-explanation, never a generic substitute**,
  and in `_length`, **shortening means fewer topics, not the same topics at lower resolution**.
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

## What the dependencies do that their names do not say

`go.mod` lists them; this section is only for the ones that behave in a way you would not guess,
and it is where the actual behaviour lives when you are editing an adapter. Check `go.mod` for the
pinned version before assuming a signature.

- `go-voicevox` — parses `/speakers` but **ships no roster**: the vocabulary is this repo's
  `assets/speakers.json`. Throughput comes from `config.Voicevox`, not from constants here, and
  an impossible speaker/style pair falls back to that speaker's default instead of failing.
- `go-http-kit` — `builder` passes `WithSkipNetworkValidation(true)`, which **disables the SSRF
  guard for the whole client**. It is shared by the VOICEVOX calls and the Slack webhook, and one
  round trip is capped by `HTTP_TIMEOUT` while the retry sits inside `VOICEVOX_SEGMENT_TIMEOUT`.
- `go-utils/jobid` — **never sort job IDs lexically**: the prefix outranks the timestamp, so use
  `jobid.SortKey`.
- `go-notify` — assembles the message; `adapters/slack.go` only decides *what* to say.
- `gcp-kit` and `go-serve-kit` split on whether the thing is Google Cloud specific, and the two are
  easy to mix up. Cloud: `worker`, `auth`, `tasks`, `cloudlog`, `cloudrun`. HTTP: `serverrole`
  (the `web`/`worker`/`both` vocabulary), `respond` (JSON and the `Accept` decision), and
  `secureheaders` (the CSP the templates are written against).

## Conventions

- **Error text**: sentinel errors are English with a package prefix (`review: diff is empty`) so a deeply wrapped error still names its origin; the context added by `fmt.Errorf` wrapping is Japanese. Existing English wrap text is not being retrofitted — apply the rule to code you touch.
