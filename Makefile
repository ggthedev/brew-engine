BINARY   := brew-engine
BUILD_DIR := ./build

.PHONY: all build build-arm64 build-amd64 build-universal tidy test clean

# Default: build for the current machine's native architecture (fast dev cycle).
all: tidy build

build:
	go build -o $(BUILD_DIR)/$(BINARY) .

# ── Architecture-specific targets ─────────────────────────────────────────────

# Apple Silicon (M1/M2/M3) native binary.
build-arm64:
	GOARCH=arm64 GOOS=darwin go build -trimpath -o $(BUILD_DIR)/$(BINARY)-arm64 .

# Intel Mac native binary.
build-amd64:
	GOARCH=amd64 GOOS=darwin go build -trimpath -o $(BUILD_DIR)/$(BINARY)-amd64 .

# Universal binary — runs natively on both Intel and Apple Silicon.
# Requires lipo (included with Xcode Command Line Tools).
# Use this output when embedding brew-engine inside a macOS app bundle.
build-universal: build-arm64 build-amd64
	lipo -create \
		$(BUILD_DIR)/$(BINARY)-arm64 \
		$(BUILD_DIR)/$(BINARY)-amd64 \
		-output $(BUILD_DIR)/$(BINARY)-universal
	@echo "Universal binary: $(BUILD_DIR)/$(BINARY)-universal"
	@lipo -info $(BUILD_DIR)/$(BINARY)-universal

# ── Utility targets ───────────────────────────────────────────────────────────

tidy:
	go mod tidy

test:
	go test ./...

clean:
	rm -rf $(BUILD_DIR)
