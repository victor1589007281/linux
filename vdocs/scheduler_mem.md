# Linux 内存管理调度原理与实现分析

## 目录

1. [概述](#概述)
2. [内存管理架构](#内存管理架构)
3. [页面回收机制](#页面回收机制)
4. [OOM Killer机制](#oom-killer机制)
5. [内存压缩机制](#内存压缩机制)
6. [NUMA内存平衡](#numa内存平衡)
7. [水位标记管理](#水位标记管理)
8. [内存限流控制](#内存限流控制)
9. [性能优化策略](#性能优化策略)
10. [核心数据结构](#核心数据结构)
11. [优点与局限性](#优点与局限性)
12. [总结](#总结)

## 概述

Linux内存管理调度是操作系统内核中负责内存资源分配、回收和优化的核心子系统。它不仅要确保系统有足够的可用内存来满足应用程序的需求，还要处理内存碎片、内存压力和内存不足等复杂情况。

### 核心设计目标

1. **内存回收效率**：通过智能的页面回收策略，最大化内存利用率
2. **系统稳定性**：在内存不足时通过OOM killer确保系统继续运行
3. **碎片管理**：通过内存压缩减少外部碎片，提高大内存分配成功率
4. **NUMA优化**：在多节点系统中优化内存访问局部性
5. **响应性能**：平衡内存回收开销与系统响应速度

### 主要组件

- **kswapd守护进程**：后台页面回收
- **直接回收机制**：同步页面回收
- **OOM Killer**：内存不足时的进程选择和终止
- **kcompactd守护进程**：内存碎片整理
- **NUMA平衡器**：跨节点内存优化
- **水位标记系统**：内存压力检测和响应

## 内存管理架构

Linux内存管理采用分层的zone架构，结合LRU算法进行页面回收调度。

### 内存zones架构

```c
// 内存zone类型定义 - include/linux/mmzone.h
enum zone_type {
    ZONE_DMA,        // DMA内存区域
    ZONE_DMA32,      // 32位DMA内存区域
    ZONE_NORMAL,     // 普通内存区域
    ZONE_HIGHMEM,    // 高端内存区域
    ZONE_MOVABLE,    // 可移动内存区域
    ZONE_DEVICE,     // 设备内存区域
    __MAX_NR_ZONES
};

// 内存zone结构
struct zone {
    // 水位标记
    unsigned long _watermark[NR_WMARK];  // 水位标记数组
    unsigned long watermark_boost;       // 水位提升值
    
    // 空闲页面管理
    struct free_area free_area[MAX_ORDER]; // 伙伴系统空闲区域
    unsigned long zone_start_pfn;          // zone起始页帧号
    unsigned long spanned_pages;           // zone总页数
    unsigned long present_pages;           // 实际存在的页数
    unsigned long managed_pages;           // 内核管理的页数
    
    // LRU链表
    struct lruvec lruvec;                  // LRU向量
    
    // 回收控制
    unsigned long pages_scanned;          // 扫描页数
    unsigned long flags;                  // zone标志位
    
    // Per-CPU页面缓存
    struct per_cpu_pages __percpu *per_cpu_pageset;
    
    // 压缩控制
    unsigned int compact_considered;      // 压缩考虑次数
    unsigned int compact_defer_shift;     // 压缩延迟位移
    int compact_order_failed;            // 压缩失败的order
    
    // 统计信息
    atomic_long_t vm_stat[NR_VM_ZONE_STAT_ITEMS]; // zone统计
    atomic_long_t vm_numa_event[NR_VM_NUMA_EVENT_ITEMS]; // NUMA事件统计
};

// 节点级别结构
struct pglist_data {
    struct zone node_zones[MAX_NR_ZONES]; // 节点中的zone数组
    struct zonelist node_zonelists[MAX_ZONELISTS]; // zone列表
    int nr_zones;                         // zone数量
    
    // 回收控制
    struct task_struct *kswapd;           // kswapd任务
    int kswapd_order;                     // kswapd回收order
    enum zone_type kswapd_highest_zoneidx; // 最高zone索引
    int kswapd_failures;                  // kswapd失败次数
    
    // 压缩控制
    struct task_struct *kcompactd;        // kcompactd任务
    int kcompactd_max_order;              // 压缩最大order
    enum zone_type kcompactd_highest_zoneidx; // 压缩最高zone索引
    wait_queue_head_t kcompactd_wait;     // kcompactd等待队列
    
    // NUMA统计
    unsigned long min_unmapped_pages;     // 最小未映射页数
    unsigned long min_slab_pages;         // 最小slab页数
    
    // 内存热插拔
    struct memory_tier *memtier;          // 内存层级
    
    // 统计信息
    struct per_cpu_nodestat __percpu *per_cpu_nodestats;
};
```

### 水位标记系统

```c
// 水位标记定义
enum zone_watermarks {
    WMARK_MIN,    // 最小水位：触发直接回收
    WMARK_LOW,    // 低水位：唤醒kswapd
    WMARK_HIGH,   // 高水位：kswapd停止回收
    WMARK_PROMO,  // 提升水位：用于内存层级
    NR_WMARK
};

// 水位检查函数
static inline bool zone_watermark_ok(struct zone *z, unsigned int order,
                                    unsigned long mark, int highest_zoneidx,
                                    unsigned int alloc_flags)
{
    long min = mark;
    int o;
    const bool alloc_harder = (alloc_flags & ALLOC_HARDER);
    const bool alloc_high = (alloc_flags & ALLOC_HIGH);

    // 调整最小水位
    if (alloc_high)
        min -= min / 4;
    if (alloc_harder)
        min -= min / 4;
    
    // 考虑保留内存
    if (alloc_flags & ALLOC_RESERVES)
        min -= min / 2;
    
    // 检查空闲内存是否满足要求
    if (free_pages <= min + z->lowmem_reserve[highest_zoneidx])
        return false;
    
    // 检查各个order的空闲页面
    for (o = 0; o < order; o++) {
        if (z->free_area[o].nr_free)
            return true;
    }
    
    return false;
}
```

## 页面回收机制

Linux通过kswapd后台回收和直接回收两种方式来释放内存页面。

### kswapd后台回收

```c
// kswapd主循环 - mm/vmscan.c
static int kswapd(void *p)
{
    pg_data_t *pgdat = (pg_data_t *)p;
    struct task_struct *tsk = current;
    DEFINE_WAIT(wait);
    struct reclaim_state reclaim_state = {
        .reclaimed_slab = 0,
    };
    const struct cpumask *cpumask = cpumask_of_node(pgdat->node_id);

    // 设置CPU亲和性
    if (!cpumask_empty(cpumask))
        set_cpus_allowed_ptr(tsk, cpumask);

    // 设置为可冻结任务
    set_freezable();
    
    pgdat->kswapd_order = 0;
    pgdat->kswapd_highest_zoneidx = MAX_NR_ZONES - 1;

    for (;;) {
        bool ret;

        // 检查是否需要冻结
        if (freezing(current) || kthread_should_stop())
            break;

        // 等待唤醒或超时
        prepare_to_wait(&pgdat->kswapd_wait, &wait, TASK_INTERRUPTIBLE);
        ret = try_to_freeze();
        
        if (kthread_should_stop())
            break;

        // 检查是否需要进行回收
        if (!ret) {
            trace_mm_vmscan_kswapd_sleep(pgdat->node_id);
            
            if (!kswapd_shrink_node(pgdat, &sc)) {
                // 没有需要回收的内存，休眠
                schedule();
            }
            
            trace_mm_vmscan_kswapd_wake(pgdat->node_id, 
                                       sc.order, sc.highest_zoneidx);
        }
        
        finish_wait(&pgdat->kswapd_wait, &wait);

        // 执行实际的回收工作
        kswapd_try_sleep(pgdat, sc.order, sc.highest_zoneidx);
    }

    tsk->flags &= ~(PF_MEMALLOC | PF_KSWAPD);

    return 0;
}

// kswapd回收逻辑
static int kswapd_shrink_node(pg_data_t *pgdat,
                             struct scan_control *sc)
{
    struct zone *zone;
    int z;

    // 遍历所有zone进行回收
    for (z = 0; z <= sc->highest_zoneidx; z++) {
        zone = &pgdat->node_zones[z];
        if (!managed_zone(zone))
            continue;

        sc->nr_scanned = 0;
        shrink_zone(zone, sc);
        
        // 检查水位是否达到要求
        if (zone_watermark_ok(zone, sc->order,
                             high_wmark_pages(zone), sc->highest_zoneidx, 0))
            sc->nr_balanced++;
    }

    // 返回是否需要继续回收
    return sc->nr_balanced < sc->nr_zones;
}
```

### 直接回收机制

```c
// 直接回收入口 - mm/page_alloc.c
static struct page *
__alloc_pages_direct_reclaim(gfp_t gfp_mask, unsigned int order,
                            unsigned int alloc_flags, const struct alloc_context *ac,
                            unsigned long *did_some_progress)
{
    struct page *page = NULL;
    unsigned long pflags;
    bool drained = false;

    // 标记内存回收开始
    psi_memstall_enter(&pflags);
    *did_some_progress = __perform_reclaim(gfp_mask, order, ac);
    
    if (unlikely(!(*did_some_progress)))
        goto out;

retry:
    // 尝试分配页面
    page = get_page_from_freelist(gfp_mask, order, alloc_flags, ac);

    // 如果分配失败，尝试排干PCP缓存
    if (!page && !drained) {
        unreserve_highatomic_pageblock(ac, false);
        drain_all_pages(NULL);
        drained = true;
        goto retry;
    }

out:
    psi_memstall_leave(&pflags);
    return page;
}

// 执行回收操作
static unsigned long
__perform_reclaim(gfp_t gfp_mask, unsigned int order,
                 const struct alloc_context *ac)
{
    unsigned int noreclaim_flag;
    unsigned long progress;

    cond_resched();

    // 进入同步回收模式
    cpuset_memory_pressure_bump();
    fs_reclaim_acquire(gfp_mask);
    noreclaim_flag = memalloc_noreclaim_save();

    // 调用回收核心函数
    progress = try_to_free_pages(ac->zonelist, order, gfp_mask, ac->nodemask);

    memalloc_noreclaim_restore(noreclaim_flag);
    fs_reclaim_release(gfp_mask);
    cond_resched();

    return progress;
}
```

### LRU页面扫描

```c
// LRU扫描控制结构
struct scan_control {
    unsigned long nr_to_reclaim;      // 需要回收的页数
    gfp_t gfp_mask;                   // GFP掩码
    int order;                        // 分配order
    nodemask_t *nodemask;             // 节点掩码
    struct mem_cgroup *target_mem_cgroup; // 目标内存cgroup
    
    int priority;                     // 扫描优先级
    unsigned int may_writepage:1;     // 是否可以写页面
    unsigned int may_unmap:1;         // 是否可以解映射
    unsigned int may_swap:1;          // 是否可以交换
    unsigned int hibernation_mode:1;  // 休眠模式
    
    int swappiness;                   // 交换倾向性
    int nr_scanned;                   // 已扫描页数
    int nr_reclaimed;                 // 已回收页数
};

// 扫描LRU链表
static unsigned long shrink_list(enum lru_list lru, unsigned long nr_to_scan,
                                struct lruvec *lruvec, struct scan_control *sc)
{
    if (is_active_lru(lru)) {
        // 扫描活跃LRU链表
        if (sc->may_deactivate & (1 << is_file_lru(lru)))
            shrink_active_list(nr_to_scan, lruvec, sc, lru);
        else
            sc->skipped_deactivate = 1;
        return 0;
    }

    // 扫描非活跃LRU链表
    return shrink_inactive_list(nr_to_scan, lruvec, sc, lru);
}

// 处理非活跃页面
static noinline_for_stack unsigned long
shrink_inactive_list(unsigned long nr_to_scan, struct lruvec *lruvec,
                    struct scan_control *sc, enum lru_list lru)
{
    LIST_HEAD(page_list);
    unsigned long nr_scanned;
    unsigned long nr_reclaimed = 0;
    unsigned long nr_taken;
    struct reclaim_stat stat;
    bool file = is_file_lru(lru);
    enum vm_event_item item;
    struct pglist_data *pgdat = lruvec_pgdat(lruvec);
    bool stalled = false;

    // 从LRU链表中隔离页面
    spin_lock_irq(&lruvec->lru_lock);
    nr_taken = isolate_lru_pages(nr_to_scan, lruvec, &page_list,
                               &nr_scanned, sc, lru);
    
    // 更新统计信息
    __mod_node_page_state(pgdat, NR_ISOLATED_ANON + file, nr_taken);
    item = PGSCAN_KSWAPD + reclaimer_offset();
    if (!cgroup_reclaim(sc))
        __count_vm_events(item, nr_scanned);
    __count_memcg_events(lruvec_memcg(lruvec), item, nr_scanned);
    spin_unlock_irq(&lruvec->lru_lock);

    if (nr_taken == 0)
        return 0;

    // 收缩页面列表
    nr_reclaimed = shrink_page_list(&page_list, pgdat, sc, &stat, false);

    // 处理剩余页面
    spin_lock_irq(&lruvec->lru_lock);
    move_pages_to_lru(lruvec, &page_list);
    
    __mod_node_page_state(pgdat, NR_ISOLATED_ANON + file, -nr_taken);
    spin_unlock_irq(&lruvec->lru_lock);

    lru_note_cost(lruvec, file, stat.nr_pageout);
    mem_cgroup_uncharge_list(&page_list);
    free_unref_page_list(&page_list);

    // 更新回收统计
    trace_mm_vmscan_lru_shrink_inactive(pgdat->node_id,
                                       nr_scanned, nr_reclaimed, &stat,
                                       sc->priority, file);
    return nr_reclaimed;
}
```

## OOM Killer机制

当系统内存严重不足时，OOM Killer会选择并终止消耗内存最多的进程以释放内存。

### OOM检测和触发

```c
// OOM控制结构 - include/linux/oom.h
struct oom_control {
    struct zonelist *zonelist;       // zone列表
    nodemask_t *nodemask;           // 节点掩码
    struct mem_cgroup *memcg;       // 内存cgroup
    const gfp_t gfp_mask;           // GFP掩码
    const int order;                // 分配order
    
    // OOM实现使用，不要设置
    unsigned long totalpages;        // 总页数
    struct task_struct *chosen;     // 选中的任务
    long chosen_points;             // 选中任务的分数
    enum oom_constraint constraint; // 约束类型
};

// OOM主函数 - mm/oom_kill.c
bool out_of_memory(struct oom_control *oc)
{
    unsigned long freed = 0;

    // 如果OOM killer被禁用
    if (oom_killer_disabled)
        return false;

    // 检查是否是内存cgroup OOM
    if (!is_memcg_oom(oc)) {
        // 调用通知链，让其他模块尝试释放内存
        blocking_notifier_call_chain(&oom_notify_list, 0, &freed);
        if (freed > 0 && !is_sysrq_oom(oc))
            return true;
    }

    // 检查当前进程是否正在退出
    if (task_will_free_mem(current)) {
        mark_oom_victim(current);
        queue_oom_reaper(current);
        return true;
    }

    // 检查文件系统分配
    if (!(oc->gfp_mask & __GFP_FS) && !is_memcg_oom(oc))
        return true;

    // 确定分配约束
    oc->constraint = constrained_alloc(oc);
    if (oc->constraint != CONSTRAINT_MEMORY_POLICY)
        oc->nodemask = NULL;
    
    check_panic_on_oom(oc);

    // 选择要杀死的进程
    select_bad_process(oc);
    
    // 如果没有找到合适的进程
    if (!oc->chosen) {
        dump_header(oc);
        pr_warn("Out of memory and no killable processes...\n");
        if (!is_sysrq_oom(oc) && !is_memcg_oom(oc))
            panic("System is deadlocked on memory\n");
    }
    
    // 执行杀死操作
    if (oc->chosen && oc->chosen != (void *)-1UL)
        oom_kill_process(oc, !is_memcg_oom(oc) ? "Out of memory" :
                        "Memory cgroup out of memory");
    
    return !!oc->chosen;
}
```

### OOM调整值和优先杀死机制深度分析

#### OOM调整值常量定义

```c
// OOM调整值常量定义 - include/uapi/linux/oom.h

/*
 * /proc/<pid>/oom_score_adj 设置为 OOM_SCORE_ADJ_MIN 将禁用
 * pid 的 OOM 杀死功能
 */
#define OOM_SCORE_ADJ_MIN	(-1000)    // 最小调整值（完全保护）
#define OOM_SCORE_ADJ_MAX	1000       // 最大调整值（最优先杀死）

/*
 * /proc/<pid>/oom_adj 设置为 -17 为传统目的保护免受 OOM 杀死
 */
#define OOM_DISABLE     (-17)          // 传统禁用值
#define OOM_ADJUST_MIN  (-16)          // 传统最小调整值
#define OOM_ADJUST_MAX  15             // 传统最大调整值
```

#### OOM调整值设置机制

```c
// OOM调整值设置函数 - fs/proc/base.c

static int __set_oom_adj(struct file *file, int oom_adj, bool legacy)
{
    struct mm_struct *mm = NULL;
    struct task_struct *task;
    int err = 0;

    task = get_proc_task(file_inode(file));
    if (!task)
        return -ESRCH;

    mutex_lock(&oom_adj_mutex);
    
    if (legacy) {
        // 处理传统 /proc/pid/oom_adj 接口
        if (oom_adj < task->signal->oom_score_adj &&
            !capable(CAP_SYS_RESOURCE)) {
            err = -EACCES;
            goto err_unlock;
        }
        
        /*
         * 警告用户使用新的接口
         * /proc/pid/oom_adj 已弃用，请使用 /proc/pid/oom_score_adj
         */
        pr_warn_once("%s (%d): /proc/%d/oom_adj is deprecated, "
                    "please use /proc/%d/oom_score_adj instead.\n",
                    current->comm, task_pid_nr(current), 
                    task_pid_nr(task), task_pid_nr(task));
    } else {
        // 处理现代 /proc/pid/oom_score_adj 接口
        if ((short)oom_adj < task->signal->oom_score_adj_min &&
            !capable(CAP_SYS_RESOURCE)) {
            err = -EACCES;
            goto err_unlock;
        }
    }

    /*
     * 确保检查共享mm的其他进程（如果这不是vfork想要自己的oom_score_adj的话）
     * 固定mm，防止它消失并在task_unlock后重用
     */
    if (!task->vfork_done) {
        struct task_struct *p = find_lock_task_mm(task);

        if (p) {
            if (test_bit(MMF_MULTIPROCESS, &p->mm->flags)) {
                mm = p->mm;
                mmgrab(mm);
            }
            task_unlock(p);
        }
    }

    // 设置OOM调整值
    task->signal->oom_score_adj = oom_adj;
    if (!legacy && has_capability_noaudit(current, CAP_SYS_RESOURCE))
        task->signal->oom_score_adj_min = (short)oom_adj;
    
    trace_oom_score_adj_update(task);

    // 如果有共享内存，更新所有相关进程
    if (mm) {
        struct task_struct *p;

        rcu_read_lock();
        for_each_process(p) {
            if (same_thread_group(task, p))
                continue;

            // 不触及内核线程或全局init
            if (p->flags & PF_KTHREAD || is_global_init(p))
                continue;

            task_lock(p);
            if (!p->vfork_done && process_shares_mm(p, mm)) {
                p->signal->oom_score_adj = oom_adj;
                if (!legacy && has_capability_noaudit(current, CAP_SYS_RESOURCE))
                    p->signal->oom_score_adj_min = (short)oom_adj;
            }
            task_unlock(p);
        }
        rcu_read_unlock();
        mmdrop(mm);
    }
    
err_unlock:
    mutex_unlock(&oom_adj_mutex);
    put_task_struct(task);
    return err;
}

// 读取OOM调整值
static ssize_t oom_score_adj_read(struct file *file, char __user *buf,
                                 size_t count, loff_t *ppos)
{
    struct task_struct *task = get_proc_task(file_inode(file));
    char buffer[PROC_NUMBUF];
    short oom_score_adj = OOM_SCORE_ADJ_MIN;
    size_t len;

    if (!task)
        return -ESRCH;
    oom_score_adj = task->signal->oom_score_adj;
    put_task_struct(task);
    len = snprintf(buffer, sizeof(buffer), "%hd\n", oom_score_adj);
    return simple_read_from_buffer(buf, count, ppos, buffer, len);
}

// 写入OOM调整值
static ssize_t oom_score_adj_write(struct file *file, const char __user *buf,
                                  size_t count, loff_t *ppos)
{
    char buffer[PROC_NUMBUF] = {};
    int oom_score_adj;
    int err;

    if (count > sizeof(buffer) - 1)
        count = sizeof(buffer) - 1;
    if (copy_from_user(buffer, buf, count)) {
        err = -EFAULT;
        goto out;
    }

    err = kstrtoint(strstrip(buffer), 0, &oom_score_adj);
    if (err)
        goto out;
    
    // 验证调整值范围
    if (oom_score_adj < OOM_SCORE_ADJ_MIN ||
        oom_score_adj > OOM_SCORE_ADJ_MAX) {
        err = -EINVAL;
        goto out;
    }

    err = __set_oom_adj(file, oom_score_adj, false);
out:
    return err < 0 ? err : count;
}
```

#### 进程不可杀死标志设置

```c
// 检查进程是否不可杀死 - mm/oom_kill.c

static bool oom_unkillable_task(struct task_struct *p)
{
    // 内核线程不可杀死
    if (is_global_init(p))
        return true;
    
    // PID为1的init进程不可杀死
    if (p->flags & PF_KTHREAD)
        return true;
    
    // 检查OOM调整值是否设置为最小值（完全保护）
    if (p->signal->oom_score_adj == OOM_SCORE_ADJ_MIN)
        return true;
    
    return false;
}

// 标记进程为OOM受害者
static void mark_oom_victim(struct task_struct *tsk)
{
    struct mm_struct *mm = tsk->mm;

    WARN_ON(oom_killer_disabled);
    
    // 设置TIF_MEMDIE标志，给予内存访问特权
    set_tsk_thread_flag(tsk, TIF_MEMDIE);
    
    // 原子地设置oom_mm
    if (cmpxchg(&tsk->signal->oom_mm, NULL, mm) == NULL) {
        mmgrab(mm);
        set_bit(MMF_OOM_VICTIM, &mm->flags);
    }
    
    /*
     * 确保被杀死的任务具有内存访问权限，这样它就能快速退出
     */
    if (!test_and_set_tsk_thread_flag(tsk, TIF_MEMDIE))
        atomic_inc(&oom_victims);
}

// 检查任务是否是OOM受害者
static inline bool tsk_is_oom_victim(struct task_struct *tsk)
{
    return tsk->signal->oom_mm;
}

// OOM免疫设置函数
static inline void set_current_oom_origin(void)
{
    current->signal->oom_flag_origin = true;
}

static inline void clear_current_oom_origin(void)
{
    current->signal->oom_flag_origin = false;
}

static inline bool oom_task_origin(const struct task_struct *p)
{
    return p->signal->oom_flag_origin;
}
```

#### OOM调整值的实际应用

```mermaid
graph **TD**
    A[**进程创建**] --> B[**继承父进程调整值**]
    B --> C{**用户设置调整值？**}
    
    C -->|**是**| D[**验证权限**]
    C -->|**否**| E[**使用默认值(0)**]
    
    D --> F{**有CAP_SYS_RESOURCE？**}
    F -->|**是**| G[**允许任意调整**]
    F -->|**否**| H[**只能增加易受攻击性**]
    
    G --> I[**设置oom_score_adj**]
    H --> I
    E --> I
    
    I --> J[**内存压力触发OOM**]
    J --> K[**遍历所有进程**]
    
    K --> L[**计算OOM分数**]
    L --> M{**调整值检查**}
    
    M -->|**-1000**| N[**完全保护<br/>跳过此进程**]
    M -->|**0**| O[**正常评分**]
    M -->|**+1000**| P[**最高优先级杀死**]
    
    O --> Q[**基础分数 + 调整值**]
    P --> Q
    N --> R[**检查下一个进程**]
    
    Q --> S{**最高分数？**}
    S -->|**是**| T[**选为受害者**]
    S -->|**否**| R
    
    T --> U[**发送SIGKILL信号**]
    U --> V[**标记为OOM受害者**]
    V --> W[**OOM Reaper回收内存**]
    
    style A fill:**#e3f2fd**
    style I fill:**#e8f5e8**
    style T fill:**#ffebee**
    style W fill:**#f3e5f5**
```

#### OOM调整值的管理命令和最佳实践

```bash
# 1. 查看进程的OOM调整值
cat /proc/<pid>/oom_score_adj
cat /proc/<pid>/oom_score        # 当前OOM分数（只读）

# 2. 设置进程完全免疫OOM killer
echo -1000 > /proc/<pid>/oom_score_adj

# 3. 设置进程为高优先级杀死目标
echo 1000 > /proc/<pid>/oom_score_adj

# 4. 恢复默认设置
echo 0 > /proc/<pid>/oom_score_adj

# 5. 批量查看系统中所有进程的OOM分数
for pid in /proc/[0-9]*; do
    [ -r "$pid/oom_score_adj" ] && \
    printf "PID: %s, OOM_ADJ: %s, OOM_SCORE: %s, CMD: %s\n" \
        $(basename $pid) \
        $(cat $pid/oom_score_adj 2>/dev/null) \
        $(cat $pid/oom_score 2>/dev/null) \
        "$(cat $pid/comm 2>/dev/null)"
done | sort -k4,4nr

# 6. 系统配置脚本示例
#!/bin/bash
# 保护关键系统服务
protect_service() {
    local service_name=$1
    local pids=$(pgrep "$service_name")
    
    for pid in $pids; do
        if [ -w "/proc/$pid/oom_score_adj" ]; then
            echo -1000 > "/proc/$pid/oom_score_adj"
            echo "Protected $service_name (PID: $pid) from OOM killer"
        fi
    done
}

# 保护重要服务
protect_service "sshd"
protect_service "systemd"
protect_service "kthreadd"

# 7. 监控OOM事件
#!/bin/bash
# OOM事件监控脚本
tail -f /var/log/kern.log | grep -i "killed process" | while read line; do
    echo "$(date): $line" >> /var/log/oom_events.log
    # 可以添加告警机制
    # send_alert "OOM event detected: $line"
done
```

#### OOM调整值的默认值和限制

| **调整值范围** | **含义** | **用途** | **权限要求** |
|---------------|---------|---------|-------------|
| **-1000** | **完全保护** | 关键系统进程<br/>数据库主进程 | **CAP_SYS_RESOURCE** |
| **-999 to -1** | **高度保护** | 重要服务进程<br/>监控进程 | **CAP_SYS_RESOURCE** |
| **0** | **默认行为** | 普通应用进程<br/>用户进程 | **无特殊要求** |
| **1 to 999** | **提高易受攻击性** | 临时进程<br/>测试程序 | **进程所有者** |
| **1000** | **最优先杀死** | 内存泄漏进程<br/>有问题的应用 | **进程所有者** |

### 进程不可杀死标志深度解析

#### 不可杀死进程的分类和机制

Linux系统中有多种机制来标记进程为不可杀死，以保护关键系统进程和服务：

```c
// 进程不可杀死检查的完整逻辑 - mm/oom_kill.c

static bool oom_unkillable_task(struct task_struct *p)
{
    // 1. 全局init进程(PID 1)永远不可杀死
    if (is_global_init(p))
        return true;
    
    // 2. 内核线程不可杀死
    if (p->flags & PF_KTHREAD)
        return true;
    
    // 3. 检查OOM调整值是否设置为完全保护
    if (p->signal->oom_score_adj == OOM_SCORE_ADJ_MIN)
        return true;
    
    return false;
}
```

#### 设置不可杀死进程的方法

```bash
# 方法1: 通过procfs设置完全保护
echo -1000 > /proc/<pid>/oom_score_adj

# 方法2: 检查进程当前保护状态
cat /proc/<pid>/oom_score_adj
cat /proc/<pid>/oom_score

# 方法3: 批量保护重要服务
for service in sshd systemd dbus; do
    pgrep "$service" | while read pid; do
        echo -1000 > "/proc/$pid/oom_score_adj" 2>/dev/null
    done
done
```

### 进程选择算法

```c
// OOM badness评分算法
long oom_badness(struct task_struct *p, unsigned long totalpages)
{
    long points;
    long adj;

    // 检查进程是否不可杀死
    if (oom_unkillable_task(p))
        return LONG_MIN;

    p = find_lock_task_mm(p);
    if (!p)
        return LONG_MIN;

    // 检查OOM调整值
    adj = (long)p->signal->oom_score_adj;
    if (adj == OOM_SCORE_ADJ_MIN ||
        test_bit(MMF_OOM_SKIP, &p->mm->flags) ||
        in_vfork(p)) {
        task_unlock(p);
        return LONG_MIN;
    }

    // 计算基础分数：RSS + 交换 + 页表
    points = get_mm_rss(p->mm) + get_mm_counter(p->mm, MM_SWAPENTS) +
             mm_pgtables_bytes(p->mm) / PAGE_SIZE;
    task_unlock(p);

    // 应用OOM调整值：调整值按比例缩放到总页数
    adj *= totalpages / 1000;
    points += adj;

    /*
     * 绝不返回0或负值，除非进程应该被忽略。
     * 0分会使算法难以选择好的候选者
     */
    return points > 0 ? points : 1;
}

### OOM受害者判断机制详解

#### 受害者选择的完整流程

Linux的OOM killer使用多阶段的算法来选择最合适的受害者进程：

```c
// 选择最坏进程的完整实现 - mm/oom_kill.c
static void select_bad_process(struct oom_control *oc)
{
    oc->chosen_points = LONG_MIN;

    if (is_memcg_oom(oc))
        mem_cgroup_scan_tasks(oc->memcg, oom_evaluate_task, oc);
    else {
        struct task_struct *p;
        
        rcu_read_lock();
        for_each_process(p)
            if (oom_evaluate_task(p, oc))
                break;
        rcu_read_unlock();
    }
}

// 评估单个任务是否适合作为OOM受害者
static int oom_evaluate_task(struct task_struct *task, void *arg)
{
    struct oom_control *oc = arg;
    long points;

    // 第1步：检查任务是否不可杀死
    if (oom_unkillable_task(task))
        goto next;

    // 第2步：检查cpuset和内存策略约束
    if (!is_memcg_oom(oc) && !oom_cpuset_eligible(task, oc))
        goto next;

    // 第3步：检查是否已经是OOM受害者
    if (!is_sysrq_oom(oc) && tsk_is_oom_victim(task)) {
        // 如果已经被标记为跳过，移到下一个
        if (test_bit(MMF_OOM_SKIP, &task->signal->oom_mm->flags))
            goto next;
        // 如果已经是受害者，停止扫描
        goto abort;
    }

    // 第4步：检查是否标记为优先杀死（OOM origin）
    if (oom_task_origin(task)) {
        points = LONG_MAX;
        goto select;
    }

    // 第5步：计算badness分数
    points = oom_badness(task, oc->totalpages);
    if (points == LONG_MIN || points < oc->chosen_points)
        goto next;

select:
    // 选择当前任务作为候选受害者
    if (oc->chosen)
        put_task_struct(oc->chosen);
    get_task_struct(task);
    oc->chosen = task;
    oc->chosen_points = points;
next:
    return 0;
abort:
    // 发现已存在受害者，停止搜索
    if (oc->chosen)
        put_task_struct(oc->chosen);
    oc->chosen = (void *)-1UL;
    return 1;
}
```

#### 受害者适格性检查机制

```c
// cpuset适格性检查 - mm/oom_kill.c
static bool oom_cpuset_eligible(struct task_struct *p, struct oom_control *oc)
{
    // 检查任务是否在允许的cpuset中
    return cpuset_mems_allowed_intersects(oc->zonelist, oc->nodemask) &&
           task_in_mem_cgroup(p, oc->memcg);
}

// 内存cgroup约束检查
static bool task_in_mem_cgroup(struct task_struct *task, struct mem_cgroup *memcg)
{
    if (!memcg)
        return true;  // 全局OOM，所有任务都符合条件
        
    return mem_cgroup_from_task(task) == memcg;
}

// 检查任务是否会释放内存
bool task_will_free_mem(struct task_struct *task)
{
    struct mm_struct *mm = task->mm;
    struct task_struct *p;
    bool ret = true;

    // 检查任务是否正在退出
    if (task_is_dying(task))
        return false;

    if (!mm)
        return false;

    // 检查是否有待处理的SIGKILL
    if (sigismember(&task->pending.signal, SIGKILL))
        return true;

    // 检查共享内存的其他进程
    rcu_read_lock();
    for_each_process(p) {
        if (!process_shares_mm(p, mm))
            continue;
        if (!(p->flags & PF_KTHREAD) && 
            !sigismember(&p->pending.signal, SIGKILL)) {
            ret = false;
            break;
        }
    }
    rcu_read_unlock();

    return ret;
}
```

#### OOM受害者选择的决策树

```mermaid
graph **TD**
    A[**开始扫描进程**] --> B[**遍历进程列表**]
    
    B --> C{**进程是否不可杀死？**}
    C -->|**是**| D[**跳过此进程**]
    C -->|**否**| E{**符合cpuset约束？**}
    
    E -->|**否**| D
    E -->|**是**| F{**已是OOM受害者？**}
    
    F -->|**是，正在退出**| G[**停止扫描**]
    F -->|**是，已跳过**| D
    F -->|**否**| H{**标记为OOM origin？**}
    
    H -->|**是**| I[**设置最高优先级<br/>points = LONG_MAX**]
    H -->|**否**| J[**计算badness分数**]
    
    I --> K{**分数最高？**}
    J --> K
    
    K -->|**是**| L[**选为候选受害者**]
    K -->|**否**| D
    
    L --> M{**还有进程？**}
    D --> M
    G --> N[**返回选中的受害者**]
    
    M -->|**是**| B
    M -->|**否**| N
    
    style A fill:**#e3f2fd**
    style I fill:**#ffebee**
    style L fill:**#fff3e0**
    style N fill:**#f3e5f5**
```

#### 特殊情况处理

```c
// 处理特殊的OOM情况
static bool handle_special_oom_cases(struct oom_control *oc)
{
    // 情况1: 当前任务正在退出且会释放内存
    if (task_will_free_mem(current)) {
        mark_oom_victim(current);
        queue_oom_reaper(current);
        return true;
    }

    // 情况2: 系统范围的panic_on_oom设置
    if (sysctl_panic_on_oom == 2) {
        panic("Out of memory: system-wide panic_on_oom is enabled\n");
        return true;
    }

    // 情况3: 受限的panic_on_oom（仅当无节点掩码约束时）
    if (sysctl_panic_on_oom == 1 && oc->constraint == CONSTRAINT_NONE) {
        panic("Out of memory: panic_on_oom is enabled\n");
        return true;
    }

    // 情况4: 内存cgroup OOM的特殊处理
    if (is_memcg_oom(oc)) {
        // 在内存cgroup中寻找合适的受害者
        mem_cgroup_scan_tasks(oc->memcg, oom_evaluate_task, oc);
        return false;
    }

    return false;
}

// 约束类型确定
static enum oom_constraint constrained_alloc(struct oom_control *oc)
{
    struct zone *zone;
    struct zoneref *z;
    enum zone_type highest_zoneidx = gfp_zone(oc->gfp_mask);
    bool cpuset_limited = false;
    int nid;

    // 检查cpuset约束
    if (oc->zonelist) {
        for_each_zone_zonelist_nodemask(zone, z, oc->zonelist,
                                       highest_zoneidx, oc->nodemask) {
            if (!cpuset_zone_allowed(zone, oc->gfp_mask))
                cpuset_limited = true;
        }
    }

    if (cpuset_limited)
        return CONSTRAINT_CPUSET;

    // 检查内存策略约束
    if (oc->nodemask &&
        !nodes_subset(node_states[N_MEMORY], *oc->nodemask)) {
        return CONSTRAINT_MEMORY_POLICY;
    }

    // 检查内存cgroup约束
    if (oc->memcg && !is_memcg_oom(oc))
        return CONSTRAINT_MEMCG;

    return CONSTRAINT_NONE;
}
```

#### 受害者确认和后续处理

```c
// 确认并处理选定的受害者
static bool oom_kill_process(struct oom_control *oc, const char *message)
{
    struct task_struct *victim = oc->chosen;
    struct task_struct *p;
    struct mm_struct *mm;
    unsigned int victim_points;
    static DEFINE_RATELIMIT_STATE(oom_rs, DEFAULT_RATELIMIT_INTERVAL,
                                 DEFAULT_RATELIMIT_BURST);
    bool can_oom_reap = true;

    // 检查受害者是否仍然有效
    p = find_lock_task_mm(victim);
    if (!p) {
        put_task_struct(victim);
        return false;
    } else if (victim != p) {
        get_task_struct(p);
        put_task_struct(victim);
        victim = p;
    }

    // 记录OOM事件
    victim_points = oom_badness(victim, oc->totalpages);
    
    if (__ratelimit(&oom_rs)) {
        dump_header(oc);
        
        pr_err("%s: Killed process %d (%s) total-vm:%lukB, "
               "anon-rss:%lukB, file-rss:%lukB, shmem-rss:%lukB, "
               "UID:%u pgtables:%lukB oom_score_adj:%hd\n",
               message, task_pid_nr(victim), victim->comm,
               K(mm->total_vm),
               K(get_mm_counter(mm, MM_ANONPAGES)),
               K(get_mm_counter(mm, MM_FILEPAGES)),
               K(get_mm_counter(mm, MM_SHMEMPAGES)),
               from_kuid(&init_user_ns, task_uid(victim)),
               mm_pgtables_bytes(mm) >> 10,
               victim->signal->oom_score_adj);
    }

    // 执行实际的杀死操作
    __oom_kill_process(victim, message);
    
    return true;
}
```

通过这套完整的受害者判断机制，Linux OOM killer能够在系统内存不足时做出明智的决定，平衡系统稳定性和进程保护的需求。

### 完整的OOM分数计算规则和时序图

#### OOM分数计算的数学模型

Linux OOM killer的核心是badness分数计算算法，它基于内存使用量和调整值来评估进程：

```c
// OOM badness分数计算的完整实现 - mm/oom_kill.c

long oom_badness(struct task_struct *p, unsigned long totalpages)
{
    long points;
    long adj;

    // 第1步：基础合法性检查
    if (oom_unkillable_task(p))
        return LONG_MIN;

    p = find_lock_task_mm(p);
    if (!p)
        return LONG_MIN;

    // 第2步：获取OOM调整值
    adj = (long)p->signal->oom_score_adj;
    
    // 特殊情况：完全保护或需要跳过的进程
    if (adj == OOM_SCORE_ADJ_MIN ||
        test_bit(MMF_OOM_SKIP, &p->mm->flags) ||
        in_vfork(p)) {
        task_unlock(p);
        return LONG_MIN;
    }

    // 第3步：计算基础分数（内存使用量）
    /*
     * 基础分数 = RSS + 交换空间使用 + 页表占用
     * RSS (Resident Set Size): 进程实际占用的物理内存
     * 交换空间: 被交换到磁盘的内存页数
     * 页表: 进程页表结构占用的内存
     */
    points = get_mm_rss(p->mm) +                      // RSS内存
             get_mm_counter(p->mm, MM_SWAPENTS) +      // 交换页数
             mm_pgtables_bytes(p->mm) / PAGE_SIZE;     // 页表大小转换为页数
    
    task_unlock(p);

    // 第4步：应用OOM调整值
    /*
     * 调整值计算公式：
     * adjusted_score = base_points + (adj * totalpages / 1000)
     * 
     * 其中：
     * - base_points: 基础分数（内存使用量）
     * - adj: OOM调整值 (-1000 到 +1000)
     * - totalpages: 系统总页数
     * 
     * 调整值的影响：
     * - adj = -1000: 完全保护（已在前面处理）
     * - adj = -500:  减少50%的被选中概率
     * - adj = 0:     无调整，使用原始分数
     * - adj = +500:  增加50%的被选中概率
     * - adj = +1000: 最高被选中概率
     */
    adj *= totalpages / 1000;
    points += adj;

    /*
     * 第5步：分数标准化
     * 确保返回值为正数，便于比较
     * 0分会使选择算法困难，至少返回1
     */
    return points > 0 ? points : 1;
}

// 辅助函数：获取进程RSS内存统计
static inline unsigned long get_mm_rss(struct mm_struct *mm)
{
    return get_mm_counter(mm, MM_FILEPAGES) +    // 文件页缓存
           get_mm_counter(mm, MM_ANONPAGES) +    // 匿名页（堆、栈等）
           get_mm_counter(mm, MM_SHMEMPAGES);    // 共享内存页
}
```

#### OOM分数计算的详细步骤分解

```mermaid
graph **TD**
    A[**开始分数计算**] --> B[**检查进程有效性**]
    
    B --> C{**进程可杀死？**}
    C -->|**否**| D[**返回 LONG_MIN<br/>（不可选择）**]
    C -->|**是**| E[**获取OOM调整值**]
    
    E --> F{**调整值检查**}
    F -->|**-1000**| D
    F -->|**其他**| G[**计算基础分数**]
    
    G --> H[**RSS内存统计**]
    H --> I[**文件页 + 匿名页 + 共享页**]
    
    I --> J[**添加交换空间使用**]
    J --> K[**添加页表占用**]
    
    K --> L[**基础分数 = RSS + SWAP + 页表**]
    L --> M[**应用OOM调整值**]
    
    M --> N[**调整分数 = 基础分数 + (adj × 总页数 ÷ 1000)**]
    N --> O{**分数 > 0？**}
    
    O -->|**是**| P[**返回计算分数**]
    O -->|**否**| Q[**返回 1<br/>（最小有效分数）**]
    
    style A fill:**#e3f2fd**
    style G fill:**#e8f5e8**
    style N fill:**#fff3e0**
    style P fill:**#f3e5f5**
```

#### OOM分数影响因素分析

```c
// 影响OOM分数的关键因素分析

/*
 * 1. 内存使用量因素 (占主要权重)
 */
struct memory_usage_factors {
    unsigned long anon_pages;    // 匿名页：堆、栈、mmap匿名映射
    unsigned long file_pages;    // 文件页：可执行文件、库文件、文件缓存
    unsigned long shmem_pages;   // 共享内存：SysV共享内存、tmpfs
    unsigned long swap_pages;    // 交换页：被换出的页面数量
    unsigned long pgtable_bytes; // 页表：进程地址空间管理开销
};

/*
 * 2. OOM调整值影响示例计算
 */
static long demonstrate_adj_impact(unsigned long base_score, 
                                  short oom_score_adj, 
                                  unsigned long total_pages)
{
    long adj_impact = ((long)oom_score_adj * total_pages) / 1000;
    long final_score = base_score + adj_impact;
    
    printk(KERN_INFO "OOM Score Calculation:\n");
    printk(KERN_INFO "  Base score (memory usage): %lu\n", base_score);
    printk(KERN_INFO "  OOM adjustment: %hd\n", oom_score_adj);
    printk(KERN_INFO "  Adjustment impact: %ld\n", adj_impact);
    printk(KERN_INFO "  Final score: %ld\n", final_score);
    
    return final_score;
}

/*
 * 3. 特殊情况处理
 */
static inline bool is_special_case(struct task_struct *p)
{
    // vfork中的进程（父子共享地址空间）
    if (in_vfork(p))
        return true;
    
    // 已经被标记为跳过的进程
    if (p->mm && test_bit(MMF_OOM_SKIP, &p->mm->flags))
        return true;
    
    // 正在进行coredump的进程
    if (p->signal->flags & SIGNAL_GROUP_COREDUMP)
        return true;
    
    return false;
}
```

#### 完整的OOM处理时序图

```mermaid
sequenceDiagram
    participant **MM** as **内存管理子系统**
    participant **OOM** as **OOM Killer**
    participant **PROC** as **进程管理**
    participant **REAPER** as **OOM Reaper**
    participant **LOG** as **系统日志**
    
    Note over **MM**,**LOG**: **内存不足触发OOM的完整处理流程**
    
    **MM**->>**OOM**: **内存分配失败，触发out_of_memory()**
    activate **OOM**
    
    **OOM**->>**OOM**: **检查OOM killer是否被禁用**
    **OOM**->>**OOM**: **调用通知链，尝试释放内存**
    
    alt **有内存被释放**
        **OOM**-->>**MM**: **返回true，重试分配**
        deactivate **OOM**
    else **没有内存释放**
        **OOM**->>**OOM**: **检查当前进程是否正在退出**
        
        alt **当前进程正在退出**
            **OOM**->>**PROC**: **mark_oom_victim(current)**
            **OOM**->>**REAPER**: **queue_oom_reaper(current)**
            **OOM**-->>**MM**: **返回true**
            deactivate **OOM**
        else **需要选择受害者**
            **OOM**->>**OOM**: **确定约束类型 (NUMA/cpuset/memcg)**
            **OOM**->>**OOM**: **开始扫描所有进程**
            
            loop **遍历每个进程**
                **OOM**->>**PROC**: **检查进程 p**
                **PROC**-->>**OOM**: **进程信息**
                
                **OOM**->>**OOM**: **oom_unkillable_task(p)**
                
                alt **进程不可杀死**
                    **OOM**->>**OOM**: **跳过此进程**
                else **进程可以评估**
                    **OOM**->>**OOM**: **检查cpuset/cgroup约束**
                    
                    alt **不符合约束**
                        **OOM**->>**OOM**: **跳过此进程**
                    else **符合约束**
                        **OOM**->>**OOM**: **计算oom_badness分数**
                        
                        Note right of **OOM**: **基础分数 = RSS + SWAP + 页表<br/>调整分数 = 基础 + (adj × 总页数 ÷ 1000)**
                        
                        alt **分数最高**
                            **OOM**->>**OOM**: **选为候选受害者**
                        else **分数较低**
                            **OOM**->>**OOM**: **继续寻找**
                        end
                    end
                end
            end
            
            alt **找到受害者**
                **OOM**->>**LOG**: **dump_header() 记录OOM信息**
                **OOM**->>**PROC**: **__oom_kill_process(victim)**
                **PROC**->>**PROC**: **发送SIGKILL信号**
                **PROC**->>**PROC**: **mark_oom_victim(victim)**
                **OOM**->>**REAPER**: **queue_oom_reaper(victim)**
                
                **LOG**-->>**OOM**: **记录受害者信息**
                **OOM**-->>**MM**: **返回true**
                deactivate **OOM**
                
                **REAPER**->>**REAPER**: **异步回收受害者内存**
                **REAPER**->>**PROC**: **oom_reap_task()**
                **REAPER**->>**MM**: **释放页面到伙伴系统**
            else **没有找到合适的受害者**
                **OOM**->>**LOG**: **记录无可杀死进程**
                
                alt **系统配置为panic_on_oom**
                    **OOM**->>**OOM**: **panic("System is deadlocked")**
                else **继续运行**
                    **OOM**-->>**MM**: **返回false**
                    deactivate **OOM**
                end
            end
        end
    end
```

#### OOM分数计算的实际案例分析

```c
// 实际的OOM分数计算示例

/*
 * 案例1: 正常Web服务器进程
 * 假设系统总内存: 16GB (约4,194,304页，4KB/页)
 */
struct oom_case_study {
    char process_name[16];
    unsigned long rss_pages;      // RSS页数
    unsigned long swap_pages;     // 交换页数  
    unsigned long pgtable_pages;  // 页表页数
    short oom_score_adj;          // OOM调整值
    long calculated_score;        // 计算得出的分数
};

// 案例分析
static void analyze_oom_cases(void)
{
    unsigned long total_pages = 4194304; // 16GB / 4KB
    
    struct oom_case_study cases[] = {
        // 案例1: 普通web服务器 (使用2GB内存)
        {
            .process_name = "nginx",
            .rss_pages = 524288,      // 2GB
            .swap_pages = 0,          // 无交换
            .pgtable_pages = 1024,    // 4MB页表
            .oom_score_adj = 0,       // 默认调整
            .calculated_score = 525312 + 0 = 525312
        },
        
        // 案例2: 内存泄漏的应用 (使用12GB内存)
        {
            .process_name = "memory_hog",
            .rss_pages = 3145728,     // 12GB
            .swap_pages = 262144,     // 1GB交换
            .pgtable_pages = 8192,    // 32MB页表
            .oom_score_adj = 0,       // 默认调整
            .calculated_score = 3416064
        },
        
        // 案例3: 重要数据库 (使用8GB内存，但受保护)
        {
            .process_name = "mysql",
            .rss_pages = 2097152,     // 8GB
            .swap_pages = 0,          // 无交换
            .pgtable_pages = 4096,    // 16MB页表
            .oom_score_adj = -900,    // 高度保护
            .calculated_score = 2101248 + (-900 * 4194304 / 1000) = -1573625
        },
        
        // 案例4: 临时测试进程 (优先被杀死)
        {
            .process_name = "test_app",
            .rss_pages = 262144,      // 1GB
            .swap_pages = 0,          // 无交换
            .pgtable_pages = 512,     // 2MB页表
            .oom_score_adj = 500,     // 提高被杀死优先级
            .calculated_score = 262656 + (500 * 4194304 / 1000) = 2359808
        }
    };
    
    printk(KERN_INFO "OOM Score Analysis Results:\n");
    printk(KERN_INFO "========================================\n");
    
    for (int i = 0; i < ARRAY_SIZE(cases); i++) {
        struct oom_case_study *c = &cases[i];
        long base_score = c->rss_pages + c->swap_pages + c->pgtable_pages;
        long adj_impact = (c->oom_score_adj * total_pages) / 1000;
        
        printk(KERN_INFO "Process: %s\n", c->process_name);
        printk(KERN_INFO "  Memory usage: %lu MB\n", 
               (c->rss_pages * 4) / 1024);
        printk(KERN_INFO "  Base score: %ld\n", base_score);
        printk(KERN_INFO "  Adjustment impact: %ld\n", adj_impact);
        printk(KERN_INFO "  Final score: %ld\n", c->calculated_score);
        printk(KERN_INFO "  Selection priority: %s\n",
               c->calculated_score < 0 ? "Protected" :
               c->calculated_score > 2000000 ? "High" : "Normal");
        printk(KERN_INFO "----------------------------------------\n");
    }
    
    /*
     * 结论：
     * 1. memory_hog (分数: 3,416,064) - 最可能被选中
     * 2. test_app (分数: 2,359,808) - 次高优先级
     * 3. nginx (分数: 525,312) - 正常优先级
     * 4. mysql (分数: -1,573,625) - 受保护，不会被选中
     */
}
```

通过这套精确的分数计算规则和完整的处理流程，Linux OOM killer能够在系统内存极度紧张时，智能地选择最合适的进程作为受害者，最大化系统的整体稳定性和可用性。

### 页面迁移机制深度分析

#### 页面迁移的核心动机

页面迁移是Linux内存管理中的关键技术，它能够在不改变进程虚拟地址的情况下，将页面的物理位置从一个地方移动到另一个地方。这项技术解决了多个关键问题：

```c
// 页面迁移的主要应用场景 - mm/migrate.c

/*
 * 页面迁移在以下场景中发挥重要作用：
 * 1. NUMA优化：将页面移动到访问它们的CPU附近
 * 2. 内存压缩：为大页分配创建连续的物理内存空间  
 * 3. 内存热插拔：支持物理内存的动态添加和移除
 * 4. CMA (Contiguous Memory Allocation)：为设备提供连续内存
 * 5. 内存故障处理：处理硬件内存错误
 * 6. 内存层级管理：在不同类型的内存间移动数据
 */

// 页面迁移的触发条件判断
enum migrate_reason {
    MR_COMPACTION,          // 内存压缩触发的迁移
    MR_MEMORY_FAILURE,      // 内存故障恢复
    MR_MEMORY_HOTPLUG,      // 内存热插拔
    MR_SYSCALL,            // 系统调用触发
    MR_MEMPOLICY_MBIND,    // 内存策略绑定
    MR_NUMA_MISPLACED,     // NUMA位置错误
    MR_CMA,                // CMA分配需求
    MR_MEMORY_TIERS,       // 内存层级间迁移
    MR_TYPES
};

// 页面可迁移性检查
static inline bool __PageMovable(struct page *page)
{
    return ((unsigned long)page->mapping & PAGE_MAPPING_MOVABLE) != 0;
}

// 页面迁移操作的基础检查
static bool page_migratable(struct page *page)
{
    // 检查页面是否可以迁移
    if (!PageMovable(page))
        return false;
    
    // 检查页面是否被锁定
    if (PageMlocked(page))
        return false;
    
    // 检查页面映射状态
    struct address_space *mapping = page_mapping(page);
    if (mapping && mapping->a_ops->migratepage)
        return true;
    
    // 匿名页面通常可以迁移
    if (PageAnon(page))
        return true;
    
    return false;
}
```

#### 页面迁移解决的关键问题

```mermaid
graph **TD**
    A[**内存管理问题**] --> B[**NUMA访问局部性问题**]
    A --> C[**内存碎片化问题**] 
    A --> D[**内存热插拔需求**]
    A --> E[**硬件故障恢复**]
    A --> F[**设备连续内存需求**]
    
    B --> G[**页面迁移解决方案**]
    C --> G
    D --> G
    E --> G
    F --> G
    
    G --> H[**NUMA平衡迁移**]
    G --> I[**内存压缩迁移**]
    G --> J[**热插拔迁移**]
    G --> K[**故障隔离迁移**]
    G --> L[**CMA连续分配**]
    
    H --> M[**提升访问性能<br/>减少跨节点开销**]
    I --> N[**创建连续物理空间<br/>支持大页分配**]
    J --> O[**支持动态内存管理<br/>提升硬件灵活性**]
    K --> P[**隔离故障内存<br/>保证系统稳定性**]
    L --> Q[**满足设备DMA需求<br/>提供连续内存块**]
    
    style A fill:**#ffebee**
    style G fill:**#e8f5e8**
    style M fill:**#e3f2fd**
    style N fill:**#e3f2fd**
    style O fill:**#e3f2fd**
    style P fill:**#e3f2fd**
    style Q fill:**#e3f2fd**
```

#### NUMA优化中的页面迁移

```c
// NUMA页面迁移的核心实现 - mm/memory.c

/*
 * NUMA故障处理和页面迁移
 * 当访问远程NUMA节点上的页面时触发页面故障，
 * 系统会考虑将页面迁移到本地节点以提高性能
 */
static vm_fault_t do_numa_page(struct vm_fault *vmf)
{
    struct vm_area_struct *vma = vmf->vma;
    struct folio *folio = NULL;
    int nid = NUMA_NO_NODE;
    int target_nid;
    pte_t pte, old_pte;
    int flags = 0, nr_pages;
    int last_cpupid;

    // 验证页表条目未在处理过程中改变
    spin_lock(vmf->ptl);
    old_pte = ptep_get(vmf->pte);
    
    if (unlikely(!pte_same(old_pte, vmf->orig_pte))) {
        pte_unmap_unlock(vmf->pte, vmf->ptl);
        return 0;
    }

    pte = pte_modify(old_pte, vma->vm_page_prot);
    folio = vm_normal_folio(vma, vmf->address, pte);
    
    if (!folio || folio_is_zone_device(folio))
        goto out_map;

    // 获取当前页面所在的NUMA节点
    nid = folio_nid(folio);
    nr_pages = folio_nr_pages(folio);

    // 检查是否需要迁移以及目标节点
    target_nid = numa_migrate_check(folio, vmf, vmf->address, &flags,
                                   true, &last_cpupid);
    
    if (target_nid == NUMA_NO_NODE)
        goto out_map;
        
    // 准备迁移：隔离页面
    if (migrate_misplaced_folio_prepare(folio, vma, target_nid)) {
        flags |= TNF_MIGRATE_FAIL;
        goto out_map;
    }
    
    pte_unmap_unlock(vmf->pte, vmf->ptl);

    // 执行实际的页面迁移
    if (!migrate_misplaced_folio(folio, vma, target_nid)) {
        nid = target_nid;
        flags |= TNF_MIGRATED;
        task_numa_fault(last_cpupid, nid, nr_pages, flags);
        return 0;
    }

    // 迁移失败，更新统计
    flags |= TNF_MIGRATE_FAIL;
    return 0;

out_map:
    // 更新页表并解锁
    pte_unmap_unlock(vmf->pte, vmf->ptl);
    return 0;
}

// NUMA迁移决策算法
bool should_numa_migrate_memory(struct task_struct *p, struct folio *folio,
                               int src_nid, int dst_cpu)
{
    struct numa_group *ng = deref_curr_numa_group(p);
    int dst_nid = cpu_to_node(dst_cpu);
    int last_cpupid, this_cpupid;

    // 不能迁移到无内存的节点
    if (!node_state(dst_nid, N_MEMORY))
        return false;

    // 处理内存层级系统的特殊逻辑
    if (folio_use_access_time(folio)) {
        struct pglist_data *pgdat = NODE_DATA(dst_nid);
        unsigned long rate_limit;
        unsigned int latency, threshold;

        // 检查目标节点是否有足够的空间
        if (pgdat_free_space_enough(pgdat)) {
            pgdat->nbp_threshold = 0;  // 重置热阈值
            return true;
        }

        // 应用热度阈值和速率限制
        threshold = pgdat->nbp_threshold ?: 
                   sysctl_numa_balancing_hot_threshold;
        rate_limit = sysctl_numa_balancing_promote_rate_limit << 
                    (20 - PAGE_SHIFT);

        latency = numa_hint_fault_latency(folio);
        if (latency >= threshold)
            return false;

        return !numa_promotion_rate_limit(pgdat, rate_limit,
                                         folio_nr_pages(folio));
    }

    // 检查最近的访问模式
    this_cpupid = cpu_pid_to_cpupid(dst_cpu, current->pid);
    last_cpupid = folio_xchg_last_cpupid(folio, this_cpupid);

    // 允许早期故障或私有故障立即迁移
    if ((p->numa_preferred_nid == NUMA_NO_NODE || p->numa_scan_seq <= 4) &&
        (cpupid_pid_unset(last_cpupid) || cpupid_match_pid(p, last_cpupid)))
        return true;

    return cpupid_match_pid(p, last_cpupid);
}
```

#### 内存压缩中的页面迁移

```c
// 内存压缩中的页面迁移实现 - mm/compaction.c

/*
 * 内存压缩通过页面迁移来创建连续的物理内存块
 * 这对于大页分配和减少外部碎片至关重要
 */

// 压缩过程中的页面迁移
static int migrate_pages_for_compaction(struct compact_control *cc,
                                        struct list_head *migratepages,
                                        struct list_head *freepages)
{
    unsigned long nr_migrated = 0;
    unsigned long nr_failed = 0;
    unsigned int retry = 0;
    int ret = 0;
    
    while (!list_empty(migratepages)) {
        // 尝试迁移页面列表
        ret = migrate_pages(migratepages,
                           compaction_alloc, compaction_free,
                           (unsigned long)cc, cc->mode,
                           MR_COMPACTION, &nr_migrated);

        if (ret == -EAGAIN && retry < 3) {
            // 迁移被阻塞，短暂延迟后重试
            retry++;
            msleep(10);
            continue;
        }
        
        break;
    }

    cc->nr_migratepages -= nr_migrated;
    cc->nr_freepages += nr_migrated;
    
    if (ret < 0) {
        putback_movable_pages(migratepages);
        cc->total_migrate_scanned += nr_failed;
    }

    return ret;
}

// 选择可迁移页面的策略
static bool suitable_migration_target(struct compact_control *cc,
                                     struct page *page)
{
    // 检查页面是否适合作为迁移目标
    if (cc->ignore_skip_hint && PageCompactionSkip(page))
        return false;

    // 确保页面在正确的迁移类型中
    if (get_pageblock_migratetype(page) != MIGRATE_MOVABLE &&
        get_pageblock_migratetype(page) != MIGRATE_CMA)
        return false;

    // 检查页面是否可以移动
    if (!PageMovable(page) && !__PageMovable(page))
        return false;

    return true;
}
```

#### 页面迁移的完整流程

```mermaid
sequenceDiagram
    participant **APP** as **应用程序**
    participant **MM** as **内存管理**
    participant **NUMA** as **NUMA子系统**
    participant **MIGRATE** as **迁移引擎**
    participant **PAGE** as **页面管理**
    
    Note over **APP**,**PAGE**: **NUMA感知页面迁移的完整流程**
    
    **APP**->>**MM**: **访问远程内存页面**
    **MM**->>**MM**: **触发页面故障 (NUMA hint)**
    
    **MM**->>**NUMA**: **do_numa_page()**
    activate **NUMA**
    
    **NUMA**->>**NUMA**: **检查当前页面所在节点**
    **NUMA**->>**NUMA**: **计算访问模式和局部性**
    
    **NUMA**->>**NUMA**: **should_numa_migrate_memory()**
    
    alt **需要迁移**
        **NUMA**->>**MIGRATE**: **migrate_misplaced_folio_prepare()**
        activate **MIGRATE**
        
        **MIGRATE**->>**PAGE**: **隔离源页面**
        **PAGE**-->>**MIGRATE**: **页面已隔离**
        
        **MIGRATE**->>**PAGE**: **分配目标页面**
        **PAGE**-->>**MIGRATE**: **返回目标页面**
        
        **MIGRATE**->>**MIGRATE**: **migrate_misplaced_folio()**
        
        Note right of **MIGRATE**: **复制页面内容<br/>更新页表映射<br/>更新反向映射**
        
        **MIGRATE**->>**MM**: **更新进程页表**
        **MIGRATE**->>**PAGE**: **释放源页面**
        
        **MIGRATE**-->>**NUMA**: **迁移成功**
        deactivate **MIGRATE**
        
        **NUMA**->>**NUMA**: **更新NUMA统计**
        **NUMA**-->>**MM**: **处理完成**
        deactivate **NUMA**
        
        **MM**-->>**APP**: **页面访问继续**
    else **不需要迁移**
        **NUMA**->>**NUMA**: **更新访问统计**
        **NUMA**-->>**MM**: **保持当前位置**
        deactivate **NUMA**
        
        **MM**-->>**APP**: **页面访问继续**
    end
```

#### 页面迁移的性能影响和优化

```c
// 页面迁移的性能优化机制

/*
 * 迁移频率控制
 * 避免过度迁移导致的性能开销
 */
struct numa_balancing_control {
    unsigned long scan_delay;           // 扫描延迟
    unsigned long migrate_rate_limit;   // 迁移速率限制
    unsigned long hot_threshold;        // 热页阈值
    unsigned int  max_scan_window;      // 最大扫描窗口
};

// 迁移成本评估
static bool migration_cost_effective(struct folio *folio, int src_nid, 
                                    int dst_nid, unsigned long access_count)
{
    // 计算迁移成本
    unsigned long migration_cost = folio_nr_pages(folio) * 
                                  MIGRATION_COST_PER_PAGE;
    
    // 计算访问成本差异
    unsigned long access_benefit = access_count * 
                                  (remote_access_cost - local_access_cost);
    
    // 只有当收益明显超过成本时才迁移
    return access_benefit > (migration_cost * 2);
}

// 迁移优先级计算
static int calculate_migration_priority(struct task_struct *p,
                                       struct folio *folio,
                                       int src_nid, int dst_nid)
{
    int priority = 0;
    
    // 基于访问频率的优先级
    unsigned long access_freq = folio_access_frequency(folio);
    priority += (access_freq > HIGH_ACCESS_THRESHOLD) ? 10 : 0;
    
    // 基于进程优先级的调整
    priority += (p->prio < 120) ? 5 : 0;  // 高优先级进程
    
    // 基于NUMA距离的调整
    int numa_distance = node_distance(src_nid, dst_nid);
    priority -= (numa_distance > 20) ? 3 : 0;
    
    return priority;
}
```

通过这套完整的页面迁移机制，Linux系统能够动态优化内存布局，在NUMA系统中提供更好的性能，同时支持内存压缩、热插拔等高级功能，极大地提升了系统的内存利用效率和整体性能。

### NUMA的CPU和内存绑定机制详解

#### NUMA绑定的基础原理

NUMA (Non-Uniform Memory Access) 架构中，不是简单地将CPU绑定到最近的内存条，而是实现了一套复杂的亲和性和平衡策略，以优化整体系统性能：

```c
// NUMA节点和CPU拓扑结构 - include/linux/numa.h

#define NUMA_NO_NODE    (-1)
#define MAX_NUMNODES    (1 << NODES_SHIFT)

// NUMA节点信息结构
struct numa_topology {
    int node_id;                    // 节点ID
    cpumask_t node_cpumask;         // 节点包含的CPU
    struct pglist_data *pgdat;      // 节点内存管理结构
    unsigned long node_start_pfn;   // 节点起始页帧号
    unsigned long node_present_pages; // 节点可用页数
    unsigned long node_spanned_pages; // 节点跨越页数
    int distance[MAX_NUMNODES];     // 到其他节点的距离
};

// CPU到节点的映射
DEFINE_PER_CPU(int, numa_node);

// 获取CPU所在的NUMA节点
static inline int cpu_to_node(int cpu)
{
    return per_cpu(numa_node, cpu);
}

// 设置CPU的NUMA节点
static inline void set_cpu_numa_node(int cpu, int node)
{
    per_cpu(numa_node, cpu) = node;
}

// 检查节点是否有内存
static inline bool node_state(int nid, enum node_states state)
{
    return test_bit(nid, node_states[state]);
}
```

#### NUMA拓扑发现和初始化

```c
// NUMA拓扑发现机制 - arch/x86/mm/numa.c

/*
 * NUMA拓扑发现的多种方式：
 * 1. SRAT (System Resource Affinity Table) - ACPI标准
 * 2. SLIT (System Locality Information Table) - 距离信息
 * 3. 硬件探测 - CPU和内存控制器检测
 */

// NUMA初始化主函数
void __init numa_init(void)
{
    int ret;

    // 尝试从ACPI SRAT表获取NUMA信息
    ret = acpi_numa_init();
    if (ret < 0) {
        // 回退到其他检测方法
        ret = dummy_numa_init();
    }

    if (ret < 0) {
        printk(KERN_INFO "No NUMA configuration found\n");
        // 设置假的NUMA配置
        numa_off = true;
        return;
    }

    // 初始化节点距离信息
    numa_init_distance();
    
    // 建立CPU-节点映射
    numa_init_cpu_to_node();
    
    // 初始化内存区域
    numa_init_memory_zones();
}

// CPU-节点亲和性建立
static void __init numa_init_cpu_to_node(void)
{
    int cpu, node;
    
    for_each_possible_cpu(cpu) {
        // 根据APIC ID确定CPU所属的节点
        node = numa_cpu_node(cpu);
        
        if (node == NUMA_NO_NODE) {
            // 如果无法确定，分配到节点0
            node = 0;
        }
        
        set_cpu_numa_node(cpu, node);
        cpumask_set_cpu(cpu, node_to_cpumask_map[node]);
    }
}

// 节点间距离计算
static void __init numa_init_distance(void)
{
    int i, j;
    
    // 初始化默认距离
    for (i = 0; i < MAX_NUMNODES; i++) {
        for (j = 0; j < MAX_NUMNODES; j++) {
            if (i == j)
                node_distance_map[i][j] = LOCAL_DISTANCE;
            else
                node_distance_map[i][j] = REMOTE_DISTANCE;
        }
    }
    
    // 从SLIT表或硬件检测获取实际距离
    acpi_parse_slit_table();
}
```

#### NUMA内存分配策略

```c
// NUMA内存分配策略实现 - mm/mempolicy.c

/*
 * NUMA内存分配并非总是优先本地节点
 * 而是根据策略和系统状态进行智能选择
 */

// 内存分配策略类型
enum mpol_mode {
    MPOL_DEFAULT,       // 默认策略：优先本地，可回退
    MPOL_BIND,         // 严格绑定到指定节点
    MPOL_INTERLEAVE,   // 交替分配到多个节点
    MPOL_PREFERRED,    // 首选指定节点，可回退
    MPOL_PREFERRED_MANY, // 首选多个节点
    MPOL_LOCAL,        // 强制本地分配
    MPOL_MAX,
};

// 根据策略选择分配节点
int policy_node(gfp_t gfp, struct mempolicy *pol, int nd)
{
    switch (pol->mode) {
    case MPOL_PREFERRED:
        // 首选策略：优先指定节点，可回退到其他节点
        nd = pol->preferred_node;
        break;
        
    case MPOL_BIND:
        // 绑定策略：严格限制在指定节点集合内
        nd = first_node(pol->nodes);
        break;
        
    case MPOL_INTERLEAVE:
        // 交替策略：在多个节点间轮流分配
        nd = interleave_nid(pol, nd);
        break;
        
    case MPOL_LOCAL:
        // 本地策略：强制在当前CPU节点分配
        nd = numa_node_id();
        break;
        
    default:
        // 默认策略：智能选择最佳节点
        nd = numa_node_preferred(nd);
        break;
    }
    
    return nd;
}

// 智能节点选择算法
static int numa_node_preferred(int preferred_nid)
{
    int current_nid = numa_node_id();
    
    // 检查首选节点是否可用
    if (node_state(preferred_nid, N_MEMORY) && 
        !node_reclaim_mode) {
        return preferred_nid;
    }
    
    // 检查当前节点是否可用
    if (node_state(current_nid, N_MEMORY)) {
        return current_nid;
    }
    
    // 寻找最近的可用节点
    return find_nearest_node(current_nid);
}
```

#### CPU调度的NUMA考虑

```c
// 调度器中的NUMA平衡 - kernel/sched/fair.c

/*
 * CPU调度器考虑NUMA亲和性，但不是严格绑定
 * 而是在性能和负载平衡间找到最佳平衡点
 */

// NUMA感知的任务唤醒
static int select_task_rq_numa(struct task_struct *p, int prev_cpu, int wake_flags)
{
    int this_cpu = smp_processor_id();
    int this_node = cpu_to_node(this_cpu);
    int prev_node = cpu_to_node(prev_cpu);
    int target_cpu;

    // 检查任务的NUMA首选节点
    if (p->numa_preferred_nid != NUMA_NO_NODE) {
        int preferred_node = p->numa_preferred_nid;
        
        // 如果首选节点有可用CPU，优先选择
        if (cpumask_intersects(cpu_online_mask, 
                              cpumask_of_node(preferred_node))) {
            target_cpu = select_idle_sibling(p, prev_cpu, 
                                           cpumask_of_node(preferred_node));
            if (target_cpu >= 0)
                return target_cpu;
        }
    }

    // 考虑内存访问局部性
    if (this_node == prev_node) {
        // 在同一NUMA节点内，优先选择空闲CPU
        target_cpu = select_idle_sibling(p, prev_cpu, 
                                        cpumask_of_node(this_node));
        if (target_cpu >= 0)
            return target_cpu;
    }

    // 回退到全局负载平衡
    return select_task_rq_fair(p, prev_cpu, wake_flags);
}

// NUMA节点间的负载平衡
static int numa_load_balance(int this_cpu, struct rq *this_rq,
                            struct sched_domain *sd, enum cpu_idle_type idle)
{
    int busiest_node = -1;
    int this_node = cpu_to_node(this_cpu);
    unsigned long max_imbalance = 0;
    
    // 寻找最繁忙的远程节点
    for_each_online_node(node) {
        if (node == this_node)
            continue;
            
        unsigned long node_load = weighted_cpuload(node);
        unsigned long this_load = weighted_cpuload(this_node);
        
        if (node_load > this_load + NUMA_IMBALANCE_THRESHOLD) {
            if (node_load - this_load > max_imbalance) {
                max_imbalance = node_load - this_load;
                busiest_node = node;
            }
        }
    }
    
    if (busiest_node >= 0) {
        return migrate_tasks_from_node(busiest_node, this_cpu, sd);
    }
    
    return 0;
}
```

#### NUMA绑定的实际机制图解

```mermaid
graph **TD**
    subgraph **NUMA_NODE_0** [**NUMA节点0**]
        CPU0[**CPU 0-7**]
        MEM0[**内存控制器0<br/>DDR4-0 32GB**]
        CPU0 -.->|**优先访问**| MEM0
    end
    
    subgraph **NUMA_NODE_1** [**NUMA节点1**]
        CPU1[**CPU 8-15**]
        MEM1[**内存控制器1<br/>DDR4-1 32GB**]
        CPU1 -.->|**优先访问**| MEM1
    end
    
    subgraph **INTERCONNECT** [**节点互联**]
        QPI[**QPI/UPI总线**]
        DISTANCE[**节点距离矩阵**]
    end
    
    CPU0 <-->|**远程访问<br/>延迟 +50%**| MEM1
    CPU1 <-->|**远程访问<br/>延迟 +50%**| MEM0
    
    QPI -.-> CPU0
    QPI -.-> CPU1
    QPI -.-> MEM0
    QPI -.-> MEM1
    
    subgraph **LINUX_SCHEDULER** [**Linux调度策略**]
        POLICY[**内存分配策略**]
        BALANCE[**负载平衡**]
        MIGRATE[**任务迁移**]
    end
    
    POLICY --> A{**分配策略**}
    A -->|**LOCAL**| B[**强制本地节点**]
    A -->|**PREFERRED**| C[**优先本地，可回退**]
    A -->|**INTERLEAVE**| D[**节点间交替**]
    A -->|**BIND**| E[**严格绑定指定节点**]
    
    BALANCE --> F[**监控节点负载**]
    F --> G{**负载不均？**}
    G -->|**是**| H[**跨节点任务迁移**]
    G -->|**否**| I[**保持当前分布**]
    
    MIGRATE --> J[**考虑内存局部性**]
    J --> K[**最小化远程访问**]
    
    style NUMA_NODE_0 fill:**#e3f2fd**
    style NUMA_NODE_1 fill:**#f3e5f5**
    style LINUX_SCHEDULER fill:**#e8f5e8**
```

#### NUMA绑定的命令行管理

```bash
#!/bin/bash
# NUMA绑定管理脚本

# 1. 查看NUMA拓扑
show_numa_topology() {
    echo "=== NUMA拓扑信息 ==="
    
    # 显示节点信息
    numactl --hardware
    
    # 显示每个节点的CPU和内存
    for node in /sys/devices/system/node/node*; do
        node_id=$(basename $node | sed 's/node//')
        echo "节点 $node_id:"
        echo "  CPU: $(cat $node/cpulist)"
        echo "  内存: $(cat $node/meminfo | grep MemTotal)"
        echo "  距离: $(cat $node/distance)"
        echo
    done
}

# 2. 进程NUMA绑定策略设置
bind_process_numa() {
    local pid=$1
    local policy=$2
    local nodes=$3
    
    case $policy in
        "bind")
            # 严格绑定到指定节点
            numactl --cpubind=$nodes --membind=$nodes --pid $pid
            ;;
        "preferred")
            # 优先指定节点，可回退
            numactl --preferred=$nodes --pid $pid
            ;;
        "interleave")
            # 在指定节点间交替分配
            numactl --interleave=$nodes --pid $pid
            ;;
        "local")
            # 绑定到当前节点
            local current_node=$(numactl --show | grep "preferred node" | cut -d: -f2)
            numactl --cpubind=$current_node --membind=$current_node --pid $pid
            ;;
    esac
    
    echo "进程 $pid 已设置NUMA策略: $policy (节点: $nodes)"
}

# 3. 启动NUMA感知应用
launch_numa_aware() {
    local app_cmd=$1
    local node=$2
    
    echo "在NUMA节点 $node 启动应用: $app_cmd"
    
    # 设置CPU和内存亲和性
    numactl --cpubind=$node --membind=$node $app_cmd &
    local pid=$!
    
    echo "应用PID: $pid, 绑定到节点: $node"
    
    # 验证绑定状态
    sleep 1
    cat /proc/$pid/numa_maps | head -5
    
    return $pid
}

# 4. NUMA性能监控
monitor_numa_performance() {
    echo "=== NUMA性能监控 ==="
    
    # 显示节点使用情况
    numastat
    
    # 显示进程的NUMA统计
    echo -e "\n=== 进程NUMA统计 ==="
    for pid in $(pgrep -f "$1"); do
        echo "进程 $pid ($1):"
        cat /proc/$pid/numa_maps | grep -E "(heap|stack|anon)" | \
        awk '{print "  " $1 ": " $2}' | head -5
        echo
    done
    
    # 显示内存带宽使用
    echo -e "\n=== 内存带宽监控 ==="
    if command -v sar >/dev/null; then
        sar -B 1 3 | tail -4
    fi
}

# 5. NUMA优化建议
optimize_numa_performance() {
    local app_name=$1
    
    echo "=== $app_name NUMA优化建议 ==="
    
    # 分析应用内存使用模式
    local pids=$(pgrep -f "$app_name")
    
    for pid in $pids; do
        echo "分析进程 $pid:"
        
        # 检查当前NUMA分布
        local numa_maps=$(cat /proc/$pid/numa_maps 2>/dev/null)
        
        if echo "$numa_maps" | grep -q "N[0-9]"; then
            echo "  当前内存分布:"
            echo "$numa_maps" | grep -E "heap|stack" | \
            sed 's/.*N\([0-9]\)=\([0-9]*\).*/    节点\1: \2 页/' | head -3
            
            # 提供优化建议
            echo "  优化建议:"
            echo "    1. 使用 taskset 绑定CPU"
            echo "    2. 使用 numactl --preferred 设置内存首选节点"
            echo "    3. 监控跨节点内存访问"
        fi
        echo
    done
}

# 主菜单
case "${1:-help}" in
    topology)
        show_numa_topology
        ;;
    bind)
        if [[ $# -lt 4 ]]; then
            echo "用法: $0 bind <PID> <策略> <节点>"
            echo "策略: bind, preferred, interleave, local"
            exit 1
        fi
        bind_process_numa "$2" "$3" "$4"
        ;;
    launch)
        if [[ $# -lt 3 ]]; then
            echo "用法: $0 launch <命令> <节点>"
            exit 1
        fi
        launch_numa_aware "$2" "$3"
        ;;
    monitor)
        monitor_numa_performance "${2:-.*}"
        ;;
    optimize)
        optimize_numa_performance "${2:-.*}"
        ;;
    help|*)
        echo "NUMA绑定管理工具"
        echo "用法: $0 <命令> [参数]"
        echo ""
        echo "命令:"
        echo "  topology              - 显示NUMA拓扑"
        echo "  bind <PID> <策略> <节点> - 绑定进程到NUMA节点"  
        echo "  launch <命令> <节点>   - 启动NUMA感知应用"
        echo "  monitor [应用名]      - 监控NUMA性能"
        echo "  optimize [应用名]     - 提供优化建议"
        echo "  help                 - 显示帮助"
        ;;
esac
```

#### NUMA绑定的关键特点总结

| **绑定类型** | **特点** | **使用场景** | **性能影响** |
|-------------|---------|-------------|-------------|
| **CPU-内存就近绑定** | CPU优先访问同节点内存 | **高计算密集型应用** | **最佳本地访问性能** |
| **跨节点负载均衡** | 任务可能跨节点调度 | **多任务并发环境** | **平衡负载与局部性** |
| **内存策略绑定** | 严格限制内存分配节点 | **内存敏感应用** | **确保内存访问可预测** |
| **自适应迁移** | 根据访问模式动态调整 | **通用工作负载** | **智能优化总体性能** |

通过这套完整的NUMA绑定机制，Linux实现了在保证性能局部性的同时，维持系统整体负载平衡和资源利用效率的最佳状态。

// 评估任务
static int oom_evaluate_task(struct task_struct *task, void *arg)
{
    struct oom_control *oc = arg;
    long points;

    // 检查任务是否不可杀死
    if (oom_unkillable_task(task))
        goto next;

    // 检查任务是否符合cpuset要求
    if (!is_memcg_oom(oc) && !oom_cpuset_eligible(task, oc))
        goto next;

    // 检查任务是否已经是OOM受害者
    if (!is_sysrq_oom(oc) && tsk_is_oom_victim(task)) {
        if (test_bit(MMF_OOM_SKIP, &task->signal->oom_mm->flags))
            goto next;
        goto abort;
    }

    // 检查是否标记为优先杀死
    if (oom_task_origin(task)) {
        points = LONG_MAX;
        goto select;
    }

    // 计算badness分数
    points = oom_badness(task, oc->totalpages);
    if (points == LONG_MIN || points < oc->chosen_points)
        goto next;

select:
    if (oc->chosen)
        put_task_struct(oc->chosen);
    get_task_struct(task);
    oc->chosen = task;
    oc->chosen_points = points;
next:
    return 0;
abort:
    if (oc->chosen)
        put_task_struct(oc->chosen);
    oc->chosen = (void *)-1UL;
    return 1;
}
```

### OOM Reaper

```c
// OOM reaper守护进程
static int oom_reaper(void *unused)
{
    while (true) {
        struct task_struct *tsk = NULL;

        // 等待需要回收的任务
        wait_event_freezable(oom_reaper_wait,
                           oom_reaper_list != NULL);
        
        spin_lock(&oom_reaper_lock);
        if (oom_reaper_list != NULL) {
            tsk = oom_reaper_list;
            oom_reaper_list = tsk->oom_reaper_list;
        }
        spin_unlock(&oom_reaper_lock);

        if (tsk)
            oom_reap_task(tsk);
    }

    return 0;
}

// 回收OOM受害者的内存
static void oom_reap_task(struct task_struct *tsk)
{
    int attempts = 0;
    struct mm_struct *mm = tsk->signal->oom_mm;

    // 重试获取mm锁
    while (attempts++ < MAX_OOM_REAP_RETRIES && 
           !oom_reap_task_mm(tsk, mm))
        schedule_timeout_idle(HZ/10);

    if (attempts <= MAX_OOM_REAP_RETRIES ||
        test_bit(MMF_OOM_SKIP, &mm->flags))
        goto done;

    pr_info("oom_reaper: unable to reap pid:%d (%s)\n",
            task_pid_nr(tsk), tsk->comm);
    debug_show_all_locks();

done:
    tsk->oom_reaper_list = NULL;
    
    // 标记mm为已跳过
    set_bit(MMF_OOM_SKIP, &mm->flags);
    
    put_task_struct(tsk);
}
```

## 内存压缩机制

内存压缩通过移动已分配的页面来减少内存碎片，为大内存分配创造连续的物理内存空间。

### kcompactd后台压缩

```c
// kcompactd主循环 - mm/compaction.c
static int kcompactd(void *p)
{
    pg_data_t *pgdat = (pg_data_t *)p;
    struct task_struct *tsk = current;
    long default_timeout = msecs_to_jiffies(HPAGE_FRAG_CHECK_INTERVAL_MSEC);
    long timeout = default_timeout;

    const struct cpumask *cpumask = cpumask_of_node(pgdat->node_id);

    // 设置CPU亲和性
    if (!cpumask_empty(cpumask))
        set_cpus_allowed_ptr(tsk, cpumask);

    set_freezable();

    pgdat->kcompactd_max_order = 0;
    pgdat->kcompactd_highest_zoneidx = pgdat->nr_zones - 1;

    while (!kthread_should_stop()) {
        unsigned long pflags;

        // 避免在禁用主动压缩时不必要的唤醒
        if (!sysctl_compaction_proactiveness)
            timeout = MAX_SCHEDULE_TIMEOUT;
            
        trace_mm_compaction_kcompactd_sleep(pgdat->node_id);
        
        // 等待工作请求或超时
        if (wait_event_freezable_timeout(pgdat->kcompactd_wait,
            kcompactd_work_requested(pgdat), timeout) &&
            !pgdat->proactive_compact_trigger) {

            psi_memstall_enter(&pflags);
            kcompactd_do_work(pgdat);
            psi_memstall_leave(&pflags);
            
            timeout = default_timeout;
            continue;
        }

        // 主动压缩工作
        timeout = default_timeout;
        if (should_proactive_compact_node(pgdat)) {
            unsigned int prev_score, score;

            prev_score = fragmentation_score_node(pgdat);
            compact_node(pgdat, true);
            score = fragmentation_score_node(pgdat);
            
            // 如果碎片分数没有下降，延迟压缩
            if (unlikely(score >= prev_score))
                timeout = default_timeout << COMPACT_MAX_DEFER_SHIFT;
        }
        
        if (unlikely(pgdat->proactive_compact_trigger))
            pgdat->proactive_compact_trigger = false;
    }

    return 0;
}

// kcompactd工作函数
static void kcompactd_do_work(pg_data_t *pgdat)
{
    int zoneid;
    struct zone *zone;
    struct compact_control cc = {
        .order = pgdat->kcompactd_max_order,
        .search_order = pgdat->kcompactd_max_order,
        .highest_zoneidx = pgdat->kcompactd_highest_zoneidx,
        .mode = MIGRATE_SYNC_LIGHT,
        .ignore_skip_hint = false,
        .gfp_mask = GFP_KERNEL,
    };
    enum compact_result ret;

    trace_mm_compaction_kcompactd_wake(pgdat->node_id, cc.order,
                                      cc.highest_zoneidx);
    count_compact_event(KCOMPACTD_WAKE);

    for (zoneid = 0; zoneid <= cc.highest_zoneidx; zoneid++) {
        int status;

        zone = &pgdat->node_zones[zoneid];
        if (!populated_zone(zone))
            continue;

        // 检查是否需要延迟压缩
        if (compaction_deferred(zone, cc.order))
            continue;

        // 检查压缩是否适合
        ret = compaction_suit_allocation_order(zone,
                                              cc.order, zoneid, ALLOC_WMARK_MIN);
        if (ret != COMPACT_CONTINUE)
            continue;

        if (kthread_should_stop())
            return;

        cc.zone = zone;
        status = compact_zone(&cc, NULL);

        if (status == COMPACT_SUCCESS) {
            compaction_defer_reset(zone, cc.order, false);
        } else if (status == COMPACT_PARTIAL_SKIPPED || 
                   status == COMPACT_COMPLETE) {
            // 排干buddy页面以便合并
            drain_all_pages(zone);
            defer_compaction(zone, cc.order);
        }

        count_compact_events(KCOMPACTD_MIGRATE_SCANNED,
                            cc.total_migrate_scanned);
        count_compact_events(KCOMPACTD_FREE_SCANNED,
                            cc.total_free_scanned);
    }

    // 重置压缩请求参数
    pgdat->kcompactd_max_order = 0;
    pgdat->kcompactd_highest_zoneidx = pgdat->nr_zones - 1;
}
```

### 直接压缩

```c
// 直接压缩入口
static struct page *
__alloc_pages_direct_compact(gfp_t gfp_mask, unsigned int order,
                            unsigned int alloc_flags, const struct alloc_context *ac,
                            enum compact_priority prio, enum compact_result *compact_result)
{
    struct page *page = NULL;
    unsigned long pflags;
    unsigned int noreclaim_flag;

    if (!order)
        return NULL;

    // 进入内存阻塞状态
    psi_memstall_enter(&pflags);
    delayacct_compact_start();
    noreclaim_flag = memalloc_noreclaim_save();

    // 执行压缩
    *compact_result = try_to_compact_pages(gfp_mask, order, alloc_flags, ac,
                                          prio, &page);

    memalloc_noreclaim_restore(noreclaim_flag);
    psi_memstall_leave(&pflags);
    delayacct_compact_end();

    if (*compact_result == COMPACT_SKIPPED)
        return NULL;

    // 统计压缩阻塞
    count_vm_event(COMPACTSTALL);

    // 准备捕获的页面
    if (page)
        prep_new_page(page, order, gfp_mask, alloc_flags);

    // 尝试从空闲列表获取页面
    if (!page)
        page = get_page_from_freelist(gfp_mask, order, alloc_flags, ac);

    if (page) {
        struct zone *zone = page_zone(page);
        
        zone->compact_blockskip_flush = false;
        compaction_defer_reset(zone, order, true);
        count_vm_event(COMPACTSUCCESS);
        return page;
    }

    // 压缩失败
    count_vm_event(COMPACTFAIL);
    cond_resched();

    return NULL;
}
```

### 页面迁移

```c
// 压缩控制结构
struct compact_control {
    struct list_head freepages;      // 空闲页面列表
    struct list_head migratepages;   // 待迁移页面列表
    unsigned int nr_freepages;       // 空闲页面数
    unsigned int nr_migratepages;    // 待迁移页面数
    unsigned long free_pfn;          // 空闲页面扫描位置
    unsigned long migrate_pfn;       // 迁移页面扫描位置
    unsigned long last_migrated_pfn; // 最后迁移的页面
    const gfp_t gfp_mask;           // GFP掩码
    int order;                      // 目标order
    int migratetype;                // 迁移类型
    struct zone *zone;              // 目标zone
    enum compact_mode mode;         // 压缩模式
    enum compact_priority priority; // 压缩优先级
    bool direct_compaction;         // 是否直接压缩
    bool proactive_compaction;      // 是否主动压缩
    bool whole_zone;                // 是否整个zone
    bool contended;                 // 是否竞争
    bool rescan;                    // 是否重扫
};

// 页面迁移函数
static int migrate_pages(struct list_head *from, new_folio_t get_new_folio,
                        free_folio_t put_new_folio, unsigned long private,
                        enum migrate_mode mode, int reason, unsigned int *ret_succeeded)
{
    int retry = 1;
    int thp_retry = 1;
    int nr_failed = 0;
    int nr_succeeded = 0;
    int nr_thp_succeeded = 0;
    int nr_thp_failed = 0;
    int nr_thp_split = 0;
    int pass = 0;
    bool is_thp = false;
    struct folio *folio, *folio2;
    int swapwrite = current->flags & PF_SWAPWRITE;
    int rc, nr_subpages;
    LIST_HEAD(ret_folios);
    LIST_HEAD(thp_split_folios);
    bool nosplit = (reason == MR_NUMA_MISPLACED);

    trace_mm_migrate_pages_start(mode, reason);

    if (!swapwrite)
        current->flags |= PF_SWAPWRITE;

    for (pass = 0; pass < 10 && (retry || thp_retry); pass++) {
        retry = 0;
        thp_retry = 0;

        list_for_each_entry_safe(folio, folio2, from, lru) {
            retry_folio:
            // 检查是否是大页面
            is_thp = folio_test_large(folio) && folio_test_pmd_mappable(folio);
            nr_subpages = folio_nr_pages(folio);
            cond_resched();

            // 尝试迁移页面
            rc = unmap_and_move(get_new_folio, put_new_folio,
                               private, folio, pass > 2, mode,
                               reason, &ret_folios);
            
            switch(rc) {
            case -ENOMEM:
                if (is_thp) {
                    thp_retry++;
                    break;
                }
                retry++;
                break;
            case -EAGAIN:
                if (is_thp) {
                    thp_retry++;
                    break;
                }
                retry++;
                break;
            case MIGRATEPAGE_SUCCESS:
                nr_succeeded += nr_subpages;
                if (is_thp)
                    nr_thp_succeeded++;
                break;
            default:
                // 处理其他错误情况
                nr_failed += nr_subpages;
                if (is_thp)
                    nr_thp_failed++;
                break;
            }
        }
    }

    // 恢复交换写标志
    if (!swapwrite)
        current->flags &= ~PF_SWAPWRITE;

    if (ret_succeeded)
        *ret_succeeded = nr_succeeded;

    trace_mm_migrate_pages_end(mode, reason);
    
    return nr_failed;
}
```

## NUMA内存平衡

在NUMA系统中，Linux通过自动的页面迁移来优化内存访问的局部性。

### NUMA平衡算法

```c
// NUMA组结构 - kernel/sched/fair.c
struct numa_group {
    refcount_t refcount;             // 引用计数
    spinlock_t lock;                 // 锁
    int nr_tasks;                    // 任务数量
    pid_t gid;                       // 组ID
    int active_nodes;                // 活跃节点数
    struct rcu_head rcu;             // RCU头
    unsigned long total_faults;      // 总故障数
    unsigned long max_faults_cpu;    // CPU最大故障数
    unsigned long *faults_cpu;       // CPU故障数组
    unsigned long faults[];          // 故障数组
};

// NUMA故障处理
void task_numa_fault(int last_cpupid, int mem_node, int pages, int flags)
{
    struct task_struct *p = current;
    bool migrated = flags & TNF_MIGRATED;
    int cpu_node = task_node(current);
    int local = !!(flags & TNF_FAULT_LOCAL);
    struct numa_group *ng;
    int priv;

    if (!static_branch_likely(&sched_numa_balancing))
        return;

    // 跳过内核线程
    if (unlikely(!p->mm))
        return;

    priv = !(flags & TNF_SHARED);

    // 分配NUMA故障统计结构
    if (unlikely(!p->numa_faults)) {
        int size = sizeof(*p->numa_faults) * NR_NUMA_HINT_FAULT_STATS * 
                   nr_node_ids;
        p->numa_faults = kzalloc(size, GFP_KERNEL | __GFP_NOWARN);
        if (!p->numa_faults)
            return;

        p->total_numa_faults = 0;
        p->numa_faults_locality[0] = 0;
        p->numa_faults_locality[1] = 0;
    }

    // 更新故障统计
    if (time_after(jiffies, p->numa_migrate_retry))
        task_numa_migrate(p);
        
    task_numa_placement(p);
    task_numa_group(p, last_cpupid, flags, &priv);
}

// NUMA页面迁移决策
bool should_numa_migrate_memory(struct task_struct *p, struct folio *folio,
                               int src_nid, int dst_cpu)
{
    struct numa_group *ng = deref_curr_numa_group(p);
    int dst_nid = cpu_to_node(dst_cpu);
    int last_cpupid, this_cpupid;

    // 不能迁移到无内存节点
    if (!node_state(dst_nid, N_MEMORY))
        return false;

    // 慢内存节点的页面应根据热/冷而不是私有/共享迁移
    if (folio_use_access_time(folio)) {
        struct pglist_data *pgdat;
        unsigned long rate_limit;
        unsigned int latency, th, def_th;

        pgdat = NODE_DATA(dst_nid);
        if (pgdat_free_space_enough(pgdat)) {
            // 工作负载改变，重置热阈值
            pgdat->nbp_threshold = 0;
            return true;
        }

        def_th = sysctl_numa_balancing_hot_threshold;
        rate_limit = sysctl_numa_balancing_promote_rate_limit << 
                    (20 - PAGE_SHIFT);
        numa_promotion_adjust_threshold(pgdat, rate_limit, def_th);

        th = pgdat->nbp_threshold ? : def_th;
        latency = numa_hint_fault_latency(folio);
        if (latency >= th)
            return false;

        return !numa_promotion_rate_limit(pgdat, rate_limit,
                                         folio_nr_pages(folio));
    }

    this_cpupid = cpu_pid_to_cpupid(dst_cpu, current->pid);
    last_cpupid = folio_xchg_last_cpupid(folio, this_cpupid);

    // 检查内存分层和cpupid有效性
    if (!(sysctl_numa_balancing_mode & NUMA_BALANCING_MEMORY_TIERING) &&
        !node_is_toptier(src_nid) && !cpupid_valid(last_cpupid))
        return false;

    // 允许早期故障或私有故障立即迁移
    if ((p->numa_preferred_nid == NUMA_NO_NODE || p->numa_scan_seq <= 4) &&
        (cpupid_pid_unset(last_cpupid) || cpupid_match_pid(p, last_cpupid)))
        return true;

    // 检查最近访问和共享模式
    if (cpupid_match_pid(p, last_cpupid))
        return true;

    return false;
}
```

### NUMA扫描任务

```c
// NUMA扫描工作
static void task_numa_work(struct callback_head *work)
{
    unsigned long migrate, next_scan, now = jiffies;
    struct task_struct *p = current;
    struct mm_struct *mm = p->mm;
    u64 runtime = p->se.sum_exec_runtime;
    struct vm_area_struct *vma;
    unsigned long start, end;
    unsigned long nr_pte_updates = 0;
    long pages, virtpages;
    struct vma_iterator vmi;
    bool vma_pids_skipped;
    bool vma_pids_forced = false;

    SCHED_WARN_ON(p != container_of(work, struct task_struct, numa_work));

    work->next = work;
    
    // 进程退出检查
    if (p->flags & PF_EXITING)
        return;

    if (!mm->numa_next_scan) {
        mm->numa_next_scan = now +
            msecs_to_jiffies(sysctl_numa_balancing_scan_delay);
    }

    // 执行最大扫描/迁移频率限制
    migrate = mm->numa_next_scan;
    if (time_before(now, migrate))
        return;

    if (p->numa_scan_period == 0) {
        p->numa_scan_period_max = task_scan_max(p);
        p->numa_scan_period = task_scan_start(p);
    }

    next_scan = now + msecs_to_jiffies(p->numa_scan_period);
    if (!try_cmpxchg(&mm->numa_next_scan, &migrate, next_scan))
        return;

    // 延迟此任务，让其他任务有机会
    p->node_stamp += 2 * TICK_NSEC;

    pages = sysctl_numa_balancing_scan_size;
    pages <<= 20 - PAGE_SHIFT; /* MB转换为页 */
    virtpages = pages * 8;     /* 扫描8倍虚拟空间 */
    if (!pages)
        return;

    if (!mmap_read_trylock(mm))
        return;

    // 扫描VMA
    start = mm->numa_scan_offset;
    vma_iter_init(&vmi, mm, start);
    vma = vma_next(&vmi);
    
    // 实际的页面扫描和设置NUMA提示
    do {
        if (!vma_migratable(vma) || !vma_policy_mof(vma) ||
            is_vm_hugetlb_page(vma) || (vma->vm_flags & VM_MIXEDMAP)) {
            continue;
        }

        // 设置NUMA提示位
        do {
            start = max(start, vma->vm_start);
            end = ALIGN(start + (pages << PAGE_SHIFT), HPAGE_SIZE);
            end = min(end, vma->vm_end);
            nr_pte_updates = change_prot_numa(vma, start, end);

            if (nr_pte_updates)
                pages -= (end - start) >> PAGE_SHIFT;

            start = end;
            if (pages <= 0 || !nr_pte_updates)
                break;

            cond_resched();
        } while (end != vma->vm_end);
    } for_each_vma(vmi, vma);

    // 更新mm扫描偏移
    mm->numa_scan_offset = start;
    mmap_read_unlock(mm);
}
```

继续下一部分...
