DIST := dist
EXECUTABLE := forgejo-runner
GOFMT ?= gofumpt -l
DIST := dist
DIST_DIRS := $(DIST)/binaries $(DIST)/release
GO ?= $(shell go env GOROOT)/bin/go
SHASUM ?= shasum -a 256
HAS_GO = $(shell hash $(GO) > /dev/null 2>&1 && echo "GO" || echo "NOGO" )

LINUX_ARCHS ?= linux/amd64,linux/arm64
DARWIN_ARCHS ?= darwin-12/amd64,darwin-12/arm64
WINDOWS_ARCHS ?= windows/amd64
GO_FMT_FILES := $(shell find . -type f -name "*.go" ! -name "generated.*")
GOFILES := $(shell find . -type f -name "*.go" -o -name "go.mod" ! -name "generated.*")

MOCKERY_PACKAGE ?= github.com/vektra/mockery/v2@v2.53.5 # renovate: datasource=go
GOLANGCI_LINT_PACKAGE ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0 # renovate: datasource=go

DOCKER_IMAGE ?= gitea/act_runner
DOCKER_TAG ?= nightly
DOCKER_REF := $(DOCKER_IMAGE):$(DOCKER_TAG)
DOCKER_ROOTLESS_REF := $(DOCKER_IMAGE):$(DOCKER_TAG)-dind-rootless

EXTLDFLAGS = -extldflags "-static" $(null)

ifeq ($(HAS_GO), GO)
	GOPATH ?= $(shell $(GO) env GOPATH)
	export PATH := $(GOPATH)/bin:$(PATH)

	CGO_EXTRA_CFLAGS := -DSQLITE_MAX_VARIABLE_NUMBER=32766
	CGO_CFLAGS ?= $(shell $(GO) env CGO_CFLAGS) $(CGO_EXTRA_CFLAGS)
endif

ifeq ($(OS), Windows_NT)
	GOFLAGS := -v -buildmode=exe
	EXECUTABLE ?= $(EXECUTABLE).exe
else ifeq ($(OS), Windows)
	GOFLAGS := -v -buildmode=exe
	EXECUTABLE ?= $(EXECUTABLE).exe
else
	GOFLAGS := -v
	EXECUTABLE ?= $(EXECUTABLE)
endif

STORED_VERSION_FILE := VERSION

ifneq ($(DRONE_TAG),)
	VERSION ?= $(subst v,,$(DRONE_TAG))
	RELEASE_VERSION ?= $(VERSION)
else
	ifneq ($(DRONE_BRANCH),)
		VERSION ?= $(subst release/v,,$(DRONE_BRANCH))
	else
		VERSION ?= main
	endif

	STORED_VERSION=$(shell cat $(STORED_VERSION_FILE) 2>/dev/null)
	ifneq ($(STORED_VERSION),)
		RELEASE_VERSION ?= $(STORED_VERSION)
	else
		RELEASE_VERSION ?= $(shell git describe --tags --always | sed 's/-/+/' | sed 's/^v//')
	endif
endif

GO_PACKAGES_TO_VET ?= $(filter-out code.forgejo.org/forgejo/runner/v11/internal/pkg/client/mocks,$(shell $(GO) list ./...))

TAGS ?=
LDFLAGS ?= -X "code.forgejo.org/forgejo/runner/v11/internal/pkg/ver.version=v$(RELEASE_VERSION)"

all: build

.PHONY: lint-check
lint-check:
	$(GO) run $(GOLANGCI_LINT_PACKAGE) run $(GOLANGCI_LINT_ARGS)

.PHONY: lint
lint:
	$(GO) run $(GOLANGCI_LINT_PACKAGE) run $(GOLANGCI_LINT_ARGS) --fix

fmt:
	@hash gofumpt > /dev/null 2>&1; if [ $$? -ne 0 ]; then \
		$(GO) install mvdan.cc/gofumpt@latest; \
	fi
	$(GOFMT) -w $(GO_FMT_FILES)

.PHONY: go-check
go-check:
	$(eval MIN_GO_VERSION_STR := $(shell grep -Eo '^go\s+[0-9]+\.[0-9]+' go.mod | cut -d' ' -f2))
	$(eval MIN_GO_VERSION := $(shell printf "%03d%03d" $(shell echo '$(MIN_GO_VERSION_STR)' | tr '.' ' ')))
	$(eval GO_VERSION := $(shell printf "%03d%03d" $(shell $(GO) version | grep -Eo '[0-9]+\.[0-9]+' | tr '.' ' ');))
	@if [ "$(GO_VERSION)" -lt "$(MIN_GO_VERSION)" ]; then \
		echo "Act Runner requires Go $(MIN_GO_VERSION_STR) or greater to build. You can get it at https://go.dev/dl/"; \
		exit 1; \
	fi

.PHONY: fmt-check
fmt-check:
	@hash gofumpt > /dev/null 2>&1; if [ $$? -ne 0 ]; then \
		$(GO) install mvdan.cc/gofumpt@latest; \
	fi
	@diff=$$($(GOFMT) -d $(GO_FMT_FILES)); \
	if [ -n "$$diff" ]; then \
		echo "Please run 'make fmt' and commit the result:"; \
		echo "$${diff}"; \
		exit 1; \
	fi;

test: lint-check fmt-check
	$(GO) test -v -race -short -cover -coverprofile coverage.txt ./internal/...
	$(GO) test -race -short ./act/container
	$(GO) test -race ./act/artifactcache/... ./act/workflowpattern/... ./act/filecollector/... ./act/common/... ./act/jobparser ./act/model ./act/exprparser ./act/schema

integration-test:
	@$(GO) test -race -v ./internal/app/run/...

.PHONY: vet
vet:
	@echo "Running go vet..."
	@$(GO) vet $(GO_PACKAGES_TO_VET)

.PHONY: generate
generate:
	$(GO) generate ./internal/...

install: $(GOFILES)
	$(GO) install -v -tags '$(TAGS)' -ldflags '$(EXTLDFLAGS)-s -w $(LDFLAGS)'

build: go-check $(EXECUTABLE)

$(EXECUTABLE): $(GOFILES) act/schema/action_schema.json act/schema/workflow_schema.json
	$(GO) build -v -tags 'netgo osusergo $(TAGS)' -ldflags '$(EXTLDFLAGS)-s -w $(LDFLAGS)' -o $@

.PHONY: deps-tools
deps-tools:
	$(GO) install $(MOCKERY_PACKAGE)

$(DIST_DIRS):
	mkdir -p $(DIST_DIRS)

.PHONY: docker
docker:
	if ! docker buildx version >/dev/null 2>&1; then \
		ARG_DISABLE_CONTENT_TRUST=--disable-content-trust=false; \
	fi; \
	docker build $${ARG_DISABLE_CONTENT_TRUST} -t $(DOCKER_REF) .
	docker build $${ARG_DISABLE_CONTENT_TRUST} -t $(DOCKER_ROOTLESS_REF) -f Dockerfile.rootless .

clean:
	$(GO) clean -x -i ./...
	rm -rf coverage.txt $(EXECUTABLE) $(DIST)

version:
	@echo $(VERSION)
