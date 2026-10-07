package agenticrun

import (
	"context"
	"errors"
	"testing"
	"time"

	agenticv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-agentic-operator/pkg/configuration"
	"github.com/openshift/lightspeed-agentic-operator/pkg/ocpversion"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type unavailableVersionReader struct{ client.Reader }

func (unavailableVersionReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return errors.New("ClusterVersion API temporarily unavailable")
}

func TestVersionGateUnknownAddsFinalizersBeforePauseAndCleansUpOnDelete(t *testing.T) {
	ctx := context.Background()
	run := testAgenticRun()
	run.UID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	cv := ocpversion.Object()
	cv.SetName("version")
	cv.Object["status"] = map[string]interface{}{
		"desired": map[string]interface{}{"version": "5.0.0"},
		"history": []interface{}{map[string]interface{}{"state": "Partial", "version": "5.0.0"}},
	}
	config := &agenticv1alpha1.AgenticOLSConfig{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(run, cv, config).WithStatusSubresource(run).Build()
	caller := newTestAgentCaller()
	cleaner := &mockTempLogCleaner{}
	r := &AgenticRunReconciler{Client: c, Agent: caller, TempLog: cleaner, Version: &ocpversion.Gate{Reader: c}}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}

	result, err := r.Reconcile(ctx, req)
	if err != nil || result.RequeueAfter != ocpversion.ClusterVersionRequeueInterval {
		t.Fatalf("Unknown eligibility should pause and requeue, result=%+v err=%v", result, err)
	}
	var got agenticv1alpha1.AgenticRun
	if err := c.Get(ctx, req.NamespacedName, &got); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&got, rbacCleanupFinalizer) || !controllerutil.ContainsFinalizer(&got, templogCleanupFinalizer) {
		t.Fatalf("Unknown eligibility left run without cleanup finalizers: %v", got.Finalizers)
	}
	if caller.releaseAllCount != 0 || cleaner.callCount != 0 {
		t.Fatal("paused run was cleaned before deletion")
	}

	if err := c.Delete(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if caller.releaseAllCount != 1 || cleaner.callCount != 1 {
		t.Fatalf("deletion cleanup calls: sandbox=%d templog=%d, want 1 each", caller.releaseAllCount, cleaner.callCount)
	}
	if err := c.Get(ctx, req.NamespacedName, &got); client.IgnoreNotFound(err) != nil || err == nil {
		t.Fatalf("deleted run still exists or get failed: %v", err)
	}
}

func TestDeactivationConditionType(t *testing.T) {
	cases := []struct {
		phase agenticv1alpha1.AgenticRunPhase
		want  string
	}{
		{agenticv1alpha1.AgenticRunPhasePending, agenticv1alpha1.AgenticRunConditionAnalyzed},
		{agenticv1alpha1.AgenticRunPhaseAnalyzing, agenticv1alpha1.AgenticRunConditionAnalyzed},
		{agenticv1alpha1.AgenticRunPhaseProposed, agenticv1alpha1.AgenticRunConditionExecuted},
		{agenticv1alpha1.AgenticRunPhaseExecuting, agenticv1alpha1.AgenticRunConditionExecuted},
		{agenticv1alpha1.AgenticRunPhaseVerifying, agenticv1alpha1.AgenticRunConditionVerified},
		{agenticv1alpha1.AgenticRunPhaseEscalating, agenticv1alpha1.AgenticRunConditionEscalated},
	}
	for _, tc := range cases {
		t.Run(string(tc.phase), func(t *testing.T) {
			got, err := deactivationConditionType(tc.phase)
			if err != nil || got != tc.want {
				t.Fatalf("deactivationConditionType(%q) = %q, %v; want %q", tc.phase, got, err, tc.want)
			}
		})
	}
	if got, err := deactivationConditionType(agenticv1alpha1.AgenticRunPhase("future-phase")); err == nil || got != "" {
		t.Fatalf("unhandled phase must fail without selecting a condition: %q, %v", got, err)
	}
}

func TestDeactivateRunDoesNotReleaseSandboxesForTerminalRun(t *testing.T) {
	run := testAgenticRun()
	run.Status.Conditions = []metav1.Condition{
		{Type: agenticv1alpha1.AgenticRunConditionAnalyzed, Status: metav1.ConditionTrue, Reason: "Complete"},
		{Type: agenticv1alpha1.AgenticRunConditionExecuted, Status: metav1.ConditionFalse, Reason: "AgenticDisabled"},
	}
	run.Status.Steps.Execution.Sandbox.ClaimName = "run-execution"
	caller := newTestAgentCaller()
	r := &AgenticRunReconciler{Agent: caller}

	if err := r.DeactivateRun(context.Background(), run, "disabled"); err != nil {
		t.Fatal(err)
	}
	if caller.releaseAllCount != 0 {
		t.Fatalf("terminal deactivation released sandbox resources %d times, want 0", caller.releaseAllCount)
	}
}

func TestDeactivateFinalizesRunsBeforeClearingHandoff(t *testing.T) {
	ctx := context.Background()
	run := testAgenticRun()
	run.Status.Steps.Analysis.Sandbox.ClaimName = "analysis-claim"
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(run).WithStatusSubresource(run).Build()
	caller := newTestAgentCaller()
	r := &AgenticRunReconciler{Client: c, Agent: caller, Config: ttlTestCache(t, "3")}
	if err := r.Deactivate(ctx, "disabled"); err != nil {
		t.Fatal(err)
	}
	var got agenticv1alpha1.AgenticRun
	if err := c.Get(ctx, client.ObjectKeyFromObject(run), &got); err != nil {
		t.Fatal(err)
	}
	if caller.releaseAllCount != 1 || agenticv1alpha1.DerivePhase(got.Status.Conditions) != agenticv1alpha1.AgenticRunPhaseFailed || got.Status.TerminalTime == nil || got.Status.DeleteAfter == nil || got.Labels[terminalTTLLabel] != "true" {
		t.Fatalf("bulk deactivation skipped cleanup: releases=%d status=%+v labels=%v", caller.releaseAllCount, got.Status, got.Labels)
	}
	if want := 3 * 24 * time.Hour; got.Status.DeleteAfter.Sub(got.Status.TerminalTime.Time) != want {
		t.Fatalf("retention = %s, want %s", got.Status.DeleteAfter.Sub(got.Status.TerminalTime.Time), want)
	}
}

func TestVersionGateDisabledCleansUpAlreadyTerminalRun(t *testing.T) {
	ctx := context.Background()
	run := terminalTestRun()
	run.Status.Steps.Verification.Sandbox.ClaimName = "verification-claim"
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(run).WithStatusSubresource(run).Build()
	caller := newTestAgentCaller()
	r := &AgenticRunReconciler{Client: c, Agent: caller, Version: &ocpversion.Gate{Reader: c}}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
		t.Fatal(err)
	}
	var got agenticv1alpha1.AgenticRun
	if err := c.Get(ctx, client.ObjectKeyFromObject(run), &got); err != nil {
		t.Fatal(err)
	}
	if caller.releaseAllCount != 1 || got.Status.TerminalTime == nil || got.Status.DeleteAfter == nil || got.Labels[terminalTTLLabel] != "true" {
		t.Fatalf("disabled terminal cleanup: releases=%d status=%+v labels=%v", caller.releaseAllCount, got.Status, got.Labels)
	}
}

