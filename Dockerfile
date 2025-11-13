FROM golang:1.25-alpine AS builder

# Set proxy environment variables
ARG HTTP_PROXY
ARG HTTPS_PROXY
ENV http_proxy=$HTTP_PROXY
ENV https_proxy=$HTTPS_PROXY

# Print HTTPS_PROXY environment variable
RUN echo "HTTPS_PROXY: $HTTPS_PROXY"

WORKDIR /app

# Install git and ca-certificates for building
RUN apk add --no-cache git ca-certificates
# Diagnostic: Print Go version in build environment
RUN go version

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
ARG VERSION
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o github-copilot-svcs ./cmd/github-copilot-svcs

# Final stage
FROM alpine:latest

# Install ca-certificates and update them (critical for TLS connections)
RUN apk --no-cache add ca-certificates tzdata wget bash curl && \
  update-ca-certificates

# Create non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Copy the binary and entrypoint script (before switching user)
COPY --from=builder /app/github-copilot-svcs /home/appuser/
COPY --from=builder /app/entrypoint.sh /home/appuser/

# Set permissions and ownership
RUN chmod +x /home/appuser/entrypoint.sh && \
  chown -R appuser:appgroup /home/appuser

# Switch to non-root user
USER appuser
WORKDIR /home/appuser/

# Create config directory for non-root user
RUN mkdir -p /home/appuser/.local/share/github-copilot-svcs

# Expose the default port
EXPOSE 8081

# Health check
HEALTHCHECK --interval=300s --timeout=10s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8081/v1/health || exit 1

# Run the binary with entrypoint script
CMD ["./entrypoint.sh"]
