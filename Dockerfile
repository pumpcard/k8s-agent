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
# Use fixed UID/GID to allow explicit Kubernetes securityContext.
RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -S -g 10001 k8s-agent && \
    adduser -S -D -H -u 10001 -G k8s-agent k8s-agent

WORKDIR /app

# Copy the binary from builder
COPY --from=builder --chown=k8s-agent:k8s-agent /app/k8s-agent .

# Switch to non-root user (service-specific user for better observability)
USER k8s-agent

# Run the application
CMD ["./k8s-agent"]
