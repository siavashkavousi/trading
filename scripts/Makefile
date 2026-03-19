.PHONY: build test test-cover test-short lint run clean docker docker-compose

BINARY_NAME=trader
BUILD_DIR=./bin
MAIN_PKG=./cmd/trader/
COVERAGE_DIR=./coverage

build:
	@echo "Building..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags="-w -s" -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PKG)
	@echo "Build complete: $(BUILD_DIR)/$(BINARY_NAME)"

test:
	go test -race -v ./...

test-cover:
	@mkdir -p $(COVERAGE_DIR)
	go test -race -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic ./...
	go tool cover -func=$(COVERAGE_DIR)/coverage.out
	@echo ""
	@echo "HTML report: go tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html"

test-short:
	go test -short -race ./...

lint:
	golangci-lint run ./...

run: build
	$(BUILD_DIR)/$(BINARY_NAME) --config configs/config.yaml

run-dry:
	go run $(MAIN_PKG) --config configs/config.yaml

run-live:
	go run $(MAIN_PKG) --config configs/config.yaml --confirm-live

clean:
	rm -rf $(BUILD_DIR)
	rm -rf $(COVERAGE_DIR)
	rm -f data/checkpoints.db

docker:
	docker build -t crypto-trader:latest .

docker-compose:
	cd scripts && docker-compose up -d

docker-compose-down:
	cd scripts && docker-compose down

tidy:
	go mod tidy

fmt:
	gofmt -s -w .

vet:
	go vet ./...

check: fmt vet lint test
