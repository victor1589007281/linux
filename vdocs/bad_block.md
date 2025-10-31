# Linux 坏块检测详解

## 1. 坏块概述

### 1.1 什么是坏块

坏块（Bad Block）是指磁盘或存储设备上无法正常读写的扇区或块。坏块的存在会导致数据丢失、系统不稳定等严重问题。

```mermaid
graph LR
    subgraph 正常块
        A1[**Block 0**<br/>正常] --> A2[**Block 1**<br/>正常]
        A2 --> A3[**Block 2**<br/>正常]
    end
    
    subgraph 坏块出现
        B1[**Block 3**<br/>正常] --> B2[**Block 4**<br/>⚠️ 坏块]
        B2 --> B3[**Block 5**<br/>正常]
    end
    
    subgraph 处理方式
        C1[**重定向**<br/>Remap]
        C2[**标记**<br/>Mark]
        C3[**隔离**<br/>Isolate]
    end
    
    style A1 fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
    style A2 fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
    style A3 fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
    style B2 fill:#ffebee,stroke:#c62828,stroke-width:3px
    style C1 fill:#e3f2fd,stroke:#1565c0,stroke-width:2px
```

### 1.2 坏块的类型

| **类型** | **说明** | **影响** | **可恢复性** |
|---------|---------|---------|------------|
| **物理坏块** | 硬件损坏 | 永久性损坏 | 不可恢复 |
| **逻辑坏块** | 软件或固件问题 | 可能修复 | 可恢复 |
| **暂时性坏块** | 临时读写错误 | 短暂影响 | 可恢复 |
| **潜在坏块** | 将要失效 | 即将损坏 | 需监控 |

### 1.3 坏块产生原因

```mermaid
mindmap
  root((坏块原因))
    **硬件因素**
      磁头碰撞
      磁性衰减
      电机故障
      温度过高
    **使用因素**
      频繁读写
      突然断电
      震动冲击
      老化磨损
    **制造因素**
      制造缺陷
      质量问题
      介质不良
    **环境因素**
      静电干扰
      磁场影响
      灰尘污染
      湿度温度
```

## 2. Linux 坏块管理架构

### 2.1 坏块管理层次

```mermaid
graph TB
    subgraph 应用层
        A[**badblocks命令**]
        B[**smartctl工具**]
        C[**文件系统工具**<br/>e2fsck/xfs_repair]
    end
    
    subgraph 内核层
        D[**块设备层**<br/>block layer]
        E[**坏块管理**<br/>badblocks.c]
        F[**MD/RAID**<br/>md.c]
        G[**NVDIMM**<br/>badrange.c]
    end
    
    subgraph 驱动层
        H[**SCSI/ATA驱动**]
        I[**NVMe驱动**]
        J[**存储控制器**]
    end
    
    subgraph 硬件层
        K[**磁盘固件**]
        L[**SMART**]
        M[**G-List/P-List**]
    end
    
    A --> D
    B --> H
    C --> D
    D --> E
    D --> F
    D --> G
    E --> H
    F --> H
    G --> I
    H --> K
    I --> K
    K --> L
    K --> M
    
    style E fill:#fff3e0,stroke:#e65100,stroke-width:3px
    style K fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
    style L fill:#e3f2fd,stroke:#1565c0,stroke-width:2px
```

### 2.2 内核坏块数据结构

源码分析 `include/linux/badblocks.h`:

```c
// 坏块表结构
struct badblocks {
    struct device *dev;         // 设备指针
    int count;                  // 坏块数量
    int unacked_exist;          // 是否存在未确认的坏块
    int shift;                  // 扇区到块的位移
                               // 负值表示坏块功能禁用
    u64 *page;                 // 坏块列表（一页内存）
    int changed;               // 是否有变化
    seqlock_t lock;            // 顺序锁保护
    sector_t sector;           // 起始扇区
    sector_t size;             // 大小（扇区数）
};

// 坏块上下文
struct badblocks_context {
    sector_t start;            // 起始扇区
    sector_t len;              // 长度
    int ack;                   // 是否已确认
};

// 坏块条目格式 (64位)
// Bit 63:    已确认标志
// Bit 62-9:  扇区号 (54位，支持8EB)
// Bit 8-0:   长度-1 (9位，最大512个扇区)

#define BB_LEN_MASK      0x00000000000001FFULL  // 长度掩码
#define BB_OFFSET_MASK   0x7FFFFFFFFFFFFE00ULL  // 偏移掩码
#define BB_ACK_MASK      0x8000000000000000ULL  // 确认掩码
#define BB_MAX_LEN       512                    // 最大长度

// 坏块表最大条目数（一页/8字节）
#define MAX_BADBLOCKS    (PAGE_SIZE/8)
```

### 2.3 坏块条目编码

