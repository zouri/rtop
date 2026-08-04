# rtop 实施方案

## 1. 项目定位

rtop 是面向 Rockchip Linux 平台的终端系统监控工具，目标体验类似 NVIDIA Jetson 平台上的 jtop。

项目采用 Go 实现，优先完整支持 RK3588，再通过动态能力探测扩展至 RK3566、RK3568、RK3576 等 SoC。工具默认只读、无需 root；当部分 sysfs 或 debugfs 指标不可访问时，应正常降级并明确展示原因。

## 2. 技术选型

- 开发语言：Go
- TUI：Bubble Tea
- 样式与布局：Lip Gloss
- 常用 TUI 组件：Bubbles
- CLI：Cobra
- 日志：标准库 log/slog
- Linux 系统调用：golang.org/x/sys
- 测试：标准库 testing 和 testdata fixtures
- 发布形式：关闭 CGO 的 ARM64 单文件二进制

选择 Go 的主要原因：

- 适合实现周期性并发采集任务。
- ARM64 交叉编译和单文件部署简单。
- 性能足以支撑秒级系统监控。
- 工程维护和社区参与门槛较低。
- 便于快速适配不同 Rockchip BSP 和驱动格式。

## 3. v0.1.0 范围

### 3.1 必须支持

- RK3588 平台和板卡信息识别。
- CPU 总体及单核心使用率。
- CPU 当前频率和大小核分组。
- 系统负载、内存和 Swap。
- thermal 与 hwmon 温度信息。
- Mali GPU 使用率和频率。
- RKNPU 各核心使用率和频率。
- DDR/DMC 当前频率。
- 进程 CPU 和内存排名。
- 实时 TUI。
- JSON 快照输出。
- doctor 环境和权限诊断。
- 无 root 场景下的正常降级。

### 3.2 暂不支持

- 修改 CPU、GPU 或 NPU governor 和频率。
- 修改风扇 PWM。
- 结束或管理进程。
- 每进程 GPU/NPU 使用率。
- Web 管理界面。
- 后台守护进程。
- VPU/RGA 精确使用率。

这些功能推迟到后续版本，以确保第一版保持只读、稳定和可移植。

## 4. 总体架构

~~~text
/proc、sysfs、debugfs、device-tree
                │
                ▼
             平台探测
                │
                ▼
 CPU/内存/进程/温度/加速器采集
                │
                ▼
          Monitor.Snapshot
                │
                ▼
       ┌────────┼────────┐
       ▼        ▼        ▼
      TUI      JSON     doctor
~~~

系统只保留以下层次：

1. SystemFS：统一访问 procfs、sysfs、debugfs 和 Device Tree，并支持测试根目录替换。
2. Platform Detector：识别 Linux、Rockchip、SoC、板卡、内核及驱动版本。
3. Collectors：按 CPU、内存、进程、温度和加速器五个当前业务域采集数据；GPU/NPU/DDR 共用一个加速器采集器。
4. Monitor：并行采集并直接组合不可变系统快照。
5. Output：命令层直接将统一快照提供给 TUI、JSON 和 doctor。

UI 不直接读取系统文件，Collector 也不包含任何终端展示逻辑。

## 5. 推荐目录结构

~~~text
rtop/
├── cmd/
│   └── rtop/
│       └── main.go
├── internal/
│   ├── app/                  # CLI、Monitor 和采样调度
│   ├── model/                # Snapshot 和指标模型
│   ├── sysfs/                # 可测试的文件访问封装
│   ├── platform/             # Linux/Rockchip 平台识别
│   ├── collector/
│   │   ├── accelerator/      # GPU/NPU/DDR 共用 devfreq 扫描
│   │   ├── cpu/
│   │   ├── memory/
│   │   ├── process/
│   │   └── thermal/
│   ├── ui/                   # Bubble Tea 状态与渲染
│   └── doctor/
├── testdata/
│   ├── generic-linux/
│   ├── rk3588-bsp510/
│   └── rk3588-kernel61/
├── docs/
├── go.mod
├── Makefile
└── README.md
~~~

