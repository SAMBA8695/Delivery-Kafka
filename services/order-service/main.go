package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/SAMBA8695/Delivery-Kafka/events"
)

var kafkaBroker = getenv("KAFKA_BROKER", "localhost:9092")
var orderTopic  = "order-events"

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" { return v }
	return fallback
}

type Order struct {
	OrderID    string    `json:"order_id"`
	CustomerID string    `json:"customer_id"`
	AgentID    string    `json:"agent_id"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

type Store struct {
	mu     sync.RWMutex
	orders map[string]*Order
}

func (s *Store) Save(o *Order) {
	s.mu.Lock(); defer s.mu.Unlock()
	s.orders[o.OrderID] = o
}

func (s *Store) Get(id string) (*Order, bool) {
	s.mu.RLock(); defer s.mu.RUnlock()
	o, ok := s.orders[id]
	return o, ok
}

type Producer struct{ w *kafkago.Writer }

func NewProducer() *Producer {
	return &Producer{w: &kafkago.Writer{
		Addr:     kafkago.TCP(kafkaBroker),
		Topic:    orderTopic,
		Balancer: &kafkago.LeastBytes{},
	}}
}

func (p *Producer) Publish(ev events.OrderEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil { return err }
	return p.w.WriteMessages(context.Background(), kafkago.Message{
		Key: []byte(ev.OrderID), Value: payload,
	})
}

func (p *Producer) Close() { p.w.Close() }

type Server struct {
	store    *Store
	producer *Producer
}

// GET / — landing page for recruiters
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintln(w, `<h2>Delivery Event Streaming — Live Demo</h2>
<p>Microservices built with Go + Kafka (Redpanda) + Kubernetes</p>
<ul>
  <li><a href="/demo">GET /demo</a> — create a demo order and stream it through Kafka</li>
  <li>POST /orders — create a real order</li>
  <li>GET /orders/{id} — fetch order state</li>
  <li>PATCH /orders/{id} — update status</li>
</ul>`)
}

// GET /demo — one-click demo for recruiters, no curl needed
func (s *Server) demo(w http.ResponseWriter, r *http.Request) {
	order := &Order{
		OrderID:    uuid.NewString(),
		CustomerID: "demo-customer",
		AgentID:    "demo-agent",
		Status:     "placed",
		CreatedAt:  time.Now().UTC(),
	}
	s.store.Save(order)
	ev := events.OrderEvent{
		EventID: uuid.NewString(), OrderID: order.OrderID,
		CustomerID: order.CustomerID, AgentID: order.AgentID,
		Status: "placed", Timestamp: time.Now().UTC(),
	}
	kafkaStatus := "published to Kafka"
	if err := s.producer.Publish(ev); err != nil {
		log.Printf("kafka publish error: %v", err)
		kafkaStatus = "kafka unavailable (order saved locally)"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":      "Demo order created and " + kafkaStatus,
		"order":        order,
		"kafka_topic":  orderTopic,
		"kafka_broker": kafkaBroker,
	})
}

// POST /orders
func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CustomerID string `json:"customer_id"`
		AgentID    string `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest); return
	}
	order := &Order{
		OrderID: uuid.NewString(), CustomerID: body.CustomerID,
		AgentID: body.AgentID, Status: "placed", CreatedAt: time.Now().UTC(),
	}
	s.store.Save(order)
	ev := events.OrderEvent{
		EventID: uuid.NewString(), OrderID: order.OrderID,
		CustomerID: order.CustomerID, AgentID: order.AgentID,
		Status: "placed", Timestamp: time.Now().UTC(),
	}
	if err := s.producer.Publish(ev); err != nil {
		log.Printf("kafka publish error: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(order)
}

// PATCH /orders/{id}
func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	order, ok := s.store.Get(id)
	if !ok { http.Error(w, "order not found", http.StatusNotFound); return }
	var body struct{ Status string `json:"status"` }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest); return
	}
	order.Status = body.Status
	s.store.Save(order)
	ev := events.OrderEvent{
		EventID: uuid.NewString(), OrderID: order.OrderID,
		CustomerID: order.CustomerID, AgentID: order.AgentID,
		Status: body.Status, Timestamp: time.Now().UTC(),
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
	if !ok { http.Error(w, "order not found", http.StatusNotFound); return }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(order)
}

func main() {
	store    := &Store{orders: make(map[string]*Order)}
	producer := NewProducer()
	defer producer.Close()

	srv := &Server{store: store, producer: producer}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /",              srv.home)
	mux.HandleFunc("GET /demo",          srv.demo)
	mux.HandleFunc("POST /orders",       srv.createOrder)
	mux.HandleFunc("PATCH /orders/{id}", srv.updateStatus)
	mux.HandleFunc("GET /orders/{id}",   srv.getOrder)
	mux.HandleFunc("GET /health",        func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	port := getenv("PORT", "8090")
	log.Printf("order-service listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}