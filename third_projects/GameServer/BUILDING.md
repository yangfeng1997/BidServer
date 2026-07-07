# Building game-server-pro

## Prerequisites

- Go 1.26+
- (Optional) protoc + protoc-gen-go for proto regeneration

## Quick Start

```bash
make build     # build all services to build/
make test      # run all tests
make quick     # build only (skip codegen)
```

## Code Generation

```
make gen-config   # generate YAML unmarshal/validate code from proto schemas
make gen-routes   # generate client route table from proto handler definitions
make gen-proto    # run full proto pipeline (requires protoc + plugins)
```

## Service Entry Points

```
cmd/gatesvr/     - Gateway server (client facing)
cmd/lobbysvr/    - Lobby server
cmd/roomsvr/     - Room server
cmd/matchsvr/   - Match server
cmd/onlinesvr/  - Online server
cmd/routeragent/ - Internal router
```

## Configuration

Each service uses two YAML configs:

- `conf/<svc>/yaml/<svc>.yaml` — startup config (node identity, infra connections)
- `conf/<svc>/yaml/<svc>_runtime.yaml` — runtime config (thresholds, log level, SIGHUP reloadable)

Config schemas are defined in `conf/<svc>/schema/*.proto` and codegen produces `*_yaml.go`.

## Project Layout

```
internal/core/     - framework layer (app, rpc, dispatcher, session, codec, conn)
internal/<svc>/    - business servers
internal/routeragent/ - internal router
pkg/               - reusable packages (timewheel, logger, configgen)
tools/             - code generation tools
protocol/          - proto definitions and generated Go code
conf/              - configuration templates and schemas
```

## CI

```bash
make ci           # vet + test + race test
```

## Code Style

- Comments in Simplified Chinese, no trailing punctuation
- No usage of `viper` — config via gen_config
- No usage of `encoding/json` — serialization is protobuf only
- All cross-goroutine work goes through `taskqueue.Post`
