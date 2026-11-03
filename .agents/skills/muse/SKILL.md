---
name: muse
description: Create, manage, and export multi-modal creative work using Muse's local JSON CLI. Use for story assets, AI music jobs, durable provider status checks, safe recovery, and artifact review.
---

# Muse · Agent-native creative tools

Work in the existing Muse repository. The local skill must not create a
second project or runtime. The CLI does **not** require MyGo to be running.

## Entrypoint

From the repository root:

```sh
./build/creative --help
./build/creative doctor
./build/creative providers # local readiness shared with MyGo, no API call
# Or use: go run ./cmd/creative <command>
```

Each command returns a single JSON envelope: `{"ok":true,"data":...}` on
stdout, or `{"ok":false,"error":...}` on stderr with a nonzero exit code.
Inspect the actual `data.job.status`; transport `ok` does not mean that a
provider generation is completed. Keep stdout machine-readable.

`OTO_USER_DATA_DIR` overrides the existing local `~/.oto` project store.
Never switch it to another user's directory or migrate/delete old data
without explicit instructions.

## Prefer these tasks

1. Run `creative workspace list` and reuse an existing project when
   appropriate. Create one with `workspace create --title ... --kind ...`
   otherwise. `workspace` is the new multi-modal project; the older
   `project` namespace refers to MIDI compositions only.
2. Use `job create --project ID --kind music.generate --provider PROVIDER_ID
   --input INPUT_JSON --estimate-micros N --max-cost-micros N
   --idempotency-key KEY` to plan one billable music request. Input is a
   JSON object, e.g. `{"prompt":"subtle ambient piano","instrumental":true}`.
   Reuse a stable idempotency key for the same logical request.
3. Present the user with the specific provider, prompt, number of outputs
   (Mureka is configured for one), **user-supplied cost estimate**, and
   spending ceiling. Explain that actual price has **not** been verified
   against provider billing. Seek explicit approval for this paid request.
4. Only after approval, record `job approve --id JOB_ID` and then run
   `job submit --id JOB_ID --allow-paid`. The second flag requires
   explicit, contemporaneous authorization from the user; saved approval
   alone does NOT authorize the agent to spend.
5. Use `job status --id JOB_ID` without network calls. Use
   `job sync --id JOB_ID` to poll a previously submitted task and, once
   the provider succeeds, download audio into the local project's
   Artifact Store. `job wait --id JOB_ID --timeout 10m` repeats
   **read-only status polls** inside the active CLI process. `job resume`
   is an alias for one `job sync` operation.
6. Check `data.job.status`, `data.artifacts`, `data.receipt` and
   `data.costVerified`. If succeeded, use `artifact review --id
   ARTIFACT_ID --decision approved` only when the user or authorized
   human actually accepts the result. Export with `workspace export --id
   PROJECT_ID`, which writes a snapshot `manifest.json`.

## Offline StoryPack delivery (no provider API charges)

For an existing structured story JSON file in the imported AIVN format:

```sh
./build/creative workspace create --title "Story project" --kind story
./build/creative story validate --input story-input.json
./build/creative story build --input story-input.json --project PROJECT_ID
# Read data.job.id and data.storyPackArtifactId from the command JSON.
./build/creative artifact export --id ARTIFACT_ID --output ./storypack.json
```

This uses the existing Bun StoryPack adapter locally, not any AI provider.
It preserves a copy of the input plus the StoryPack under the Muse project
and exports a fresh `manifest.json`. Input bytes determine a stable
idempotency key per project; rerunning exactly the same input returns
`data.cached=true` and does not create duplicate jobs or assets. When
using `--project`, do not also pass `--output`; Muse manages output files.
The existing `story build --input ... --output NEW_FILE.json` command
still performs a standalone build without a project.

`artifact export` writes to a NEW destination only (no overwrite),
verifies byte length and SHA-256 against the manifest, and reports the
resulting absolute path. Only use that explicit exported path for further
agent processing; the Bun build's temporary paths are deleted. Remember
that story sources and generated packs may contain user-private text.
Do not upload or publish them without explicit user permission.

## Provider readiness

Run `creative providers` before planning a provider task. The JSON response
identifies `mureka`, `elevenlabs`, `lyria` (music) and `text-pattern` (text),
with `configured`, `credentialSource` and `detail`. This is only a local
configuration check; it does not contact providers or check balance.
`MUREKA_API_KEY` must exist in the invoking process environment, including
when Muse.app launches separately from a terminal. MyGo text-model credentials
do not configure Mureka. Keys may still be in legacy SQLite for Pattern;
Keychain migration is pending. A true `configured` is never paid authorization.

## Official music APIs

- `mureka`: asynchronous music service, task ID returned. `job sync` polls
  the original remote job and archives successful audio.
