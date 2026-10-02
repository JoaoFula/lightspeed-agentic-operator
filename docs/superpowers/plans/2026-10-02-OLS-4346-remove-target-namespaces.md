# OLS-4346 Remove `AgenticRun.spec.targetNamespaces` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the run-level `targetNamespaces` field and every operator-owned API, CLI, context, documentation, and test dependency on it.

**Architecture:** Namespace-scoped execution RBAC targets will come only from unique, non-empty `NamespaceScoped[].Namespace` values in the approved remediation RBAC result. Delete `AgenticRunSpec.TargetNamespaces` and its validation; stop sending it to sandboxes; remove the CLI flag and all corresponding examples/docs/fixtures. Keep `targetCluster` and `RBACRule.namespace` unchanged.

**Tech Stack:** Go, Kubernetes API / controller-runtime, Kubebuilder-generated CRDs, Cobra CLI, YAML examples.

**Spec:** `.ai/spec/OLS-4346-remove-target-namespaces-design.md`

## Global Constraints

- Do not hand-edit `config/crd/bases/` or generated `api/v1alpha1/zz_generated.deepcopy.go`; regenerate CRDs with `make manifests` and deep-copy code with `bin/controller-gen object paths=./api/v1alpha1/...`.
- Preserve `AgenticRun.spec.targetCluster`, `RBACRule.namespace`, RBAC rule validation, and cluster-scoped RBAC behavior.
- Do not add a compatibility alias or deprecation period for `spec.targetNamespaces`.
- Use `make test` for unit verification and `make api-lint` for API linting; `test/e2e` requires a live cluster and is not part of `make test`.

## Review Focus

- A run-level namespace must not override approved RBAC rule namespaces — add a controller test that supplies different run/rule namespaces and asserts Roles are created only in the rule namespace (Task 1).
- Duplicate RBAC namespaces must yield a single namespace target — retain/strengthen the literal deduplication assertion in `TestRBACTargetNamespaces` (Task 1).
- Rules with empty namespace and nil RBAC must not yield namespace-scoped Roles — retain/strengthen the existing empty/nil cases in `TestRBACTargetNamespaces` (Task 1).
- The sandbox input `context` must no longer expose `targetNamespaces` while retaining its other context data — assert against the marshaled ConfigMap payload (Task 2).
- The CLI must no longer advertise or accept `--target-namespaces` — assert `NewCreateCmd` has no such flag (Task 2).

---

### Task 1: Derive execution RBAC namespaces only from approved rules

**Files:**
- Modify: `controller/agenticrun/rbac.go`
- Test: `controller/agenticrun/rbac_test.go`

**Interfaces:**
- Consumes: `agenticv1alpha1.RBACResult.NamespaceScoped []RBACRule`, with `RBACRule.Namespace`.
- Produces: `rbacTargetNamespaces(rbacResult *agenticv1alpha1.RBACResult) []string`, returning unique non-empty rule namespaces in their original order; nil input returns nil.

- [ ] **Step 1: Write the failing behavior test.** Add a test around `ensureExecutionRBAC` where the run's existing `TargetNamespaces` contains `spec-only`, while the approved RBAC result contains `rule-only`. Assert a Role/RoleBinding is created in `rule-only`, and none is created in `spec-only`. Also update helper tests to assert literal expected values for multiple, duplicate, empty, and nil rule namespaces.
- [ ] **Step 2: Run `make fmt`, then `make test`; confirm the new precedence test fails** because the current implementation scopes to the run's `TargetNamespaces`.
- [ ] **Step 3: Implement rule-only derivation.** Change `rbacTargetNamespaces` to accept only the RBAC result, retain order while deduplicating and skipping empty namespaces, update `ensureExecutionRBAC` to use it, and add explicit `Namespace` values to existing RBAC fixtures whose expected Role namespaces previously came from the run spec.
- [ ] **Step 4: Run `make test` and confirm the RBAC tests pass.**
- [ ] **Step 5: Commit the task.** `git add controller/agenticrun/rbac.go controller/agenticrun/rbac_test.go && git commit -m "OLS-4346 Derive RBAC namespaces from approved rules"`

### Task 2: Remove the field from the API, sandbox context, and CLI

**Files:**
- Modify: `api/v1alpha1/agenticrun_types.go`, `api/v1alpha1/agenticrun_analysis_types.go`
- Generate: `api/v1alpha1/zz_generated.deepcopy.go`, `config/crd/bases/agentic.openshift.io_agenticruns.yaml`, `config/crd/bases/agentic.openshift.io_analysisresults.yaml`
- Modify: `controller/agenticrun/sandbox_agent.go`, `controller/agenticrun/schemas.go`
- Test: `controller/agenticrun/sandbox_agent_test.go`, `controller/agenticrun/input_configmap_test.go`
- Modify/test: `cli/run/create.go`, `cli/run/get.go`, `cli/run/list.go`, `cli/run/create_test.go`, `cli/run/testutil_test.go`
- Update affected run struct literals in `controller/agenticrun/*_test.go`, `test/e2e/*_test.go`, and API/CLI tests so they compile without `TargetNamespaces`.

**Interfaces:**
- Consumes: Task 1's `rbacTargetNamespaces(rbacResult)` behavior.
- Produces: `AgenticRunSpec` has no `TargetNamespaces`; sandbox agent context has no `targetNamespaces`; CLI create has no `--target-namespaces` option and run get/list output has no target-namespace column/line.

