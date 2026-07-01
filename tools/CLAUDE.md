# CLAUDE.md

本文件是 `tools/` 的局部索引。进入开发工具目录工作时，先读本文件，再进入具体工具包或脚本。

> **维护约定**：本文件只记录生成与构建工具的导航；当新增、删除、移动工具脚本或工具包时同步更新索引。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 开发期工具目录。
- 包括配置生成、构建编排和配置生成器。

## 子目录

- [`configgen/`](configgen/)

## 主要文件

- [`build.py`](build.py)
- [`config.py`](config.py)
- [`config_bake.py`](config_bake.py)

## 快速读法

- `config.py` 负责读取环境值并烘焙运行目录。
- `build.py` 负责按环境编译并铺二进制。
- `config_bake.py` 负责把模板和环境值渲染成最终配置。

## 工作规则

- `config.py`、`build.py`、`config_bake.py` 之间的职责要分清。
- 改生成链路时先看输入 / 输出，再看调用顺序。
- `__pycache__/` 这类缓存不作为文档目标。
