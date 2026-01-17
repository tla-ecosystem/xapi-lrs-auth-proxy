.PHONY: help build run test clean docker-build docker-run install deps

# Default target
help:
	@echo "xAPI LRS Auth Proxy - Makefile commands:"
	@echo ""
	@echo "  make build         - Build the binary"
	@echo "  make run           - Run the proxy (single-tenant)"
	@echo "  make run-multi     - Run the proxy (multi-tenant)"
	@echo "  make test          - Run tests"
	@echo "  make clean         - Clean build artifacts"
	@echo "  make docker-build  - Build Docker image"
	@echo "  make docker-run    - Run with Docker Compose"
	@echo "  make docker-down   - Stop Docker Compose"
	@echo "  make install       - Install binary to GOPATH/bin"
	@echo "  make deps          - Download dependencies"
	@echo "  make db-setup      - Setup PostgreSQL database"
	@echo ""

# Build the application
build:
	@echo "Building xapi-proxy..."
	@go build -o xapi-proxy cmd/proxy/main.go

# Run single-tenant mode
run: build
	@echo "Running proxy (single-tenant mode)..."
	@./xapi-proxy --config config.example.yaml

# Run multi-tenant mode
run-multi: build
	@echo "Running proxy (multi-tenant mode)..."
	@./xapi-proxy --multi-tenant --db "postgresql://xapi_proxy:postgres@localhost:5432/xapi_proxy"

# Run tests
test:
	@echo "Running tests..."
	@go test -v ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -f xapi-proxy
	@go clean

# Build Docker image
docker-build:
	@echo "Building Docker image..."
	@docker build -t xapi-lrs-auth-proxy:latest .

# Run with Docker Compose (single-tenant)
docker-run:
	@echo "Starting with Docker Compose..."
	@docker-compose up -d proxy-single

# Run multi-tenant with Docker Compose
docker-run-multi:
	@echo "Starting multi-tenant with Docker Compose..."
	@docker-compose up -d proxy-multi postgres redis

# Stop Docker Compose
docker-down:
	@echo "Stopping Docker Compose..."
	@docker-compose down

# Install to GOPATH/bin
install:
	@echo "Installing xapi-proxy..."
	@go install cmd/proxy/main.go

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	@go mod download
	@go mod tidy

# Setup PostgreSQL database
db-setup:
	@echo "Setting up PostgreSQL database..."
	@psql -U postgres -c "CREATE DATABASE xapi_proxy;"
	@psql -U postgres -c "CREATE USER xapi_proxy WITH PASSWORD 'postgres';"
	@psql -U postgres -c "GRANT ALL PRIVILEGES ON DATABASE xapi_proxy TO xapi_proxy;"
	@psql -U xapi_proxy -d xapi_proxy -f schema.sql

# Generate JWT secret
generate-secret:
	@echo "Generating JWT secret (256-bit):"
	@openssl rand -base64 32

# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...

# Lint code
lint:
	@echo "Linting code..."
	@golangci-lint run

# View logs (Docker)
logs:
	@docker-compose logs -f proxy-single

logs-multi:
	@docker-compose logs -f proxy-multi
