package holmesgateway

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	registry       *prometheus.Registry
	investigations *prometheus.CounterVec
	modelCalls     *prometheus.CounterVec
	toolCalls      *prometheus.CounterVec
	sshFailures    *prometheus.CounterVec
	compactions    prometheus.Counter
	truncations    prometheus.Counter
	running        prometheus.Gauge
	awaiting       prometheus.Gauge
}

func NewMetrics(registry *prometheus.Registry) *Metrics {
	metrics := &Metrics{
		registry:       registry,
		investigations: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "holmes_gateway_investigations_total", Help: "Investigations by public model alias, status, and normalized code."}, []string{"model", "status", "code"}),
		modelCalls:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "holmes_gateway_model_calls_total", Help: "Holmes model calls by public alias and normalized result."}, []string{"model", "result"}),
		toolCalls:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "holmes_gateway_tool_calls_total", Help: "Bounded diagnostic calls by tool and result."}, []string{"tool", "result"}),
		sshFailures:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "holmes_gateway_ssh_stage_failures_total", Help: "SSH and Erlang diagnostic failures by explicit stage."}, []string{"stage", "code"}),
		compactions:    prometheus.NewCounter(prometheus.CounterOpts{Name: "holmes_gateway_compactions_total", Help: "Holmes conversation compactions."}),
		truncations:    prometheus.NewCounter(prometheus.CounterOpts{Name: "holmes_gateway_tool_output_truncations_total", Help: "Tool outputs truncated by gateway limits."}),
		running:        prometheus.NewGauge(prometheus.GaugeOpts{Name: "holmes_gateway_running_investigations", Help: "Currently running investigations."}),
		awaiting:       prometheus.NewGauge(prometheus.GaugeOpts{Name: "holmes_gateway_awaiting_approval_investigations", Help: "Investigations awaiting a user decision."}),
	}
	registry.MustRegister(metrics.investigations, metrics.modelCalls, metrics.toolCalls, metrics.sshFailures, metrics.compactions, metrics.truncations, metrics.running, metrics.awaiting)
	return metrics
}

func (m *Metrics) stateTransition(from, to SessionStatus) {
	if m == nil || from == to {
		return
	}
	if from == StatusRunning {
		m.running.Dec()
	}
	if from == StatusAwaitingApproval {
		m.awaiting.Dec()
	}
	if to == StatusRunning {
		m.running.Inc()
	}
	if to == StatusAwaitingApproval {
		m.awaiting.Inc()
	}
}
