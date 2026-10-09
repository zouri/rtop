# CPUFreq policy 与 RK3588 CPU 频率

本文整理 rtop 读取 CPU 频率时依赖的 Linux CPUFreq 接口、RK3588 的共享调频域，
以及界面中 `Frq` 和各种状态的含义。资料核对日期为 2026-08-13。

## Linux CPUFreq 的 policy 模型

Linux CPUFreq 的基本对象不是单个逻辑 CPU，而是 `cpufreq_policy`。多个 CPU
可能共享同一套 P-state 控制寄存器或固件接口；向该接口设置频率时，整个 CPU
集合会一起受到影响。内核用一个 policy 表示这样的共享调频域，即使某个 policy
只包含一个 CPU 也采用相同模型。

标准 sysfs 入口为：

```text
/sys/devices/system/cpu/cpufreq/policyX/
```

与 policy 关联的 `cpuY/cpufreq` 通常是指向 `policyX` 的符号链接。`X` 和 `Y`
不保证相同，因此不能根据 `policyX` 的编号推断它控制哪个 CPU。应读取 policy
目录中的 CPU 列表：

- `related_cpus`：属于该 policy 的全部 CPU，包括在线和离线 CPU。
- `affected_cpus`：属于该 policy 且当前在线的 CPU。

rtop 优先使用 `related_cpus` 建立完整映射；节点缺失或内容为空时回退到
`affected_cpus`。解析器同时接受常见的空格列表 `0 1 2 3` 和 Linux cpulist
范围格式 `0-3,6-7`。

### 常用 policy 属性

| 属性 | 单位 | 含义 |
|---|---:|---|
| `scaling_cur_freq` | kHz | 调频驱动最近请求的当前频率；受架构和驱动实现影响，不一定等于硬件瞬时实际频率 |
| `cpuinfo_cur_freq` | kHz | 从硬件获得的当前频率；硬件或驱动无法提供时该节点可能不存在 |
| `scaling_min_freq` | kHz | 当前允许的最低频率 |
| `scaling_max_freq` | kHz | 当前允许的最高频率 |
| `cpuinfo_min_freq` | kHz | 硬件支持的最低频率 |
| `cpuinfo_max_freq` | kHz | 硬件支持的最高频率 |
| `scaling_governor` | 文本 | 当前 policy 使用的 governor，例如 `schedutil` |
| `scaling_available_governors` | 文本列表 | 当前 policy 可选择的 governor；部分厂商驱动可能不提供 |

rtop 使用 `scaling_cur_freq` 作为动态显示值，因为它是 CPUFreq 标准 policy
接口中普遍可用的节点。这里显示的是该 policy 的频率，不应被解释为每个核心都有
一套独立时钟。如果同一 policy 包含多个 CPU，它们在同一轮采样中应显示相同频率。

## RK3588 的三个共享调频域

Linux 主线 RK3588 设备树将八个 CPU 分为三个 cluster：

| Linux CPU 编号 | 核心 | 共享时钟 | 主线 OPP 表 |
|---|---|---|---|
| CPU 0–3 | Cortex-A55 | `SCMI_CLK_CPUL` | `cluster0_opp_table`，`opp-shared` |
| CPU 4–5 | Cortex-A76 | `SCMI_CLK_CPUB01` | `cluster1_opp_table`，`opp-shared` |
| CPU 6–7 | Cortex-A76 | `SCMI_CLK_CPUB23` | `cluster2_opp_table`，`opp-shared` |

主线设备树当前为 A55 调频域列出最高 1.8 GHz 的 OPP，为两个 A76 调频域列出
最高 2.4 GHz 的 OPP。这些是所引用主线设备树中的定义，不是对所有开发板和 BSP
的固定承诺。实际可用范围还会受到 SoC 分档、固件、内核版本、板级设备树、供电、
温控以及 `scaling_min_freq`/`scaling_max_freq` 软件限制影响。

在典型 RK3588 系统上，policy 的 CPU 集合因而应对应：

```text
0-3
4-5
6-7
```

policy 目录名称常见为 `policy0`、`policy4`、`policy6`，但 rtop 不依赖这些名称
与首个 CPU 编号一致，而是始终读取 `related_cpus` 或 `affected_cpus`。

## rtop 的发现与回退顺序

rtop 的 CPU 频率发现流程如下：

1. 扫描 `/sys/devices/system/cpu/cpufreq/policy[0-9]*`。
2. 从每个 policy 的 `related_cpus` 建立 CPU 映射，必要时回退到
   `affected_cpus`。
3. 每轮刷新只读取一次该 policy 的动态频率、上下限和 governor，并复用到 policy
   内的所有 CPU，避免同簇核心出现互相矛盾的瞬时值。
4. 为兼容部分旧 BSP，保留对
   `/sys/devices/system/cpu/cpuN/cpufreq/scaling_cur_freq` 和
   `cpuinfo_cur_freq` 的 per-CPU 目录扫描。
5. policy 映射优先于 per-CPU 回退结果。

TUI 使用从 1 开始的用户界面编号，因此界面 `CPU 1` 到 `CPU 8` 分别对应 Linux
sysfs 的 `cpu0` 到 `cpu7`。

## `Frq` 与状态含义

`Frq` 是 frequency 的缩写。正常情况下，一行包含频率范围进度条、policy 的允许
范围和当前频率，例如：

```text
Frq [||||||||········] 408–1800MHz 1200MHz
```

