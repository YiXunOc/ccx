# Responses reasoning compatibility verification

## Verdict

T6 VERIFIED: latest Go regression passed; the unchanged installed-client matrix
passed all 22/22 cases after regenerating real converter output. Gemini/tool now
produces a lookup tool-call block with arguments {} and ends with tool-calls.
Reasoning survives actual parser consumption, JSON accumulator storage and replay.
The earlier T2 result remains failed (21/22); its raw artifacts are preserved as
t2-fixtures-failed.json and t2-installed-results-failed.json. T6 does not rewrite
that historical verdict or replace production/real-model acceptance.

## Real installed dependencies

Read-only install root: D:/Software/AI/DSH-Desktop-x64-Portable/App/resources/app.asar/dsh

- DSH desktop runtime: 0.2.1-alpha.2
- @earendil-works/pi-ai: 1.0.2
- @deepseek-ai/dsh-llm-pi-ai: 0.2.1-alpha.2
- @deepseek-ai/dsh-llm: 0.2.1-alpha.2
- Electron Node runtime: v24.21.0

Actual import entry paths and raw output evidence are in installed-results.json.
The chain invokes real PiAiAdapter.stream, pi-ai Models, openai-responses API,
installed OpenAI SDK SSE parsing, AssistantStreamAccumulator, expandAssistantStream
and BlockAssembler. Only transport is substituted: fetch returns a real in-memory
Response with seven-byte UTF-8 fragments. Socket connect is blocked and counted.
Final run: 22 in-memory requests, zero socket attempts, no model/billing calls.
No copied or mock parser/accumulator is used. No user credentials/config are read.

## Coverage

For each chat/claude/gemini converter: reasoning only, reasoning then text, reasoning
then tool, and no reasoning. All reasoning paths preserve first + [space]second in
stream deltas, assembled blocks and JSON-serialized accumulator replay. The actual
pi-ai parser adds a paragraph separator delta at summary-part completion; the
assembled final reasoning is exactly first second. Standard direct Responses input
with Chinese and an emoji also survives byte-boundary splitting and replay.

Per source, synthetic early EOF, response.incomplete(upstream_error) and
response.failed(upstream_error) preserve already-delivered reasoning and finish as
error, not success. These are client consumption tests, not proof that real
upstream_error causes are repaired or that the live server emits these sequences.

## Historical T2 blocking finding (resolved by T5, verified in T6)

The following describes the pre-T5 implementation, not the latest source.
backend-go/internal/converters/responses_to_gemini.go:498-510 collects Gemini
functionCall only for terminal output. At 681-686, output has type=function_call
and content={call_id,name,arguments}, without normal added/delta/done tool events.
The installed pi-ai consumer does not discover this terminal-only nested tool.
The old converted wire is preserved in t2-fixtures-failed.json under gemini/tool;
the actual old consumer output, saved stream and blocks are preserved in
t2-installed-results-failed.json. The unprefixed filenames now contain T6 results.
The same terminal shape is visible in baseline 112e837a6a67711a58278e5d240a6cb2810875e1
via git show. This is an existing adjacent tool-compatibility problem, not caused
by the reasoning text-to-delta edit. No production fix is attempted here.

## T6 fresh verification evidence

Executed with D:/Software/Go/bin/go.exe in backend-go:

- Affected packages: exit 0; converters 6.511s / responses 10.063s / utils 5.223s.
- vet ./...: exit 0, no output.
- test ./... -count=1: exit 0; all packages passed; background job pwsh-338 was collected, autopilot 112.459s.
- Independent tool identity + implementation contract specialty: exit 0, 5.410s; 2 independent cases plus 24 implementation cases.
- Final overlay export + independent specialty: exit 0, 5.206s.
- Final installed-client matrix: exit 0, all original 22 cases passed; 22 memory requests, socketAttempts=0.
- Tool scenarios now explicitly require finish.kind=tool-calls, not just any successful terminal.

identity_test.go independently constructs three calls (two same-name lookup and
one search) in a single chunk and in separate chunks, between reasoning/body
prefix and body suffix. Assertions cover globally contiguous sequence numbers;
unique nonempty item IDs; added-before-arguments; exact four-event tool lifecycle;
matching item_id/output_index across delta/done; indices [2,3,4]; retained
function-name call_id semantics; top-level terminal items equal done items in
index order. encoding/json deep comparison checks nested arrays, boolean/null,
Unicode including surrogate-pair emoji, quotes/backslashes, empty objects/arrays
and distinct parameters. This independent test uses no implementation test helper.
It is loaded using Go overlay and does not modify backend files.

## Historical T2 Go regression evidence

Executed in backend-go with D:/Software/Go/bin/go.exe:

- test ./internal/converters ./internal/handlers/responses ./internal/utils -count=1: exit 0; 3.002s / 9.096s / 3.606s.
- vet ./...: exit 0; no output.
- test ./... -count=1: exit 0; all packages passed (autopilot 93.531s).
- fixture export via overlay: exit 0; converters 5.121s.
- installed-client verification: exit 1; 21 passed, gemini/tool failed.

Initial isolated runs failed before transport because the custom provider lacked
its public auth descriptor. This test-harness configuration was corrected to use
installed envApiKeyAuth with an explicit fake key and an empty env-var list; it is
not a production fix. Electron must be started with Start-Process -Wait -PassThru
to propagate its actual child exit status; direct PowerShell invocation can show
exit 0 even when the script printed an uncaught assertion.

## Reproduction

Run from backend-go (overlay.json contains absolute paths for this workspace):

```powershell
$env:RESPONSES_COMPAT_FIXTURES = (Resolve-Path ../tests/responses-reasoning-compat).Path + "/fixtures.json"
& D:/Software/Go/bin/go.exe test -overlay ../tests/responses-reasoning-compat/overlay.json ./internal/converters -run "^TestExportInstalledClientFixtures$" -count=1
```

Run from repository root:

```powershell
$env:ELECTRON_RUN_AS_NODE = "1"
$p = Start-Process -FilePath "D:/Software/AI/DSH-Desktop-x64-Portable/App/DeepSeek Harness.exe" -ArgumentList "tests/responses-reasoning-compat/verify-installed.mjs" -NoNewWindow -Wait -PassThru
exit $p.ExitCode
```

export_test.go is compiled only through go test -overlay, reusing the in-memory
upstream fixture builder in responses_reasoning_delta_contract_test.go. It invokes
the actual three conversion functions; it does not edit the backend directory.
Run the independent specialty from backend-go:

```powershell
& D:/Software/Go/bin/go.exe test -overlay ../tests/responses-reasoning-compat/overlay.json ./internal/converters -run "^TestIndependentGeminiToolIdentityAndParameters$|^TestGeminiResponsesToolLifecycleContract$" -count=1 -v
```

Generated fixtures/results contain synthetic data only. No install/lockfile changes,
commit, deployment, packaging or service operation is performed.
