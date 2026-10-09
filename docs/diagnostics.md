# Diagnostics

`rtop doctor` produces a read-only report covering:

- Device Tree SoC and board detection.
- Kernel and architecture.
- Mali GPU, RKNPU and DMC devfreq driver discovery.
- debugfs mounting and current-user access.
- CPU, memory, load, thermal and process sources.
- Current `vm.swappiness` source and availability.
- Active Swap backend paths, types, capacities, usage, and priorities from
  `/proc/swaps` in snapshots and the TUI (`rtop tune swap list` provides the
  dedicated live view).
- GPU, NPU and DDR load/frequency sources.
- CPU and devfreq minimum/maximum frequency and governor sources.
- Accelerator runtime power-state sources.
- Per-core RKNPU load status.
- CMA accounting and PSI memory-pressure availability.
- Thermal trip-point and cooling-device discovery.
- hwmon voltage, current, power, energy and fan/PWM sources.

Use JSON output when attaching a report to an issue:

```sh
rtop doctor --json > rtop-doctor.json
```

Review the file before sharing it. It includes the hostname, board model, kernel
version, device names and filesystem paths, but does not include process command
lines or file contents.

CPUFreq policy mapping, RK3588 shared frequency domains, `Frq` status meanings,
and read-only inspection commands are documented in
[cpufreq.md](cpufreq.md).

Runtime Swap backend selection, the pre-`swapoff` memory check, rollback, and
reboot behavior are documented in [swap.md](swap.md). `doctor` remains
read-only and never tests a Swap change by disabling a live backend.

## Common warnings

### debugfs is not mounted

Generic metrics, aggregate GPU/NPU/DDR devfreq metrics, and procfs-backed RGA
load metrics may still work.
Per-core RKNPU load commonly requires `/sys/kernel/debug/rknpu/load`.
Mounting debugfs requires administrative privileges and is never performed by
rtop.

### debugfs is not readable

rtop displays `permission_denied` rather than reporting a false 0% value.
If per-core metrics are required, grant narrowly scoped access to the required
node or run with suitable privileges. Avoid making the entire debugfs tree
world-readable.

### no matching devfreq device

Check that the corresponding Device Tree node is enabled, the kernel driver is
loaded or built in, and the driver registered a devfreq device. Include
`rtop doctor --json` output when reporting an unsupported BSP.