```mermaid
graph LR
    subgraph 64位坏块条目
        A[**Bit 63**<br/>ACK标志]
        B[**Bit 62-9**<br/>扇区号54位]
        C[**Bit 8-0**<br/>长度9位]
    end
    
    A --> D{**已确认?**}
    D -->|1| E[**已确认坏块**]
    D -->|0| F[**未确认坏块**]
    
    B --> G[**起始扇区**<br/>0 - 8EB]
    C --> H[**坏块长度**<br/>1 - 512扇区]
    
    style A fill:#e3f2fd,stroke:#1565c0,stroke-width:2px
    style B fill:#fff3e0,stroke:#e65100,stroke-width:2px
    style C fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
    style E fill:#ffebee,stroke:#c62828,stroke-width:2px
```

## 3. 坏块检测工具

### 3.1 badblocks - 通用坏块扫描工具

```bash
# 基本用法 - 非破坏性只读测试
badblocks /dev/sda

# 指定块大小（默认1024字节）
badblocks -b 4096 /dev/sda

# 非破坏性读写测试（推荐）
badblocks -n /dev/sda

# 破坏性写入测试（会丢失数据！）
badblocks -w /dev/sda

# 指定测试模式
# -b: 块大小
# -c: 一次测试的块数
# -p: 测试次数（遍数）
# -v: 详细输出
badblocks -b 4096 -c 65536 -p 4 -v /dev/sda

# 输出到文件
badblocks -o /root/badblocks.txt /dev/sda

# 显示进度
badblocks -s -v /dev/sda

# 测试指定范围（从第100000块开始，测试10000块）
badblocks -s -v /dev/sda 110000 100000
```

#### 3.1.1 badblocks 测试模式

| **模式** | **选项** | **特点** | **数据安全** |
|---------|---------|---------|------------|
| **只读测试** | 默认 | 只读取，不写入 | 安全 |
| **非破坏性读写** | -n | 读-写-读对比 | 安全 |
| **破坏性写入** | -w | 写入测试模式 | **危险** |

#### 3.1.2 badblocks 测试流程

```mermaid
sequenceDiagram
    participant U as **用户**
    participant B as **badblocks工具**
    participant K as **内核**
    participant D as **磁盘**
    
    U->>B: 启动扫描<br/>badblocks /dev/sda
    
    Note over B: **选择测试模式**
    B->>B: 非破坏性读写(-n)
    
    loop 遍历所有块
        B->>D: 读取原始数据
        D->>B: 返回数据
        
        B->>D: 写入测试模式
        D->>B: 写入完成
        
        B->>D: 读取验证
        D->>B: 返回数据
        
        B->>B: 对比数据
        
        alt 数据一致
            B->>D: 写回原始数据
            Note right of B: ✓ 块正常
        else 数据不一致
            Note right of B: ✗ 发现坏块
            B->>B: 记录坏块位置
            B->>D: 尝试写回原始数据
        end
    end
    
    B->>U: 输出坏块列表
```

### 3.2 smartctl - SMART监控工具

SMART (Self-Monitoring, Analysis and Reporting Technology) 是硬盘内置的自监控技术。

```bash
# 安装smartmontools
apt-get install smartmontools   # Debian/Ubuntu
yum install smartmontools        # CentOS/RHEL

# 查看设备SMART信息
smartctl -i /dev/sda

# 查看SMART健康状态
smartctl -H /dev/sda

# 查看所有SMART属性
smartctl -A /dev/sda

# 查看错误日志
smartctl -l error /dev/sda

# 查看自检日志
smartctl -l selftest /dev/sda

# 执行短自检
smartctl -t short /dev/sda

# 执行长自检
smartctl -t long /dev/sda

# 执行conveyance自检（运输测试）
smartctl -t conveyance /dev/sda

# 查看坏块数量
smartctl -A /dev/sda | grep -i 'Reallocated\|Pending\|Uncorrectable'
```

#### 3.2.1 关键SMART属性

| **ID** | **属性名** | **说明** | **正常值** |
|--------|-----------|---------|-----------|
| **5** | Reallocated_Sector_Ct | 重新映射扇区数 | 0 |
| **187** | Reported_Uncorrect | 不可修正的错误 | 0 |
| **188** | Command_Timeout | 命令超时 | 0 |
| **196** | Reallocated_Event_Count | 重映射事件计数 | 0 |
| **197** | Current_Pending_Sector | 当前待处理扇区 | 0 |
| **198** | Offline_Uncorrectable | 离线不可修正 | 0 |
| **199** | UDMA_CRC_Error_Count | UDMA CRC错误 | 0 |

#### 3.2.2 SMART监控流程

```mermaid
graph TD
    A[**磁盘运行**] --> B{**SMART监控**}
    
    B --> C[**实时监测**]
    C --> C1[**读写操作**]
    C --> C2[**温度监控**]
    C --> C3[**错误计数**]
    
    B --> D[**定期自检**]
    D --> D1[**短自检**<br/>1-2分钟]
    D --> D2[**长自检**<br/>数小时]
    
    B --> E[**离线扫描**]
    E --> E1[**后台扫描**]
    E --> E2[**坏块检测**]
    
    C1 --> F{**发现异常?**}
    C2 --> F
    C3 --> F
    D1 --> F
    D2 --> F
    E2 --> F
    
    F -->|是| G[**记录到SMART日志**]
    F -->|否| A
    
    G --> H[**增加错误计数**]
    H --> I{**严重?**}
    
    I -->|是| J[**重新映射扇区**<br/>使用备用扇区]
    I -->|否| K[**标记待处理**]
    
    J --> L[**更新G-List**]
    K --> L
    L --> M[**触发告警**]
    
    style F fill:#fff3e0,stroke:#e65100,stroke-width:2px
    style J fill:#ffebee,stroke:#c62828,stroke-width:2px
    style M fill:#e3f2fd,stroke:#1565c0,stroke-width:2px
```

