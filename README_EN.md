# rtop

[简体中文](README.md) | English

`rtop` is a terminal system monitor and tuning utility for Rockchip Linux. It
brings CPU, memory, thermal, process, GPU, NPU, DDR, and RGA status into one TUI
and can also emit JSON snapshots for scripts. Monitoring is read-only by
default; settings change only after explicitly using the `6CTRL` page or a
`tune` command.

[![rtop system overview](docs/images/rtop-overview.png)](docs/images/rtop-overview.png)

## Highlights

- **Read-only by default:** snapshots, continuous monitoring, and diagnostics
  only read `/proc`, sysfs, and debugfs.
- **Explicit system tuning:** detects and adjusts total Swap capacity at
  runtime, changes `vm.swappiness`, or safely switches every CPUFreq policy to
  the `performance` or `powersave` governor.
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
per-core RKNPU load and some RGA data depend on debugfs permissions, while
system tuning normally requires root.

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
| `rtop tune swap list` | List active zram, swapfile, and block Swap backends at runtime |
| `sudo rtop tune swap swappiness 10` | Set runtime `vm.swappiness` to 10 |
| `sudo rtop tune swap size 4G` | Set total active Swap capacity to 4 GiB |
| `sudo rtop tune swap size 0` | Disable all active Swap after safety checks |
| `sudo rtop tune cpu performance` | Switch all CPUFreq policies to performance mode |
| `sudo rtop tune cpu powersave` | Switch all CPUFreq policies to powersave mode |

> **Note:** `--top N` applies only to the JSON output from
> `snapshot --json` and `snapshot --watch`. `--no-processes` skips process
> collection entirely.

Run `rtop --help` or `rtop <command> --help` for all available options.

### TUI keys

| Key | Action |
|---|---|
| `1`–`7` | Open the ALL, CPU, MEMORY, ENGINES, PROCESSES, CTRL, or INFO page |
| `←` / `→` / `Tab` | Change pages |
| `↑` / `↓` / `j` / `k` | Select a process or scroll details |
| `c` / `m` | Sort by CPU or memory usage |
| `p` | Show or hide executable paths |
| `Enter` | Expand or collapse process details |
| CTRL: `p` / `e` | Switch the CPU to performance / powersave mode |
| CTRL: `s` / `c` | Set swappiness / total Swap capacity |
| `r` | Refresh immediately |
| `?` | Show help |
| `q` / `Ctrl+C` | Exit |

### System controls

Tuning normally requires root privileges. Changes affect runtime state only;
rtop does not write `/etc/sysctl.conf`, systemd units, or distribution power
management configuration. A reboot or another power-management service may
therefore restore previous values.

- Linux accepts `vm.swappiness` values from 0 through 200. This controls the
  kernel's relative tendency to use swap; it does not resize swap, and zero
  does not disable swap.
- Swap capacity means the sum of all active entries in `/proc/swaps`. rtop
  reads that file while planning, immediately before the first write, and
  after the change, detecting zram, ordinary swapfiles, and block backends such
  as partitions, LVM, or encrypted mappings. The user never selects a backend
  type. Accepted inputs include `512M`, `4G`, `1.5GiB`, and `0`.
- zram and ordinary swapfiles can be rebuilt directly. rtop never edits a
  partition table, LVM volume, or encrypted container. For fixed-size block
  backends it keeps or disables complete devices and fills the exact residual
  with the rtop-owned `/var/lib/rtop/swapfile`. `size 0` disables all active
  Swap.
- Before any `swapoff`, rtop checks that `MemAvailable` can hold the used Swap
  being evacuated while retaining a 64 MiB reserve. Allocation, `mkswap`,
  `swapon`, or post-write verification failures trigger an attempt to restore
  the original capacity and active backends. Root privileges, sufficient RAM
  and storage, and the standard Swap commands are required.
- rtop does not write `/etc/fstab`, systemd units, or zram configuration. Its
  supplemental managed file is therefore not enabled automatically after a
  reboot, and distribution services may recreate zram or reactivate configured
  block devices. Directly resizing an ordinary swapfile changes that file's
  length until another service recreates it.
- CPU mode discovers standard `/sys/devices/system/cpu/cpufreq/policy*`
  controls and verifies that every policy supports the requested governor
  before writing. If a write or verification fails, rtop attempts to restore
  policies already changed so heterogeneous CPUs do not remain in a mixed
  mode.
- If the kernel's governor list omits `performance` or `powersave`, rtop reports
  the available governors; when a driver omits that list, the write result is
  authoritative. It never silently substitutes one with different semantics.

See [docs/swap.md](docs/swap.md) for backend selection, rollback behavior, and
filesystem limitations.

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
