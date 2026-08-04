# rtop

rtop is a read-only terminal system monitor for Rockchip Linux platforms. The
current implementation includes generic Linux collectors, process ranking, an
interactive terminal interface, and RK3588 GPU/NPU/DDR metrics discovered
through devfreq. Per-core RKNPU load is shown when its debugfs node is readable;
otherwise each core is explicitly marked `permission_denied`.

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

- `1`: ALL board overview.
- `2`: CPU detail.
- `3`: memory, load and memory-heavy processes.
- `4`: Rockchip GPU, NPU, DDR and thermal detail.
- `5`: process table.
- `6` or `i`: board, driver and metric-source information.
- `?`: show Help without leaving the active page.
- `↑`/`↓` or `j`/`k`: select a process or scroll a detail page.
- `c`/`m`: sort processes by CPU or memory.
- `Enter`: toggle details for the selected process.
- `r`: request an immediate refresh.
- `q` or `Ctrl+C`: exit.

Every metric reports a status and source. Missing or unreadable nodes are never
reported as a zero measurement. Use `--root testdata/generic-linux` to run
against a fixture tree instead of the live system.

CPU, memory and accelerator metrics follow `--interval`. Process and thermal
collection use lower default cadences of 2s and 5s respectively to reduce
observer overhead; adjust them with `--process-interval` and
`--thermal-interval`. Pressing `r` forces the active page's collectors to refresh.
The TUI also collects by page. ALL collects the board-wide view, CPU combines
CPU/load/thermal metrics, Memory adds process ranking, Engines combines
accelerator and thermal metrics, Processes scans only processes, and Info reads
the lightweight capability groups. Help is an overlay and does not change the
active collection scope. The UI retains the last value for groups that are not
collected on the active page instead of presenting them as missing.
For headless output, `snapshot --top N` emits only the busiest N processes and
`snapshot --no-processes` skips process collection entirely.

## Diagnostics

`rtop doctor` checks platform detection, devfreq drivers, debugfs mounting and
permissions, and the selected source for each metric. It is read-only and never
mounts filesystems or changes permissions. See
[docs/diagnostics.md](docs/diagnostics.md) for interpreting the report.

## Packaging

```sh
make package VERSION=v0.1.0
```

This creates an ARM64 tarball, Debian package and `SHA256SUMS` under `dist/`.
The current platform support status is documented in
[docs/support-matrix.md](docs/support-matrix.md).
