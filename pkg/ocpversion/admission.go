package ocpversion

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Admission rejects writes unless Agentic is opted in and the cluster version is positively >= 5.0.
// In particular a mutating webhook must not return Allowed without a patch.
type Admission struct {
	Gate *Gate
	Next admission.Handler
}

func (a Admission) Handle(ctx context.Context, req admission.Request) admission.Response {
	state, err := a.Gate.State(ctx)
	if err != nil {
		return admission.Denied(fmt.Sprintf("unable to determine agentic eligibility: %v", err))
	}
	if state != EligibilityEnabled {
		return admission.Denied("agentic operations are disabled: explicit AgenticOLSConfig/cluster opt-in and a completed supported OpenShift version are required")
	}
	return a.Next.Handle(ctx, req)
}
