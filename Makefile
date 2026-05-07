##############################################################################
# Arguments
##############################################################################

VERSION=
TEST_TARGET?=./...

##############################################################################
# Variables
##############################################################################

# This Makefile's directory
SCRIPT_DIR=$(abspath $(dir $(lastword $(MAKEFILE_LIST))))

# Remote repository for the project
REMOTE_REPO_URL=https://github.com/dagucloud/dagu

# Directories for miscellaneous files for the local environment
LOCAL_DIR=$(SCRIPT_DIR)/.local
LOCAL_BIN_DIR=$(LOCAL_DIR)/bin

# Configuration directory
CONFIG_DIR=$(SCRIPT_DIR)/config

# Local build settings
BIN_DIR=$(SCRIPT_DIR)/.local/bin
BUILD_VERSION=$(shell git describe --tags)
LDFLAGS=-X 'main.version=$(BUILD_VERSION)'

# Application name
APP_NAME=dagu

# Docker image build configuration
DOCKER_CMD := docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7,linux/arm64/v8 --builder container --build-arg LDFLAGS="$(LDFLAGS)" --push --no-cache

# PostgreSQL control-plane store development configuration
DEV_PG_COMPOSE_FILE=$(SCRIPT_DIR)/compose.postgres.yaml
DEV_PG_COMPOSE_PROJECT?=dagu-dev-pg
DEV_PG_DSN?=postgres://dagu:dagu@localhost:54321/dagu?sslmode=disable
DEV_PG_WAIT_RETRIES?=60
DEV_PG_WAIT_INTERVAL_SEC?=1

# Arguments for the tests
GOTESTSUM_ARGS=--format=standard-quiet
# Go test flags (see https://github.com/golang/go/issues/61229#issuecomment-1988965927)
GO_TEST_FLAGS=-v --race -ldflags=-extldflags=-Wl

# OpenAPI configuration
OAPI_SPEC_DIR_V1=./api/v1
OAPI_SPEC_FILE_V1=${OAPI_SPEC_DIR_V1}/api.yaml
OAPI_CONFIG_FILE_V1=${OAPI_SPEC_DIR_V1}/config.yaml

# Frontend directories

FE_DIR=./internal/service/frontend
FE_GEN_DIR=${FE_DIR}/gen
FE_ASSETS_DIR=${FE_DIR}/assets
FE_BUILD_DIR=./ui/dist
FE_BUNDLE_JS=${FE_ASSETS_DIR}/bundle.js

# Colors for the output

