# rtop

简体中文 | [English](README_EN.md)

`rtop` 是面向 Rockchip Linux 的只读终端系统监视器。它在一个 TUI 中集中展示
CPU、内存、温度、进程以及 GPU、NPU、DDR、RGA 状态，也可输出适合脚本处理的
JSON 快照。

[![rtop 系统总览](docs/images/rtop-overview.png)](docs/images/rtop-overview.png)

## 主要功能

- **只读监控**：读取 `/proc`、sysfs 和 debugfs，不修改调频策略、权限或系统状态。
- **Rockchip 指标**：动态发现 GPU、NPU、DDR devfreq 设备，并采集 RKNPU 逐核负载和
  RGA scheduler 负载。
- **Linux 通用指标**：CPU、内存、Swap、PSI、CMA、zram、温控、冷却设备、hwmon
  电源与风扇，以及进程排行。
- **明确的数据状态**：节点缺失或无权读取时显示 `unsupported`、
  `permission_denied` 或 `unavailable`，不会误报为 0。
- **多种输出方式**：交互式 TUI、文本/JSON 快照、持续 NDJSON 输出和只读诊断报告。

## 界面预览

| 内存与压力 | Rockchip 计算引擎 |
|:---:|:---:|
| [![rtop 内存与压力监控](docs/images/rtop-memory.png)](docs/images/rtop-memory.png) | [![rtop Rockchip GPU、NPU、DDR 和 RGA 监控](docs/images/rtop-engines.png)](docs/images/rtop-engines.png) |

点击图片可查看原图。截图由实际 TUI 使用 RK3588 示例数据渲染。

## 安装

当前预编译版本支持 Linux ARM64。完整的平台和内核支持情况见
[支持矩阵](docs/support-matrix.md)。

### GitHub

```sh
curl -fsSL https://raw.githubusercontent.com/zouri/rtop/main/scripts/install.sh | sudo sh
rtop
```

### Gitee

GitHub 访问受限时，可让脚本、版本信息、安装包和校验文件全部从 Gitee 获取：

```sh
curl -fsSL https://gitee.com/zouri/rtop/raw/main/scripts/install.sh | sudo env RTOP_SOURCE=gitee sh
rtop
```

安装脚本会校验发布包的 SHA-256，并将程序安装到 `/usr/bin/rtop`。普通指标通常
不需要 root 权限；RKNPU 逐核负载和部分 RGA 数据是否可见取决于 debugfs 权限。

### 从源码构建

需要 Go 1.24 或更高版本：

```sh
git clone https://github.com/zouri/rtop.git
cd rtop
make build
./bin/rtop
```

## 使用

### 常用命令

| 命令 | 用途 |
|---|---|
| `rtop` | 启动交互式 TUI |
| `rtop info` | 显示开发板、SoC、内核和系统信息 |
| `rtop info --json` | 以 JSON 输出系统信息 |
| `rtop snapshot` | 输出一次文本快照 |
| `rtop snapshot --json` | 输出一次 JSON 快照 |
| `rtop snapshot --watch --interval 1s` | 持续输出每行一个对象的 NDJSON |
| `rtop snapshot --watch --top 20` | 仅保留 CPU 占用最高的 20 个进程 |
| `rtop snapshot --watch --no-processes` | 跳过进程采集 |
| `rtop doctor` | 检查平台、驱动、权限和指标来源 |

> **注意：** `--top N` 仅作用于 `snapshot --json` 和
> `snapshot --watch` 的 JSON 输出。`--no-processes` 会完全跳过进程采集。

使用 `rtop --help` 或 `rtop <command> --help` 查看全部参数。

### TUI 快捷键

| 按键 | 操作 |
|---|---|
| `1`–`6` | 打开 ALL、CPU、MEMORY、ENGINES、PROCESSES、INFO 页面 |
| `←` / `→` / `Tab` | 切换页面 |
| `↑` / `↓` / `j` / `k` | 选择进程或滚动详情 |
| `c` / `m` | 按 CPU 或内存占用排序 |
| `p` | 显示或隐藏可执行文件路径 |
| `Enter` | 展开或收起进程详情 |
| `r` | 立即刷新 |
| `?` | 显示帮助 |
| `q` / `Ctrl+C` | 退出 |

## 诊断与兼容性

先运行以下命令检查平台识别、devfreq 驱动、debugfs 权限和实际指标来源：

```sh
rtop doctor
rtop doctor --json > rtop-doctor.json
```

分享 JSON 报告前请先检查内容；其中包含主机名、开发板型号、内核版本、设备名和
文件系统路径。详细说明见：

- [诊断指南](docs/diagnostics.md)
- [平台支持矩阵](docs/support-matrix.md)
- [CPUFreq policy 与 RK3588 CPU 频率](docs/cpufreq.md)

## 反馈与贡献

项目由 [zouri](https://github.com/zouri) 和贡献者维护。问题或兼容性报告请提交到
[GitHub Issues](https://github.com/zouri/rtop/issues)，并附上 rtop 版本、开发板/
SoC、内核/BSP 信息以及检查后的 `rtop doctor --json` 输出。

提交 Pull Request 前请运行：

```sh
make test
make lint
make build
```

## 许可证

[MIT License](LICENSE)
