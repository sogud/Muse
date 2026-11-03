# Contributing with an agent

Read [README.md](README.md) for commands, supported features and current limits.

- Keep `internal/core/` as the shared implementation for `cmd/creative/` and the optional MyGo UI. Do not add a second database or frontend runtime.
- The native UI manages projects, task status, artifacts and settings. Creation and execution use the CLI. Local UI refresh must not submit paid jobs or poll providers automatically.
- Preserve existing user data. Use `OTO_USER_DATA_DIR` and synthetic inputs for local checks; never use real keys, paid APIs or personal content as fixtures.
- Paid generation requires explicit approval for the particular request. Do not retry an uncertain paid submission. Budget fields are estimates, not verified provider spending limits.
- Keep artifact hashing, safe preview types and non-overwriting export in the shared Core.
- Preserve third-party attribution and license terms. Do not include credentials, databases, provider receipts or private generated content in commits.
- Inspect branch, worktree and index before editing; preserve unrelated work. Run the smallest existing check relevant to a change and report anything not verified.

## Existing checks

```sh
go test . ./cmd/creative ./internal/core
npm run typecheck:story
npm run test:story
```

StoryPack checks are relevant when changing `packages/` or the story CLI. A build or automated check does not establish native GUI acceptance, successful paid output or distribution readiness.