func TestVersionGateDeactivatesOnFourAndRequiresExplicitConfig(t *testing.T) {
	ctx := context.Background()
	inactiveRun := testAgenticRun()
	inactiveRun.Name = "inactive-run"
	inactiveRun.Status.Conditions = []metav1.Condition{
		{Type: agenticv1alpha1.AgenticRunConditionAnalyzed, Status: metav1.ConditionTrue, Reason: "Complete"},
		{Type: agenticv1alpha1.AgenticRunConditionExecuted, Status: metav1.ConditionUnknown, Reason: "InProgress"},
	}
	inactiveRun.Status.Steps.Execution.Sandbox.ClaimName = "run-execution"
	cv := ocpversion.Object()
	cv.SetName("version")
	cv.Object["status"] = map[string]interface{}{
		"desired": map[string]interface{}{"version": "4.22.0"},
		"history": []interface{}{map[string]interface{}{"state": "Completed", "version": "4.22.0"}},
	}
	config := &agenticv1alpha1.AgenticOLSConfig{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	c := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(inactiveRun, cv).WithStatusSubresource(inactiveRun).Build()
	caller := newTestAgentCaller()
	r := &AgenticRunReconciler{Client: c, Agent: caller, Version: &ocpversion.Gate{Reader: c}}
	inactiveReq := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(inactiveRun)}
	if _, err := r.Reconcile(ctx, inactiveReq); err != nil {
		t.Fatal(err)
	}
	if caller.releaseAllCount != 1 {
		t.Fatalf("inactive reconcile released sandbox resources %d times, want 1", caller.releaseAllCount)
	}
	got := testAgenticRun()
	if err := c.Get(ctx, inactiveReq.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	executed := meta.FindStatusCondition(got.Status.Conditions, agenticv1alpha1.AgenticRunConditionExecuted)
	if executed == nil || executed.Status != metav1.ConditionFalse || executed.Reason != "AgenticDisabled" {
		t.Fatalf("unsupported cluster did not terminalize in-flight run: %#v", executed)
	}
	if phase := agenticv1alpha1.DerivePhase(got.Status.Conditions); phase != agenticv1alpha1.AgenticRunPhaseFailed {
		t.Fatalf("deactivated run phase = %s, want Failed", phase)
	}
	if got.Status.TerminalTime == nil || got.Status.DeleteAfter == nil || got.Labels[terminalTTLLabel] != "true" {
		t.Fatalf("disabled run did not complete terminal cleanup: status=%+v labels=%v", got.Status, got.Labels)
	}
	if want := time.Duration(configuration.DefaultTerminalTTLDays) * 24 * time.Hour; got.Status.DeleteAfter.Sub(got.Status.TerminalTime.Time) != want {
		t.Fatalf("fallback retention = %s, want %s", got.Status.DeleteAfter.Sub(got.Status.TerminalTime.Time), want)
	}

	activeRun := testAgenticRun()
	activeRun.Name = "active-run"
	if err := c.Create(ctx, activeRun); err != nil {
		t.Fatal(err)
	}
	activeReq := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(activeRun)}
	if err := c.Create(ctx, config.DeepCopy()); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKey{Name: "version"}, cv); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(cv.Object, "5.0.0", "status", "desired", "version"); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedSlice(cv.Object, []interface{}{map[string]interface{}{"state": "Partial", "version": "5.0.0"}}, "status", "history"); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(ctx, cv); err != nil {
		t.Fatal(err)
	}
	if state, err := r.Version.State(ctx); state != ocpversion.EligibilityUnknown || err != nil {
		t.Fatalf("partial upgrade state = %s, %v; want Unknown", state, err)
	}
	if result, err := r.Reconcile(ctx, activeReq); err != nil || result.RequeueAfter == 0 {
		t.Fatalf("partial upgrade should pause and requeue, result=%+v err=%v", result, err)
	}
	if err := c.Get(ctx, activeReq.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	if agenticv1alpha1.DerivePhase(got.Status.Conditions) != agenticv1alpha1.AgenticRunPhasePending {
		t.Fatalf("Unknown eligibility changed run phase to %s", agenticv1alpha1.DerivePhase(got.Status.Conditions))
	}

	if err := c.Get(ctx, client.ObjectKey{Name: "version"}, cv); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedSlice(cv.Object, []interface{}{map[string]interface{}{"state": "Completed", "version": "5.0.0"}}, "status", "history"); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(ctx, cv); err != nil {
		t.Fatal(err)
	}
	if state, err := r.Version.State(ctx); state != ocpversion.EligibilityEnabled || err != nil {
		t.Fatalf("completed supported version plus AgenticOLSConfig state = %s, %v; want Enabled", state, err)
	}
	if _, err := r.Reconcile(ctx, activeReq); err == nil {
		t.Fatal("expected missing handoff configuration after activation")
	}

	r.Version = &ocpversion.Gate{Reader: unavailableVersionReader{}}
	if _, err := r.Reconcile(ctx, activeReq); err == nil {
		t.Fatal("transient read failure must be retried")
	}
	if err := c.Get(ctx, activeReq.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	if phase := agenticv1alpha1.DerivePhase(got.Status.Conditions); phase == agenticv1alpha1.AgenticRunPhaseFailed {
		t.Fatal("transient eligibility read error must not terminalize an in-flight run")
	}
	if caller.releaseAllCount != 1 {
		t.Fatalf("transient read error triggered cleanup; ReleaseSandboxes count=%d", caller.releaseAllCount)
	}
	r.Version = &ocpversion.Gate{Reader: c}
	if _, err := r.Reconcile(ctx, activeReq); err == nil {
		t.Fatal("expected missing handoff configuration after version recovery")
	}
	if err := c.Get(ctx, activeReq.NamespacedName, got); err != nil {
		t.Fatal(err)
	}
	if len(got.Finalizers) == 0 {
		t.Fatal("run did not resume reconciliation after version recovery")
	}
}
