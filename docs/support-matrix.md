# Platform support matrix

| Platform | Kernel/BSP | Generic metrics | GPU/NPU/DDR | Per-core NPU | RGA load | Validation |
|---|---|---:|---:|---:|---:|---|
| RK3588 | Linux 6.1 | Full | Full | Full when debugfs is readable | Full when rkrga load is readable | Existing metrics hardware-verified; RGA parser and fixture verified, hardware validation pending |
| RK3588 | Rockchip BSP 5.10 | Full | Full | Full when debugfs is readable | Full when rkrga load is readable | Parser and virtual-system fixtures; additional board reports welcome |
| RK3566 | Vendor kernels | Expected | Partial | Single-core expected | Capability-probed | Not yet hardware-verified |
| RK3568 | Vendor kernels | Expected | Partial | Single-core expected | Capability-probed | Not yet hardware-verified |
| RK3576 | Vendor kernels | Expected | Partial | Two-core expected | Capability-probed | Not yet hardware-verified |
| Non-Rockchip Linux | Mainline/vendor | CPU, memory, load, thermal and processes | Unsupported unless compatible devfreq nodes exist | Unsupported | Unsupported | Generic Linux fixtures |

Status definitions:

- **Full:** implemented and covered by hardware or fixture tests.
- **Partial:** dynamic discovery is implemented, but the platform has not
  completed hardware validation.
- **Expected:** generic Linux interfaces should work, but no release claim is
  made without hardware testing.

Missing or inaccessible metrics retain an explicit status such as
`unsupported`, `permission_denied` or `unavailable`; they are not converted
to zero.

CPU/devfreq policy limits, runtime power state, PSI, CMA, zram, thermal trip
points and cooling devices are capability-probed on every platform. Their
availability depends on the kernel configuration, BSP and board Device Tree;
absence does not reduce the generic support level in the table above.

Board voltage, current, power, energy and fan/PWM metrics are likewise
capability-probed through the standard hwmon ABI. They are reported only when
the board exposes corresponding channels.

Runtime tuning uses generic Linux interfaces rather than Rockchip-specific
drivers. `vm.swappiness` adjustment requires a writable `/proc/sys/vm/swappiness`;
CPU performance/powersave modes require writable CPUFreq policies and the
corresponding governors. rtop preflights every policy and does not claim tuning
support when the kernel omits these controls.

Swap capacity tuning is likewise runtime-discovered and platform-independent.
rtop rereads `/proc/swaps` for each operation, directly rebuilds zram or regular
swapfiles, and treats partitions, LVM volumes, and encrypted mappings as fixed
block backends. It never edits partition tables or resizes block devices; an
rtop-owned `/var/lib/rtop/swapfile` supplies any residual capacity. Support
therefore depends on sufficient `MemAvailable`, root privileges, the standard
`swapoff`/`mkswap`/`swapon` tools, and a filesystem that supports swapfiles.
See [swap.md](swap.md) for the transaction and reboot semantics.

RGA scheduler load is read from the unified Rockchip RGA driver's
`/sys/kernel/debug/rkrga/load` or `/proc/rkrga/load` interface. Older RGA/RGA2
drivers that do not implement this accounting are reported as `unsupported`.
The aggregate RGA usage is the arithmetic mean of the reported scheduler
loads; each raw scheduler percentage is also retained in the snapshot. When
the same report includes a session table, rtop retains its session ID, status,
TGID and process command. These records identify clients but do not provide
per-process utilization. Malformed session rows are skipped independently of
the scheduler load parser and produce a diagnostic warning.
