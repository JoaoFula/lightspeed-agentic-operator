// Package ocpversion guards agentic work using the live OpenShift ClusterVersion.
// Eligibility is tri-state: unknown or incomplete version data is never treated
// as either a confirmed enablement or a confirmed disablement.
package ocpversion

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	agenticv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var GVK = schema.GroupVersionKind{Group: "config.openshift.io", Version: "v1", Kind: "ClusterVersion"}

// Eligibility is the Agentic runtime's view of the shared version/opt-in gate.
type Eligibility string

const (
	EligibilityUnknown  Eligibility = "Unknown"
	EligibilityDisabled Eligibility = "Disabled"
	EligibilityEnabled  Eligibility = "Enabled"
)

const ClusterVersionRequeueInterval = time.Minute

func Object() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(GVK)
	return obj
}

// Gate uses a direct API reader, not the manager's cached client: stale cached
// versions must not authorize operations after a version change or lookup failure.
type Gate struct{ Reader client.Reader }

// State combines confirmed OCP support with explicit customer opt-in.
// Unknown version/config reads block new work but do not trigger destructive
// deactivation; callers should retry while preserving existing runtime state.
func (g *Gate) State(ctx context.Context) (Eligibility, error) {
	versionState, versionErr := g.versionState(ctx)
	if g == nil || g.Reader == nil {
		return versionState, versionErr
	}

	var config agenticv1alpha1.AgenticOLSConfig
	configErr := g.Reader.Get(ctx, client.ObjectKey{Name: "cluster"}, &config)
	if apierrors.IsNotFound(configErr) {
		return EligibilityDisabled, nil // explicit opt-in is absent
	}
	if versionState == EligibilityDisabled {
		return EligibilityDisabled, nil // confirmed unsupported completed OCP release
	}
	if versionErr != nil {
		return EligibilityUnknown, versionErr
	}
	if configErr != nil {
		return EligibilityUnknown, fmt.Errorf("read AgenticOLSConfig/cluster: %w", configErr)
	}
	if versionState == EligibilityUnknown {
		return EligibilityUnknown, nil
	}
	return EligibilityEnabled, nil
}

// Enabled is a convenience for non-destructive event filters. Reconciliation
// paths that need to distinguish Unknown from Disabled must use State.
func (g *Gate) Enabled(ctx context.Context) bool {
	state, err := g.State(ctx)
	return err == nil && state == EligibilityEnabled
}

// Check reports only the version portion of the gate. It is retained for
// environment checks such as the e2e suite; runtime callers must use State.
func (g *Gate) Check(ctx context.Context) (bool, error) {
	state, err := g.versionState(ctx)
	return state == EligibilityEnabled, err
}

func (g *Gate) versionState(ctx context.Context) (Eligibility, error) {
	if g == nil || g.Reader == nil {
		return EligibilityEnabled, nil // nil gate is used only by legacy unit fixtures
	}
	obj := Object()
	if err := g.Reader.Get(ctx, client.ObjectKey{Name: "version"}, obj); err != nil {
		return EligibilityUnknown, fmt.Errorf("read ClusterVersion/version: %w", err)
	}
	version, found, err := unstructured.NestedString(obj.Object, "status", "desired", "version")
	if err != nil || !found {
		return EligibilityUnknown, nil
	}
	// Status.Desired advances at the start of an upgrade. Until history confirms
	// the same desired release as Completed, the gate is Unknown, not Disabled.
	history, found, err := unstructured.NestedSlice(obj.Object, "status", "history")
	if err != nil || !found || len(history) == 0 {
		return EligibilityUnknown, nil
	}
	entry, ok := history[0].(map[string]interface{})
	if !ok || entry["state"] != "Completed" || entry["version"] != version {
		return EligibilityUnknown, nil
	}
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return EligibilityUnknown, nil
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return EligibilityUnknown, nil
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return EligibilityUnknown, nil
	}
	if major < 5 {
		return EligibilityDisabled, nil
	}
	return EligibilityEnabled, nil
}
