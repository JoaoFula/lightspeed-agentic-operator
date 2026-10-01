package ocpversion

import (
	"context"
	"errors"
	"strings"
	"testing"

	agenticv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type failingReader struct{ client.Reader }

func (failingReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return errors.New("forbidden")
}

type configFailReader struct{ client.Reader }

func (f configFailReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*agenticv1alpha1.AgenticOLSConfig); ok {
		return errors.New("config API unavailable")
	}
	return f.Reader.Get(ctx, key, obj, opts...)
}

type allowHandler struct{}

func (allowHandler) Handle(_ context.Context, _ admission.Request) admission.Response {
	return admission.Allowed("ok")
}

func TestTriStateEligibilityAndAdmission(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := agenticv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cv := Object()
	cv.SetName("version")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cv).Build()
	gate := &Gate{Reader: c}
	admissionGate := Admission{Gate: gate, Next: allowHandler{}}
	check := func(want Eligibility) {
		t.Helper()
		if got, err := gate.State(ctx); got != want || err != nil {
			t.Fatalf("State() = %q, %v; want %q, nil", got, err, want)
		}
		if got := admissionGate.Handle(ctx, admission.Request{}).Allowed; got != (want == EligibilityEnabled) {
			t.Fatalf("admission allowed = %v, want %v for %s", got, want == EligibilityEnabled, want)
		}
	}
	check(EligibilityDisabled) // absent AgenticOLSConfig is explicit non-opt-in
	if err := c.Create(ctx, &agenticv1alpha1.AgenticOLSConfig{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}); err != nil {
		t.Fatal(err)
	}
	check(EligibilityUnknown) // an unreported version is neither Yes nor No

	partial := Object()
	if err := c.Get(ctx, client.ObjectKey{Name: "version"}, partial); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(partial.Object, "5.0.0", "status", "desired", "version"); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedSlice(partial.Object, []interface{}{map[string]interface{}{"state": "Partial", "version": "5.0.0"}}, "status", "history"); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(ctx, partial); err != nil {
		t.Fatal(err)
	}
	check(EligibilityUnknown) // desired advanced, rollout not yet completed

	if err := c.Get(ctx, client.ObjectKey{Name: "version"}, partial); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedSlice(partial.Object, []interface{}{map[string]interface{}{"state": "Completed", "version": "5.0.0"}}, "status", "history"); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(ctx, partial); err != nil {
		t.Fatal(err)
	}
	check(EligibilityEnabled)

	for _, tc := range []struct {
		version string
		history []interface{}
		want    Eligibility
	}{
		{"4.22.0", []interface{}{map[string]interface{}{"state": "Completed", "version": "4.22.0"}}, EligibilityDisabled},
		{"5.0.0", []interface{}{map[string]interface{}{"state": "Completed", "version": "5.0.0"}}, EligibilityEnabled},
		{"5.1.0", []interface{}{map[string]interface{}{"state": "Completed", "version": "5.1.0"}}, EligibilityEnabled},
		{"5.1.0", []interface{}{map[string]interface{}{"state": "Completed", "version": "5.0.0"}}, EligibilityUnknown},
		{"5.1.0", []interface{}{map[string]interface{}{"state": "Partial", "version": "5.1.0"}}, EligibilityUnknown},
		{"5.0.0", nil, EligibilityUnknown},
		{"5.0.0", []interface{}{}, EligibilityUnknown},
		{"5.0.0", []interface{}{map[string]interface{}{"state": "Completed"}}, EligibilityUnknown},
		{"invalid", []interface{}{map[string]interface{}{"state": "Completed", "version": "invalid"}}, EligibilityUnknown},
		{"5.not-a-minor", []interface{}{map[string]interface{}{"state": "Completed", "version": "5.not-a-minor"}}, EligibilityUnknown},
		{"", []interface{}{map[string]interface{}{"state": "Completed", "version": ""}}, EligibilityUnknown},
	} {
		obj := Object()
		if err := c.Get(ctx, client.ObjectKey{Name: "version"}, obj); err != nil {
			t.Fatal(err)
		}
		if err := unstructured.SetNestedField(obj.Object, tc.version, "status", "desired", "version"); err != nil {
			t.Fatal(err)
		}
		if tc.history == nil {
			delete(obj.Object["status"].(map[string]interface{}), "history")
		} else if err := unstructured.SetNestedSlice(obj.Object, tc.history, "status", "history"); err != nil {
			t.Fatal(err)
		}
		if err := c.Update(ctx, obj); err != nil {
			t.Fatal(err)
		}
		check(tc.want)
	}

	if state, err := (&Gate{Reader: failingReader{}}).State(ctx); state != EligibilityUnknown || err == nil {
		t.Fatalf("unreadable ClusterVersion must be Unknown with retryable error: state=%s err=%v", state, err)
	}
	if state, err := (&Gate{Reader: configFailReader{Reader: c}}).State(ctx); state != EligibilityUnknown || err == nil {
		t.Fatalf("unreadable AgenticOLSConfig must be Unknown with retryable error: state=%s err=%v", state, err)
	}
	if versionEnabled, err := (&Gate{Reader: failingReader{}}).Check(ctx); versionEnabled || err == nil {
		t.Fatalf("version read failure must fail closed and be retried: enabled=%v err=%v", versionEnabled, err)
	}

	if err := c.Delete(ctx, &agenticv1alpha1.AgenticOLSConfig{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}); err != nil {
		t.Fatal(err)
	}
	check(EligibilityDisabled) // deleting the CR disables Agentic even if OCP state is Unknown
}

func TestAdmissionDistinguishesDisabledFromEligibilityReadError(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := agenticv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cv := Object()
	cv.SetName("version")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cv).Build()

	disabled := Admission{Gate: &Gate{Reader: c}, Next: allowHandler{}}.Handle(ctx, admission.Request{})
	if disabled.Allowed || !strings.Contains(disabled.Result.Message, "agentic operations are disabled") {
		t.Fatalf("disabled eligibility response = %q, want intentional-disabled message", disabled.Result.Message)
	}

	failed := Admission{Gate: &Gate{Reader: failingReader{Reader: c}}, Next: allowHandler{}}.Handle(ctx, admission.Request{})
	if failed.Allowed || !strings.Contains(failed.Result.Message, "unable to determine agentic eligibility") || !strings.Contains(failed.Result.Message, "forbidden") {
		t.Fatalf("eligibility read failure response = %q, want diagnostic error message", failed.Result.Message)
	}
}
