# Multi-stage build — keeps the final image tiny (~10MB)

# Stage 1: build
FROM golang:1.22-alpine AS builder
ARG SERVICE_NAME
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o service ./services/${SERVICE_NAME}/

# Stage 2: run
FROM alpine:3.19
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/service .
EXPOSE 8080
CMD ["./service"]