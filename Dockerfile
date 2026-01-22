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

# Build the application - multi-arch support
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -installsuffix cgo -ldflags="-w -s" -o k8s-agent .

# Runtime stage - use distroless nonroot image
# This image comes pre-configured with a non-root user (UID 65532)
# No user creation required - prevents creating users on client infrastructure
# Minimal attack surface: no shell, no package manager
FROM gcr.io/distroless/static-debian12:nonroot

# Copy the binary from builder to /usr/local/bin (standard location)
COPY --from=builder /app/k8s-agent /usr/local/bin/k8s-agent

# distroless:nonroot already runs as UID 65532 (nonroot user)
# No need to explicitly set USER - it's built into the image

# Run the application
CMD ["k8s-agent"]
