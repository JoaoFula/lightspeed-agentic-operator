package main

import (
	"context"
	"testing"

	agenticv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"
	"github.com/openshift/lightspeed-agentic-operator/controller/agenticrun"
	"github.com/openshift/lightspeed-agentic-operator/pkg/configuration"
	"github.com/openshift/lightspeed-agentic-operator/pkg/ocpversion"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHandoffDeletedDuringUnknownEligibilityIsNotReused(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := agenticv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cv := ocpversion.Object()
	cv.SetName("version")
	cv.Object["status"] = map[string]interface{}{
		"desired": map[string]interface{}{"version": "5.0.0"},
		"history": []interface{}{map[string]interface{}{"state": "Partial", "version": "5.0.0"}},
	}
	optIn := &agenticv1alpha1.AgenticOLSConfig{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cv, optIn).Build()
	gate := &ocpversion.Gate{Reader: c}
	if state, err := gate.State(ctx); state != ocpversion.EligibilityUnknown || err != nil {
		t.Fatalf("gate = %q, %v; want Unknown", state, err)
	}
	cache := &configuration.Cache{}
	handoff := &corev1.ConfigMap{Data: map[string]string{configuration.KeyTerminalTTLDays: "7"}}
	if err := cache.OnConfigMapChange(ctx, handoff); err != nil {
		t.Fatal(err)
	}
	if err := gatedConfigMapHandler(cache, gate)(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if cache.Available() {
		t.Fatal("deleting the ConfigMap during Unknown eligibility left a cached handoff")
	}

	// Even if the delete event was dropped, completion must not retain stale config.
	if err := cache.OnConfigMapChange(ctx, handoff); err != nil {
		t.Fatal(err)
	}
	cv.Object["status"] = map[string]interface{}{
		"desired": map[string]interface{}{"version": "5.0.0"},
		"history": []interface{}{map[string]interface{}{"state": "Completed", "version": "5.0.0"}},
	}
	if err := c.Update(ctx, cv); err != nil {
		t.Fatal(err)
	}
	if state, err := gate.State(ctx); state != ocpversion.EligibilityEnabled || err != nil {
		t.Fatalf("gate after upgrade = %q, %v; want Enabled", state, err)
	}
	available, err := reloadHandoff(ctx, c, "lightspeed", cache)
	if err != nil || available || cache.Available() {
		t.Fatalf("missing handoff after activation: available=%t cache=%t err=%v", available, cache.Available(), err)
	}
}

func TestDeactivateAgenticClearsRuntimeResources(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := agenticv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	namespace := "lightspeed"
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "lightspeed-agent", Namespace: namespace}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sa).Build()
	cache := &configuration.Cache{}
	cache.SetOTELProvider(configuration.NewProvider(&agenticrun.AgenticRunIDGenerator{}))
	if err := cache.OnConfigMapChange(ctx, &corev1.ConfigMap{Data: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	runs := &agenticrun.AgenticRunReconciler{Client: c, Agent: &agenticrun.StubAgentCaller{}, Namespace: namespace}
	if err := deactivateAgentic(ctx, runs, cache, c, namespace); err != nil {
		t.Fatal(err)
	}
	if cache.Available() {
		t.Fatal("deactivation retained cached handoff configuration")
	}
	if err := c.Get(ctx, types.NamespacedName{Name: sa.Name, Namespace: sa.Namespace}, &corev1.ServiceAccount{}); !apierrors.IsNotFound(err) {
		t.Fatalf("runtime ServiceAccount remains after deactivation: %v", err)
	}
}
