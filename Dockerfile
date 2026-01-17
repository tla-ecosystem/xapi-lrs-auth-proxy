# Multi-stage build for xAPI LRS Auth Proxy

# Stage 1: Build
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -ldflags="-w -s" -o xapi-proxy ./cmd/proxy

# Stage 2: Runtime
FROM alpine:latest

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN addgroup -g 1000 xapi && \
    adduser -D -u 1000 -G xapi xapi

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/xapi-proxy .

# Copy example config
COPY config.example.yaml config.yaml

# Change ownership
RUN chown -R xapi:xapi /app

# Switch to non-root user
USER xapi

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the application
ENTRYPOINT ["./xapi-proxy"]
CMD ["--config", "config.yaml"]