### 3.3 e2fsck/fsck - 文件系统检查工具

```bash
# 检查并修复ext4文件系统
e2fsck -f -c /dev/sda1

# -f: 强制检查，即使文件系统看起来正常
# -c: 使用badblocks检查坏块
# -y: 自动回答yes

# 使用已知坏块列表
e2fsck -l badblocks.txt /dev/sda1

# 更新坏块列表（非破坏性）
e2fsck -c /dev/sda1

# 更新坏块列表（破坏性，读写测试）
e2fsck -cc /dev/sda1
```

### 3.4 dd - 手动坏块测试

```bash
# 读取测试 - 检测读取错误
dd if=/dev/sda of=/dev/null bs=4k conv=noerror,sync status=progress

# 写入测试（危险！会清空数据）
dd if=/dev/zero of=/dev/sda bs=4k conv=noerror,sync status=progress

# 跳过指定块
dd if=/dev/sda of=/dev/null bs=4k skip=1000 count=1000
```

### 3.5 hdparm - 硬盘参数工具

```bash
# 查看硬盘信息
hdparm -I /dev/sda

# 测试读取速度（可能暴露坏块）
hdparm -t /dev/sda
hdparm -T /dev/sda

# 查看SMART状态
hdparm -H /dev/sda
```

## 4. 坏块检测原理

### 4.1 内核坏块检查函数

源码分析 `block/badblocks.c`:

```c
/**
 * badblocks_check() - 检查指定范围是否有坏块
 * @bb: badblocks结构
 * @s: 起始扇区
 * @sectors: 扇区数量
 * @first_bad: 返回第一个坏块位置
 * @bad_sectors: 返回坏块数量
 * 
 * 返回值:
 *  0:  范围内没有坏块
 *  1:  有已确认的坏块
 * -1:  有未确认的坏块
 */
int badblocks_check(struct badblocks *bb, sector_t s, int sectors,
                    sector_t *first_bad, int *bad_sectors)
{
    int unacked_badblocks, acked_badblocks;
    struct badblocks_context bad;
    u64 *p;
    
    // 使用seqlock保护并发访问
retry:
    seq = read_seqbegin(&bb->lock);
    
    p = bb->page;
    unacked_badblocks = 0;
    acked_badblocks = 0;
    
re_check:
    bad.start = s;
    bad.len = sectors;
    
    // 如果坏块表为空，直接返回
    if (badblocks_empty(bb)) {
        len = sectors;
        goto update_sectors;
    }
    
    // 二分查找第一个可能重叠的坏块范围
    prev = prev_badblocks(bb, &bad, hint);
    
    // 检查是否与前一个坏块重叠
    if ((prev >= 0) && overlap_front(bb, prev, &bad)) {
        // 检查是否已确认
        if (BB_ACK(p[prev]))
            acked_badblocks++;
        else
            unacked_badblocks++;
            
        // 计算重叠长度
        if (BB_END(p[prev]) >= (s + sectors))
            len = sectors;
        else
            len = BB_END(p[prev]) - s;
            
        // 记录第一个坏块
        if (set == 0) {
            *first_bad = BB_OFFSET(p[prev]);
            *bad_sectors = BB_LEN(p[prev]);
            set = 1;
        }
        goto update_sectors;
    }
    
update_sectors:
    s += len;
    sectors -= len;
    
    if (sectors > 0)
        goto re_check;
    
    // 检查seqlock是否有变化
    if (read_seqretry(&bb->lock, seq))
        goto retry;
    
    // 返回结果
    if (unacked_badblocks > 0)
        return -1;
    else if (acked_badblocks > 0)
        return 1;
    else
        return 0;
}
```

### 4.2 坏块设置函数

