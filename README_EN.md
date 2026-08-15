# rtop

[简体中文](README.md) | English

`rtop` is a read-only terminal system monitor for Rockchip Linux. It brings
CPU, memory, thermal, process, GPU, NPU, DDR, and RGA status into one TUI and
can also emit JSON snapshots for scripts.

[![rtop system overview](docs/images/rtop-overview.png)](docs/images/rtop-overview.png)

## Highlights

- **Read-only monitoring:** reads `/proc`, sysfs, and debugfs without changing
  frequency policies, permissions, or system state.
- **Rockchip metrics:** discovers GPU, NPU, and DDR devfreq devices dynamically,
  including per-core RKNPU load and RGA scheduler load.
- **Generic Linux metrics:** CPU, memory, swap, PSI, CMA, zram, thermal and
  cooling devices, hwmon power and fans, and process ranking.
- **Explicit data status:** reports missing or unreadable nodes as
  `unsupported`, `permission_denied`, or `unavailable` instead of a false zero.
- **Multiple output modes:** interactive TUI, text/JSON snapshots, continuous
  NDJSON, and a read-only diagnostic report.

## Interface preview

| Memory and pressure | Rockchip engines |
|:---:|:---:|
| [![rtop memory and pressure monitoring](docs/images/rtop-memory.png)](docs/images/rtop-memory.png) | [![rtop Rockchip GPU, NPU, DDR, and RGA monitoring](docs/images/rtop-engines.png)](docs/images/rtop-engines.png) |

Click an image to view it at full size. The screenshots use the real TUI
rendered with representative RK3588 data.

## Installation

Current prebuilt releases support Linux ARM64. See the
[support matrix](docs/support-matrix.md) for platform and kernel coverage.

### GitHub

```sh
curl -fsSL https://raw.githubusercontent.com/zouri/rtop/main/scripts/install.sh | sudo sh
rtop
```

### Gitee

When GitHub connectivity is limited, fetch the installer, release metadata,
archive, and checksum entirely from Gitee:

```sh
curl -fsSL https://gitee.com/zouri/rtop/raw/main/scripts/install.sh | sudo env RTOP_SOURCE=gitee sh
rtop
```

The installer verifies the release SHA-256 checksum and installs the binary as
`/usr/bin/rtop`. Generic metrics usually do not require root privileges;
per-core RKNPU load and some RGA data depend on debugfs permissions.

### Build from source

Go 1.24 or newer is required:

```sh
git clone https://github.com/zouri/rtop.git
cd rtop
make build
./bin/rtop
```

## Usage

### Common commands

| Command | Purpose |
|---|---|
| `rtop` | Start the interactive TUI |
| `rtop info` | Show board, SoC, kernel, and operating system information |
| `rtop info --json` | Emit system information as JSON |
| `rtop snapshot` | Print one text snapshot |
| `rtop snapshot --json` | Emit one JSON snapshot |
| `rtop snapshot --watch --interval 1s` | Continuously emit one JSON object per line |
| `rtop snapshot --watch --top 20` | Keep the 20 processes with the highest CPU usage |
| `rtop snapshot --watch --no-processes` | Skip process collection |
| `rtop doctor` | Check the platform, drivers, permissions, and metric sources |

> **Note:** `--top N` applies only to the JSON output from
> `snapshot --json` and `snapshot --watch`. `--no-processes` skips process
> collection entirely.

Run `rtop --help` or `rtop <command> --help` for all available options.

### TUI keys

| Key | Action |
|---|---|
| `1`–`6` | Open the ALL, CPU, MEMORY, ENGINES, PROCESSES, or INFO page |
| `←` / `→` / `Tab` | Change pages |
| `↑` / `↓` / `j` / `k` | Select a process or scroll details |
| `c` / `m` | Sort by CPU or memory usage |
| `p` | Show or hide executable paths |
| `Enter` | Expand or collapse process details |
| `r` | Refresh immediately |
| `?` | Show help |
| `q` / `Ctrl+C` | Exit |

## Diagnostics and compatibility

Start with these commands to inspect platform detection, devfreq drivers,
debugfs permissions, and the metric sources selected on the current system:

```sh
rtop doctor
rtop doctor --json > rtop-doctor.json
```

Review the JSON report before sharing it. It includes the hostname, board
model, kernel version, device names, and filesystem paths. More details:

- [Diagnostics guide](docs/diagnostics.md)
- [Platform support matrix](docs/support-matrix.md)
- [CPUFreq policies and RK3588 CPU frequency](docs/cpufreq.md)

## Support and contributing

The project is maintained by [zouri](https://github.com/zouri) and contributors.
Report bugs and compatibility issues through
[GitHub Issues](https://github.com/zouri/rtop/issues). Include the rtop version,
board/SoC, kernel or BSP, and a reviewed `rtop doctor --json` report.

Before opening a pull request, run:

```sh
make test
make lint
make build
```

## License

[MIT License](LICENSE)
