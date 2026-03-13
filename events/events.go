// Package events defines the shared Kafka message schemas used by all services.
// Import path: github.com/yourusername/delivery-tracker/events
//
// Import it as:
//   import "github.com/yourusername/delivery-tracker/events"
// Then use: events.OrderEvent{...}  events.LocationEvent{...}
package events

import "time"

// OrderEvent is published to the "order-events" Kafka topic whenever
// an order is created or its status changes.
// Kafka key = OrderID — keeps all events for one order on one partition.
type OrderEvent struct {
	EventID    string    `json:"event_id"`
	OrderID    string    `json:"order_id"`
	CustomerID string    `json:"customer_id"`
	AgentID    string    `json:"agent_id"`
	Status     string    `json:"status"` // "placed" | "picked_up" | "delivered"
	Timestamp  time.Time `json:"timestamp"`
}

// LocationEvent is published to the "location-events" Kafka topic on
// every GPS ping from a delivery agent (~every 3 seconds per agent).
// Kafka key = AgentID — keeps one agent's pings ordered on one partition.
type LocationEvent struct {
	EventID   string    `json:"event_id"`
	AgentID   string    `json:"agent_id"`
	OrderID   string    `json:"order_id"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Timestamp time.Time `json:"timestamp"`
}