# Platform support matrix

| Platform | Kernel/BSP | Generic metrics | GPU/NPU/DDR | Per-core NPU | Validation |
|---|---|---:|---:|---:|---|
| RK3588 | Linux 6.1 | Full | Full | Full when debugfs is readable | Verified on RK3588 hardware and fixtures |
| RK3588 | Rockchip BSP 5.10 | Full | Full | Full when debugfs is readable | Parser and virtual-system fixtures; additional board reports welcome |
| RK3566 | Vendor kernels | Expected | Partial | Single-core expected | Not yet hardware-verified |
| RK3568 | Vendor kernels | Expected | Partial | Single-core expected | Not yet hardware-verified |
| RK3576 | Vendor kernels | Expected | Partial | Two-core expected | Not yet hardware-verified |
| Non-Rockchip Linux | Mainline/vendor | CPU, memory, load, thermal and processes | Unsupported unless compatible devfreq nodes exist | Unsupported | Generic Linux fixtures |

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
