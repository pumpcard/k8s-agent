# Build stage
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download && go mod verify

# Copy source code
COPY main.go ./

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -installsuffix cgo -ldflags="-w -s" -o k8s-agent .

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests and create non-root user
RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -S -g 1001 k8s-agent && \
    adduser -S -u 1001 -G k8s-agent k8s-agent

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/k8s-agent .

# Change ownership to k8s-agent user (non-root)
RUN chown -R k8s-agent:k8s-agent /app

# Switch to non-root user (service-specific user for better observability)
USER k8s-agent

# Run the application
CMD ["./k8s-agent"]
