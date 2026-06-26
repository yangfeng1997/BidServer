PY ?= python
GO ?= go
ENV ?= dev

BUILD_DIR := build
RUN_DIR := run

.PHONY: all config build gen-config test fmt clean run-clean

all: config build test

gen-config:
	@echo "  GEN     config"
	@$(GO) run ./tools/configgen

config:
	@echo "  CONFIG env=$(ENV)"
	@$(PY) tools/config.py --env $(ENV) --out $(RUN_DIR)

build:
	@echo "  BUILD  services"
	@$(PY) tools/build.py --out $(RUN_DIR) --build $(BUILD_DIR)

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

run-clean:
	rm -rf $(RUN_DIR)

clean:
	rm -rf $(BUILD_DIR) $(RUN_DIR)
