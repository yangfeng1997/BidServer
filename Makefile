PY ?= python
GO ?= go
ENV ?= dev
WORLDID ?=

BUILD_DIR := build
RUN_DIR := run

.PHONY: all config build gen-config test fmt clean run-clean

all: config build test

gen-config:
	@echo "  GEN     config"
	@$(GO) run ./tools/configgen

config: gen-config
	@if [ -z "$(WORLDID)" ]; then echo "ERROR: WORLDID is required, usage: make config ENV=$(ENV) WORLDID=1"; exit 1; fi
	@echo "  CONFIG env=$(ENV) world=$(WORLDID)"
	@$(PY) scripts/config.py --env $(ENV) --world-id $(WORLDID) --out $(RUN_DIR)

build:
	@echo "  BUILD  services"
	@$(PY) scripts/build.py --out $(RUN_DIR) --build $(BUILD_DIR)

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

run-clean:
	rm -rf $(RUN_DIR)

clean:
	rm -rf $(BUILD_DIR) $(RUN_DIR)