频率数据缺失时不能据此判断 CPU 离线。CPU 负载来自 `/proc/stat`，频率来自
CPUFreq sysfs，两者是独立数据源。rtop 使用以下状态：

| 显示 | 指标状态 | 含义 |
|---|---|---|
| `N/A` | `unsupported` | 内核或驱动没有暴露所需 CPUFreq 节点 |
| `DENIED` | `permission_denied` | 当前用户无权读取节点 |
| `--` | `unavailable` | 节点存在但本次读取或解析失败 |
| `STALE` | `stale` | 当前显示的是未及时更新的数据 |
| `1200MHz` 等 | `available` | 当前频率读取成功 |

当前频率可读、但最低或最高频率不可读时，rtop 仍显示 MHz 数值，只把进度条绘制为
未知范围。`OFF` 不再用于表达 CPU 频率数据缺失。若未来展示真正的 CPU hotplug
状态，应单独读取 `cpuN/online` 并明确显示 `OFFLINE`。

## 性能与省电模式调优

rtop 将“性能模式”和“省电模式”分别映射到 Linux CPUFreq 的 `performance` 与
`powersave` governor，不自动使用 `schedutil`、`ondemand` 等其他策略代替。执行：

```sh
sudo rtop tune cpu performance
sudo rtop tune cpu powersave
```

也可以在 TUI 中按 `6` 进入 `CTRL` 页面，再按 `p` 或 `e`。同一页面中的 `s` 调整
`vm.swappiness`，`c` 则按运行时探测结果调整总 Swap 容量，详见
[swap.md](swap.md)。rtop 会先发现所有 policy
并读取 `scaling_available_governors`（驱动未提供该可选节点时以实际写入结果为准）；
只有全部 policy 通过预检后才开始修改。每次写入后会重新读取校验，发生失败时按
相反顺序恢复已经修改的 policy。

调优只影响当前内核运行时状态，重启、发行版的电源管理守护进程或热管理策略都
可能再次改变 governor。

## 实机只读核查

下面的命令不会修改调频设置：

```sh
for policy in /sys/devices/system/cpu/cpufreq/policy*; do
    [ -d "$policy" ] || continue
    printf '\n%s\n' "$policy"
    for node in related_cpus affected_cpus scaling_cur_freq \
        scaling_min_freq scaling_max_freq cpuinfo_min_freq \
        cpuinfo_max_freq scaling_governor scaling_available_governors; do
        if [ -r "$policy/$node" ]; then
            printf '  %-24s %s\n' "$node" "$(tr -d '\n' < "$policy/$node")"
        else
            printf '  %-24s %s\n' "$node" '<not readable or absent>'
        fi
    done
done
```

需要区分 `scaling_cur_freq` 和硬件报告频率时，可另外查看可选节点：

```sh
for node in /sys/devices/system/cpu/cpufreq/policy*/cpuinfo_cur_freq; do
    [ -e "$node" ] || continue
    printf '%s: ' "$node"
    cat "$node"
done
```

提交兼容性问题时，建议同时附上 `rtop doctor --json` 输出和上述 policy 属性，但应
先检查报告中是否包含不希望公开的主机名、板卡型号或路径信息。

## 外部资料

以下资料均在 2026-08-13 核对：

1. [Linux Kernel：CPU Performance Scaling](https://docs.kernel.org/admin-guide/pm/cpufreq.html)
   - CPUFreq policy 对象、policy sysfs 目录和各属性的规范说明。
   - [原始 reStructuredText 文档](https://github.com/torvalds/linux/blob/3d6d817622b0a9721e3cc404df3469171582be13/Documentation/admin-guide/pm/cpufreq.rst)
   - [原始文件（raw）](https://raw.githubusercontent.com/torvalds/linux/3d6d817622b0a9721e3cc404df3469171582be13/Documentation/admin-guide/pm/cpufreq.rst)
2. [Linux Kernel：RK3588 CPU 与 cpu-map](https://github.com/torvalds/linux/blob/3d6d817622b0a9721e3cc404df3469171582be13/arch/arm64/boot/dts/rockchip/rk3588-base.dtsi)
   - 八个 CPU 的三 cluster 划分，以及 `SCMI_CLK_CPUL`、
     `SCMI_CLK_CPUB01`、`SCMI_CLK_CPUB23` 共享时钟关系。
   - [原始文件（raw）](https://raw.githubusercontent.com/torvalds/linux/3d6d817622b0a9721e3cc404df3469171582be13/arch/arm64/boot/dts/rockchip/rk3588-base.dtsi)
3. [Linux Kernel：RK3588 OPP 表](https://github.com/torvalds/linux/blob/3d6d817622b0a9721e3cc404df3469171582be13/arch/arm64/boot/dts/rockchip/rk3588-opp.dtsi)
   - 三个 CPU OPP 表、`opp-shared` 标记和主线频率档位。
   - [原始文件（raw）](https://raw.githubusercontent.com/torvalds/linux/3d6d817622b0a9721e3cc404df3469171582be13/arch/arm64/boot/dts/rockchip/rk3588-opp.dtsi)

设备树链接固定在 Linux 上游提交
`3d6d817622b0a9721e3cc404df3469171582be13`，便于复现本文结论；CPUFreq 在线文档仍会
持续更新。以后修改采集策略时，应重新核对当时使用的内核/BSP 源码，不应仅根据
本文记录推断厂商内核行为。