```c
/**
 * badblocks_set() - 添加坏块到表中
 * @bb: badblocks结构
 * @s: 起始扇区
 * @sectors: 扇区数量
 * @acknowledged: 是否已确认
 * 
 * 返回值:
 *  0: 成功
 *  1: 失败（表已满）
 */
int badblocks_set(struct badblocks *bb, sector_t s, int sectors,
                  int acknowledged)
{
    u64 *p;
    int rv = 0;
    
    // 如果坏块功能禁用
    if (bb->shift < 0)
        return 1;
    
    // 对齐到块边界
    if (bb->shift) {
        sector_t next = s + sectors;
        s = rounddown(s, bb->shift);
        next = roundup(next, bb->shift);
        sectors = next - s;
    }
    
    // 使用写锁保护
    write_seqlock_irqsave(&bb->lock, flags);
    
    p = bb->page;
    
re_insert:
    bad.start = s;
    bad.len = sectors;
    bad.ack = acknowledged;
    
    // 如果表为空，直接插入
    if (badblocks_empty(bb)) {
        len = insert_at(bb, 0, &bad);
        bb->count++;
        goto update_sectors;
    }
    
    // 查找插入位置
    prev = prev_badblocks(bb, &bad, hint);
    
    // 尝试合并相邻的坏块
    if (can_merge_front(bb, prev, &bad)) {
        len = front_merge(bb, prev, &bad);
        goto update_sectors;
    }
    
    // 如果表已满，无法插入
    if (badblocks_full(bb)) {
        rv = 1;
        goto out;
    }
    
    // 插入新条目
    len = insert_at(bb, prev + 1, &bad);
    bb->count++;
    
update_sectors:
    s += len;
    sectors -= len;
    
    if (sectors > 0)
        goto re_insert;
    
    // 更新状态
    set_changed(bb);
    
out:
    write_sequnlock_irqrestore(&bb->lock, flags);
    return rv;
}
```

### 4.3 坏块检测流程图

```mermaid
graph TD
    A[**开始扫描**] --> B[**初始化坏块表**]
    B --> C[**设置扫描参数**<br/>起始地址、范围]
    
    C --> D{**选择测试模式**}
    D -->|只读| E[**读取扇区**]
    D -->|读写| F[**读-写-读**]
    D -->|写入| G[**写入模式**]
    
    E --> H{**读取成功?**}
    F --> I[**保存原始数据**]
    I --> J[**写入测试模式**]
    J --> K[**读取验证**]
    K --> L{**数据一致?**}
    
    H -->|成功| M[**标记为好块**]
    H -->|失败| N[**标记为坏块**]
    
    L -->|一致| O[**恢复原始数据**]
    L -->|不一致| N
    O --> M
    
    M --> P{**扫描完成?**}
    N --> Q[**调用badblocks_set**]
    Q --> R[**更新坏块表**]
    R --> P
    
    P -->|否| C
    P -->|是| S[**生成报告**]
    S --> T[**输出坏块列表**]
    
    style N fill:#ffebee,stroke:#c62828,stroke-width:2px
    style M fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
    style Q fill:#fff3e0,stroke:#e65100,stroke-width:2px
```

### 4.4 读写测试模式详解

```mermaid
sequenceDiagram
    participant T as **测试程序**
    participant B as **块设备**
    participant D as **磁盘**
    
    Note over T,D: **模式0: 0x00测试**
    T->>B: 读取原始数据
    B->>D: READ
    D->>B: 原始数据
    B->>T: 保存原始数据
    
    T->>B: 写入0x00模式
    B->>D: WRITE 0x00
    D->>B: 完成
    
    T->>B: 读取验证
    B->>D: READ
    D->>B: 返回数据
    B->>T: 验证数据
    
    alt 数据正确(0x00)
        T->>B: 写回原始数据
        Note right of T: ✓ 通过
    else 数据错误
        T->>T: 记录坏块
        Note right of T: ✗ 失败
    end
    
    Note over T,D: **模式1: 0xFF测试**
    Note over T,D: (重复上述流程，使用0xFF)
    
    Note over T,D: **模式2: 0xAA测试**
    Note over T,D: (重复上述流程，使用0xAA)
    
    Note over T,D: **模式3: 0x55测试**
    Note over T,D: (重复上述流程，使用0x55)
```

## 5. 坏块处理机制

### 5.1 磁盘内部重映射

```mermaid
graph TB
    subgraph 正常访问
        A1[**应用请求**<br/>扇区1000]
        A2[**直接访问**]
        A3[**扇区1000**<br/>物理位置]
    end
    
    subgraph 坏块发现
        B1[**读写错误**]
        B2[**固件检测**]
        B3[**标记坏块**]
    end
    
    subgraph 重映射过程
        C1[**分配备用扇区**<br/>从保留区]
        C2[**更新G-List**<br/>增长缺陷列表]
        C3[**建立映射**<br/>1000 → 备用扇区]
    end
    
    subgraph 重映射后访问
        D1[**应用请求**<br/>扇区1000]
        D2[**固件转换**]
        D3[**备用扇区**<br/>新物理位置]
    end
    
    A1 --> A2 --> A3
    A3 --> B1 --> B2 --> B3
    B3 --> C1 --> C2 --> C3
    C3 --> D1 --> D2 --> D3
    
    style B1 fill:#ffebee,stroke:#c62828,stroke-width:2px
    style C2 fill:#fff3e0,stroke:#e65100,stroke-width:2px
    style D3 fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
```

### 5.2 G-List 和 P-List

| **列表** | **名称** | **类型** | **何时生成** |
|---------|---------|---------|------------|
| **P-List** | Primary Defect List | 出厂缺陷 | 工厂测试 |
| **G-List** | Grown Defect List | 增长缺陷 | 使用过程 |

