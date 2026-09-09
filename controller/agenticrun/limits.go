package agenticrun

import (
	agenticv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"
)

const (
	// Default timeout values for each step in seconds
	DefaultAnalysisTimeout     = 600  // 10 minutes
	DefaultExecutionTimeout    = 600  // 10 minutes
	DefaultVerificationTimeout = 1800 // 30 minutes
	DefaultEscalationTimeout   = 600  // 10 minutes

	// Default max turns per step
	DefaultMaxTurns = 200
)

// resolveTimeout returns the effective agent timeout in seconds for the given step.
// It checks the Agent CR for configured timeouts and falls back to defaults.
// The step parameter uses lowercase strings ("analysis", "execution", etc.)
// as used throughout the controller, not the title-case SandboxStep constants.
func resolveTimeout(agent *agenticv1alpha1.Agent, step string) int {
	if agent != nil {
		switch step {
		case "analysis":
			if agent.Spec.Timeouts.AnalysisSeconds > 0 {
				return int(agent.Spec.Timeouts.AnalysisSeconds)
			}
		case "execution":
			if agent.Spec.Timeouts.ExecutionSeconds > 0 {
				return int(agent.Spec.Timeouts.ExecutionSeconds)
			}
		case "verification":
			if agent.Spec.Timeouts.VerificationSeconds > 0 {
				return int(agent.Spec.Timeouts.VerificationSeconds)
			}
		case "escalation":
			if agent.Spec.Timeouts.EscalationSeconds > 0 {
				return int(agent.Spec.Timeouts.EscalationSeconds)
			}
		}
	}

	switch step {
	case "execution":
		return DefaultExecutionTimeout
	case "verification":
		return DefaultVerificationTimeout
	case "escalation":
		return DefaultEscalationTimeout
	default:
		return DefaultAnalysisTimeout
	}
}

// resolveMaxTurns returns the effective max turns.
// It checks the Agent CR for configured max turns and falls back to the default.
func resolveMaxTurns(agent *agenticv1alpha1.Agent) int {
	if agent != nil && agent.Spec.MaxTurns > 0 {
		return int(agent.Spec.MaxTurns)
	}
	return DefaultMaxTurns
}
