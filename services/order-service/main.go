package main

// Order Service — port 8080
//
// Routes:
//   POST   /orders         create order, publish "placed" to Kafka
//   PATCH  /orders/{id}    update status, publish event to Kafka
//   GET    /orders/{id}    fetch order state
//   GET    /health         K8s liveness/readiness probe

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"    // aliased to avoid clash with our /events pkg
	"github.com/yourusername/delivery-tracker/events"
)

var kafkaBroker = getenv("KAFKA_BROKER", "localhost:9092")
var orderTopic  = "order-events"

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" { return v }
	return fallback
}

// ── Domain ────────────────────────────────────────────────────────────────────

type Order struct {
	OrderID    string    `json:"order_id"`
	CustomerID string    `json:"customer_id"`
	AgentID    string    `json:"agent_id"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// ── In-memory store (swap for Postgres in production) ─────────────────────────

type Store struct {
	mu     sync.RWMutex
	orders map[string]*Order
}

func (s *Store) Save(o *Order) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orders[o.OrderID] = o
}

func (s *Store) Get(id string) (*Order, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orders[id]
	return o, ok
}

// ── Kafka producer ────────────────────────────────────────────────────────────

type Producer struct {
	w *kafkago.Writer
}

func NewProducer() *Producer {
	return &Producer{
		w: &kafkago.Writer{
			Addr:     kafkago.TCP(kafkaBroker),
			Topic:    orderTopic,
			Balancer: &kafkago.LeastBytes{},
		},
	}
}

func (p *Producer) Publish(ev events.OrderEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return p.w.WriteMessages(context.Background(), kafkago.Message{
		Key:   []byte(ev.OrderID), // partition key: same order → same partition → ordered
		Value: payload,
	})
}

func (p *Producer) Close() { p.w.Close() }

// ── HTTP server ───────────────────────────────────────────────────────────────

type Server struct {
	store    *Store
	producer *Producer
}

// POST /orders
func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CustomerID string `json:"customer_id"`
		AgentID    string `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	order := &Order{
		OrderID:    uuid.NewString(),
		CustomerID: body.CustomerID,
		AgentID:    body.AgentID,
		Status:     "placed",
		CreatedAt:  time.Now().UTC(),
	}
	s.store.Save(order)

	ev := events.OrderEvent{
		EventID:    uuid.NewString(),
		OrderID:    order.OrderID,
		CustomerID: order.CustomerID,
		AgentID:    order.AgentID,
		Status:     "placed",
		Timestamp:  time.Now().UTC(),
	}
	if err := s.producer.Publish(ev); err != nil {
		log.Printf("kafka publish error: %v", err) // don't fail the request
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(order)
}

// PATCH /orders/{id}
func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	order, ok := s.store.Get(id)
	if !ok {
		http.Error(w, "order not found", http.StatusNotFound)
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	order.Status = body.Status
	s.store.Save(order)

	ev := events.OrderEvent{
		EventID:    uuid.NewString(),
		OrderID:    order.OrderID,
		CustomerID: order.CustomerID,
		AgentID:    order.AgentID,
		Status:     body.Status,
		Timestamp:  time.Now().UTC(),
	}
	if err := s.producer.Publish(ev); err != nil {
		log.Printf("kafka publish error: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(order)
}

// GET /orders/{id}
func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	order, ok := s.store.Get(id)
	if !ok {
		http.Error(w, "order not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(order)
}

func main() {
	store    := &Store{orders: make(map[string]*Order)}
	producer := NewProducer()
	defer producer.Close()

	srv := &Server{store: store, producer: producer}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders",       srv.createOrder)
	mux.HandleFunc("PATCH /orders/{id}", srv.updateStatus)
	mux.HandleFunc("GET /orders/{id}",   srv.getOrder)
	mux.HandleFunc("GET /health",        func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Println("order-service listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}