初期采用单个 Go module。只有在 collector 需要被其他项目复用时，再考虑提取公共 package。

## 6. 核心接口设计

### 6.1 文件系统抽象

~~~go
type SystemFS interface {
    ReadFile(path string) ([]byte, error)
    ReadDir(path string) ([]os.DirEntry, error)
    Readlink(path string) (string, error)
    Glob(pattern string) ([]string, error)
}
~~~

生产环境实现读取真实根文件系统。测试环境将根路径映射到 testdata，从而在没有 Rockchip 硬件的开发机上运行完整采集测试。

### 6.2 直接依赖具体采集器

~~~go
type Monitor struct {
    cpu         *cpu.Collector
    accelerator *accelerator.Collector
    memory      *memory.Collector
    process     *process.Collector
    thermal     *thermal.Collector
}
~~~

当前没有第二套采集器实现，因此不定义统一 Collector 接口。Monitor 直接依赖具体类型；加速器节点只在启动时扫描一次，刷新只采样已有节点。

### 6.3 快照模型

~~~go
type Snapshot struct {
    Timestamp    time.Time
    System       SystemInfo
    CPU          CPUInfo
    Memory       MemoryInfo
    Thermals     []ThermalZone
    Accelerators []Accelerator
    Processes    []ProcessInfo
    Warnings     []Warning
}
~~~

### 6.4 指标状态

~~~go
type MetricStatus string

const (
    Available        MetricStatus = "available"
    Unsupported      MetricStatus = "unsupported"
    PermissionDenied MetricStatus = "permission_denied"
    Unavailable      MetricStatus = "unavailable"
    Stale            MetricStatus = "stale"
)
~~~

每个指标需要包含值、单位、时间、来源和状态。读取失败不能展示为 0%，否则会将“不可读取”错误地表达为“设备空闲”。

## 7. 数据源和探测策略

