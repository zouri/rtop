# rtop

rtop is a read-only terminal system monitor for Rockchip Linux platforms. The
current implementation includes generic Linux collectors, process ranking, an
interactive terminal interface, and RK3588 GPU/NPU/DDR metrics discovered
through devfreq. It also reports CPU/devfreq policy limits and governors,
runtime power state, thermal trip points and cooling state, PSI pressure, CMA,
zram statistics, hwmon power rails and fan state when the kernel exposes them.
Per-core RKNPU load is shown when its debugfs node is readable; otherwise each
core is explicitly marked `permission_denied`.

## Build

```sh
go build -buildvcs=false ./cmd/rtop
```

## Usage

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

The TUI follows a jtop-style board monitor layout:

The layout responds to terminal resizing down to 24 columns by 8 rows. Smaller
windows show a compact resize hint instead of drawing clipped cards.

- `1`: ALL board overview.
- `2`: CPU load, frequency policy, thermal limits, cooling, power, fans and CPU-heavy processes.
- `3`: memory, CMA, PSI, zram, load and memory-heavy processes.
- `4`: Rockchip engines, devfreq policy, runtime state, thermals, board power and fans.
- `5`: process table.
- `6` or `i`: board, driver and metric-source information.
- `?`: show Help without leaving the active page.
- `↑`/`↓` or `j`/`k`: select a process or scroll a detail page.
- `c`/`m`: sort processes by CPU or memory.
- `p`: show or hide executable paths on pages containing process lists.
- `Enter`: toggle details for the selected process.
- `r`: request an immediate refresh.
- `q` or `Ctrl+C`: exit.

Every metric reports a status and source. Missing or unreadable nodes are never
reported as a zero measurement. Use `--root testdata/generic-linux` to run
against a fixture tree instead of the live system.

CPU, memory, accelerator, power and fan metrics follow `--interval`. Process and thermal
collection use lower default cadences of 2s and 5s respectively to reduce
observer overhead; adjust them with `--process-interval` and
`--thermal-interval`. Pressing `r` forces the active page's collectors to refresh.
The TUI also collects by page. ALL collects the board-wide view, CPU combines
CPU/load/thermal/power metrics, Memory adds process ranking, Engines combines
accelerator/thermal/power metrics, Processes scans only processes, and Info reads
the lightweight capability groups. Help is an overlay and does not change the
active collection scope. The UI retains the last value for groups that are not
collected on the active page instead of presenting them as missing.
For headless output, `snapshot --top N` emits only the busiest N processes and
`snapshot --no-processes` skips process collection entirely.

## Diagnostics

`rtop doctor` checks platform detection, devfreq drivers, debugfs mounting and
permissions, DVFS controls, memory-pressure capabilities, thermal controls and
hwmon power/fan capabilities, and the selected source for each metric. It is read-only and never mounts
filesystems or changes permissions. See
[docs/diagnostics.md](docs/diagnostics.md) for interpreting the report.

## Packaging

```sh
make package VERSION=v0.1.0
```

This creates an ARM64 tarball, Debian package and `SHA256SUMS` under `dist/`.
The current platform support status is documented in
[docs/support-matrix.md](docs/support-matrix.md).
