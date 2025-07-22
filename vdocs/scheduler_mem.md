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

    // 应用OOM调整值
    adj *= totalpages / 1000;
    points += adj;

    return points;
}

// 选择最坏进程
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
