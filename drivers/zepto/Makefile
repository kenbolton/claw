BINARY := claw-driver-zepto
BUILD_DIR := ../../build

.PHONY: all build clean install build-all

all: build

build:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY) .
	@echo "Built: $(BUILD_DIR)/$(BINARY)"

install: build
	cp $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)
	@echo "Installed to /usr/local/bin/$(BINARY)"

clean:
	rm -f $(BUILD_DIR)/$(BINARY)

build-all:
	GOOS=darwin  GOARCH=arm64  go build -o $(BUILD_DIR)/$(BINARY)-darwin-arm64  .
	GOOS=darwin  GOARCH=amd64  go build -o $(BUILD_DIR)/$(BINARY)-darwin-amd64  .
	GOOS=linux   GOARCH=amd64  go build -o $(BUILD_DIR)/$(BINARY)-linux-amd64   .
	GOOS=linux   GOARCH=arm64  go build -o $(BUILD_DIR)/$(BINARY)-linux-arm64   .