- `elevenlabs`: official Music API, direct MP3 result, `music_v2_5` default.
  Can request 3–600 seconds with `durationSeconds`.
- `lyria`: Gemini Interactions API, direct base64 MP3 result.
  `lyria-3-clip-preview` defaults to 30-second output; `lyria-3.5` for songs.

`creative music generate --provider ID --prompt ... --dry-run` checks model,
duration and input limits locally, without billing. Paid jobs still require
an idempotency key, an approved user-estimated cost and ceiling, and the
user's separate explicit authorization. Keys are `MUREKA_API_KEY`,
`ELEVENLABS_API_KEY`, `GEMINI_API_KEY` respectively, from the process
environment; no Keychain integration has yet been built.

ElevenLabs and Lyria have **no Mureka-style pollable task ID**. Muse saves
their completed MP3 to a private staging file and then archives it using
the same Artifact Store. Their `job sync` can only recover a complete
*local* stage; it never repeats a paid request. `job attach` is only
for Mureka. Interrupted direct requests need manual provider/billing
review rather than resubmission. Their real paid API calls have not
been performed in the project yet.

## Failure and money rules

- NEVER auto-append `--allow-paid` or `--approve-paid` based only on a
  saved draft or a generic user request. A paid POST can be charged even
  if the command later reports a network failure.
- A job in `submitting` or `needs_reconciliation` may have been
  accepted remotely. Do **not** resubmit it. For Mureka only, verify the
  remote task ID before using `job attach --provider-task-id EXISTING_TASK_ID`.
  Direct providers instead need local staging/billing reconciliation.
- Mureka jobs in `processing` or `awaiting_archive` can be polled
  again; expiring download URLs are re-fetched. For direct providers,
  `job sync` recovers only a completed local file, never remote generation.
- Mureka media downloads accept HTTPS URLs from trusted Mureka domains
  only. Unsupported CDN URLs need operator verification; do not bypass
  SSRF protections by rewriting them to local proxies.
- `estimatedCostMicros` and `maxCostMicros` are user-entered figures,
  **not a verified provider quote or guaranteed spend limit**.
  `costVerified:false` is expected until a billing integration exists.
- Do not assume the user owns redistribution rights to an AI-generated
  asset. The local license note is not a legal grant.
- Store provider keys outside task inputs, prompts, manifests, Git and
  logs. Mureka currently reads `MUREKA_API_KEY` from the process
  environment. Text AI provider credentials still require a future
  Keychain migration. Do not show their values to users.

## Missing local dependencies

Muse native UI has a **环境依赖** page with Bun, Node/npm,
StoryPack package and bundled audio-resource status. If the CLI reports
`RUNTIME_UNAVAILABLE`, guide the user to this page to inspect and
click the relevant install button, then confirm the exact command.
Never install global software silently in an Agent workflow.

Official macOS fallback: `brew install oven-sh/bun/bun` for Bun,
`brew install node` for Node/npm; from the selected Muse source root
run `npm ci --ignore-scripts --no-audit --no-fund` for StoryPack packages.
The last command replaces the existing source repo `node_modules`.
A packaged Muse.app outside the source tree may require users to select
their Muse source root first.

## Other existing capabilities

- `creative music providers` lists all three implemented music adapters.
- `creative music generate --prompt ... --dry-run` does not call the
  network. Its paid compatibility shortcut requires a project, a stable
  idempotency key, a cost estimate, a ceiling and explicit approval; it
  uses the same JobRuntime internally.
- `creative story validate --input STORY_INPUT_JSON` and
  `creative story build --input STORY_INPUT_JSON --output STORYPACK_JSON`
  use the imported AIVN StoryPack modules, through optional Bun.
- `creative render check` and `render project --id MIDI_PROJECT_ID`
  support the legacy local FluidSynth/MIDI flow, without a music API.
- `creative pattern generate` still uses the older text-model request
  path and requires `--approve-paid`. It is **not yet** a durable Job.
- Image, voice and video provider adapters, MCP server, automatic
  multi-step story composition and verified billing are **not implemented**.
  Do not invent commands or silently switch providers.

MyGo refreshes the local SQLite project/job/artifact view every three seconds
while its window remains open; it does not poll providers or start paid work.
In task details the user can inspect provider receipts, errors and unverified
budget metadata. Archived artifacts can be opened in the macOS default
application or exported through a native Save dialog, using the same
SHA-256-verified non-overwriting Core operation as the CLI.

The MyGo native window is a management and configuration surface only:
project overview, task status, artifact review, model setup and local
dependency installation. It must not become an AI chat client or offer
prompt/creative-intent entry. Do not start it from an automated CLI workflow. When this Skill is used in a different
Agent host, first confirm that host can actually execute local processes.
