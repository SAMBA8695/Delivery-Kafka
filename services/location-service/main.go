package main

// Location Service — port 8081
//
// Routes:
//   POST  /ping     receive GPS ping from delivery agent, publish to Kafka
//   GET   /health   K8s liveness/readiness probe
//
// This is the highest-traffic service: 1000 agents × 1 ping/3s = ~333 req/sec.
// The K8s HPA scales it up to 10 replicas automatically during peak hours.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/yourusername/delivery-tracker/events"
)

var kafkaBroker   = getenv("KAFKA_BROKER", "localhost:9092")
var locationTopic = "location-events"

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" { return v }
	return fallback
}

// ── Kafka producer ────────────────────────────────────────────────────────────

type Producer struct {
	w *kafkago.Writer
}

func NewProducer() *Producer {
	return &Producer{
		w: &kafkago.Writer{
			Addr:     kafkago.TCP(kafkaBroker),
			Topic:    locationTopic,
			Balancer: &kafkago.LeastBytes{},
			// Batching: instead of one Kafka write per HTTP request,
			// collect up to 100 messages or wait 10ms, then flush together.
			// This is what handles 333 req/sec without hammering the broker.
			BatchTimeout: 10 * time.Millisecond,
			BatchSize:    100,
		},
	}
}

func (p *Producer) Publish(ev events.LocationEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return p.w.WriteMessages(context.Background(), kafkago.Message{
		Key:   []byte(ev.AgentID), // same agent → same partition → ordered history
		Value: payload,
	})
}

func (p *Producer) Close() { p.w.Close() }

// ── HTTP handlers ─────────────────────────────────────────────────────────────

type Server struct {
	producer *Producer
}

// POST /ping — called by delivery agent app every ~3 seconds
func (s *Server) ping(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID   string  `json:"agent_id"`
		OrderID   string  `json:"order_id"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ev := events.LocationEvent{
		EventID:   uuid.NewString(),
		AgentID:   req.AgentID,
		OrderID:   req.OrderID,
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
		Timestamp: time.Now().UTC(),
	}
	if err := s.producer.Publish(ev); err != nil {
		log.Printf("kafka publish error: %v", err)
		http.Error(w, "failed to publish", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted) // 202: accepted, not yet processed downstream
}

func main() {
	producer := NewProducer()
	defer producer.Close()

	srv := &Server{producer: producer}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /ping",  srv.ping)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Println("location-service listening on :8081")
	log.Fatal(http.ListenAndServe(":8081", mux))
}