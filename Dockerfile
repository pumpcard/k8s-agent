# Build stage
FROM golang:1.21-alpine AS builder

# Build arguments for multi-architecture support
ARG TARGETOS=linux
ARG TARGETARCH

# Install build dependencies
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download && go mod verify

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -installsuffix cgo -ldflags="-w -s" -o k8s-agent ./cmd/k8s-agent

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests and create non-root user with numeric UID
RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -S -g 1000 k8s-agent && \
    adduser -S -u 1000 -G k8s-agent k8s-agent

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/k8s-agent .

# Change ownership to allow any user to run the binary
RUN chmod 755 /app/k8s-agent

# Switch to non-root user (service-specific user for better observability)
USER 65532:65532

# Run the application
CMD ["./k8s-agent"]
