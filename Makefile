# HP859X_SA — build / test convenience targets.
#
# All compiled cmd/ tool binaries land in bin/ (git-ignored). Build them
# with `make tools`, then run e.g. `./bin/naturalkey -trace 2000000`.
# On macOS the Unicorn oracle needs DYLD_FALLBACK_LIBRARY_PATH; the test
# targets set it for you.

BIN := bin
DYLD := DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib

.PHONY: all tools build test test-fast clean

all: build tools

# Build the whole module (libraries + cgo Musashi). Catches compile errors
# without emitting the cmd/ binaries.
build:
	go build ./...

# Compile every cmd/<name> tool into bin/. This is the canonical way to
# produce the diagnostic probes — never `go build ./cmd/x` from the root
# (that drops a stray binary in the working dir).
tools:
	@mkdir -p $(BIN)
	go build -o $(BIN)/ ./cmd/...
	@echo "built $$(ls $(BIN) | wc -l | tr -d ' ') tools into $(BIN)/"

# Full test suite (Unicorn-linked tests need the DYLD path on macOS).
test:
	$(DYLD) go test ./...

# Pure-cgo tests that do not need the Unicorn dynamic library.
test-fast:
	go test ./pkg/emu/cpu/musashi/ ./pkg/emu/machine/ ./internal/emutest/

clean:
	rm -rf $(BIN)
