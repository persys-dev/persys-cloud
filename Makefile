SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

GO ?= go
ROOT_BIN_DIR ?= bin
GOCACHE ?= $(CURDIR)/.cache/go-build
GOMODCACHE ?= $(CURDIR)/.cache/go-mod
GO_ENV = GOCACHE="$(GOCACHE)" GOMODCACHE="$(GOMODCACHE)"

SERVICES := compute-agent persys-scheduler persysctl persys-gateway persys-federation persys-forgery persys-operator vault-mtls-mock

compute-agent_DIR := compute-agent
compute-agent_PKG := ./cmd/agent
compute-agent_BIN := persys-agent

persys-scheduler_DIR := persys-scheduler
persys-scheduler_PKG := ./cmd/scheduler
persys-scheduler_BIN := persys-scheduler

persysctl_DIR := persysctl
persysctl_PKG := ./main.go
persysctl_BIN := persysctl

persys-gateway_DIR := persys-gateway
persys-gateway_PKG := ./cmd
persys-gateway_BIN := persys-api

persys-federation_DIR := persys-federation
persys-federation_PKG := ./cmd/main.go
persys-federation_BIN := persys-federation

persys-forgery_DIR := persys-forgery
persys-forgery_PKG := ./cmd/main.go
persys-forgery_BIN := persys-forgery

persys-operator_DIR := persys-operator
persys-operator_PKG := ./cmd/main.go
persys-operator_BIN := persys-operator

vault-mtls-mock_DIR := vault-mtls-mock
vault-mtls-mock_PKG := ./main.go
vault-mtls-mock_BIN := vault-mtls-mock

.PHONY: help init certs deps build build-all test test-all clean clean-all $(addprefix build-,$(SERVICES)) $(addprefix test-,$(SERVICES)) $(addprefix clean-,$(SERVICES))

help:
	@echo "Persys Cloud Root Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  make init                      - bootstrap project (deps + certs + build-all)"
	@echo "  make certs                     - generate development certificates"
	@echo "  make deps                      - download go dependencies for all services"
	@echo "  make build-all                 - build all services into $(ROOT_BIN_DIR)/"
	@echo "  make build SERVICE=<name>      - build one service into $(ROOT_BIN_DIR)/"
	@echo "  make build-<service>           - build one service (shortcut)"
	@echo "  make test-all                  - run tests for all services"
	@echo "  make test SERVICE=<name>       - run tests for one service"
	@echo "  make clean-all                 - remove root and service bin artifacts"
	@echo "  make clean SERVICE=<name>      - clean one service"
	@echo ""
	@echo "Services:"
	@for service in $(SERVICES); do echo "  - $$service"; done

init:
	@./init.sh

certs:
	@./generate-certs.sh

deps:
	@mkdir -p "$(ROOT_BIN_DIR)"
	@mkdir -p "$(GOCACHE)" "$(GOMODCACHE)"
	@for service in $(SERVICES); do \
		echo "==> Downloading dependencies for $$service"; \
		dir_var="$${service}_DIR"; \
		service_dir="$${!dir_var}"; \
		( cd "$$service_dir" && $(GO_ENV) $(GO) mod download ); \
	done

build:
	@if [[ -z "$${SERVICE:-}" ]]; then \
		echo "Usage: make build SERVICE=<$(SERVICES)>"; \
		exit 1; \
	fi
	@$(MAKE) --no-print-directory "build-$${SERVICE}"

build-all: $(addprefix build-,$(SERVICES))

define SERVICE_BUILD_TEMPLATE
build-$(1):
	@mkdir -p "$(ROOT_BIN_DIR)"
	@mkdir -p "$(GOCACHE)" "$(GOMODCACHE)"
	@echo "==> Building $(1)"
	@cd "$$($(1)_DIR)" && $(GO_ENV) $(GO) build -v -o "../$(ROOT_BIN_DIR)/$$($(1)_BIN)" "$$($(1)_PKG)"
endef

$(foreach service,$(SERVICES),$(eval $(call SERVICE_BUILD_TEMPLATE,$(service))))

test:
	@if [[ -z "$${SERVICE:-}" ]]; then \
		echo "Usage: make test SERVICE=<$(SERVICES)>"; \
		exit 1; \
	fi
	@$(MAKE) --no-print-directory "test-$${SERVICE}"

test-all: $(addprefix test-,$(SERVICES))

define SERVICE_TEST_TEMPLATE
test-$(1):
	@echo "==> Testing $(1)"
	@mkdir -p "$(GOCACHE)" "$(GOMODCACHE)"
	@cd "$$($(1)_DIR)" && $(GO_ENV) $(GO) test -v ./...
endef

$(foreach service,$(SERVICES),$(eval $(call SERVICE_TEST_TEMPLATE,$(service))))

clean:
	@if [[ -z "$${SERVICE:-}" ]]; then \
		echo "Usage: make clean SERVICE=<$(SERVICES)>"; \
		exit 1; \
	fi
	@$(MAKE) --no-print-directory "clean-$${SERVICE}"

clean-all: $(addprefix clean-,$(SERVICES))
	@echo "==> Removing root binaries"
	@rm -rf "$(ROOT_BIN_DIR)"

define SERVICE_CLEAN_TEMPLATE
clean-$(1):
	@echo "==> Cleaning $(1)"
	@rm -f "$(ROOT_BIN_DIR)/$$($(1)_BIN)"
	@rm -rf "$$($(1)_DIR)/bin"
endef

$(foreach service,$(SERVICES),$(eval $(call SERVICE_CLEAN_TEMPLATE,$(service))))
