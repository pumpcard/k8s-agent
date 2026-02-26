# Multi-arch image: expects pre-built binaries k8s-agent-amd64 and k8s-agent-arm64
# in the build context (built natively in CI to avoid slow QEMU emulation).
ARG TARGETARCH

# Runtime stage
FROM alpine:latest
ARG TARGETARCH

# Install ca-certificates for HTTPS and create non-root user
RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -S -g 1000 k8s-agent && \
    adduser -S -u 1000 -G k8s-agent k8s-agent

WORKDIR /app

# Copy both binaries from context, then keep only the one for this arch (set by buildx)
COPY k8s-agent-amd64 k8s-agent-arm64 /build/
RUN cp /build/k8s-agent-${TARGETARCH} /app/k8s-agent && \
    rm -rf /build && \
    chmod 755 /app/k8s-agent

USER 65532:65532

CMD ["./k8s-agent"]
