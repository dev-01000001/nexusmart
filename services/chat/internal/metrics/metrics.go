package chatmetrics

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds domain-specific Prometheus collectors for the chat service.
type Metrics struct {
	WSConnectionsActive prometheus.Gauge
	MessagesTotal       *prometheus.CounterVec
	PubSubEventsTotal   *prometheus.CounterVec
	WSDroppedTotal      prometheus.Counter
}

// New registers chat metrics on reg (defaults to prometheus.DefaultRegisterer).
// If reg is nil, collectors are created unregistered (useful for unit tests).
func New(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		WSConnectionsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chat_ws_connections_active",
			Help: "Current number of active WebSocket connections on this chat instance.",
		}),
		MessagesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "chat_messages_total",
			Help: "Total number of persisted chat messages.",
		}, []string{"kind", "sender_role", "auto_reply"}),
		PubSubEventsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "chat_pubsub_events_total",
			Help: "Total number of Redis pub/sub events processed.",
		}, []string{"direction", "type"}),
		WSDroppedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "chat_ws_dropped_messages_total",
			Help: "Total number of outbound WS frames dropped due to full per-client send buffer.",
		}),
	}
	if reg != nil {
		reg.MustRegister(
			m.WSConnectionsActive,
			m.MessagesTotal,
			m.PubSubEventsTotal,
			m.WSDroppedTotal,
		)
	}
	return m
}
