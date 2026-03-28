BINARY   := brew-engine
BUILD_DIR := ./build

.PHONY: all build tidy test clean

all: tidy build

build:
	go build -o $(BUILD_DIR)/$(BINARY) .

tidy:
	go mod tidy

test:
	go test ./...

clean:
	rm -rf $(BUILD_DIR)
