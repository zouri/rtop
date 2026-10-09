# Swap 策略与容量调整

rtop 把 `vm.swappiness` 和 Swap 容量视为两个独立控制项：前者决定内核使用
Swap 的相对倾向，后者是 `/proc/swaps` 中所有活动后端的容量总和。

## 命令

```sh
rtop tune swap list
sudo rtop tune swap swappiness 10
sudo rtop tune swap size 4G
sudo rtop tune swap size 0
```

容量支持 `K/KiB`、`M/MiB`、`G/GiB` 和 `T/TiB` 后缀，也可以使用小数，未写
单位时按 MiB 解释。非零值至少为 1 MiB；`0` 表示停用当前全部 Swap，而不是把
`vm.swappiness` 设为零。

## 实时发现与后端选择

rtop 不要求用户指定设备类型，也不缓存启动时的判断。每次容量操作都会在规划前、
第一条写入前和执行后读取 `/proc/swaps`，并结合 sysfs 将每个活动项分类。写入前
发现路径、类型、容量或优先级被其他服务改变时，操作会在零写入状态下退出并要求
重试：

| 后端 | 识别方式 | 调整策略 |
|---|---|---|
| zram | 块设备名及 `/sys/block/zramN/disksize` | `swapoff` 后 reset，恢复压缩设置，写入新 `disksize`，再执行 `mkswap` 和 `swapon` |
| 普通 swapfile | `/proc/swaps` 的 `Type=file` | 停用后非稀疏分配到新长度，再格式化和启用 |
| 分区、LVM、加密映射等块设备 | 其余 `Type=partition` 项 | 从不修改分区表或块设备长度；只保留或停用整个后端 |

固定块设备无法安全地改成任意长度。此时 rtop 会选择不超过目标的最佳块设备
组合，并使用带所有权标记的 `/var/lib/rtop/swapfile` 补齐余量。该文件只会在标记
有效且路径为普通文件时修改；符号链接、遗留标记或来源不明的同名文件都会被拒绝。

`/proc/swaps` 报告的是去掉 Swap 头之后的可用容量，通常比 zram 或文件长度少一个
页面。rtop 对不足 1 MiB 的差异采用容量容差，不会为几 KiB 的头部差异创建无效的
微小 Swap 后端。

## 安全检查与事务恢复

所有需要停用后端的计划都会先汇总其中的已用容量，并读取
`/proc/meminfo` 的 `MemAvailable`。只有可用内存容纳这些页面后仍能保留 64 MiB
余量时才会执行第一条 `swapoff`。因此即使其他 Swap 还有空闲空间，安全检查也不会
依赖内核一定把页面迁移到另一个 Swap 后端。

执行器记录原始激活顺序、优先级、Swap UUID/标签、文件长度，以及 zram 的容量、
压缩算法、压缩流数量和内存上限。需要替换容量时会先让新后端上线，再停用不再
需要的固定后端；回滚时则先重新启用原后端，避免人为制造 Swap 容量空窗。任何
一步失败都会按相反顺序尝试恢复，包括：

- `swapoff`、文件分配、zram 重置、`mkswap` 或 `swapon` 失败；
- 操作完成后重新读取 `/proc/swaps` 失败；
- 内核报告的活动总容量与目标不匹配。

回滚也可能因内存压力、设备消失或外部服务同时修改 Swap 而失败；此时错误会同时
包含原始失败和恢复失败，管理员应立即运行 `rtop tune swap list` 和 `swapon --show`
确认实际状态。

## 权限、文件系统与重启

容量调整需要 root 权限，并依赖系统中的 `swapoff`、`mkswap` 和 `swapon`。创建补充
文件还需要 `/var/lib/rtop` 所在文件系统有足够空间且支持 Swap 文件。Btrfs 上新建
的受管文件会先设置 NOCOW；如果文件系统或内核仍拒绝 `swapon`，事务会回滚并报告
原始错误。

rtop 不写 `/etc/fstab`、systemd unit、systemd-zram-generator 或发行版配置：

- 受管补充 swapfile 在重启后仍可能留在磁盘，但不会由 rtop 自动启用；
- 发行版服务可能按自己的配置重建 zram；
- fstab、LVM 或加密配置可能重新启用原来的块设备；
- 直接调整普通 swapfile 会改变文件长度，这个存储层变化会保留，除非发行版服务
  或管理员重新创建该文件。

因此该命令调整的是当前活动 Swap 拓扑，不是持久化配置编辑器。需要永久配置时，
应使用发行版提供的 sysctl、fstab、zram 或存储管理工具。
