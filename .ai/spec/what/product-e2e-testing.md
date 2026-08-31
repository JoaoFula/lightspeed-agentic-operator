# Product E2E testing

Behavioral specification for **product-level end-to-end tests** that exercise
the full AgenticRun lifecycle against real OpenShift clusters with real LLM
providers. Distinct from `test/e2e/` mock-agent tests (`make test-e2e`) which
validate operator logic in isolation.

## Relationship to other specs

| Spec | Relationship |
|------|-------------|
| [run-lifecycle.md](run-lifecycle.md) | Product-e2e validates the phase transitions defined there |
| [sandbox-execution.md](sandbox-execution.md) | Product-e2e exercises the sandbox wiring end-to-end |
| [approval.md](approval.md) | Tests use automatic approval policies |

## Existing product-e2e (`make product-e2e`)

`scripts/e2e-cluster.sh` deploys the operator, iterates over providers (claude,
gemini, openai), creates real LLM fixtures, and runs `make test-e2e` per
provider. This exercises the mock-agent Go e2e tests with real provider
credentials.

## Troubleshooting scenario tests (OLS-3739)

### Scope

- **In scope:** Phase transition verification for AgenticRun CRs created with
  troubleshooting prompts against clusters with injected broken states. Asserts
  that the run completes the full lifecycle (Pending → Analyzing → Proposed →
  Executing → Verifying → Completed) with real LLM providers.
- **Out of scope:** Sandbox output quality verification (sandbox repo
  responsibility), behavioral correctness of fixes (future work).

### Build tag

Tests are gated behind the `product_e2e` build tag, separate from the `e2e`
tag used by mock-agent tests. They are only invoked via `make product-e2e`,
never via `make test-e2e`.

```go
//go:build product_e2e
```

### Test file

`test/e2e/troubleshooting_test.go` — one parametrized test function covering
all 11 troubleshooting scenarios.

### Test flow per scenario

1. Run scenario `setup.sh` via `os/exec` to inject broken cluster state
2. Create AgenticRun CR with the scenario's request text, pointing at the real
   provider's Agent/LLMProvider fixtures (from `createRealProviderFixtures`)
3. `waitForPhase(t, c, name, AgenticRunPhaseCompleted)` — reuses existing
   helper with configurable timeout
4. Assert: AnalysisResult, ExecutionResult, VerificationResult CRs exist with
   owner references
5. Assert: no `Failed` conditions on the AgenticRun
6. Run `cleanup.sh` in `t.Cleanup`

### Scenario script access

Troubleshooting scenario scripts are owned by `lightspeed-agentic-sandbox`
under `scenarios/troubleshooting/`. The operator accesses them at CI time by
extracting from the sandbox container image.

`e2e-cluster.sh` changes:
- Before running tests, extract `scenarios/` from the sandbox image to a temp
  directory
- Export `E2E_SCENARIOS_DIR` pointing at the extracted path
- The Go test reads `E2E_SCENARIOS_DIR` to locate setup/cleanup scripts and
  `scenario_metadata.yaml` for scenario parameters

### Scenarios

The 11 scenarios and their metadata are defined in the sandbox repo. See
`lightspeed-agentic-sandbox/.ai/spec/what/e2e-testing.md` for the full
scenario table.

### e2e-cluster.sh extension

After running the existing `make test-e2e` (mock-agent tests) per provider,
the script runs:

```bash
go test -tags=product_e2e ./test/e2e/... -count=1 -v -timeout 60m
```

With environment:
- `E2E_PROVIDER`, `E2E_MODEL`, `E2E_PROVIDER_KEY_PATH` — from existing flow
- `E2E_SCENARIOS_DIR` — extracted scenario scripts path
- `E2E_POLL_TIMEOUT` — default 20m per scenario (longer than mock-agent tests)

### Assertions

Phase transition only:
- AgenticRun reaches `Completed` phase (derived from conditions)
- `Analyzed=True`, `Executed=True`, `Verified=True` conditions present
- AnalysisResult CR exists with owner reference to AgenticRun
- ExecutionResult CR exists with owner reference to AgenticRun
- VerificationResult CR exists with owner reference to AgenticRun
- No `False`-status conditions with failure reasons

No assertions on result content quality — that is the sandbox repo's
responsibility via LLM judge.

### Constraints

- Requires a live OpenShift cluster with the operator deployed
- Requires real LLM provider credentials
- Scenario setup/cleanup scripts must be idempotent
- Tests run sequentially (one scenario at a time) to avoid cluster state
  interference

### Future work

- [PLANNED] Behavioral correctness assertions: verify ExecutionResult actions
  match expected fix patterns per scenario
- [PLANNED] Failure scenario tests: verify graceful handling when the LLM
  cannot diagnose the problem (phase reaches Failed or Escalated)

## Commands

```bash
make test          # unit tests (no cluster, no credentials)
make test-e2e      # mock-agent e2e (cluster + operator, no real LLM)
make product-e2e   # full product e2e including troubleshooting scenarios
```
