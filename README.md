# Real-Time Delivery Tracker

Microservices backend in Go, Kafka, and Kubernetes.

## Architecture

```
Client → Order Service → Kafka (order-events)
                              ↓
Agent App → Location Service → Kafka (location-events)
                              ↓
                    Notification Service → WebSocket / SMS
```

## Services

| Service | Port | Responsibility |
|---|---|---|
| order-service | 8080 | Create/update orders, publish to Kafka |
| location-service | 8081 | Ingest agent GPS pings, publish to Kafka |
| notification-service | 8082 | Consume Kafka, push WebSocket updates |

## Kafka Topics

| Topic | Producer | Consumer |
|---|---|---|
| order-events | order-service | notification-service |
| location-events | location-service | notification-service |

## Running locally

```bash
# Start Kafka + Zookeeper
docker-compose up -d

# Run each service
cd services/order-service && go run main.go
cd services/location-service && go run main.go
cd services/notification-service && go run main.go
```

## Deploy to Kubernetes

```bash
kubectl apply -f k8s/
```

## Scaling

```bash
# Scale location-service to 5 replicas during peak hours
kubectl scale deployment location-service --replicas=5

# Or use HPA (auto-scaling already configured in k8s/hpa.yaml)
```
