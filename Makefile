BINARY   := claw
SRC_DIR  := ./src
BUILD_DIR := ./build
PREFIX   := $(HOME)/.local

.PHONY: all build build-drivers build-all clean test lint fmt install install-drivers install-all completions install-completions

all: build

build:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY) $(SRC_DIR)
	@echo "Built: $(BUILD_DIR)/$(BINARY)"

install: build
	@mkdir -p $(PREFIX)/bin
	cp $(BUILD_DIR)/$(BINARY) $(PREFIX)/bin/$(BINARY)
	@echo "Installed: $(PREFIX)/bin/$(BINARY)"

build-drivers:
	@mkdir -p $(BUILD_DIR)
	@for d in drivers/*/; do \
		name=$$(basename $$d); \
		echo "Building claw-driver-$$name..."; \
		(cd $$d && go build -o ../../$(BUILD_DIR)/claw-driver-$$name .) || exit 1; \
		echo "Built: $(BUILD_DIR)/claw-driver-$$name"; \
	done

install-drivers: build-drivers
	@mkdir -p $(PREFIX)/bin
	@for bin in $(BUILD_DIR)/claw-driver-*; do \
		cp $$bin $(PREFIX)/bin/; \
		echo "Installed: $(PREFIX)/bin/$$(basename $$bin)"; \
	done

install-all: install install-drivers

test:
	go test ./...
	@for d in drivers/*/; do \
		echo "Testing $$(basename $$d) driver..."; \
		(cd $$d && go test ./...) || exit 1; \
	done

fmt:
	go fmt ./...
	@for d in drivers/*/; do \
		(cd $$d && go fmt ./...); \
	done

lint:
	golangci-lint run
	@for d in drivers/*/; do \
		echo "Linting $$(basename $$d) driver..."; \
		(cd $$d && golangci-lint run) || exit 1; \
	done

completions: build
	@mkdir -p $(BUILD_DIR)/completions
	$(BUILD_DIR)/$(BINARY) completion bash > $(BUILD_DIR)/completions/claw.bash
	$(BUILD_DIR)/$(BINARY) completion zsh  > $(BUILD_DIR)/completions/_claw
	$(BUILD_DIR)/$(BINARY) completion fish > $(BUILD_DIR)/completions/claw.fish
	@echo "Completion scripts written to $(BUILD_DIR)/completions/"

install-completions: build
	$(BUILD_DIR)/$(BINARY) completion bash --install
	$(BUILD_DIR)/$(BINARY) completion zsh  --install
	$(BUILD_DIR)/$(BINARY) completion fish --install

clean:
	rm -rf $(BUILD_DIR)

build-all:
	GOOS=darwin  GOARCH=arm64  go build -o $(BUILD_DIR)/$(BINARY)-darwin-arm64  $(SRC_DIR)
	GOOS=darwin  GOARCH=amd64  go build -o $(BUILD_DIR)/$(BINARY)-darwin-amd64  $(SRC_DIR)
	GOOS=linux   GOARCH=amd64  go build -o $(BUILD_DIR)/$(BINARY)-linux-amd64   $(SRC_DIR)
	GOOS=linux   GOARCH=arm64  go build -o $(BUILD_DIR)/$(BINARY)-linux-arm64   $(SRC_DIR)
	@echo "Cross-compiled binaries in $(BUILD_DIR)/"
