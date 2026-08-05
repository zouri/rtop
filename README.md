# rtop

简体中文 | [English](README_EN.md)

rtop 是面向 Rockchip Linux 平台的只读终端系统监视器。当前版本提供
Linux 通用指标采集、进程排行、交互式终端界面，以及通过 devfreq 动态发现的
RK3588 GPU、NPU 和 DDR 指标。当内核提供相应接口时，还可显示 CPU/devfreq
频率策略、上下限与调速器、运行时电源状态、温控点与冷却状态、PSI 压力、
CMA、zram、hwmon 电源轨和风扇状态。

当 RKNPU debugfs 节点可读时，rtop 会显示各个 NPU 核心的负载；无法读取时，
对应核心会明确显示 `permission_denied`，不会将缺失数据误报为 0。

## 构建

```sh
go build -buildvcs=false ./cmd/rtop
```

## 安装

使用一行命令安装最新的 Linux ARM64 版本：

```sh
curl -fsSL https://raw.githubusercontent.com/zouri/rtop/main/scripts/install.sh | sudo sh
```

安装脚本会校验发布包的 SHA-256，并要求使用 root 权限将程序安装到全局路径
`/usr/bin/rtop`。这样普通用户和 `sudo` 环境都能直接找到该命令。可以通过
`RTOP_VERSION=v0.1.0` 安装指定版本：

```sh
curl -fsSL https://raw.githubusercontent.com/zouri/rtop/main/scripts/install.sh | sudo env RTOP_VERSION=v0.1.0 sh
```

## 使用

```text
rtop
rtop info
rtop info --json
rtop snapshot --json
rtop snapshot --watch --interval 1s
rtop snapshot --watch --top 20
rtop snapshot --watch --no-processes
rtop --interval 1s --process-interval 2s --thermal-interval 5s
rtop doctor
rtop doctor --json
```

交互界面采用类似 jtop 的开发板监控布局。界面可响应终端尺寸变化，最小支持
24 列、8 行；窗口更小时会显示简洁的尺寸提示，而不会绘制被截断的卡片。

- `1`：ALL，开发板总览。
- `2`：CPU 负载、频率策略、温控限制、冷却、电源、风扇及 CPU 高占用进程。
- `3`：内存、CMA、PSI、zram、系统负载及内存高占用进程。
- `4`：Rockchip 计算引擎、devfreq 策略、运行状态、温度、电源及风扇。
- `5`：进程列表。
- `6` 或 `i`：开发板、驱动和指标来源信息。
- `?`：在当前页面上显示帮助。
- `↑`/`↓` 或 `j`/`k`：选择进程或滚动详情页面。
- `c`/`m`：按 CPU 或内存占用排序进程。
- `p`：在包含进程列表的页面显示或隐藏可执行文件路径。
- `Enter`：展开或收起当前进程的详细信息。
- `r`：立即刷新。
- `q` 或 `Ctrl+C`：退出。

每项指标都会报告状态和数据来源。缺失或无法读取的节点不会显示为 0。可以使用
`--root testdata/generic-linux` 读取测试数据树，而不是访问当前系统。

CPU、内存、加速器、电源和风扇指标按照 `--interval` 设置的周期采集。进程和温度
默认分别以 2 秒和 5 秒的较低频率采集，以减少监控开销；可通过
`--process-interval` 和 `--thermal-interval` 调整。按下 `r` 会立即刷新当前页面
所需的采集器。

TUI 会根据当前页面选择采集范围：ALL 收集整机视图；CPU 汇总 CPU、负载、温度
和电源指标；Memory 增加进程排行；Engines 汇总加速器、温度和电源指标；
Processes 仅扫描进程；Info 读取轻量级能力信息。帮助页面以覆盖层显示，不会改变
当前采集范围。切换页面后，未被当前页面采集的指标会保留最近一次数据，而不会被
显示为缺失。

在无界面模式下，`snapshot --top N` 仅输出最繁忙的 N 个进程，
`snapshot --no-processes` 会完全跳过进程采集。

## 诊断

`rtop doctor` 会检查平台识别、devfreq 驱动、debugfs 挂载与访问权限、DVFS
控制节点、内存压力能力、温控能力、hwmon 电源/风扇能力，以及每项指标实际选择的
数据来源。该命令严格只读，不会挂载文件系统或修改权限。

诊断报告的解释请参阅 [docs/diagnostics.md](docs/diagnostics.md)。

## 打包

```sh
make package VERSION=v0.1.0
```

构建产物位于 `dist/`，包括 ARM64 tar 包、Debian 软件包和 `SHA256SUMS`。
当前平台支持情况请参阅 [docs/support-matrix.md](docs/support-matrix.md)。