COLOR_GREEN=\033[0;32m
COLOR_RESET=\033[0m
COLOR_RED=\033[0;31m

# Go packages for the tools

PKG_golangci_lint=github.com/golangci/golangci-lint/v2/cmd/golangci-lint
PKG_gotestsum=gotest.tools/gotestsum
PKG_addlicense=github.com/google/addlicense
PKG_changelog-from-release=github.com/rhysd/changelog-from-release/v3@latest
PKG_oapi_codegen=github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
PKG_kin_openapi_validate=github.com/getkin/kin-openapi/cmd/validate
PKG_protolint=github.com/yoheimuta/protolint/cmd/protolint
PKG_protoc_gen_go=google.golang.org/protobuf/cmd/protoc-gen-go
PKG_protoc_gen_go_grpc=google.golang.org/grpc/cmd/protoc-gen-go-grpc

# Certificates for the development environment

CERTS_DIR=${LOCAL_DIR}/certs

DEV_CERT_SUBJ_CA="/C=TR/ST=ASIA/L=TOKYO/O=DEV/OU=DAGU/CN=*.dagu.dev/emailAddress=ca@dev.com"
DEV_CERT_SUBJ_SERVER="/C=TR/ST=ASIA/L=TOKYO/O=DEV/OU=SERVER/CN=*.server.dev/emailAddress=server@dev.com"
DEV_CERT_SUBJ_CLIENT="/C=TR/ST=ASIA/L=TOKYO/O=DEV/OU=CLIENT/CN=*.client.dev/emailAddress=client@dev.com"
DEV_CERT_SUBJ_ALT="subjectAltName=DNS:localhost"

CA_CERT_FILE=${CERTS_DIR}/ca-cert.pem
CA_KEY_FILE=${CERTS_DIR}/ca-key.pem
SERVER_CERT_REQ=${CERTS_DIR}/server-req.pem
SERVER_CERT_FILE=${CERTS_DIR}/server-cert.pem
SERVER_KEY_FILE=${CERTS_DIR}/server-key.pem
CLIENT_CERT_REQ=${CERTS_DIR}/client-req.pem
CLIENT_CERT_FILE=${CERTS_DIR}/client-cert.pem
CLIENT_KEY_FILE=${CERTS_DIR}/client-key.pem
OPENSSL_CONF=${CONFIG_DIR}/openssl.local.conf

# Variables for installing protoc

# Detect the OS
OS := $(shell uname -s)
ifeq (${OS},Darwin)
	OS := osx
else
	OS := linux
endif

# Detect the Architecture
ARCH := $(shell uname -m)
ifeq (${ARCH},arm64)
	ARCH := aarch_64
else
	ARCH := x86_64
endif

# Protobuf release URL
PB_RELEASE_URL=https://github.com/protocolbuffers/protobuf/releases
PB_VERSION=31.1
PB_RELEASE_NAME=protoc-${PB_VERSION}-${OS}-${ARCH}

##############################################################################
# Targets
##############################################################################

# run starts the frontend server and the scheduler.
.PHONY: run
run: ${FE_BUNDLE_JS}
	@printf '%b\n' "${COLOR_GREEN}Starting the frontend server and the scheduler...${COLOR_RESET}"
	@DAGU_DEBUG=1 go run ./cmd start-all

# dev-pg starts a local PostgreSQL instance and runs Dagu with the Postgres control-plane store.
.PHONY: dev-pg dev-pg-up dev-pg-wait dev-pg-start dev-pg-down
dev-pg: dev-pg-start

dev-pg-start: ${FE_BUNDLE_JS} dev-pg-wait
	@printf '%b\n' "${COLOR_GREEN}Starting Dagu with PostgreSQL control-plane store...${COLOR_RESET}"
	@DAGU_DEBUG=1 \
		DAGU_CONTROL_PLANE_STORE_BACKEND=postgres \
		DAGU_CONTROL_PLANE_STORE_POSTGRES_SERVER_DSN='${DEV_PG_DSN}' \
		DAGU_CONTROL_PLANE_STORE_POSTGRES_SERVER_AUTO_MIGRATE=true \
		DAGU_CONTROL_PLANE_STORE_POSTGRES_SCHEDULER_DSN='${DEV_PG_DSN}' \
		DAGU_CONTROL_PLANE_STORE_POSTGRES_SCHEDULER_AUTO_MIGRATE=true \
		DAGU_CONTROL_PLANE_STORE_POSTGRES_AGENT_DSN='${DEV_PG_DSN}' \
		DAGU_CONTROL_PLANE_STORE_POSTGRES_AGENT_AUTO_MIGRATE=false \
		DAGU_CONTROL_PLANE_STORE_POSTGRES_AGENT_DIRECT_ACCESS=true \
		go run ./cmd start-all

dev-pg-wait: dev-pg-up
	@printf '%b\n' "${COLOR_GREEN}Waiting for PostgreSQL to become ready...${COLOR_RESET}"
	@i=0; \
	while [ $$i -lt ${DEV_PG_WAIT_RETRIES} ]; do \
		if docker compose -p ${DEV_PG_COMPOSE_PROJECT} -f ${DEV_PG_COMPOSE_FILE} exec -T postgres pg_isready -U dagu -d dagu >/dev/null 2>&1; then \
			break; \
		fi; \
		i=$$((i + 1)); \
		sleep ${DEV_PG_WAIT_INTERVAL_SEC}; \
	done; \
	if [ $$i -ge ${DEV_PG_WAIT_RETRIES} ]; then \
		printf '%b\n' "${COLOR_RED}Error: PostgreSQL did not become ready.${COLOR_RESET}"; \
		docker compose -p ${DEV_PG_COMPOSE_PROJECT} -f ${DEV_PG_COMPOSE_FILE} ps; \
		exit 1; \
	fi

dev-pg-up:
	@printf '%b\n' "${COLOR_GREEN}Starting PostgreSQL for control-plane store development...${COLOR_RESET}"
	@docker compose -p ${DEV_PG_COMPOSE_PROJECT} -f ${DEV_PG_COMPOSE_FILE} up -d postgres

dev-pg-down:
	@printf '%b\n' "${COLOR_GREEN}Stopping PostgreSQL control-plane store development environment...${COLOR_RESET}"
	@docker compose -p ${DEV_PG_COMPOSE_PROJECT} -f ${DEV_PG_COMPOSE_FILE} down

# server build the binary and start the server.
.PHONY: run-server
run-server: golangci-lint bin
	@printf '%b\n' "${COLOR_GREEN}Starting the server...${COLOR_RESET}"
	${LOCAL_BIN_DIR}/${APP_NAME} server

# scheduler build the binary and start the scheduler.
.PHONY: run-scheduler
run-scheduler: golangci-lint bin
	@printf '%b\n' "${COLOR_GREEN}Starting the scheduler...${COLOR_RESET}"
	${LOCAL_BIN_DIR}/${APP_NAME} scheduler

# check if the frontend assets are built.
${FE_BUNDLE_JS}:
	@printf '%b\n' "${COLOR_RED}Error: frontend assets are not built.${COLOR_RESET}"
	@printf '%b\n' "${COLOR_RED}Please run 'make ui' before starting the server.${COLOR_RESET}"

# https starts the server with the HTTPS protocol.
.PHONY: run-server-https
run-server-https: ${SERVER_CERT_FILE} ${SERVER_KEY_FILE}
	@printf '%b\n' "${COLOR_GREEN}Starting the server with HTTPS...${COLOR_RESET}"
	@DAGU_DEBUG=1 \
		DAGU_CERT_FILE=${SERVER_CERT_FILE} \
		DAGU_KEY_FILE=${SERVER_KEY_FILE} \
		go run ./cmd start-all

# test runs all tests.
.PHONY: test
test: bin
	@printf '%b\n' "${COLOR_GREEN}Running tests...${COLOR_RESET}"
	@GOBIN=${LOCAL_BIN_DIR} go install ${PKG_gotestsum}
	@go clean -testcache
	@${LOCAL_BIN_DIR}/gotestsum ${GOTESTSUM_ARGS} -- ${GO_TEST_FLAGS} ${TEST_TARGET}

# test-postgres runs PostgreSQL integration tests with testcontainers.
.PHONY: test-postgres
test-postgres:
	@printf '%b\n' "${COLOR_GREEN}Running PostgreSQL integration tests...${COLOR_RESET}"
	@go test -timeout=10m -tags postgres_integration ./internal/persis/controlplanestore/postgres -run TestPostgresControlPlaneStoreIntegration -count=1

# test-coverage runs all tests with coverage.
.PHONY: test-coverage
test-coverage:
	@printf '%b\n' "${COLOR_GREEN}Running tests with coverage...${COLOR_RESET}"
	@GOBIN=${LOCAL_BIN_DIR} go install ${PKG_gotestsum}
	@${LOCAL_BIN_DIR}/gotestsum ${GOTESTSUM_ARGS} -- ${GO_TEST_FLAGS} -coverpkg=./... -coverprofile="coverage.out" -covermode=atomic ${TEST_TARGET}
	@go tool cover -html=coverage.out -o coverage.html

# lint runs the linter.
.PHONY: lint
lint: golangci-lint

# api generates the server code from the OpenAPI specification.
.PHONY: api
api: api-validate
	@printf '%b\n' "${COLOR_GREEN}Generating API...${COLOR_RESET}"
	@GOBIN=${LOCAL_BIN_DIR} go install ${PKG_oapi_codegen}
	@${LOCAL_BIN_DIR}/oapi-codegen --config=${OAPI_CONFIG_FILE_V1} ${OAPI_SPEC_FILE_V1}

# api-validate validates the OpenAPI specification.
.PHONY: api-validate
api-validate:
	@printf '%b\n' "${COLOR_GREEN}Validating API...${COLOR_RESET}"
	@GOBIN=${LOCAL_BIN_DIR} go install ${PKG_kin_openapi_validate}
	@${LOCAL_BIN_DIR}/validate ${OAPI_SPEC_FILE_V1}

# api generates the swagger server code.
.PHONY: swagger
swagger: clean-swagger gen-swagger

.PHONY: proto
proto: protolint protoc

# Lint proto files
protolint:
	@GOBIN=${LOCAL_BIN_DIR} go install ${PKG_protolint}
	@printf '%b\n' "${COLOR_GREEN}Linting proto files...${COLOR_RESET}"
	@${LOCAL_BIN_DIR}/protolint lint ${SCRIPT_DIR}/proto

# Generate Go code from proto files
protoc: ${LOCAL_DIR}/${PB_RELEASE_NAME}
	@ln -sf ${LOCAL_DIR}/${PB_RELEASE_NAME}/bin/protoc ${LOCAL_BIN_DIR}/protoc
	@GOBIN=${LOCAL_BIN_DIR} go install ${PKG_protoc_gen_go}
	@GOBIN=${LOCAL_BIN_DIR} go install ${PKG_protoc_gen_go_grpc}
	@printf '%b\n' "${COLOR_GREEN}Generating Go code from proto files...${COLOR_RESET}"
	@env PATH="${LOCAL_BIN_DIR}:/usr/local/bin:/usr/bin:/bin" ${LOCAL_BIN_DIR}/protoc --go_out=. --go_opt=paths=source_relative \
	    --go_opt=apilevelMproto/coordinator/v1/coordinator.proto=API_HYBRID \
	    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	    proto/coordinator/v1/*.proto
	@env PATH="${LOCAL_BIN_DIR}:/usr/local/bin:/usr/bin:/bin" ${LOCAL_BIN_DIR}/protoc --go_out=. --go_opt=paths=source_relative \
	    proto/index/v1/*.proto

# Download protoc
${LOCAL_DIR}/${PB_RELEASE_NAME}:
	@printf '%b\n' "${COLOR_GREEN}Downloading protoc...${COLOR_RESET}"
	@mkdir -p ${LOCAL_BIN_DIR}
	@curl -L ${PB_RELEASE_URL}/download/v${PB_VERSION}/${PB_RELEASE_NAME}.zip -o ${LOCAL_DIR}/${PB_RELEASE_NAME}.zip
	@unzip ${LOCAL_DIR}/${PB_RELEASE_NAME} -d ${LOCAL_DIR}/${PB_RELEASE_NAME}
	@rm ${LOCAL_DIR}/${PB_RELEASE_NAME}.zip

# certs generates the certificates to use in the development environment.
.PHONY: certs
certs: ${CERTS_DIR} ${SERVER_CERT_FILE} ${CLIENT_CERT_FILE} certs-check

# build build the binary.
.PHONY: build
build: ui bin

# build-image build the docker image and push to the registry.
# VERSION should be set via the argument as follows:
# ```sh
# make build-image VERSION={version}
# ```
# {version} should be the version number such as "1.13.0".

.PHONY: build-image
build-image: build-image-version

.PHONY: build-image-version
build-image-version:
	@if [ -z "$(VERSION)" ]; then \
		printf '%b\n' "${COLOR_RED}Error: VERSION is not set${COLOR_RESET}"; \
		echo "Usage: make build-image VERSION={version}"; \
		exit 1; \
	fi
	@printf '%b\n' "${COLOR_GREEN}Building the docker image with the version $(VERSION)...${COLOR_RESET}"
	@$(DOCKER_CMD) -t ghcr.io/dagucloud/${APP_NAME}:$(VERSION) .

# build-image-latest build the docker image with the latest tag and push to 
# the registry.
.PHONY: build-image-latest
build-image-latest:
	@printf '%b\n' "${COLOR_GREEN}Building the docker image...${COLOR_RESET}"
	@$(DOCKER_CMD) -t ghcr.io/dagucloud/${APP_NAME}:latest .

${LOCAL_DIR}/merged:
	@mkdir -p ${LOCAL_DIR}/merged

# addlicense adds license header to all files.
.PHONY: addlicense
addlicense:
	@printf '%b\n' "${COLOR_GREEN}Adding license headers...${COLOR_RESET}"
	@GOBIN=${LOCAL_BIN_DIR} go install ${PKG_addlicense}
	@${LOCAL_BIN_DIR}/addlicense \
		-ignore "**/node_modules/**" \
		-ignore "./**/gen/**" \
		-ignore "Dockerfile" \
		-ignore "ui/*" \
		-ignore "ui/**/*" \
		-ignore "bin/*" \
		-ignore "local/*" \
		-ignore "docs/**" \
		-ignore "**/examples/*" \
		-ignore ".github/*" \
		-ignore ".github/**/*" \
		-ignore ".*" \
		-ignore "**/*.yml" \
		-ignore "**/*.yaml" \
		-ignore "**/filenotify/*" \
		-ignore "**/testdata/**" \
		-c "Yota Hamada" \
		-f scripts/header.txt \
		.

# rfc creates a new RFC draft with the next available number.
# Usage: make rfc NAME=my-feature-name
.PHONY: rfc
rfc:
	@if [ -z "$(NAME)" ]; then \
		printf '%b\n' "${COLOR_RED}Error: NAME is not set${COLOR_RESET}"; \
		echo "Usage: make rfc NAME=my-feature-name"; \
		exit 1; \
	fi
	@NEXT=$$(find rfcs -name '*.md' ! -name 'README.md' ! -name 'TEMPLATE.md' | sed 's/.*\///' | grep -oE '^[0-9]+' | sort -n | tail -1 | awk '{printf "%03d", $$1+1}'); \
	if [ -z "$$NEXT" ]; then NEXT="001"; fi; \
	FILE="rfcs/draft/$${NEXT}-$(NAME).md"; \
	sed "s/RFC NNN: Title/RFC $${NEXT}: $(NAME)/" rfcs/TEMPLATE.md > "$$FILE"; \
	printf '%b\n' "${COLOR_GREEN}Created $$FILE${COLOR_RESET}"

# cpuprof opens the CPU profile in the browser.
cpuprof:
	@go tool pprof -http=:9999 cpu.prof

##############################################################################
# Internal targets
##############################################################################

# bin builds the go application.
.PHONY: bin
bin:
	@printf '%b\n' "${COLOR_GREEN}Building the binary...${COLOR_RESET}"
	@mkdir -p ${BIN_DIR}
	@go build -ldflags="$(LDFLAGS)" -o ${BIN_DIR}/${APP_NAME} ./cmd

# bin-e2e builds the go application for browser E2E tests.
.PHONY: bin-e2e
bin-e2e:
	@printf '%b\n' "${COLOR_GREEN}Building the E2E binary...${COLOR_RESET}"
	@mkdir -p ${BIN_DIR}
	@go build -ldflags="$(LDFLAGS)" -o ${BIN_DIR}/${APP_NAME}-e2e ./cmd

# build-keepalive builds the keepalive binary for all architectures using Zig.
.PHONY: build-keepalive
build-keepalive:
	@printf '%b\n' "${COLOR_GREEN}Building keepalive binaries with Zig for all architectures...${COLOR_RESET}"
	@mkdir -p internal/container/assets
	@rm -rf internal/container/assets/keepalive_*
	@cd internal/container/keepalive && \
	echo "Building Darwin binaries..." && \
	zig build-exe main.zig -target x86_64-macos -O ReleaseSmall -femit-bin=../assets/keepalive_darwin_amd64 && \
	zig build-exe main.zig -target aarch64-macos -O ReleaseSmall -femit-bin=../assets/keepalive_darwin_arm64 && \
	echo "Building Linux binaries..." && \
	zig build-exe main.zig -target x86-linux-musl -O ReleaseSmall -femit-bin=../assets/keepalive_linux_386 && \
	zig build-exe main.zig -target x86_64-linux-musl -O ReleaseSmall -femit-bin=../assets/keepalive_linux_amd64 && \
	zig build-exe main.zig -target aarch64-linux-musl -O ReleaseSmall -femit-bin=../assets/keepalive_linux_arm64 && \
	zig build-exe main.zig -target arm-linux-musleabihf -O ReleaseSmall -mcpu=generic+v7a -femit-bin=../assets/keepalive_linux_armv7 && \
	zig build-exe main.zig -target arm-linux-musleabi -O ReleaseSmall -mcpu=generic+v6 -femit-bin=../assets/keepalive_linux_armv6 && \
	zig build-exe main.zig -target powerpc64le-linux-musl -O ReleaseSmall -femit-bin=../assets/keepalive_linux_ppc64le && \
	zig build-exe main.zig -target s390x-linux-musl -O ReleaseSmall -femit-bin=../assets/keepalive_linux_s390x && \
	echo "Skipping BSD targets (require additional setup)..."
	@chmod +x internal/container/assets/keepalive_* 2>/dev/null || true
	@printf '%b\n' "${COLOR_GREEN}Cleaning up build artifacts...${COLOR_RESET}"
	@rm -f internal/container/assets/keepalive_*.o internal/container/assets/keepalive_*.obj
	@printf '%b\n' "${COLOR_GREEN}Generating checksums...${COLOR_RESET}"
	@cd internal/container/assets && \
		if command -v sha256sum >/dev/null 2>&1; then \
			sha256sum keepalive_* > keepalive_checksums.txt; \
		else \
			shasum -a 256 keepalive_* > keepalive_checksums.txt; \
		fi
	@printf '%b\n' "${COLOR_GREEN}Done! Checksums saved to internal/container/assets/keepalive_checksums.txt${COLOR_RESET}"

.PHONY: ui
# ui builds the frontend codes.
ui: clean-ui build-ui cp-assets

.PHONY: test-e2e
test-e2e:
	@printf '%b\n' "${COLOR_GREEN}Installing frontend dependencies...${COLOR_RESET}"
	@cd ui; pnpm install --frozen-lockfile
	@printf '%b\n' "${COLOR_GREEN}Installing Playwright browser...${COLOR_RESET}"
	@cd ui; pnpm test:e2e:install
	@$(MAKE) test-e2e-build
	@$(MAKE) test-e2e-run

.PHONY: test-e2e-build
test-e2e-build:
	@printf '%b\n' "${COLOR_GREEN}Building browser E2E assets...${COLOR_RESET}"
	@cd ui; NODE_OPTIONS="--max-old-space-size=8192" pnpm build
	@$(MAKE) cp-assets
	@$(MAKE) bin-e2e

.PHONY: test-e2e-run
test-e2e-run:
	@printf '%b\n' "${COLOR_GREEN}Running browser E2E tests...${COLOR_RESET}"
	@cd ui; pnpm test:e2e $(PLAYWRIGHT_TEST_ARGS)

# build-ui builds the frontend codes.
.PHONY: build-ui
build-ui:
	@printf '%b\n' "${COLOR_GREEN}Building UI...${COLOR_RESET}"
	@cd ui; \
		pnpm install; \
		NODE_OPTIONS="--max-old-space-size=8192" pnpm webpack --config webpack.dev.js --progress --color; \
		pnpm webpack --config webpack.prod.js --progress --color
	@printf '%b\n' "${COLOR_GREEN}Waiting for the build to finish...${COLOR_RESET}"
	@sleep 3 # wait for the build to finish

# build-ui-prod builds the frontend codes for production.
.PHONY: cp-assets
cp-assets:
	@printf '%b\n' "${COLOR_GREEN}Copying UI assets...${COLOR_RESET}"
	@rm -f ${FE_ASSETS_DIR}/*
	@cp ${FE_BUILD_DIR}/* ${FE_ASSETS_DIR}

# clean-ui removes the UI build cache.
.PHONY: clean-ui
clean-ui:
	@printf '%b\n' "${COLOR_GREEN}Cleaning UI build cache...${COLOR_RESET}"
	@cd ui; \
		rm -rf node_modules; \
		rm -rf .cache;

# fmt auto-fixes all code style issues (go fix + go fmt + linter --fix).
# AI agents and developers should run this before committing.
.PHONY: fmt
fmt:
	@printf '%b\n' "${COLOR_GREEN}Running go fix...${COLOR_RESET}"
	@go fix ./...
	@printf '%b\n' "${COLOR_GREEN}Running go fmt...${COLOR_RESET}"
	@go fmt ./...
	@printf '%b\n' "${COLOR_GREEN}Running linter with --fix...${COLOR_RESET}"
	@GOBIN=${LOCAL_BIN_DIR} go install $(PKG_golangci_lint)
	@${LOCAL_BIN_DIR}/golangci-lint run --fix ./...

# check verifies code style without modifying files (for CI).
.PHONY: check
check:
	@printf '%b\n' "${COLOR_GREEN}Checking go fix...${COLOR_RESET}"
	@diff=$$(go fix -diff ./... 2>&1); if [ -n "$$diff" ]; then echo "$$diff"; printf '%b\n' "${COLOR_RED}Run 'make fmt'${COLOR_RESET}"; exit 1; fi
	@printf '%b\n' "${COLOR_GREEN}Checking go fmt...${COLOR_RESET}"
	@go fmt ./...
	@git diff --exit-code || (printf '%b\n' "${COLOR_RED}Run 'make fmt'${COLOR_RESET}" && exit 1)
	@printf '%b\n' "${COLOR_GREEN}Running linter...${COLOR_RESET}"
	@GOBIN=${LOCAL_BIN_DIR} go install $(PKG_golangci_lint)
	@${LOCAL_BIN_DIR}/golangci-lint run --timeout=10m ./...

# golangci-lint run linting tool.
.PHONY: golangci-lint
golangci-lint:
	@printf '%b\n' "${COLOR_GREEN}Running linter...${COLOR_RESET}"
	@GOBIN=${LOCAL_BIN_DIR} go install $(PKG_golangci_lint)
	@${LOCAL_BIN_DIR}/golangci-lint run --fix ./...

# changelog generates a changelog from the releases.
.PHONY: changelog
changelog:
	@printf '%b\n' "${COLOR_GREEN}Running changelog...${COLOR_RESET}"
	@GOBIN=${LOCAL_BIN_DIR} go install $(PKG_changelog-from-release)
	@${LOCAL_BIN_DIR}/changelog-from-release -r ${REMOTE_REPO_URL} -c > CHANGELOG.md

##############################################################################
# Certificates
##############################################################################

${CA_CERT_FILE}:
	@printf '%b\n' "${COLOR_GREEN}Generating CA certificates...${COLOR_RESET}"
	@openssl req -x509 -newkey rsa:4096 \
		-nodes -days 365 -keyout ${CA_KEY_FILE} \
		-out ${CA_CERT_FILE} \
		-subj "$(DEV_CERT_SUBJ_CA)"

${SERVER_KEY_FILE}:
	@printf '%b\n' "${COLOR_GREEN}Generating server key...${COLOR_RESET}"
	@openssl req -newkey rsa:4096 -nodes -keyout ${SERVER_KEY_FILE} \
		-out ${SERVER_CERT_REQ} \
		-subj "$(DEV_CERT_SUBJ_SERVER)"

${SERVER_CERT_FILE}: ${CA_CERT_FILE} ${SERVER_KEY_FILE}
	@printf '%b\n' "${COLOR_GREEN}Generating server certificate...${COLOR_RESET}"
	@openssl x509 -req -in ${SERVER_CERT_REQ} -CA ${CA_CERT_FILE} -CAkey ${CA_KEY_FILE} \
		-CAcreateserial -out ${SERVER_CERT_FILE} \
		-extfile ${OPENSSL_CONF}

${CLIENT_KEY_FILE}:
	@printf '%b\n' "${COLOR_GREEN}Generating client key...${COLOR_RESET}"
	@openssl req -newkey rsa:4096 -nodes -keyout ${CLIENT_KEY_FILE} \
		-out ${CLIENT_CERT_REQ} \
		-subj "$(DEV_CERT_SUBJ_CLIENT)"

${CLIENT_CERT_FILE}: ${CA_CERT_FILE} ${CLIENT_KEY_FILE}
	@printf '%b\n' "${COLOR_GREEN}Generating client certificate...${COLOR_RESET}"
	@openssl x509 -req -in ${CLIENT_CERT_REQ} -days 60 -CA ${CA_CERT_FILE} \
		-CAkey ${CA_KEY_FILE} -CAcreateserial -out ${CLIENT_CERT_FILE} \
		-extfile ${OPENSSL_CONF}

${CERTS_DIR}:
	@printf '%b\n' "${COLOR_GREEN}Creating the certificates directory...${COLOR_RESET}"
	@mkdir -p ${CERTS_DIR}

.PHONY: certs-check
certs-check:
	@printf '%b\n' "${COLOR_GREEN}Checking CA certificate...${COLOR_RESET}"
	@openssl x509 -in ${SERVER_CERT_FILE} -noout -text
