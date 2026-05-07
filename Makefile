BINARY := job
PKG    := ./cmd/job

.PHONY: build install run test test-js fmt fix vet clean help docs docs-build docs-schema

build:
	go build -o $(BINARY) $(PKG)

install:
	go install $(PKG)

# Usage: make run ARGS="list --mine"
run:
	go run $(PKG) $(ARGS)

test:
	go test ./...

# JS tests (Node 18+ built-in test runner). Tests live in
# internal/web/jstest/, outside the asset embed so they aren't
# served. They import production modules from internal/web/assets/js/.
test-js:
	node --test 'internal/web/jstest/*.test.mjs'

fmt:
	go fmt ./...

fix:
	go fix ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)

# Regenerate the plan-grammar JSON Schema from the live `job schema`
# output. Committed alongside hand-written prose so GitHub renders the
# schema page without a build step. Run after any change to the
# import grammar.
docs-schema:
	go run $(PKG) schema > docs/content/docs/plan-grammar/_schema.json

# Serve the documentation site locally on http://localhost:1313/.
# Requires `hugo` (extended). `brew install hugo` if missing.
# Regenerates the schema partial first so local previews never lag
# the binary.
docs: docs-schema
	cd docs && hugo serve

# Build the docs site to docs/public/.
docs-build: docs-schema
	cd docs && hugo --minify

help:
	@echo "Targets:"
	@echo "  build    - compile ./$(BINARY) from $(PKG)"
	@echo "  install  - go install to \$$GOBIN"
	@echo "  run      - go run (pass args via ARGS=\"...\")"
	@echo "  test     - run all Go tests"
	@echo "  test-js  - run JS tests (node --test internal/web/jstest/)"
	@echo "  fmt      - go fmt ./..."
	@echo "  fix      - go fix ./..."
	@echo "  vet      - go vet ./..."
	@echo "  clean    - remove the local binary"
	@echo "  docs        - serve docs/ on localhost:1313 (requires hugo)"
	@echo "  docs-build  - build docs/ to docs/public/"
	@echo "  docs-schema - regenerate docs/content/docs/plan-grammar/_schema.json from \`job schema\`"