```c
// 缺陷列表的概念（伪代码）
struct defect_list {
    u32 cylinder;      // 柱面号
    u32 head;          // 磁头号
    u32 sector;        // 扇区号
    u32 spare_sector;  // 备用扇区号
};

struct disk_firmware {
    struct defect_list p_list[1000];  // P-List (只读)
    struct defect_list g_list[1000];  // G-List (可写)
    int g_list_count;                 // G-List条目数
    struct spare_area spare_sectors;  // 备用扇区池
};
```

### 5.3 坏块处理策略

```mermaid
graph TD
    A[**检测到坏块**] --> B{**可重映射?**}
    
    B -->|是| C[**检查备用池**]
    B -->|否| D[**标记永久坏块**]
    
    C --> E{**有备用扇区?**}
    E -->|是| F[**执行重映射**]
    E -->|否| D
    
    F --> G[**更新G-List**]
    G --> H[**数据迁移**]
    H --> I[**更新LBA映射**]
    I --> J[**标记旧扇区**]
    
    D --> K[**更新坏块表**]
    K --> L[**通知操作系统**]
    L --> M[**文件系统处理**]
    
    M --> N{**文件系统类型**}
    N -->|ext4| O[**标记坏块inode**]
    N -->|xfs| P[**重新分配**]
    N -->|btrfs| Q[**CoW绕过**]
    
    J --> R[**完成**]
    O --> R
    P --> R
    Q --> R
    
    style A fill:#ffebee,stroke:#c62828,stroke-width:2px
    style F fill:#e3f2fd,stroke:#1565c0,stroke-width:2px
    style R fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
```

## 6. 不同设备的坏块处理

### 6.1 HDD（机械硬盘）

```mermaid
graph LR
    subgraph HDD坏块特点
        A1[**磁性介质**<br/>易损坏]
        A2[**机械结构**<br/>磁头碰撞]
        A3[**备用扇区**<br/>约2-5%]
    end
    
    subgraph 处理方式
        B1[**硬件重映射**<br/>固件自动]
        B2[**软件标记**<br/>badblocks]
        B3[**文件系统预留**<br/>坏块inode]
    end
    
    subgraph 监控方法
        C1[**SMART监控**]
        C2[**定期自检**]
        C3[**日志分析**]
    end
    
    A1 --> B1
    A2 --> B2
    A3 --> B3
    
    B1 --> C1
    B2 --> C2
    B3 --> C3
    
    style A1 fill:#ffebee,stroke:#c62828,stroke-width:2px
    style B1 fill:#e3f2fd,stroke:#1565c0,stroke-width:2px
```

### 6.2 SSD（固态硬盘）

```mermaid
graph TB
    subgraph SSD坏块管理
        A[**FTL层**<br/>Flash Translation Layer]
        B[**坏块表**<br/>Bad Block Table]
        C[**预留空间**<br/>Over Provisioning]
    end
    
    subgraph 磨损均衡
        D[**写入次数统计**]
        E[**动态映射**]
        F[**垃圾回收**]
    end
    
    subgraph 检测机制
        G[**ECC校验**]
        H[**读取重试**]
        I[**RAISE错误**]
    end
    
    A --> B
    A --> C
    B --> D
    B --> E
    C --> F
    
    D --> G
    E --> H
    F --> I
    
    style B fill:#fff3e0,stroke:#e65100,stroke-width:2px
    style C fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
```

#### SSD特点

| **特性** | **说明** | **影响** |
|---------|---------|---------|
| **磨损均衡** | 分散写入到不同块 | 延长寿命 |
| **Over-Provisioning** | 预留7-30%空间 | 坏块替换 |
| **TRIM支持** | 通知已删除块 | 提升性能 |
| **ECC纠错** | 硬件纠错能力 | 减少坏块 |

### 6.3 NVMe SSD

```bash
# 查看NVMe SMART信息
nvme smart-log /dev/nvme0

# 查看错误日志
nvme error-log /dev/nvme0

# 查看坏块信息
nvme list-ns /dev/nvme0

# 格式化并标记坏块
nvme format /dev/nvme0 --ses=1

# 查看健康状况
nvme get-feature /dev/nvme0 -f 0x02
```

### 6.4 RAID磁盘阵列

```mermaid
graph TD
    A[**RAID阵列**] --> B{**坏块检测**}
    
    B --> C[**单盘坏块**]
    B --> D[**多盘坏块**]
    
    C --> E[**MD层处理**]
    E --> F[**标记坏块**]
    F --> G[**从其他盘重建**]
    
    D --> H{**RAID级别**}
    H -->|RAID1/10| I[**从镜像读取**]
    H -->|RAID5/6| J[**从校验重建**]
    
    I --> K[**重写坏块**]
    J --> K
    
    K --> L{**重写成功?**}
    L -->|是| M[**继续运行**]
    L -->|否| N[**标记永久坏块**]
    
    N --> O[**考虑更换磁盘**]
    
    style C fill:#fff3e0,stroke:#e65100,stroke-width:2px
    style D fill:#ffebee,stroke:#c62828,stroke-width:2px
    style M fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
```

