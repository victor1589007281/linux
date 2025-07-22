# Linux cgroups v2 架构与实现原理分析

## 目录

1. [概述](#概述)
2. [核心架构](#核心架构)
3. [统一层级结构](#统一层级结构)
4. [控制器子系统](#控制器子系统)
5. [资源分配模型](#资源分配模型)
6. [实现原理](#实现原理)
7. [接口文件系统](#接口文件系统)
8. [与v1的区别](#与v1的区别)
9. [总结](#总结)

## 概述

Control Groups v2（cgroups v2）是Linux内核中用于组织和管理进程的机制，它提供了层次化的进程组织方式，并沿着这个层次结构以受控和可配置的方式分配系统资源。

### 核心特点

- **统一层级结构**：与v1不同，v2只有单一层级结构
- **无内部进程约束**：域控制器不能在有进程的cgroup中启用
- **线程支持**：支持部分控制器的线程粒度控制
- **一致性接口**：所有控制器使用统一的接口规范

## 核心架构

### 主要组件

cgroups v2由两个主要部分组成：

1. **核心（Core）**：负责层次化组织进程
2. **控制器（Controllers）**：负责沿层次结构分配特定类型的系统资源

### 核心数据结构

```c
// 根层级结构
struct cgroup_root cgrp_dfl_root = { 
    .cgrp.rstat_cpu = &cgrp_dfl_root_rstat_cpu 
};

// cgroup实例
struct cgroup {
    struct cgroup_subsys_state self;    // 自身CSS
    unsigned long flags;                // 标志位
    int level;                         // 层级深度
    int nr_descendants;                // 子孙节点数量
    struct kernfs_node *kn;            // kernfs节点
    struct cgroup_root *root;          // 所属根
    // ...
};

// 控制器状态
struct cgroup_subsys_state {
    struct cgroup *cgroup;             // 所属cgroup
    struct cgroup_subsys *ss;          // 控制器指针
    struct percpu_ref refcnt;          // 引用计数
    // ...
};
```

## 统一层级结构

### 层级特点

- **单一层级**：整个系统只有一个cgroup层级树
- **树形结构**：每个进程只属于一个cgroup
- **继承关系**：子cgroup继承父cgroup的约束
- **层级约束**：越接近根节点的限制越无法被覆盖

### 挂载方式

```bash
# 挂载cgroups v2
mount -t cgroup2 none /sys/fs/cgroup
```

### 层级管理

```c
// 创建cgroup
static struct cgroup *cgroup_create(struct cgroup *parent, 
                                   const char *name, umode_t mode)
{
    struct cgroup_root *root = parent->root;
    struct cgroup *cgrp;
    int level = parent->level + 1;
    
    // 分配cgroup结构
    cgrp = kzalloc(struct_size(cgrp, ancestors, (level + 1)), GFP_KERNEL);
    
    // 初始化层级关系
    cgrp->root = root;
    cgrp->level = level;
    
    // 设置祖先链
    for (tcgrp = cgrp; tcgrp; tcgrp = cgroup_parent(tcgrp)) {
        cgrp->ancestors[tcgrp->level] = tcgrp;
    }
    
    return cgrp;
}
```

## 控制器子系统

### 控制器注册机制

控制器通过宏定义的方式注册到系统中：

```c
// include/linux/cgroup_subsys.h
#if IS_ENABLED(CONFIG_CPUSETS)
SUBSYS(cpuset)
#endif
#if IS_ENABLED(CONFIG_CGROUP_SCHED)
SUBSYS(cpu)
#endif
#if IS_ENABLED(CONFIG_MEMCG)
SUBSYS(memory)
#endif
// ...更多控制器

// 生成控制器数组
#define SUBSYS(_x) [_x ## _cgrp_id] = &_x ## _cgrp_subsys,
struct cgroup_subsys *cgroup_subsys[] = {
#include <linux/cgroup_subsys.h>
};
```

### 主要控制器

| 控制器 | 功能 | 主要接口文件 |
|--------|------|--------------|
| cpu | CPU时间分配 | cpu.weight, cpu.max |
| memory | 内存使用限制 | memory.max, memory.high, memory.low |
| io | 磁盘IO限制 | io.max, io.weight |
| pids | 进程数量限制 | pids.max |
| cpuset | CPU和内存节点分配 | cpuset.cpus, cpuset.mems |
| freezer | 进程冻结 | cgroup.freeze |

### 控制器实现示例

```c
// CPU控制器结构
struct cgroup_subsys cpu_cgrp_subsys = {
    .css_alloc      = cpu_cgroup_css_alloc,
    .css_online     = cpu_cgroup_css_online,
    .css_released   = cpu_cgroup_css_released,
    .css_free       = cpu_cgroup_css_free,
    .css_extra_stat_show = cpu_extra_stat_show,
    .can_attach     = cpu_cgroup_can_attach,
    .attach         = cpu_cgroup_attach,
    .legacy_cftypes = cpu_legacy_files,
    .dfl_cftypes    = cpu_files,
    .early_init     = true,
    .threaded       = true,
};
```

## 资源分配模型

### 1. 权重模型（Weights）

- **原理**：按权重比例分配父资源
- **特点**：工作保守，只有活跃子节点参与分配
- **范围**：[1, 10000]，默认100
- **示例**：cpu.weight

```c
// 权重分配算法伪代码
total_weight = sum(active_children.weight);
for each child in active_children:
    child.allocation = parent.resource * (child.weight / total_weight);
```

### 2. 限制模型（Limits）

- **原理**：硬限制子节点最大资源使用量
- **特点**：可以超额分配（总限制>父资源）
- **范围**：[0, max]，默认"max"（无限制）
- **示例**：io.max, memory.max

### 3. 保护模型（Protections）

- **原理**：保证子节点的最小资源量
- **特点**：软保护，只在所有祖先都未超过保护值时生效
- **范围**：[0, max]，默认0
- **示例**：memory.low

### 4. 分配模型（Allocations）

- **原理**：独占分配有限资源
- **特点**：不能超额分配
- **范围**：[0, max]，默认0
- **示例**：cpu.rt.max（实时CPU时间片）

## 实现原理

### 初始化流程

```c
// 系统启动时的cgroup初始化
int __init cgroup_init_early(void)
{
    struct cgroup_subsys *ss;
    int i;
    
    // 初始化默认根层级
    ctx.root = &cgrp_dfl_root;
    init_cgroup_root(&ctx);
    
    // 初始化各个子系统
    for_each_subsys(ss, i) {
        ss->id = i;
        ss->name = cgroup_subsys_name[i];
        
        if (ss->early_init)
            cgroup_init_subsys(ss, true);
    }
    
    return 0;
}

int __init cgroup_init(void)
{
    // 注册文件系统
    register_filesystem(&cgroup2_fs_type);
    
    // 完成子系统初始化
    for_each_subsys(ss, ssid) {
        if (!ss->early_init)
            cgroup_init_subsys(ss, false);
            
        // 设置控制器掩码
        cgrp_dfl_root.subsys_mask |= 1 << ss->id;
    }
    
    return 0;
}
```

### 控制器启用机制

```c
// 控制器启用/禁用
static ssize_t cgroup_subtree_control_write(struct kernfs_open_file *of,
                                           char *buf, size_t nbytes, loff_t off)
{
    u16 enable = 0, disable = 0;
    
    // 解析输入：+cpu +memory -io
    while ((tok = strsep(&buf, " "))) {
        if (*tok == '+') {
            enable |= 1 << ssid;
        } else if (*tok == '-') {
            disable |= 1 << ssid;
        }
    }
    
    // 应用控制更改
    cgrp->subtree_control |= enable;
    cgrp->subtree_control &= ~disable;
    
    ret = cgroup_apply_control(cgrp);
    
    return ret ?: nbytes;
}
```

### 进程迁移

```c
// 进程在cgroup间迁移
static ssize_t cgroup_procs_write(struct kernfs_open_file *of,
                                 char *buf, size_t nbytes, loff_t off)
{
    struct cgroup *cgrp;
    struct task_struct *task;
    pid_t pid;
    
    // 解析进程PID
    if (kstrtoint(strstrip(buf), 0, &pid) || pid < 0)
        return -EINVAL;
        
    // 查找目标进程
    task = find_task_by_vpid(pid);
    if (!task)
        return -ESRCH;
        
    // 执行迁移
    ret = cgroup_procs_write_permission(task, cgrp, of);
    if (!ret)
        ret = cgroup_attach_task(cgrp, task, true);
        
    return ret ?: nbytes;
}
```

## 接口文件系统

### 核心接口文件

cgroups v2使用kernfs虚拟文件系统提供用户空间接口：

```c
// 基础接口文件定义
static struct cftype cgroup_base_files[] = {
    {
        .name = "cgroup.type",
        .seq_show = cgroup_type_show,
        .write = cgroup_type_write,
    },
    {
        .name = "cgroup.procs",
        .seq_show = cgroup_procs_show,
        .write = cgroup_procs_write,
    },
    {
        .name = "cgroup.controllers",
        .seq_show = cgroup_controllers_show,
    },
    {
        .name = "cgroup.subtree_control",
        .seq_show = cgroup_subtree_control_show,
        .write = cgroup_subtree_control_write,
    },
    {
        .name = "cgroup.events",
        .seq_show = cgroup_events_show,
    },
    // ...
};
```

### 主要接口文件功能

| 文件 | 功能 | 操作 |
|------|------|------|
| cgroup.procs | 进程列表管理 | 读取：显示PID列表<br/>写入：迁移进程 |
| cgroup.controllers | 可用控制器 | 读取：显示可用控制器列表 |
| cgroup.subtree_control | 子树控制器 | 读取：显示启用的控制器<br/>写入：启用/禁用控制器 |
| cgroup.type | cgroup类型 | 读取：显示类型（domain/threaded）<br/>写入：设置为threaded |
| cgroup.events | 事件通知 | 读取：显示populated等事件状态 |

### 文件操作实现

```c
// 控制器接口文件创建
static int css_populate_dir(struct cgroup_subsys_state *css)
{
    struct cgroup *cgrp = css->cgroup;
    struct cftype *cfts;
    
    if (!css->ss) {
        // 核心接口文件
        ret = cgroup_addrm_files(css, cgrp, cgroup_base_files, true);
    } else {
        // 控制器特定文件
        list_for_each_entry(cfts, &css->ss->cfts, node) {
            ret = cgroup_addrm_files(css, cgrp, cfts, true);
        }
    }
    
    css->flags |= CSS_VISIBLE;
    return 0;
}
```

## 与v1的区别

### 设计理念变化

| 特性 | cgroups v1 | cgroups v2 |
|------|------------|------------|
| 层级结构 | 多层级，每个控制器可独立层级 | 单一统一层级 |
| 线程支持 | 线程可属于不同cgroup | 默认进程粒度，部分支持线程模式 |
| 接口一致性 | 各控制器接口差异较大 | 统一接口规范 |
| 内部进程 | 允许内部进程与子cgroup竞争 | 域控制器禁止内部进程 |

### v1存在的问题

1. **多层级复杂性**：管理多个相似层级增加复杂度
2. **线程粒度混乱**：模糊了应用API和系统管理接口界限
3. **控制器竞争**：内部进程与子cgroup竞争资源，难以解决
4. **接口不一致**：不同控制器接口格式和行为差异巨大

### v2的改进

1. **统一层级**：简化管理，降低复杂度
2. **明确边界**：清晰的系统管理接口定义
3. **无内部进程约束**：避免竞争问题
4. **一致性接口**：标准化的文件格式和行为

## 线程模式支持

### 线程模式概念

cgroups v2支持部分控制器的线程粒度控制：

```c
// 线程模式检查
static bool cgroup_is_threaded(struct cgroup *cgrp)
{
    return cgrp->dom_cgrp != cgrp;
}

// 设置为线程模式
static int cgroup_type_write(struct kernfs_open_file *of, char *buf,
                           size_t nbytes, loff_t off)
{
    if (!strcmp(buf, "threaded")) {
        ret = cgroup_enable_threaded(cgrp);
    }
    return ret ?: nbytes;
}
```

### 线程控制器

支持线程模式的控制器：
- cpu
- cpuset
- perf_event
- pids

## 性能优化

### 引用计数优化

```c
// CSS引用计数管理
static void css_release(struct percpu_ref *ref)
{
    struct cgroup_subsys_state *css;
    css = container_of(ref, struct cgroup_subsys_state, refcnt);
    INIT_RCU_WORK(&css->destroy_rwork, css_free_rwork_fn);
    queue_rcu_work(cgroup_destroy_wq, &css->destroy_rwork);
}
```

### 统计优化

```c
// 递归统计系统
static void cgroup_rstat_flush_locked(struct cgroup *cgrp, bool may_sleep)
{
    struct cgroup *pos = NULL;
    
    // 遍历子树进行统计更新
    rcu_read_lock();
    cgroup_for_each_live_descendant_pre(pos, d_css, cgrp) {
        struct cgroup_rstat_cpu *rstatc;
        
        // 更新每CPU统计数据
        for_each_possible_cpu(cpu) {
            rstatc = cgroup_rstat_cpu(pos, cpu);
            // 执行统计更新
        }
    }
    rcu_read_unlock();
}
```

## 总结

cgroups v2作为Linux内核资源管理的核心机制，通过以下关键设计实现了高效的进程组织和资源控制：

### 架构优势

1. **统一层级结构**：简化了管理复杂度，提供清晰的资源控制模型
2. **模块化控制器**：每个控制器专注特定资源类型，易于扩展
3. **一致性接口**：标准化的用户接口，降低学习和使用成本
4. **性能优化**：percpu统计、RCU保护、异步销毁等优化手段

### 实现特点

1. **内核集成**：深度集成到进程调度、内存管理、IO调度等核心子系统
2. **动态配置**：支持运行时动态启用/禁用控制器
3. **事件通知**：提供丰富的事件通知机制
4. **安全隔离**：结合namespace提供完整的容器化支持

### 应用场景

1. **容器技术**：Docker、Kubernetes等容器平台的资源隔离基础
2. **系统调优**：服务器资源精细化管理和性能优化
3. **负载控制**：防止单个应用影响系统整体性能
4. **资源计量**：为云计算计费和资源监控提供数据支持

cgroups v2通过其优雅的设计和高效的实现，为现代Linux系统提供了强大而灵活的资源管理能力，是构建高性能、高可靠性系统的重要基础设施。
