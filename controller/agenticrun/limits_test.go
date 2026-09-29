package agenticrun

import (
	"testing"

	agenticv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"
)

func TestResolveTimeout(t *testing.T) {
	tests := []struct {
		name  string
		agent *agenticv1alpha1.Agent
		step  string
		want  int
	}{
		{
			name:  "nil agent uses default for analysis",
			agent: nil,
			step:  "analysis",
			want:  DefaultAnalysisTimeout,
		},
		{
			name:  "nil agent uses default for execution",
			agent: nil,
			step:  "execution",
			want:  DefaultExecutionTimeout,
		},
		{
			name:  "nil agent uses default for verification",
			agent: nil,
			step:  "verification",
			want:  DefaultVerificationTimeout,
		},
		{
			name:  "nil agent uses default for escalation",
			agent: nil,
			step:  "escalation",
			want:  DefaultEscalationTimeout,
		},
		{
			name: "configured analysis timeout",
			agent: &agenticv1alpha1.Agent{
				Spec: agenticv1alpha1.AgentSpec{
					Timeouts: agenticv1alpha1.AgentTimeouts{
						AnalysisSeconds: 300,
					},
				},
			},
			step: "analysis",
			want: 300,
		},
		{
			name: "configured execution timeout",
			agent: &agenticv1alpha1.Agent{
				Spec: agenticv1alpha1.AgentSpec{
					Timeouts: agenticv1alpha1.AgentTimeouts{
						ExecutionSeconds: 900,
					},
				},
			},
			step: "execution",
			want: 900,
		},
		{
			name: "configured verification timeout",
			agent: &agenticv1alpha1.Agent{
				Spec: agenticv1alpha1.AgentSpec{
					Timeouts: agenticv1alpha1.AgentTimeouts{
						VerificationSeconds: 2400,
					},
				},
			},
			step: "verification",
			want: 2400,
		},
		{
			name: "configured escalation timeout",
			agent: &agenticv1alpha1.Agent{
				Spec: agenticv1alpha1.AgentSpec{
					Timeouts: agenticv1alpha1.AgentTimeouts{
						EscalationSeconds: 450,
					},
				},
			},
			step: "escalation",
			want: 450,
		},
		{
			name: "empty timeouts uses defaults",
			agent: &agenticv1alpha1.Agent{
				Spec: agenticv1alpha1.AgentSpec{},
			},
			step: "analysis",
			want: DefaultAnalysisTimeout,
		},
		{
			name: "partial config falls back to default for unconfigured step",
			agent: &agenticv1alpha1.Agent{
				Spec: agenticv1alpha1.AgentSpec{
					Timeouts: agenticv1alpha1.AgentTimeouts{
						AnalysisSeconds: 250,
					},
				},
			},
			step: "execution",
			want: DefaultExecutionTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveTimeout(tt.agent, tt.step)
			if got != tt.want {
				t.Errorf("resolveTimeout() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestResolveMaxTurns(t *testing.T) {
	tests := []struct {
		name  string
		agent *agenticv1alpha1.Agent
		want  int
	}{
		{
			name:  "nil agent uses default",
			agent: nil,
			want:  DefaultMaxTurns,
		},
		{
			name: "configured maxTurns",
			agent: &agenticv1alpha1.Agent{
				Spec: agenticv1alpha1.AgentSpec{
					MaxTurns: 100,
				},
			},
			want: 100,
		},
		{
			name: "zero maxTurns uses default",
			agent: &agenticv1alpha1.Agent{
				Spec: agenticv1alpha1.AgentSpec{},
			},
			want: DefaultMaxTurns,
		},
		{
			name: "max maxTurns value",
			agent: &agenticv1alpha1.Agent{
				Spec: agenticv1alpha1.AgentSpec{
					MaxTurns: 500,
				},
			},
			want: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveMaxTurns(tt.agent)
			if got != tt.want {
				t.Errorf("resolveMaxTurns() = %d, want %d", got, tt.want)
			}
		})
	}
}