## 7. 坏块检测最佳实践

### 7.1 定期检测策略

```bash
#!/bin/bash
# 坏块定期检测脚本

DEVICE="/dev/sda"
LOG_DIR="/var/log/badblocks"
EMAIL="admin@example.com"

# 创建日志目录
mkdir -p "$LOG_DIR"

# 获取当前日期
DATE=$(date +%Y%m%d_%H%M%S)
LOG_FILE="$LOG_DIR/badblocks_$DATE.log"

# 执行坏块扫描（非破坏性）
echo "Starting badblocks scan on $DEVICE at $(date)" | tee -a "$LOG_FILE"
badblocks -n -s -v -o "$LOG_DIR/badblocks_$DATE.txt" "$DEVICE" 2>&1 | tee -a "$LOG_FILE"

# 检查是否发现坏块
if [ -s "$LOG_DIR/badblocks_$DATE.txt" ]; then
    echo "WARNING: Bad blocks detected!" | tee -a "$LOG_FILE"
    
    # 发送邮件告警
    mail -s "Bad Blocks Detected on $DEVICE" "$EMAIL" < "$LOG_DIR/badblocks_$DATE.txt"
    
    # 记录到系统日志
    logger -p user.crit "Bad blocks detected on $DEVICE"
else
    echo "No bad blocks detected." | tee -a "$LOG_FILE"
fi

# 清理旧日志（保留30天）
find "$LOG_DIR" -name "badblocks_*.log" -mtime +30 -delete
```

### 7.2 SMART监控脚本

```bash
#!/bin/bash
# SMART健康监控脚本

DEVICES="/dev/sda /dev/sdb /dev/sdc"
THRESHOLD_REALLOCATED=10
THRESHOLD_PENDING=5

for DEVICE in $DEVICES; do
    echo "Checking $DEVICE..."
    
    # 检查SMART健康状态
    HEALTH=$(smartctl -H "$DEVICE" | grep "PASSED\|FAILED")
    
    if echo "$HEALTH" | grep -q "FAILED"; then
        echo "CRITICAL: SMART health check FAILED on $DEVICE"
        logger -p user.crit "SMART health check FAILED on $DEVICE"
    fi
    
    # 检查重新映射扇区数
    REALLOCATED=$(smartctl -A "$DEVICE" | grep "Reallocated_Sector" | awk '{print $10}')
    if [ "$REALLOCATED" -gt "$THRESHOLD_REALLOCATED" ]; then
        echo "WARNING: $DEVICE has $REALLOCATED reallocated sectors"
    fi
    
    # 检查待处理扇区数
    PENDING=$(smartctl -A "$DEVICE" | grep "Current_Pending_Sector" | awk '{print $10}')
    if [ "$PENDING" -gt "$THRESHOLD_PENDING" ]; then
        echo "WARNING: $DEVICE has $PENDING pending sectors"
    fi
    
    # 检查不可修正的错误
    UNCORRECTABLE=$(smartctl -A "$DEVICE" | grep "Offline_Uncorrectable" | awk '{print $10}')
    if [ "$UNCORRECTABLE" -gt 0 ]; then
        echo "CRITICAL: $DEVICE has $UNCORRECTABLE uncorrectable sectors"
    fi
done
```

### 7.3 cron定时任务

```bash
# 编辑crontab
crontab -e

# 添加定时任务

# 每周日凌晨2点执行坏块检测
0 2 * * 0 /usr/local/bin/badblocks_check.sh

# 每天早上8点检查SMART状态
0 8 * * * /usr/local/bin/smart_check.sh

# 每月1号执行长自检
0 3 1 * * smartctl -t long /dev/sda
```

## 8. 功能时序图

### 8.1 坏块添加时序

```mermaid
sequenceDiagram
    participant A as **应用/工具**
    participant K as **内核**
    participant BB as **badblocks子系统**
    participant D as **设备驱动**
    participant H as **硬件**
    
    Note over A,H: **发现坏块**
    A->>K: write操作失败
    K->>D: 写入命令
    D->>H: 硬件写入
    H->>D: 返回错误
    D->>K: I/O错误
    
    Note over K: **处理错误**
    K->>BB: badblocks_set()
    Note right of BB: 扇区号: 1000<br/>长度: 8<br/>未确认
    
    BB->>BB: 获取写锁
    BB->>BB: 查找插入位置
    BB->>BB: 检查是否可合并
    
    alt 可以合并
        BB->>BB: 合并相邻坏块
    else 不能合并
        BB->>BB: 插入新条目
        BB->>BB: 更新count
    end
    
    BB->>BB: 设置changed标志
    BB->>BB: 释放锁
    
    BB->>K: 返回成功
    K->>A: 报告I/O错误
    
    Note over A,H: **后续访问**
    A->>K: 读取扇区1000
    K->>BB: badblocks_check()
    BB->>BB: 查找坏块表
    BB->>K: 返回-1（未确认坏块）
    K->>A: 返回错误
```

