FROM golang:1.23-alpine AS builder

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
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o github-copilot-svcs ./cmd/github-copilot-svcs

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates tzdata wget

# Create non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Switch to non-root user
USER appuser
WORKDIR /home/appuser/

# Copy the binary from builder
COPY --from=builder /app/github-copilot-svcs .

# Create config directory for non-root user
RUN mkdir -p /home/appuser/.local/share/github-copilot-svcs

# Expose the default port
EXPOSE 8081

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8081/health || exit 1

# Run the binary
CMD ["./github-copilot-svcs", "start"]