| 指标 | 主要数据源 | 说明 |
|---|---|---|
| CPU 使用率 | /proc/stat | 通过两次采样差值计算 |
| CPU 频率 | /sys/devices/system/cpu/*/cpufreq | 支持大小核分组 |
| 内存与 Swap | /proc/meminfo | Linux 通用 |
| 系统负载 | /proc/loadavg | Linux 通用 |
| 进程 | /proc/PID | CPU、内存和线程数 |
| 温度 | /sys/class/thermal、hwmon | 根据 type/name 识别用途 |
| GPU | devfreq、Mali sysfs/debugfs | 优先使用稳定的 sysfs 接口 |
| NPU | RKNPU debugfs、NPU devfreq | 兼容不同驱动输出格式 |
| DDR | DMC devfreq | 第一版主要展示当前频率 |
| 风扇 | hwmon、pwm-fan | 取决于板卡 Device Tree |
| 功耗 | hwmon、板载传感器 | 作为可选能力 |

Rockchip 设备探测不能依赖固定物理地址，推荐按以下优先级识别：

1. Device Tree compatible。
2. sysfs 设备名称、name 和 type。
3. devfreq 设备指向的真实设备和 OF 节点。
4. debugfs 节点特征。
5. SoC 和特定 BSP 的兼容规则。

加速器采集器按固定顺序尝试少量已知节点。当前不实现 Provider、策略切换或运行时重探测；新增真实兼容需求时再加。

## 8. 命令行设计

~~~text
rtop                         启动 TUI
rtop --interval 1s           设置刷新周期
rtop info                    输出板卡、SoC、内核和驱动信息
rtop snapshot --json         输出一次 JSON 快照
rtop snapshot --watch        持续输出机器可读快照
rtop doctor                  检查节点、驱动、权限和数据源
~~~

doctor 至少检查：

- SoC 和板卡识别结果。
- 当前内核及 Rockchip BSP 信息。
- Mali GPU 和 RKNPU 驱动是否加载。
- debugfs 是否挂载。
- 所需 sysfs/debugfs 节点是否存在。
- 当前用户是否有读取权限。
- 每一项指标最终选择的数据源。

doctor 只给出诊断信息和修复建议，不自动 mount、chmod 或修改系统配置。

## 9. TUI 设计

第一版包含以下页面：

- Overview：CPU、内存、GPU、NPU、温度和告警摘要。
- Engines：GPU、NPU、DDR 的负载、频率和历史曲线。
- Processes：进程 CPU、内存和线程数排名。
- Thermal：温度区域、风扇、功耗和降频状态。
- System：SoC、板卡、内核、驱动和能力信息。
- Help：快捷键说明。

建议快捷键：

- 1～5：切换页面。
- 上下方向键：选择进程或列表项。
- c：按 CPU 排序。
- m：按内存排序。
- r：立即采样一次。
- ?：帮助。
- q 或 Ctrl+C：退出。

UI 必须支持窄终端降级布局，不能假设固定终端尺寸。

## 10. 采样与并发模型

推荐采样周期：

- CPU、内存、GPU、NPU、DDR、温度：1 秒。
- 进程列表：2 秒。
- 风扇和功耗：1～2 秒。
- 系统、板卡和驱动信息：启动时读取一次。
- 历史曲线：保存最近 60～120 个点。

实现原则：

- 使用根 context 控制整个应用生命周期。
- 使用固定调度器，而不是每次刷新无限创建 goroutine。
- 限制并行采集数量。
- Snapshot 通过容量为 1 的通道发送给 UI。
- UI 繁忙时覆盖或丢弃旧快照，避免数据无限积压。
- 退出时等待采集任务停止，不留下后台 goroutine。

## 11. 分阶段实施计划

### 阶段一：工程骨架和通用 Linux 指标

预计时间：1 周。

实施内容：

- 初始化 Go module、CLI、日志和构建脚本。
- 实现 SystemFS 和统一数据模型。
- 实现 /proc/stat、/proc/meminfo、/proc/loadavg 解析。
- 实现 CPU cpufreq、thermal 和 hwmon 扫描。
- 实现 info 和 JSON snapshot 命令。
- 为所有解析器建立 fixture 测试。

验收标准：

- 普通 Linux ARM64 和 x86 主机均可运行。
- 不需要 root。
- CPU 使用率基于采样差值，而不是瞬时文件值。
- 单项指标读取失败不影响其他采集器。

### 阶段二：基础 TUI 和进程监控

预计时间：1 周。

实施内容：

- 建立 Bubble Tea 应用模型。
- 实现 Overview、Processes、Thermal、System 页面。
- 实现采样调度、快照通道和历史缓存。
- 实现 /proc/PID 进程采集和排序。
- 完成终端缩放、退出和错误展示。

验收标准：

- 终端缩放不会崩溃或破坏布局。
- 采集超时不会冻结界面。
- q 和 Ctrl+C 能安全退出。
- 连续运行一小时无明显内存增长或 goroutine 泄漏。

### 阶段三：RK3588 GPU、NPU 和 DDR

预计时间：1～2 周。

实施内容：

- 实现 Device Tree 和 Rockchip SoC 识别。
- 扫描并分类 devfreq 设备。
- 用一个加速器采集器读取 Mali GPU、RKNPU 和 DMC/DDR。
- 解析 RKNPU 多核心负载。
- 兼容常见 5.10 和 6.1 BSP 格式。
- 增加 Engines 页面。

验收标准：

- RK3588 BSP 5.10 可展示 GPU、NPU、DDR 可用指标。
- 常见 RK3588 6.1 BSP 可正常识别或明确降级。
- NPU 多核心指标分别展示。
- 无 debugfs 权限时，UI 展示 PermissionDenied 而不是 0%。
- GPU/NPU 不可用时，CPU、内存等通用功能仍正常工作。

### 阶段四：诊断、兼容性和发布

预计时间：1 周。

实施内容：

- 实现 doctor 完整诊断报告。
- 增加真实板卡 testdata。
- 完成 RK3588 真机长时间运行测试。
- 增加 RK3566 或 RK3568 的基础验证。
- 建立 ARM64 交叉构建和发布流程。
- 编写 README、支持矩阵和问题反馈模板。

验收标准：

- 可以生成关闭 CGO 的 Linux ARM64 单文件二进制。
- 发布 tar.gz、deb 和 SHA256 校验文件。
- 支持矩阵明确区分完整支持、部分支持和未验证。
- doctor 输出足以协助用户提交有效兼容性问题。

## 12. 测试方案

### 12.1 解析器单元测试

每一种 sysfs/debugfs 输出格式都保存输入样例，并验证：

- 正常值。
- 空文件。
- 格式变化。
- 数字溢出。
- 权限错误。
- 节点临时消失。

### 12.2 虚拟系统集成测试

将真实设备上的相关 procfs、sysfs、debugfs 和 Device Tree 节点复制并脱敏到 testdata，使用替换根目录运行完整探测和采集流程。

推荐首批 fixture：

- generic-linux。
- rk3588-bsp510。
- rk3588-kernel61。
- rk3568。

### 12.3 真机测试

至少覆盖：

- RK3588 + BSP 5.10。
- RK3588 + 6.1 内核。
- RK3566 或 RK3568。
- 非 Rockchip Linux，验证通用功能和正常降级。
- 普通用户与 root 两种权限环境。

## 13. 构建和发布

ARM64 构建命令：

~~~bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" ./cmd/rtop
~~~

建议发布产物：

- rtop-linux-arm64.tar.gz。
- rtop-linux-armv7.tar.gz，可在后续提供。
- rtop_VERSION_arm64.deb。
- SHA256SUMS。
- 支持矩阵和已知限制。

版本号建议：

- v0.1.0：RK3588 MVP。
- v0.2.0：RK3566/RK3568/RK3576 扩展。
- v0.3.0：功耗、风扇、VPU/RGA 和导出能力增强。
- v1.0.0：接口稳定且主要平台经过真机验证。

## 14. 已知风险及处理策略

### 14.1 BSP 接口不统一

风险：不同厂商内核中的节点路径和输出格式不同。

策略：启动时按名称动态扫描一次，并用 fixture 回归测试覆盖已知格式；不为尚未出现的驱动变体预设 Provider 层。

### 14.2 debugfs 权限限制

风险：普通用户可能无法读取 NPU 或 GPU 负载。

策略：默认无 root 运行；展示权限状态；doctor 给出建议，但不自动修改系统权限。

### 14.3 部分指标本身不可获得

风险：某些驱动不暴露 VPU/RGA 使用率或每进程 GPU/NPU 使用率。

策略：支持矩阵明确标注，不通过估算数据冒充真实硬件指标。

### 14.4 高频读取影响系统

风险：频繁扫描 /proc 和 debugfs 可能增加低端板卡负载。

策略：分类采样、缓存探测结果、限制并发、进程列表降低刷新频率。

## 15. 完成定义

v0.1.0 达到以下条件后可以发布：

- RK3588 上 CPU、内存、温度、GPU、NPU 和 DDR 主要指标可用。
- TUI、info、snapshot 和 doctor 命令完整可用。
- 普通用户环境能运行并正确表达权限限制。
- 关键解析器都有 fixture 测试。
- RK3588 5.10 和 6.1 环境经过真机验证。
- 连续运行测试没有明显泄漏或界面冻结。
- ARM64 发布包可以在没有 Go 环境的设备上直接运行。

完成 v0.1.0 后，再按 RK3566/RK3568、RK3576、功耗风扇、VPU/RGA、外部指标导出的顺序扩展。