### 8.2 坏块扫描时序

```mermaid
sequenceDiagram
    participant U as **用户**
    participant B as **badblocks工具**
    participant K as **内核**
    participant D as **磁盘**
    
    U->>B: 启动扫描<br/>badblocks -n /dev/sda
    
    B->>B: 打开设备
    B->>B: 分配缓冲区
    
    loop 扫描所有块
        Note over B: **测试块#N**
        
        B->>K: read(buf1)
        K->>D: READ命令
        D->>K: 数据
        K->>B: 返回数据
        
        B->>B: 保存原始数据
        
        B->>K: write(pattern)
        K->>D: WRITE命令
        D->>K: 完成
        
        B->>K: read(buf2)
        K->>D: READ命令
        D->>K: 数据
        K->>B: 返回数据
        
        B->>B: 比较buf2与pattern
        
        alt 数据一致
            B->>K: write(buf1)
            Note right of B: ✓ 恢复原始数据
        else 数据不一致
            Note right of B: ✗ 发现坏块
            B->>B: 记录坏块#N
            B->>K: write(buf1)
            Note right of B: 尝试恢复
        end
        
        B->>U: 显示进度
    end
    
    B->>B: 统计结果
    B->>U: 输出坏块列表
```

### 8.3 SMART自检时序

```mermaid
sequenceDiagram
    participant U as **用户**
    participant S as **smartctl**
    participant K as **内核SCSI/ATA**
    participant F as **磁盘固件**
    participant H as **硬件**
    
    U->>S: smartctl -t long /dev/sda
    S->>K: SMART命令<br/>启动自检
    K->>F: ATA命令
    F->>F: 开始后台自检
    F->>K: 返回接受
    K->>S: 命令接受
    S->>U: 自检已启动<br/>完成时间：约X小时
    
    Note over F,H: **后台自检进行中**
    
    loop 扫描磁盘
        F->>H: 读取扇区
        H->>F: 数据
        
        F->>F: ECC校验
        
        alt 校验通过
            Note right of F: ✓ 扇区正常
        else 校验失败
            F->>H: 重新读取
            H->>F: 数据
            
            F->>F: 再次校验
            
            alt 成功
                Note right of F: ✓ 软错误
                F->>F: 记录日志
            else 失败
                Note right of F: ✗ 硬错误
                F->>F: 尝试重映射
                F->>F: 更新G-List
                F->>F: 记录错误日志
            end
        end
    end
    
    Note over F: **自检完成**
    
    U->>S: smartctl -l selftest /dev/sda
    S->>K: 读取自检日志
    K->>F: 获取日志
    F->>K: 返回日志
    K->>S: 日志数据
    S->>U: 显示自检结果
```

## 9. 坏块统计与可视化

### 9.1 坏块统计脚本

```python
#!/usr/bin/env python3
# 坏块统计分析工具

import subprocess
import json
import datetime

class BadBlocksAnalyzer:
    def __init__(self, device):
        self.device = device
        self.stats = {
            'device': device,
            'timestamp': datetime.datetime.now().isoformat(),
            'smart_status': {},
            'badblocks_count': 0,
            'badblocks_list': []
        }
    
    def get_smart_status(self):
        """获取SMART状态"""
        try:
            cmd = ['smartctl', '-A', self.device]
            output = subprocess.check_output(cmd, text=True)
            
            for line in output.split('\n'):
                if 'Reallocated_Sector' in line:
                    parts = line.split()
                    self.stats['smart_status']['reallocated'] = int(parts[9])
                elif 'Current_Pending_Sector' in line:
                    parts = line.split()
                    self.stats['smart_status']['pending'] = int(parts[9])
                elif 'Offline_Uncorrectable' in line:
                    parts = line.split()
                    self.stats['smart_status']['uncorrectable'] = int(parts[9])
        except Exception as e:
            print(f"Error getting SMART status: {e}")
    
    def scan_badblocks(self):
        """扫描坏块"""
        try:
            cmd = ['badblocks', '-n', '-s', self.device]
            output = subprocess.check_output(cmd, text=True, stderr=subprocess.STDOUT)
            
            # 解析输出
            for line in output.split('\n'):
                if line.strip().isdigit():
                    self.stats['badblocks_list'].append(int(line.strip()))
            
            self.stats['badblocks_count'] = len(self.stats['badblocks_list'])
        except Exception as e:
            print(f"Error scanning badblocks: {e}")
    
    def generate_report(self):
        """生成报告"""
        print("\n" + "="*50)
        print(f"Bad Blocks Analysis Report for {self.device}")
        print("="*50)
        print(f"Timestamp: {self.stats['timestamp']}")
        print("\nSMART Status:")
        print(f"  Reallocated Sectors: {self.stats['smart_status'].get('reallocated', 'N/A')}")
        print(f"  Pending Sectors: {self.stats['smart_status'].get('pending', 'N/A')}")
        print(f"  Uncorrectable: {self.stats['smart_status'].get('uncorrectable', 'N/A')}")
        print(f"\nBad Blocks Count: {self.stats['badblocks_count']}")
        
        if self.stats['badblocks_list']:
            print("\nBad Block List:")
            for bb in self.stats['badblocks_list'][:10]:  # 只显示前10个
                print(f"  Block: {bb}")
            if len(self.stats['badblocks_list']) > 10:
                print(f"  ... and {len(self.stats['badblocks_list']) - 10} more")
        
        # 保存JSON报告
        with open(f'badblocks_report_{self.device.replace("/", "_")}.json', 'w') as f:
            json.dump(self.stats, f, indent=2)
    
    def run(self):
        """执行完整分析"""
        print(f"Analyzing device: {self.device}")
        print("Getting SMART status...")
        self.get_smart_status()
        print("Scanning for bad blocks (this may take a while)...")
        self.scan_badblocks()
        self.generate_report()

if __name__ == '__main__':
    import sys
    if len(sys.argv) < 2:
        print("Usage: badblocks_analyzer.py <device>")
        sys.exit(1)
    
    analyzer = BadBlocksAnalyzer(sys.argv[1])
    analyzer.run()
```

