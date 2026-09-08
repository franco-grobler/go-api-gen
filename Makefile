# Development entry points. CI runs the same commands; see
# .github/workflows/ci.yml.

GO      ?= go
GOBIN   := $(CURDIR)/.bin
LINT    := $(GOBIN)/golangci-lint
MONOREPO := examples/monorepo/go-api-gen.yaml
PETSTORE := examples/petstore/go-api-gen.yaml
MICRO    := examples/microservices/go-api-gen.yaml

# Every module under examples/, which is more than one per example: the
# microservices example is three, which is the point of it.
EXAMPLE_MODULES := \
	examples/microservices/schema \
	examples/microservices/identity \
	examples/microservices/billing \
	examples/monorepo \
	examples/petstore

# Keep in step with GOLANGCI_VERSION in .github/workflows/ci.yml, so a local
# run and a CI run judge the same code the same way.
LINT_VERSION := v2.13.2

.PHONY: all
all: build lint test generated

.PHONY: build
build:
	$(GO) build ./...
	cd runtime && $(GO) build ./...

.PHONY: test
test:
	$(GO) test -race -shuffle=on ./...
	cd runtime && $(GO) test -race -shuffle=on ./...
	@for m in $(EXAMPLE_MODULES); do \
		echo "$(GO) test ($$m)"; \
		(cd $$m && $(GO) test -race -shuffle=on ./...) || exit 1; \
	done

.PHONY: cover
cover:
	$(GO) test -covermode=atomic -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

$(LINT):
	GOBIN=$(GOBIN) $(GO) install \
		github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)

.PHONY: lint
lint: $(LINT)
	$(LINT) run
	cd runtime && $(LINT) run --config=../.golangci.yml

.PHONY: fix
fix: $(LINT)
	$(LINT) run --fix
	cd runtime && $(LINT) run --fix --config=../.golangci.yml

.PHONY: generate
generate:
	$(GO) run ./cmd/go-api-gen build -c $(MONOREPO)
	$(GO) run ./cmd/go-api-gen unify -c $(MONOREPO)
	$(GO) run ./cmd/go-api-gen build -c $(PETSTORE)
	$(GO) run ./cmd/go-api-gen build -c $(MICRO)
	$(GO) run ./cmd/go-api-gen unify -c $(MICRO)

# Prove the committed output matches the specs and that it compiles. Uses
# git status rather than git diff so an uncommitted new file is caught too.
.PHONY: generated
generated: generate
	@changed=$$(git status --porcelain -- examples); \
	if [ -n "$$changed" ]; then \
		echo "$$changed"; \
		echo "Generated output is stale or uncommitted; commit the result."; \
		exit 1; \
	fi
	@for m in $(EXAMPLE_MODULES); do \
		echo "$(GO) build and vet ($$m)"; \
		(cd $$m && $(GO) build ./... && $(GO) vet ./...) || exit 1; \
	done

.PHONY: tidy
tidy:
	$(GO) mod tidy
	cd runtime && $(GO) mod tidy
	@for m in $(EXAMPLE_MODULES); do \
		(cd $$m && GOWORK=off $(GO) mod tidy) || exit 1; \
	done

.PHONY: clean
clean:
	rm -rf $(GOBIN) coverage.out
