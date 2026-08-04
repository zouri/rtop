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

TUI keys:

- `1`–`5`: switch between Overview, Engines, Processes, Thermal and System.
- `?` or `6`: show Help.
- `↑`/`↓` or `j`/`k`: select a process.
- `c`/`m`: sort processes by CPU or memory.
- `r`: request an immediate refresh.
- `q` or `Ctrl+C`: exit.

Every metric reports a status and source. Missing or unreadable nodes are never
reported as a zero measurement. Use `--root testdata/generic-linux` to run
against a fixture tree instead of the live system.

CPU, memory and accelerator metrics follow `--interval`. Process and thermal
collection use lower default cadences of 2s and 5s respectively to reduce
observer overhead; adjust them with `--process-interval` and
`--thermal-interval`. Pressing `r` forces every collector to refresh.
The TUI also collects by page: Overview skips process scanning, Engines reads
only accelerator metrics, Processes reads only process metrics, Thermal reads
only sensors, and Help performs no dynamic collection. System collects all
groups because it displays their capability status.
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
