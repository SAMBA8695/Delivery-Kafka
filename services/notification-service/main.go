package main

// Notification Service — port 8092
//
// Routes:
//   GET /ws?order_id=xxx   WebSocket endpoint — frontend connects here
//   GET /health            K8s liveness/readiness probe
//
// Flow:
//   1. Agent pings location-service
//   2. location-event published to Kafka "location-events" topic
//   3. This service consumes it
//   4. Pushes {lat, lng} JSON to every WebSocket client watching that order_id
//
// Same flow for order status changes via "order-events" topic.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/gorilla/websocket"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/SAMBA8695/Delivery-Kafka/events"
)

var kafkaBroker = getenv("KAFKA_BROKER", "localhost:9092")

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" { return v }
	return fallback
}

// ── WebSocket hub ─────────────────────────────────────────────────────────────
// Maps order_id → set of connected WebSocket clients.
// When a Kafka event arrives for order X, broadcast to all clients in that set.

type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*websocket.Conn]bool
}

func NewHub() *Hub {
	return &Hub{clients: make(map[string]map[*websocket.Conn]bool)}
}

func (h *Hub) Register(orderID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[orderID] == nil {
		h.clients[orderID] = make(map[*websocket.Conn]bool)
	}
	h.clients[orderID][conn] = true
}

func (h *Hub) Unregister(orderID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients[orderID], conn)
}

func (h *Hub) Broadcast(orderID string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for conn := range h.clients[orderID] {
		if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Printf("ws write error: %v", err)
		}
	}
}

// ── Kafka consumers ───────────────────────────────────────────────────────────

// consumeOrderEvents reads "order-events" and pushes status updates to clients.
func consumeOrderEvents(ctx context.Context, hub *Hub) {
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  []string{kafkaBroker},
		Topic:    "order-events",
		GroupID:  "notification-service-orders", // offset saved per group
		MinBytes: 1,
		MaxBytes: 1e6,
	})
	defer r.Close()

	log.Println("consuming order-events...")
	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil { return } // clean shutdown
			log.Printf("order consumer error: %v", err)
			continue
		}

		var ev events.OrderEvent // uses shared struct from /events package
		if err := json.Unmarshal(msg.Value, &ev); err != nil {
			log.Printf("unmarshal error: %v", err)
			continue
		}

		push, _ := json.Marshal(map[string]string{
			"type":     "order_status",
			"order_id": ev.OrderID,
			"status":   ev.Status,
		})
		hub.Broadcast(ev.OrderID, push)
		log.Printf("order %s → %s", ev.OrderID[:8], ev.Status)
	}
}

// consumeLocationEvents reads "location-events" and pushes GPS pings to clients.
func consumeLocationEvents(ctx context.Context, hub *Hub) {
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  []string{kafkaBroker},
		Topic:    "location-events",
		GroupID:  "notification-service-location", // separate group = separate offset
		MinBytes: 1,
		MaxBytes: 1e6,
	})
	defer r.Close()

	log.Println("consuming location-events...")
	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil { return }
			log.Printf("location consumer error: %v", err)
			continue
		}

		var ev events.LocationEvent
		if err := json.Unmarshal(msg.Value, &ev); err != nil {
			log.Printf("unmarshal error: %v", err)
			continue
		}

		push, _ := json.Marshal(map[string]interface{}{
			"type":      "location_update",
			"order_id":  ev.OrderID,
			"agent_id":  ev.AgentID,
			"latitude":  ev.Latitude,
			"longitude": ev.Longitude,
		})
		hub.Broadcast(ev.OrderID, push)
	}
}

// ── WebSocket handler ─────────────────────────────────────────────────────────

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // relax for prototype
}

func wsHandler(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orderID := r.URL.Query().Get("order_id")
		if orderID == "" {
			http.Error(w, "order_id required", http.StatusBadRequest)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade error: %v", err)
			return
		}
		defer conn.Close()

		hub.Register(orderID, conn)
		defer hub.Unregister(orderID, conn)
		log.Printf("ws client connected: order %s", orderID[:8])

		// Block until client disconnects (reads ping frames to keep alive)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	hub := NewHub()
	ctx := context.Background()

	go consumeOrderEvents(ctx, hub)
	go consumeLocationEvents(ctx, hub)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws",        wsHandler(hub))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Println("notification-service listening on :8092")
	log.Fatal(http.ListenAndServe(":8092", mux))
}