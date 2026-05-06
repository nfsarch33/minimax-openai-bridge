# v303 MiniMax Bridge First-Light Evidence

Recorded: 2026-05-07T05:20:00+10:00
Story: v303-3 MiniMax dual-key first-light smoke and rotation evidence

## Status

Mechanism status: SOLID.
Live provider status: BLOCKED until real MiniMax keys are present in the target
host secret store and the Mem0 self-host stack is running.

## Evidence

```text
go test ./...
?    github.com/nfsarch33/minimax-openai-bridge/cmd/minimax-openai-bridge [no test files]
ok   github.com/nfsarch33/minimax-openai-bridge/internal/bridge
```

```text
runx sentrux gate --repo minimax-openai-bridge
Quality: 6661 -> 6940
No degradation detected
```

```text
runx shell-leak-scan --repo minimax-openai-bridge
no findings
```

## Rotation Coverage

Existing tests cover:

- `TestEmbeddingFallbackToSecondKeyOnQuota`: first embedding call receives 429,
  second key succeeds.
- `TestChatCompletionsFallbackToSecondKeyOnQuota`: first chat call receives 402,
  second key succeeds.
- `TestLoadAPIKeysFiltersPlaceholdersAndDuplicates`: placeholder and duplicate
  keys are filtered before use.

## Live Smoke Gate

Do not run live provider calls until:

1. `MINIMAX_API_KEY_1` and `MINIMAX_API_KEY_2` are real values on the target host.
2. The values are injected through the approved secret flow, never argv.
3. `mem0-selfhost doctor` passes on the wsl1 Docker host.
4. The first-light request is run through the loopback Mem0 / MiniMax bridge path.

## Carry-forward

v303-4 can start dual-write implementation because the bridge rotation mechanism
is already tested. Live first-light remains a deployment-gated smoke, not a code
blocker.
