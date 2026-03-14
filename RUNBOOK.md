# Delivery Tracker — Runbook

Complete guide: local dev → Docker → Kubernetes (free tier).

---

## Part 1 — Prerequisites

Install these once on your Mac:

```bash
# Homebrew (if not installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Go 1.22+
brew install go
go version  # should print go1.22.x

# Docker Desktop
brew install --cask docker
# Then open Docker Desktop app and wait for it to start

# wscat (for testing WebSockets)
npm install -g wscat
```

---

## Part 2 — Local Development (no Docker for services)

This is the fastest way to iterate. Kafka runs in Docker, services run as plain Go processes.

### Step 1 — Get dependencies

```bash
cd delivery-tracker
go mod tidy        # downloads all libraries, generates go.sum
```

### Step 2 — Start Kafka

```bash
docker-compose up -d
```

Wait ~20 seconds for Kafka to be ready. Check:

```bash
docker-compose ps
# kafka should show "healthy" or "running"
```

Open Kafka UI at: http://localhost:9090
You'll see 0 topics for now. Topics are auto-created when services first publish.

### Step 3 — Run the three services (3 separate terminals)

Terminal 1 — order-service:
```bash
cd delivery-tracker
KAFKA_BROKER=localhost:9092 go run ./services/order-service/
```

Terminal 2 — location-service:
```bash
cd delivery-tracker
KAFKA_BROKER=localhost:9092 go run ./services/location-service/
```

Terminal 3 — notification-service:
```bash
cd delivery-tracker
KAFKA_BROKER=localhost:9092 go run ./services/notification-service/
```

Each service prints its port when ready:
- order-service        → :8090
- location-service     → :8091
- notification-service → :8092

### Step 4 — Test the full flow

```bash
chmod +x test.sh
./test.sh
```

### Step 5 — Watch it in real time

While test.sh runs, open another terminal and connect via WebSocket.
The ORDER_ID is printed by test.sh after the first curl.

```bash
wscat -c "ws://localhost:8092/ws?order_id=PASTE_ORDER_ID_HERE"
```

You'll see JSON messages streaming in:
```json
{"type":"order_status","order_id":"...","status":"picked_up"}
{"type":"location_update","order_id":"...","latitude":12.973,"longitude":77.596}
```

Also watch Kafka UI at http://localhost:9090 — you'll see order-events and
location-events topics appear with messages accumulating in real time.

### Stopping everything

```bash
# Stop Go services: Ctrl+C in each terminal

# Stop Kafka
docker-compose down
```

---

## Part 3 — Full Docker Build (all services containerised)

Do this once you're happy with local testing. This is closer to production.

### Step 1 — Build images for all three services

```bash
cd delivery-tracker

docker build -f docker/Dockerfile --build-arg SERVICE_NAME=order-service -t delivery/order-service:latest .
docker build -f docker/Dockerfile --build-arg SERVICE_NAME=location-service -t delivery/location-service:latest .
docker build -f docker/Dockerfile --build-arg SERVICE_NAME=notification-service -t delivery/notification-service:latest .
```

Each image will be ~12MB.

Verify:
```bash
docker images | grep delivery
```

### Step 2 — Run everything with Docker (update docker-compose)

Add the three service definitions to docker-compose.yml:

```yaml
  order-service:
    image: delivery/order-service:latest
    ports:
      - "8090:8090"
    environment:
      KAFKA_BROKER: kafka:9092
    depends_on:
      kafka:
        condition: service_healthy

  location-service:
    image: delivery/location-service:latest
    ports:
      - "8091:8091"
    environment:
      KAFKA_BROKER: kafka:9092
    depends_on:
      kafka:
        condition: service_healthy

  notification-service:
    image: delivery/notification-service:latest
    ports:
      - "8092:8092"
    environment:
      KAFKA_BROKER: kafka:9092
    depends_on:
      kafka:
        condition: service_healthy
```

Then:
```bash
docker-compose up
```

Everything starts together. Run test.sh as before.

---

## Part 4 — Kubernetes Deployment (free on Render / minikube local)

### Option A — minikube (local K8s, best for learning)

