.PHONY: all build test clean gen-bins gen-config gen-routes gen-proto tools vet fmt config run-clean

GO       := go
GOFLAGS  :=
MODULE   := project
OUT_DIR  := build

# 所有服务入口
SERVICES := gatesvr lobbysvr roomsvr matchsvr onlinesvr routeragent

# 默认环境
ENV ?= dev

# ── 默认目标 ──
all: build test

# ── 构建所有可执行文件 ──
build: $(SERVICES)
	@python3 scripts/build.py

$(SERVICES):
	@echo "  BUILD  $@"
	@$(GO) build $(GOFLAGS) -o $(OUT_DIR)/$@ ./cmd/$@

# ── 快速构建（跳过生成步骤） ──
quick:
	$(GO) build $(GOFLAGS) ./...

# ── 测试 ──
test:
	$(GO) test $(GOFLAGS) -count=1 ./...

test-short:
	$(GO) test $(GOFLAGS) -short -count=1 ./...

test-race:
	$(GO) test $(GOFLAGS) -race -count=1 ./...

test-cover:
	$(GO) test $(GOFLAGS) -coverprofile=coverage.out -count=1 ./...
	$(GO) tool cover -func=coverage.out

bench:
	$(GO) test $(GOFLAGS) -bench=. -benchmem ./internal/...

# ── 部署环境 ──
config:
	@echo "  CONFIG env=$(ENV)"
	@python3 scripts/config.py --env=$(ENV)

run-clean:
	rm -rf run/
	@echo "run/ removed"

# ── 代码生成 ──
gen-bins: gen-config gen-routes
	@echo "generation complete"

gen-config:
	@echo "  GEN    config Go code"
	@$(GO) run tools/gen_config/main.go

gen-routes:
	@echo "  GEN    route table"
	@$(GO) run tools/gen_routes/main.go --proto protocol/handler --out protocol/gen/routes.go

config-build:
	@echo "  BUILD  config_build"
	@$(GO) build $(GOFLAGS) -o $(OUT_DIR)/config_build ./tools/config_build

gen-proto:
	@echo "  GEN    proto (requires protoc + protoc-gen-go + protoc-gen-svcstub)"
	@bash tools/gen_proto.sh

# ── 工具构建 ──
tools:
	$(GO) build $(GOFLAGS) -o $(OUT_DIR)/gen_routes ./tools/gen_routes
	$(GO) build $(GOFLAGS) -o $(OUT_DIR)/gen_config ./tools/gen_config
	$(GO) build $(GOFLAGS) -o $(OUT_DIR)/protoc-gen-svcstub ./tools/protoc-gen-svcstub

# ── 代码质量 ──
vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

# ── 清理 ──
clean:
	rm -rf $(OUT_DIR)
	rm -f coverage.out
	@echo "clean done"

# ── CI ──
ci: vet test test-race
	@echo "CI passed"