- [ ] **Step 1: Write failing boundary tests.** Add a Cobra command test asserting `NewCreateCmd(...).Flags().Lookup("target-namespaces") == nil`. Update the input ConfigMap test to parse the marshaled `context`, assert the `targetNamespaces` key is absent, and assert an unaffected context field is still present.
- [ ] **Step 2: Run `make fmt`, then `make test`; confirm the new CLI/context assertions fail** against the current flag and serialized context.
- [ ] **Step 3: Remove the API field and its validation.** Delete `TargetNamespaces`, its field markers/comments, the struct-level `targetNamespaces` immutability CEL rule, and comments/example fragments that claim the field exists. Change `RBACRule.Namespace` comments and the analysis schema's rule description so they no longer require matching run-level target namespaces.
- [ ] **Step 4: Remove sandbox/CLI consumption.** Delete the context field and builder assignment; remove target-namespace-specific schema/prompt wording; remove the CLI option, stored option, API assignment, and get/list rendering. Remove obsolete API/context assertions and update all Go struct literals that referenced the deleted field.
- [ ] **Step 5: Run `make manifests` and `bin/controller-gen object paths=./api/v1alpha1/...`** to regenerate CRD YAML and deep-copy code; inspect the generated diff to confirm `spec.targetNamespaces` and its CEL rule are absent while `targetCluster` remains.
- [ ] **Step 6: Run `make test` and confirm the CLI, context, API, and controller tests pass.**
- [ ] **Step 7: Commit the task.** `git add api controller cli config/crd/bases test/e2e && git commit -m "OLS-4346 Remove targetNamespaces API field"`

### Task 3: Remove obsolete namespace-field use from examples, mock agent, and documentation

**Files:**
- Modify: `test/agent/main.go`, `test/e2e/helpers_test.go`, `test/e2e/troubleshooting_test.go`, and affected e2e test run fixtures.
- Modify: `config/samples/agentic_v1alpha1_agenticrun.yaml`, `examples/setup/*.yaml`, `hack/quickstart/examples/*.yaml`, `README.md`, `hack/quickstart/README.md`, `docs/architecture-redesign-spec.md`, `docs/component-developer-guide.md`, `docs/rbac.md`, and `.ai/spec/` API/behavior docs including `what/crd-api.md`, `what/run-lifecycle.md`, `what/product-e2e-testing.md`, `what/sandbox-execution.md`, `how/cli.md`, and `how/reconciler.md`.
- Test: affected CLI/controller/e2e fixture tests; no new test is needed for prose-only edits.

**Interfaces:**
- Consumes: Task 2's API and context with no `targetNamespaces`.
- Produces: examples and documentation no longer tell users to set the removed field; mock-agent results use its existing `default` fallback rather than reading namespace values from sandbox context.

- [ ] **Step 1: Remove context parsing from the mock agent.** Delete its `targetNamespaces` context decoder and use the existing `default` fallback for canned action commands and RBAC rule namespace; remove corresponding contract comments.
- [ ] **Step 2: Update e2e scenario plumbing.** Remove `TargetNamespaces` from the parsed scenario type and run-creation helper signature; remove obsolete assignments from test fixtures and adjust any assertions that depended on the mock reading this field.
- [ ] **Step 3: Update examples, user/developer docs, and behavioral specs.** Remove the field from YAML examples and replace RBAC guidance that scopes to run-level namespaces with guidance that namespace-scoped rules identify their own `namespace`. Retain unrelated prose that merely discusses a target namespace or target cluster.
- [ ] **Step 4: Review all changed YAML/Markdown and run `make test`.**
- [ ] **Step 5: Commit the task.** `git add test config/samples examples hack/quickstart README.md docs .ai/spec api/v1alpha1/agenticrun_analysis_types.go config/crd/bases/agentic.openshift.io_analysisresults.yaml && git commit -m "OLS-4346 Update docs for removed namespace field"`

### Task 4: Final generated-artifact and repository verification

**Files:**
- Verify: all files changed by Tasks 1–3; generated manifests and deep-copy output.

**Interfaces:**
- Consumes: complete implementation from Tasks 1–3.
- Produces: verified removal with no stale active API, context, CLI, documentation, example, or fixture references.

- [ ] **Step 1: Run `make manifests` and `bin/controller-gen object paths=./api/v1alpha1/...`; verify neither produces further generated changes.**
- [ ] **Step 2: Run `make test` and `make api-lint`; both must pass.**
- [ ] **Step 3: Search active code, CRDs, examples, and docs for `TargetNamespaces`, `targetNamespaces`, and `--target-namespaces`. Confirm no removed API/context/CLI contract remains; any remaining occurrence must be an intentional historical/design reference or unrelated namespace wording, and must be explained in the review.
- [ ] **Step 4: Inspect `git diff --check` and `git status`; confirm only OLS-4346 changes remain and all generated artifacts are included.**
- [ ] **Step 5: Commit remaining docs and generated API-comment artifacts.** `git add .ai/spec/what/run-lifecycle.md .ai/spec/what/product-e2e-testing.md .ai/spec/how/cli.md .ai/spec/how/reconciler.md docs/superpowers/plans/2026-10-02-OLS-4346-remove-target-namespaces.md api/v1alpha1/agenticrun_analysis_types.go config/crd/bases/agentic.openshift.io_analysisresults.yaml && git commit -m "OLS-4346 Refresh namespace execution docs"`