```bash
# Install minikube
brew install minikube

# Start a local cluster
minikube start --driver=docker

# Point Docker to minikube's registry (so it can find your images)
eval $(minikube docker-env)

# Rebuild images inside minikube's Docker
docker build -f docker/Dockerfile --build-arg SERVICE_NAME=order-service -t delivery/order-service:latest .
docker build -f docker/Dockerfile --build-arg SERVICE_NAME=location-service -t delivery/location-service:latest .
docker build -f docker/Dockerfile --build-arg SERVICE_NAME=notification-service -t delivery/notification-service:latest .

# Deploy everything
kubectl apply -f k8s/kafka.yaml
kubectl apply -f k8s/order-service.yaml
kubectl apply -f k8s/location-service.yaml
kubectl apply -f k8s/notification-service.yaml

# Wait for pods to come up (~60s for Kafka)
kubectl get pods --watch

# Once all pods are Running, expose order-service locally
kubectl port-forward svc/order-service 8090:80 &
kubectl port-forward svc/location-service 8091:80 &
kubectl port-forward svc/notification-service 8092:80 &

# Now test exactly as before
./test.sh
```

Useful kubectl commands:
```bash
kubectl get pods                          # see all pods + status
kubectl logs -f deployment/order-service  # tail logs
kubectl describe pod <pod-name>           # debug a crashed pod
kubectl get hpa                           # see auto-scaler status

# Manually trigger the HPA by hammering location-service
# (watch it scale from 2 to more pods)
for i in $(seq 1 200); do curl -s -X POST localhost:8091/ping \
  -H "Content-Type: application/json" \
  -d '{"agent_id":"a1","order_id":"o1","latitude":12.9,"longitude":77.5}' & done
kubectl get hpa --watch
```

### Option B — Deploy to the cloud (free tier: Render + Upstash Kafka)

This is actual internet deployment. Free.

#### 1. Managed Kafka (Upstash — free tier, no credit card)

1. Sign up at https://upstash.com
2. Create a Kafka cluster → copy the bootstrap URL and credentials
3. You'll get something like:
   ```
   KAFKA_BROKER=guided-condor-12345-us1-kafka.upstash.io:9092
   KAFKA_SASL_USERNAME=youruser
   KAFKA_SASL_PASSWORD=yourpassword
   ```

Update each service's main.go to use SASL auth:
```go
// Replace the kafka.Writer init with:
w := &kafka.Writer{
    Addr:  kafka.TCP(kafkaBroker),
    Topic: orderTopic,
    Transport: &kafka.Transport{
        SASL: plain.Mechanism{
            Username: os.Getenv("KAFKA_SASL_USERNAME"),
            Password: os.Getenv("KAFKA_SASL_PASSWORD"),
        },
        TLS: &tls.Config{},
    },
}
```
Add import: `"github.com/segmentio/kafka-go/sasl/plain"` and `"crypto/tls"`

#### 2. Deploy services to Render (free tier)

1. Push your project to GitHub
2. Sign up at https://render.com
3. New → Web Service → connect your GitHub repo
4. For each service, set:
   - Build Command: `go build -o service ./services/order-service/`
   - Start Command: `./service`
   - Environment variables: KAFKA_BROKER, KAFKA_SASL_USERNAME, KAFKA_SASL_PASSWORD
5. Repeat for location-service (change build command) and notification-service

Each service gets a free URL like `https://order-service-xxxx.onrender.com`

#### 3. Deploy to Kubernetes on the cloud (GKE Autopilot — free $300 credit)

```bash
# Install gcloud CLI
brew install google-cloud-sdk

# Login and create project
gcloud auth login
gcloud projects create delivery-tracker-demo
gcloud config set project delivery-tracker-demo

# Create GKE Autopilot cluster (fully managed, no node management)
gcloud container clusters create-auto delivery-cluster \
  --region=asia-south1  # Mumbai — closest to Chennai

# Authenticate kubectl
gcloud container clusters get-credentials delivery-cluster --region=asia-south1

# Push images to Google Container Registry
gcloud auth configure-docker
docker tag delivery/order-service gcr.io/delivery-tracker-demo/order-service:latest
docker push gcr.io/delivery-tracker-demo/order-service:latest
# repeat for other two services

# Update k8s yamls to use gcr.io/... image names, then:
kubectl apply -f k8s/
kubectl get pods --watch
```

---

## Common errors and fixes

| Error | Cause | Fix |
|---|---|---|
| `connection refused :9092` | Kafka not ready yet | Wait 20s, check `docker-compose ps` |
| `go: missing go.sum entry` | go.sum not generated | Run `go mod tidy` |
| `pod CrashLoopBackOff` | Service crashed on start | `kubectl logs <pod>` to see why |
| `ImagePullBackOff` on minikube | Image not in minikube's Docker | Re-run `eval $(minikube docker-env)` then rebuild |
| `websocket: bad handshake` | Wrong URL or missing order_id | Check `ws://` not `http://` and order_id param |