### 9.2 监控告警系统集成

```bash
# Prometheus监控配置
cat > /etc/prometheus/badblocks_exporter.sh <<'EOF'
#!/bin/bash
# Badblocks Prometheus Exporter

echo "# HELP badblocks_count Number of bad blocks detected"
echo "# TYPE badblocks_count gauge"

for device in /dev/sd?; do
    count=$(badblocks -n "$device" 2>/dev/null | wc -l)
    device_name=$(basename "$device")
    echo "badblocks_count{device=\"$device_name\"} $count"
done

echo "# HELP smart_reallocated_sectors Reallocated sectors count"
echo "# TYPE smart_reallocated_sectors gauge"

for device in /dev/sd?; do
    count=$(smartctl -A "$device" | grep "Reallocated_Sector" | awk '{print $10}')
    device_name=$(basename "$device")
    echo "smart_reallocated_sectors{device=\"$device_name\"} $count"
done
EOF

chmod +x /etc/prometheus/badblocks_exporter.sh
```

## 10. 总结

### 10.1 坏块检测工具对比

| **工具** | **检测能力** | **破坏性** | **速度** | **推荐度** |
|---------|------------|-----------|---------|-----------|
| **badblocks** | 全面 | 可选 | 慢 | ⭐⭐⭐⭐ |
| **smartctl** | SMART | 无 | 快 | ⭐⭐⭐⭐⭐ |
| **e2fsck -c** | 文件系统级 | 否 | 慢 | ⭐⭐⭐ |
| **dd** | 基本 | 可选 | 慢 | ⭐⭐ |
| **硬盘自检** | 硬件级 | 否 | 中 | ⭐⭐⭐⭐⭐ |

### 10.2 关键源码路径

- **坏块管理核心**: `block/badblocks.c`
- **坏块头文件**: `include/linux/badblocks.h`
- **MD RAID坏块**: `drivers/md/md.c`
- **NVDIMM坏块**: `drivers/nvdimm/badrange.c`

### 10.3 最佳实践建议

```mermaid
mindmap
  root((坏块管理))
    **预防**
      使用高质量硬盘
      避免频繁断电
      保持良好散热
      定期健康检查
    **监控**
      启用SMART监控
      定期自检
      日志分析
      告警机制
    **检测**
      新硬盘初检
      定期全盘扫描
      关键数据区检查
      性能异常排查
    **处理**
      及时备份数据
      更换问题硬盘
      RAID冗余保护
      文件系统检查
```

### 10.4 决策树

```mermaid
graph TD
    A[**发现坏块**] --> B{**坏块数量**}
    
    B -->|0-5个| C[**继续监控**]
    B -->|5-50个| D[**增加检查频率**]
    B -->|50-200个| E[**准备更换**]
    B -->|>200个| F[**立即更换**]
    
    C --> G[**每月检查**]
    D --> H[**每周检查**]
    E --> I[**每天检查**]
    F --> J[**备份数据**]
    
    I --> K{**坏块增长?**}
    K -->|快速增长| F
    K -->|缓慢增长| E
    K -->|稳定| D
    
    J --> L[**更换硬盘**]
    L --> M[**恢复数据**]
    M --> N[**验证完整性**]
    
    style F fill:#ffebee,stroke:#c62828,stroke-width:3px
    style L fill:#fff3e0,stroke:#e65100,stroke-width:2px
    style N fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px
```

---

**文档基于Linux内核源码分析**
- 内核版本：基于最新主线
- 主要源码路径：
  - `block/badblocks.c` - 坏块管理核心
  - `include/linux/badblocks.h` - 坏块接口
  - `drivers/md/md.c` - RAID坏块处理
  - `drivers/nvdimm/` - NVDIMM坏块管理

**参考工具**
- badblocks - 坏块扫描工具
- smartmontools (smartctl) - SMART监控
- e2fsck - EXT文件系统检查
- hdparm - 硬盘参数工具

