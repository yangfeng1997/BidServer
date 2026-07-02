# CLAUDE.md

本文件是 `scripts/` 的局部索引。进入工程脚本目录工作时，先读本文件，再看具体脚本。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 工程脚本入口目录。
- 这里放构建、配置烘焙、协议生成这类脚本型编排入口。
- 可编译、可测试的 Go 开发工具仍放在 `tools/`。

## 主要文件

- [`build.py`](build.py)
- [`config.py`](config.py)
- [`config_bake.py`](config_bake.py)
- [`gen_proto.sh`](gen_proto.sh)

## 快速读法

- `config.py` 负责读取环境值并烘焙运行目录。
- `config_bake.py` 负责把模板和环境值渲染成最终配置。
- `build.py` 负责按环境编译并铺二进制。
- `gen_proto.sh` 负责编排协议 `.pb.go`、handler / remote / RPC stub 和路由表生成。

## 工作规则

- 脚本路径变更时要同步 `Makefile`、根 `CLAUDE.md`、`docs/代码生成工具.md` 和相关工具子目录索引。
- 这里的脚本可以调用 `tools/` 下的 Go 工具，但不要把 Go 工具源码放进本目录。
