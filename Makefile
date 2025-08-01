# Thrylos V2 Makefile

.PHONY: proto build run test clean deps setup

# Generate protobuf Go code
proto:
	@echo "Generating protobuf code..."
	protoc --go_out=. --go_opt=paths=source_relative proto/account.proto
	protoc --go_out=. --go_opt=paths=source_relative proto/api.proto
	@echo "Protobuf generation complete!"

# Build the project
build: proto
	@echo "Building thrylos-v2..."
	go build -o bin/thrylos ./cmd/thrylos
	@echo "Build complete!"

# Run the node
run: build
	@echo "Starting Thrylos V2 node..."
	./bin/thrylos

# Run tests
test:
	@echo "Running tests..."
	go test ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf bin/
	rm -f proto/core/*.pb.go
	rm -f proto/api/*.pb.go
	@echo "Clean complete!"

# Install dependencies
deps:
	@echo "Installing dependencies..."
	go mod tidy
	go mod download
	@echo "Dependencies installed!"

# Development setup
setup: deps proto
	@echo "Development setup complete!"

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...
	@echo "Code formatted!"

# Run linter
lint:
	@echo "Running linter..."
	golangci-lint run ./...
	@echo "Linting complete!"

# Generate mocks (if you use mockgen)
mocks:
	@echo "Generating mocks..."
	# Add mockgen commands here if needed
	@echo "Mocks generated!"

# Run all checks
check: fmt lint test
	@echo "All checks passed!"