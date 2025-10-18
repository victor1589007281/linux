# Linux NUMA架构原理与实现

## 概述

NUMA (Non-Uniform Memory Access, 非均匀内存访问) 是现代多核、多插槽计算机系统中的一种内存架构设计。在NUMA系统中，内存访问时间依赖于处理器和内存的相对位置 - 访问本地内存比访问远程内存更快。Linux内核提供了完整的NUMA感知支持，包括拓扑发现、内存分配策略、进程调度优化等机制。

## 核心概念

### NUMA节点 (NUMA Nodes)

Linux将系统的硬件资源抽象为多个软件节点，每个节点可能包含：
- CPU核心
- 本地内存
- I/O总线和外围设备

**关键特征**：
- **本地访问优势**：访问同节点内存速度最快
- **远程访问延迟**：跨节点内存访问有额外延迟
- **可变配置**：节点可能是无内存节点或无CPU节点

### NUMA距离 (NUMA Distance)

节点间的相对访问成本用距离值表示：
- **本地距离** (`LOCAL_DISTANCE = 10`)：同节点内访问
- **远程距离** (`REMOTE_DISTANCE = 20`)：跨节点访问
- **自定义距离**：根据实际拓扑设置

## 核心数据结构

### 节点描述符 (pglist_data)

每个NUMA节点用 `pg_data_t` 结构表示，包含节点的所有内存管理信息：

```c
// 节点描述符 - include/linux/mmzone.h
typedef struct pglist_data {
    // 节点包含的所有内存区域
    struct zone node_zones[MAX_NR_ZONES];
    
    // 内存分配回退列表（按NUMA距离排序）
    struct zonelist node_zonelists[MAX_ZONELISTS];
    
    int nr_zones;                    // 该节点活跃zone数量
    struct page *node_mem_map;       // 页面描述符数组
    
    // 节点内存统计
    unsigned long node_start_pfn;    // 起始物理页帧号
    unsigned long node_present_pages; // 实际存在的页数
    unsigned long node_spanned_pages; // 跨越的总页数（包括洞）
    
    int node_id;                     // 节点ID
    
    // 页面回收相关
    wait_queue_head_t kswapd_wait;
    struct task_struct *kswapd;      // 页面回收守护进程
    
    // 内存压缩相关  
    struct task_struct *kcompactd;   // 内存压缩守护进程
    
#ifdef CONFIG_NUMA
    // NUMA专用字段
    unsigned long min_unmapped_pages; // 最小未映射页数
    unsigned long min_slab_pages;     // 最小slab页数
#endif

#ifdef CONFIG_NUMA_BALANCING
    // NUMA平衡相关
    unsigned int nbp_rl_start;       // 当前推广速率限制周期开始时间
    unsigned long nbp_rl_nr_cand;    // 推广候选页数
    unsigned int nbp_threshold;      // 推广阈值
#endif
} pg_data_t;

// 全局节点数组
extern struct pglist_data *node_data[MAX_NUMNODES];
#define NODE_DATA(nid) (node_data[nid])
```

### NUMA拓扑管理

```c
// NUMA拓扑类型 - kernel/sched/topology.c
enum numa_topology_type {
    NUMA_DIRECT,        // 直接连接：所有节点直连或非NUMA系统
    NUMA_GLUELESS_MESH, // 无胶网格：通过中间节点通信
    NUMA_BACKPLANE      // 背板架构：通过背板控制器通信
};

// 距离表管理 - mm/numa_memblks.c
static u8 *numa_distance;           // 节点间距离表
static int numa_distance_cnt;       // 距离表大小

// 获取节点间距离
int __node_distance(int from, int to)
{
    if (from >= numa_distance_cnt || to >= numa_distance_cnt)
        return from == to ? LOCAL_DISTANCE : REMOTE_DISTANCE;
    return numa_distance[from * numa_distance_cnt + to];
}

// 设置节点间距离
void __init numa_set_distance(int from, int to, int distance)
{
    if (!numa_distance && numa_alloc_distance() < 0)
        return;
        
    if (from >= numa_distance_cnt || to >= numa_distance_cnt ||
        from < 0 || to < 0) {
        pr_warn_once("Warning: node ids are out of bound\n");
        return;
    }
    
    numa_distance[from * numa_distance_cnt + to] = distance;
}
```

### CPU-NUMA映射

```c
// CPU到节点映射 - include/asm-generic/numa.h
extern cpumask_var_t node_to_cpumask_map[MAX_NUMNODES];

// 获取节点上的CPU掩码
static inline const struct cpumask *cpumask_of_node(int node)
{
    if (node == NUMA_NO_NODE)
        return cpu_all_mask;
    return node_to_cpumask_map[node];
}

// 获取CPU所属节点
#ifdef CONFIG_USE_PERCPU_NUMA_NODE_ID
DECLARE_PER_CPU(int, numa_node);
static inline int numa_node_id(void)
{
    return raw_cpu_read(numa_node);
}
#endif
```

## NUMA拓扑发现

### 拓扑类型识别

```c
// 拓扑类型初始化 - kernel/sched/topology.c
static void init_numa_topology_type(int offline_node)
{
    int a, b, c, n;
    n = sched_max_numa_distance;

    if (sched_domains_numa_levels <= 2) {
        sched_numa_topology_type = NUMA_DIRECT;
        return;
    }

    for_each_cpu_node_but(a, offline_node) {
        for_each_cpu_node_but(b, offline_node) {
            // 寻找距离最远的两个节点
            if (node_distance(a, b) < n)
                continue;

            // 检查是否存在中间节点
            for_each_cpu_node_but(c, offline_node) {
                if (node_distance(a, c) < n && node_distance(b, c) < n) {
                    sched_numa_topology_type = NUMA_GLUELESS_MESH;
                    return;
                }
            }
            
            sched_numa_topology_type = NUMA_BACKPLANE;
            return;
        }
    }
    
    sched_numa_topology_type = NUMA_DIRECT;
}
```

### 调度域初始化

```c
// NUMA调度域初始化
void sched_init_numa(int offline_node)
{
    struct sched_domain_topology_level *tl;
    unsigned long *distance_map;
    int nr_levels = 0;
    int i, j, *distances;
    struct cpumask ***masks;

    // 收集所有唯一的距离值
    distance_map = bitmap_alloc(NR_DISTANCE_VALUES, GFP_KERNEL);
    if (!distance_map)
        return;

    bitmap_zero(distance_map, NR_DISTANCE_VALUES);
    for_each_cpu_node_but(i, offline_node) {
        for_each_cpu_node_but(j, offline_node) {
            int distance = node_distance(i, j);
            if (distance < LOCAL_DISTANCE || 
                distance >= NR_DISTANCE_VALUES) {
                sched_numa_warn("Invalid distance value range");
                bitmap_free(distance_map);
                return;
            }
            bitmap_set(distance_map, distance, 1);
        }
    }
    
    nr_levels = bitmap_weight(distance_map, NR_DISTANCE_VALUES);
    distances = kcalloc(nr_levels, sizeof(int), GFP_KERNEL);
    
    // 为每个距离级别构建CPU掩码
    for (i = 0; i < nr_levels; i++) {
        masks[i] = kzalloc(nr_node_ids * sizeof(void *), GFP_KERNEL);
        
        for_each_cpu_node_but(j, offline_node) {
            struct cpumask *mask = kzalloc(cpumask_size(), GFP_KERNEL);
            masks[i][j] = mask;
            
            for_each_cpu_node_but(k, offline_node) {
                if (node_distance(j, k) > sched_domains_numa_distance[i])
                    continue;
                cpumask_or(mask, mask, cpumask_of_node(k));
            }
        }
    }
    
    rcu_assign_pointer(sched_domains_numa_masks, masks);
    sched_domains_numa_levels = nr_levels;
    
    init_numa_topology_type(offline_node);
}
```

## NUMA感知内存分配

### Zonelist构建

每个节点维护一个zonelist，按NUMA距离排序，用于内存分配回退：

```c
// 构建zonelist - mm/page_alloc.c
static void build_zonelists(pg_data_t *pgdat)
{
    static int node_order[MAX_NUMNODES];
    int node, nr_nodes = 0;
    nodemask_t used_mask = NODE_MASK_NONE;
    int local_node, prev_node;

    local_node = pgdat->node_id;
    prev_node = local_node;

    memset(node_order, 0, sizeof(node_order));
    
    // 按距离排序节点
    while ((node = find_next_best_node(local_node, &used_mask)) >= 0) {
        // 对相同距离组中的第一个节点增加惩罚，实现轮询
        if (node_distance(local_node, node) !=
            node_distance(local_node, prev_node))
            node_load[node] += 1;

        node_order[nr_nodes++] = node;
        prev_node = node;
    }

    build_zonelists_in_node_order(pgdat, node_order, nr_nodes);
}

// 寻找最佳下一个节点
int find_next_best_node(int node, nodemask_t *used_node_mask)
{
    int n, val;
    int min_val = INT_MAX;
    int best_node = NUMA_NO_NODE;

    // 优先使用本地节点（如果有内存）
    if (!node_isset(node, *used_node_mask) && node_state(node, N_MEMORY)) {
        node_set(node, *used_node_mask);
        return node;
    }

    for_each_node_state(n, N_MEMORY) {
        if (node_isset(n, *used_node_mask))
            continue;

        // 使用距离数组计算距离
        val = node_distance(node, n);

        // 惩罚序号较小的节点（"偏好下一个节点"）
        val += (n < node);

        // 偏好无头节点和未使用节点
        if (!cpumask_empty(cpumask_of_node(n)))
            val += PENALTY_FOR_NODE_WITH_CPUS;

        // 轻微偏好负载较少的节点
        val *= MAX_NUMNODES;
        val += node_load[n];

        if (val < min_val) {
            min_val = val;
            best_node = n;
        }
    }

    if (best_node >= 0)
        node_set(best_node, *used_node_mask);

    return best_node;
}
```

### 页面分配流程

```c
// 页面分配主函数 - mm/page_alloc.c
struct page *__alloc_pages_noprof(gfp_t gfp, unsigned int order,
                                  int preferred_nid, nodemask_t *nodemask)
{
    struct page *page;
    unsigned int alloc_flags = ALLOC_WMARK_LOW;
    gfp_t alloc_gfp;
    struct alloc_context ac = { };

    // 准备分配上下文
    if (!prepare_alloc_pages(gfp, order, preferred_nid, nodemask, &ac,
                           &alloc_gfp, &alloc_flags))
        return NULL;

    // 首次分配尝试
    page = get_page_from_freelist(alloc_gfp, order, alloc_flags, &ac);
    if (likely(page))
        goto out;

    // 如果失败，进入慢路径
    return __alloc_pages_slowpath(alloc_gfp, order, &ac);
}

// 从空闲列表获取页面
static struct page *get_page_from_freelist(gfp_t gfp_mask, unsigned int order,
                                         int alloc_flags,
                                         const struct alloc_context *ac)
{
    struct zoneref *z;
    struct zone *zone;
    struct pglist_data *last_pgdat = NULL;

    // 扫描zonelist，寻找有足够空闲内存的zone
    for_next_zone_zonelist_nodemask(zone, z, ac->highest_zoneidx,
                                   ac->nodemask) {
        struct page *page;
        unsigned long mark;

        // 检查cpuset限制
        if (cpusets_enabled() &&
            (alloc_flags & ALLOC_CPUSET) &&
            !__cpuset_zone_allowed(zone, gfp_mask))
                continue;

        // 检查脏页限制
        if (ac->spread_dirty_pages) {
            if (last_pgdat != zone->zone_pgdat) {
                last_pgdat = zone->zone_pgdat;
                last_pgdat_dirty_ok = node_dirty_ok(zone->zone_pgdat);
            }
            if (!last_pgdat_dirty_ok)
                continue;
        }

        // 检查水印
        mark = wmark_pages(zone, alloc_flags & ALLOC_WMARK_MASK);
        if (!zone_watermark_fast(zone, order, mark,
                               ac->highest_zoneidx, alloc_flags, gfp_mask)) {
            
            // 尝试节点回收
            if (!node_reclaim_enabled() ||
                !zone_allows_reclaim(zonelist_zone(ac->preferred_zoneref), zone))
                continue;

            ret = node_reclaim(zone->zone_pgdat, gfp_mask, order);
            switch (ret) {
            case NODE_RECLAIM_NOSCAN:
                continue;
            case NODE_RECLAIM_FULL:
                continue;
            default:
                if (zone_watermark_ok(zone, order, mark,
                    ac->highest_zoneidx, alloc_flags))
                    goto try_this_zone;
                continue;
            }
        }

try_this_zone:
        page = rmqueue(zonelist_zone(ac->preferred_zoneref), zone, order,
                      gfp_mask, alloc_flags, ac->migratetype);
        if (page) {
            prep_new_page(page, order, gfp_mask, alloc_flags);
            return page;
        }
    }

    return NULL;
}
```

## 内存策略 (Memory Policy)

Linux提供了多种NUMA内存分配策略：

```c
// 内存策略类型 - include/uapi/linux/mempolicy.h
enum {
    MPOL_DEFAULT,        // 默认策略：本地节点优先
    MPOL_PREFERRED,      // 首选策略：优先指定节点
    MPOL_BIND,          // 绑定策略：仅从指定节点分配  
    MPOL_INTERLEAVE,    // 交错策略：在节点间轮询分配
    MPOL_LOCAL,         // 本地策略：仅从本地节点分配
    MPOL_PREFERRED_MANY // 多首选策略：从多个首选节点分配
};

// 内存策略结构
struct mempolicy {
    atomic_t refcnt;                // 引用计数
    unsigned short mode;            // 策略模式
    unsigned short flags;           // 策略标志
    nodemask_t nodes;              // 节点掩码
    int home_node;                 // 主节点（用于BIND和PREFERRED_MANY）
    
    union {
        nodemask_t cpuset_mems_allowed; // cpuset允许的内存节点
        nodemask_t user_nodemask;       // 用户指定的节点掩码
    } w;
};

// 策略应用示例
static int policy_node(gfp_t gfp, struct mempolicy *policy, int nd)
{
    switch (policy->mode) {
    case MPOL_PREFERRED:
        if (node_isset(curnid, policy->nodes))
            goto out;
        polnid = first_node(policy->nodes);
        break;

    case MPOL_INTERLEAVE:
        polnid = interleave_nid(policy, ilx);
        break;

    case MPOL_BIND:
        // 仅使用策略允许的节点
        if (node_isset(curnid, policy->nodes))
            goto out;
        z = first_zones_zonelist(
                node_zonelist(thisnid, GFP_HIGHUSER),
                gfp_zone(GFP_HIGHUSER),
                &policy->nodes);
        polnid = zonelist_node_idx(z);
        break;

    case MPOL_LOCAL:
        polnid = numa_node_id();
        break;
    }
    
    return polnid;
}
```

## NUMA感知调度

### NUMA平衡 (NUMA Balancing)

Linux通过自动页面迁移来优化NUMA局部性：

```c
// NUMA故障处理 - kernel/sched/fair.c  
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
    if (!p->mm)
        return;

    // 分配NUMA故障统计数组
    if (unlikely(!p->numa_faults)) {
        int size = sizeof(*p->numa_faults) * 
                   NR_NUMA_HINT_FAULT_BUCKETS * nr_node_ids;

        p->numa_faults = kzalloc(size, GFP_KERNEL|__GFP_NOWARN);
        if (!p->numa_faults)
            return;

        p->total_numa_faults = 0;
        memset(p->numa_faults_locality, 0, sizeof(p->numa_faults_locality));
    }

    // 确定访问类型（私有/共享）
    if (unlikely(last_cpupid == (-1 & LAST_CPUPID_MASK))) {
        priv = 1;
    } else {
        priv = cpupid_match_pid(p, last_cpupid);
        if (!priv && !(flags & TNF_NO_GROUP))
            task_numa_group(p, last_cpupid, flags, &priv);
    }

    // 更新NUMA统计
    task_numa_placement(p);

    // 检查是否需要迁移
    if (time_after(jiffies, p->numa_migrate_retry)) {
        task_numa_migrate(p);
    }
}

// 页面迁移决策
bool should_numa_migrate_memory(struct task_struct *p, struct folio *folio,
                               int src_nid, int dst_cpu)
{
    struct numa_group *ng = deref_curr_numa_group(p);
    int dst_nid = cpu_to_node(dst_cpu);
    int last_cpupid, this_cpupid;

    // 不能迁移到无内存节点
    if (!node_state(dst_nid, N_MEMORY))
        return false;

    // 慢内存节点处理
    if (folio_use_access_time(folio)) {
        struct pglist_data *pgdat;
        unsigned long rate_limit;
        unsigned int latency, th, def_th;

        pgdat = NODE_DATA(dst_nid);
        if (pgdat_free_space_enough(pgdat)) {
            pgdat->nbp_threshold = 0;  // 重置热阈值
            return true;
        }

        // 检查访问延迟阈值
        def_th = sysctl_numa_balancing_hot_threshold;
        rate_limit = sysctl_numa_balancing_promote_rate_limit << (20 - PAGE_SHIFT);
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

    // 允许早期故障或私有故障立即迁移
    if ((p->numa_preferred_nid == NUMA_NO_NODE || p->numa_scan_seq <= 4) &&
        (cpupid_pid_unset(last_cpupid) || cpupid_match_pid(p, last_cpupid)))
        return true;

    // 多阶段节点选择过滤
    if (!cpupid_pid_unset(last_cpupid) &&
        cpupid_to_nid(last_cpupid) != dst_nid)
        return false;

    // 总是允许私有故障迁移  
    if (cpupid_match_pid(p, last_cpupid))
        return true;

    return false;
}
```

### NUMA组调度

```c
// NUMA组结构
struct numa_group {
    refcount_t refcount;             // 引用计数
    spinlock_t lock;                 // 组锁
    
    int nr_tasks;                    // 任务数量
    pid_t gid;                       // 组ID
    int active_nodes;                // 活跃节点数
    
    struct rcu_head rcu;             // RCU头
    unsigned long total_faults;      // 总故障数
    unsigned long max_faults_cpu;    // CPU最大故障数
    
    unsigned long *faults_cpu;       // 每CPU故障数组  
    unsigned long faults[];          // 故障数组
};

// 任务迁移评估
static int task_numa_migrate(struct task_struct *p)
{
    struct task_numa_env env = {
        .p = p,
        .src_cpu = task_cpu(p),
        .src_nid = task_node(p),
        .imbalance_pct = 112,
        .best_task = NULL,
        .best_imp = 0,
        .best_cpu = -1,
    };
    
    unsigned long taskweight, groupweight;
    struct sched_domain *sd;
    long taskimp, groupimp;
    struct numa_group *ng;
    int nid, ret, dist;

    // 获取最小的SD_NUMA域
    rcu_read_lock();
    sd = rcu_dereference(per_cpu(sd_numa, env.src_cpu));
    if (sd) {
        env.imbalance_pct = 100 + (sd->imbalance_pct - 100) / 2;
        env.imb_numa_nr = sd->imb_numa_nr;
    }
    rcu_read_unlock();

    if (unlikely(!sd)) {
        sched_setnuma(p, task_node(p));
        return -EINVAL;
    }

    env.dst_nid = p->numa_preferred_nid;
    dist = env.dist = node_distance(env.src_nid, env.dst_nid);
    
    // 计算任务和组权重
    taskweight = task_weight(p, env.src_nid, dist);
    groupweight = group_weight(p, env.src_nid, dist);
    
    // 更新源和目标统计
    update_numa_stats(&env, &env.src_stats, env.src_nid, false);
    taskimp = task_weight(p, env.dst_nid, dist) - taskweight;
    groupimp = group_weight(p, env.dst_nid, dist) - groupweight;
    update_numa_stats(&env, &env.dst_stats, env.dst_nid, true);

    // 在首选节点上寻找位置
    task_numa_find_cpu(&env, taskimp, groupimp);

    // 如果没找到合适位置，搜索其他节点
    ng = deref_curr_numa_group(p);
    if (env.best_cpu == -1 || (ng && ng->active_nodes > 1)) {
        for_each_node_state(nid, N_CPU) {
            if (nid == env.src_nid || nid == p->numa_preferred_nid)
                continue;

            dist = node_distance(env.src_nid, env.dst_nid);
            
            // 只考虑对任务和组都有益的节点
            taskimp = task_weight(p, nid, dist) - taskweight;
            groupimp = group_weight(p, nid, dist) - groupweight;
            if (taskimp < 0 && groupimp < 0)
                continue;

            env.dist = dist;
            env.dst_nid = nid;
            update_numa_stats(&env, &env.dst_stats, env.dst_nid, true);
            task_numa_find_cpu(&env, taskimp, groupimp);
        }
    }

    // 执行迁移
    if (env.best_cpu == -1) {
        trace_sched_stick_numa(p, env.src_cpu, NULL, -1);
        return -EAGAIN;
    }

    best_rq = cpu_rq(env.best_cpu);
    if (env.best_task == NULL) {
        ret = migrate_task_to(p, env.best_cpu);
        if (ret != 0)
            trace_sched_stick_numa(p, env.src_cpu, NULL, env.best_cpu);
        return ret;
    }

    // 执行任务交换
    ret = migrate_swap(p, env.best_task, env.best_cpu, env.src_cpu);
    if (ret != 0)
        trace_sched_stick_numa(p, env.src_cpu, env.best_task, env.best_cpu);
    
    put_task_struct(env.best_task);
    return ret;
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

    // 检查进程是否退出
    if (p->flags & PF_EXITING)
        return;

    if (!mm->numa_next_scan) {
        mm->numa_next_scan = now +
            msecs_to_jiffies(sysctl_numa_balancing_scan_delay);
    }

    // 执行扫描频率限制
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

    // 延迟任务，让其他任务有机会
    p->node_stamp += 2 * TICK_NSEC;

    pages = sysctl_numa_balancing_scan_size;
    pages <<= 20 - PAGE_SHIFT; /* MB转页 */
    virtpages = pages * 8;     /* 扫描8倍虚拟空间 */
    
    if (!pages)
        return;

    if (!mmap_read_trylock(mm))
        return;

    // 扫描VMA并设置NUMA提示
    start = mm->numa_scan_offset;
    // ... VMA扫描逻辑 ...
    
    mmap_read_unlock(mm);
    
    // 更新扫描周期
    if (nr_pte_updates)
        update_task_scan_period(p, jiffies, nr_pte_updates);
}

// 初始化NUMA平衡
void init_numa_balancing(unsigned long clone_flags, struct task_struct *p)
{
    int mm_users = 0;
    struct mm_struct *mm = p->mm;

    if (mm) {
        mm_users = atomic_read(&mm->mm_users);
        if (mm_users == 1) {
            mm->numa_next_scan = jiffies + 
                msecs_to_jiffies(sysctl_numa_balancing_scan_delay);
            mm->numa_scan_seq = 0;
        }
    }
    
    p->node_stamp = 0;
    p->numa_scan_seq = mm ? mm->numa_scan_seq : 0;
    p->numa_scan_period = sysctl_numa_balancing_scan_delay;
    p->numa_migrate_retry = 0;
    
    p->numa_work.next = &p->numa_work;
    p->numa_faults = NULL;
    p->numa_pages_migrated = 0;
    p->total_numa_faults = 0;
    RCU_INIT_POINTER(p->numa_group, NULL);
    p->last_task_numa_placement = 0;
    p->last_sum_exec_runtime = 0;

    init_task_work(&p->numa_work, task_numa_work);

    // 新地址空间，重置首选nid
    if (!(clone_flags & CLONE_VM)) {
        p->numa_preferred_nid = NUMA_NO_NODE;
        return;
    }

    // 新线程，错开扫描开始时间
    if (mm) {
        unsigned int delay;
        delay = min_t(unsigned int, task_scan_max(current),
                     current->numa_scan_period * mm_users * NSEC_PER_MSEC);
        delay += 2 * TICK_NSEC;
        p->node_stamp = delay;
    }
}
```

## 性能优化

### 本地性优化策略

```c
// 检查迁移是否降低局部性 - kernel/sched/fair.c
static int migrate_degrades_locality(struct task_struct *p, struct lb_env *env)
{
    struct numa_group *numa_group = rcu_dereference(p->numa_group);
    unsigned long src_weight, dst_weight;
    int src_nid, dst_nid, dist;

    if (!static_branch_likely(&sched_numa_balancing))
        return -1;

    if (!p->numa_faults || !(env->sd->flags & SD_NUMA))
        return -1;

    src_nid = cpu_to_node(env->src_cpu);
    dst_nid = cpu_to_node(env->dst_cpu);

    if (src_nid == dst_nid)
        return -1;

    // 从首选节点迁移总是不好的
    if (src_nid == p->numa_preferred_nid) {
        if (env->src_rq->nr_running > env->src_rq->nr_preferred_running)
            return 1;
        else
            return -1;
    }

    // 鼓励迁移到首选节点
    if (dst_nid == p->numa_preferred_nid)
        return 0;

    // 保持核心空闲通常比降低局部性更糟糕
    if (env->idle == CPU_IDLE)
        return -1;

    dist = node_distance(src_nid, dst_nid);
    if (numa_group) {
        src_weight = group_weight(p, src_nid, dist);
        dst_weight = group_weight(p, dst_nid, dist);
    } else {
        src_weight = task_weight(p, src_nid, dist);
        dst_weight = task_weight(p, dst_nid, dist);
    }

    return dst_weight < src_weight;
}
```

### CPU缓存友好调度

```c
// NUMA感知唤醒 - kernel/sched/fair.c
static int select_idle_sibling(struct task_struct *p, int prev, int target)
{
    bool has_idle_core = false;
    struct sched_domain *sd;
    unsigned long task_util, util_min, util_max;
    int i, recent_used_cpu, prev_aff = -1;

    // 考虑NUMA亲和性
    if (static_branch_likely(&sched_numa_balancing)) {
        if (p->numa_preferred_nid != NUMA_NO_NODE) {
            int preferred_cpu = cpumask_any(cpumask_of_node(p->numa_preferred_nid));
            if (preferred_cpu < nr_cpu_ids && 
                cpumask_test_cpu(preferred_cpu, p->cpus_ptr)) {
                if (available_idle_cpu(preferred_cpu) || 
                    sched_idle_cpu(preferred_cpu))
                    return preferred_cpu;
            }
        }
    }

    // 继续常规调度逻辑...
    return target;
}
```

## 系统配置

### 内核参数

NUMA相关的重要内核参数：

```bash
# NUMA平衡开关
/proc/sys/kernel/numa_balancing = 1

# NUMA平衡扫描延迟（毫秒）
/proc/sys/kernel/numa_balancing_scan_delay_ms = 1000

# NUMA平衡扫描周期最大值（毫秒）  
/proc/sys/kernel/numa_balancing_scan_period_max_ms = 60000

# NUMA平衡扫描大小（MB）
/proc/sys/kernel/numa_balancing_scan_size_mb = 256

# 节点回收距离阈值
/proc/sys/vm/node_reclaim_distance = 30

# 节点回收模式
/proc/sys/vm/zone_reclaim_mode = 0
```

### 用户空间工具

```bash
# 查看NUMA拓扑
numactl --hardware

# 设置NUMA策略运行程序
numactl --membind=0,1 --cpunodebind=0,1 ./program

# 查看进程NUMA信息  
numastat -p <pid>

# 查看NUMA统计
cat /proc/meminfo | grep Numa
cat /sys/devices/system/node/node*/meminfo
```

## 架构图

```mermaid
graph TB
    subgraph "NUMA系统架构"
        subgraph "Node 0"
            CPU0[CPU Core 0-3]
            MEM0[Local Memory]
            CPU0 <--> MEM0
        end
        
        subgraph "Node 1"  
            CPU1[CPU Core 4-7]
            MEM1[Local Memory]
            CPU1 <--> MEM1
        end
        
        subgraph "Node 2"
            CPU2[CPU Core 8-11] 
            MEM2[Local Memory]
            CPU2 <--> MEM2
        end
        
        CPU0 <-.慢.-> MEM1
        CPU0 <-.慢.-> MEM2
        CPU1 <-.慢.-> MEM0
        CPU1 <-.慢.-> MEM2  
        CPU2 <-.慢.-> MEM0
        CPU2 <-.慢.-> MEM1
    end
    
    subgraph "Linux NUMA管理"
        TOPO[拓扑发现]
        POLICY[内存策略] 
        SCHED[调度优化]
        BAL[NUMA平衡]
        
        TOPO --> POLICY
        POLICY --> SCHED
        SCHED --> BAL
    end
    
    subgraph "核心组件"
        subgraph "数据结构"
            PGDAT[pglist_data节点描述符]
            DIST[距离表]
            ZONELIST[Zonelist回退列表]
        end
        
        subgraph "分配策略"  
            LOCAL[本地优先分配]
            FALLBACK[跨节点回退]
            RECLAIM[节点回收]
        end
        
        subgraph "调度优化"
            MIGRATE[页面迁移]
            BALANCE[负载均衡] 
            LOCALITY[局部性优化]
        end
    end
```

## **NUMA内存分配均衡分析**

### **NUMA用途和解决的核心问题**

NUMA设计主要用来解决多核系统中的内存访问瓶颈和可扩展性问题：

```c
// NUMA内存分配均衡的核心作用 - mm/mempolicy.c

/*
 * NUMA内存分配的主要用途：
 * 1. 优化内存访问延迟：将内存分配在最近的节点上
 * 2. 提高内存带宽：分散内存访问到多个内存控制器
 * 3. 减少总线竞争：避免所有CPU竞争单一内存总线
 * 4. 提升系统可扩展性：支持更多CPU和更大内存容量
 * 5. 改善应用性能：通过数据局部性减少访问延迟
 */

// NUMA均衡内存分配策略
enum numa_balancing_reason {
    NUMA_MIGRATE_CPUPID,           // 基于CPU PID的迁移
    NUMA_MIGRATE_MEMORY_POLICY,    // 基于内存策略的迁移
    NUMA_MIGRATE_TASK_PREFERRED,   // 任务首选节点迁移
    NUMA_MIGRATE_GROUP_PREFERRED,  // 组首选节点迁移
    NUMA_MIGRATE_RATE_LIMITED,     // 速率限制迁移
    NUMA_MIGRATE_MEMORY_HOTPLUG,   // 内存热插拔迁移
    NUMA_NR_MIGRATE_REASONS
};

// 解决的关键问题分析
struct numa_problem_solution {
    // 1. 内存访问延迟问题
    struct memory_access_problem {
        u64 local_access_latency;      // 本地访问延迟：~100-200ns
        u64 remote_access_latency;     // 远程访问延迟：~300-500ns
        float performance_penalty;     // 性能损失：150%-250%
        
        // 解决方案：本地化内存分配
        struct local_allocation {
            int preferred_node;         // 首选节点
            struct zonelist *node_zonelists; // 节点分配列表
            int fallback_distance;     // 回退距离阈值
        };
    } access_latency;
    
    // 2. 内存带宽瓶颈问题
    struct memory_bandwidth_problem {
        u64 single_node_bandwidth;     // 单节点带宽限制
        u64 aggregate_bandwidth;       // 聚合带宽
        float bandwidth_utilization;   // 带宽利用率
        
        // 解决方案：分布式内存分配
        struct distributed_allocation {
            int interleave_policy;      // 交错分配策略
            struct nodemask_t allowed_nodes; // 允许的节点掩码
            int spread_factor;          // 分散因子
        };
    } bandwidth_bottleneck;
    
    // 3. 可扩展性问题
    struct scalability_problem {
        int max_cpus_single_node;      // 单节点最大CPU数
        u64 max_memory_single_node;    // 单节点最大内存
        int interconnect_latency;      // 互联延迟
        
        // 解决方案：NUMA感知调度
        struct numa_aware_scheduling {
            bool enable_numa_balancing; // 启用NUMA平衡
            int migration_cost_threshold; // 迁移成本阈值
            int numa_group_weight;      // NUMA组权重
        };
    } scalability_limits;
};

// NUMA内存分配的具体优化措施
static int numa_optimize_memory_allocation(struct mm_struct *mm,
                                         unsigned long addr,
                                         int len, int prot)
{
    struct mempolicy *pol;
    struct page *page;
    int target_node, current_node;
    
    // 1. 确定最优分配节点
    target_node = numa_find_best_node(current);
    if (target_node < 0) {
        target_node = numa_node_id(); // 使用当前节点
    }
    
    // 2. 检查内存策略
    pol = get_task_policy(current);
    switch (pol->mode) {
    case MPOL_BIND:
        // 绑定到特定节点
        target_node = first_node(pol->nodes);
        break;
        
    case MPOL_INTERLEAVE:
        // 交错分配
        target_node = interleave_nid(pol, addr, PAGE_SHIFT);
        break;
        
    case MPOL_PREFERRED:
        // 首选节点分配
        if (pol->nodes.bits[0] != 0) {
            target_node = first_node(pol->nodes);
        }
        break;
        
    case MPOL_LOCAL:
        // 本地节点分配
        target_node = numa_node_id();
        break;
    }
    
    // 3. 执行内存分配
    page = alloc_pages_node(target_node, GFP_KERNEL, get_order(len));
    if (!page) {
        // 分配失败，尝试回退节点
        target_node = numa_get_fallback_node(target_node);
        page = alloc_pages_node(target_node, GFP_KERNEL, get_order(len));
    }
    
    return page ? 0 : -ENOMEM;
}
```

### **NUMA引入的新问题和解决方案**

虽然NUMA解决了内存访问的问题，但也引入了一些新的挑战：

```c
// NUMA引入的问题及其解决方案

// 1. 内存访问不一致性问题
struct numa_access_inconsistency {
    // 问题：不同节点访问同一数据的延迟不同
    struct access_pattern {
        u64 local_latency;              // 本地访问延迟
        u64 remote_latency;             // 远程访问延迟
        float variance_factor;          // 方差因子
    };
    
    // 解决方案：NUMA感知的数据放置
    struct numa_aware_placement {
        bool enable_auto_migration;     // 自动页面迁移
        int migration_threshold;        // 迁移阈值
        int scan_period_ms;             // 扫描周期
        
        // 页面迁移策略
        struct page_migration_policy {
            int min_fault_ratio;        // 最小故障比率
            int max_migrate_pages;      // 最大迁移页数
            bool migrate_on_demand;     // 按需迁移
        };
    };
};

// 2. 负载不平衡问题  
struct numa_load_imbalance {
    // 问题：任务和内存分布不均
    struct imbalance_metrics {
        float cpu_utilization[MAX_NUMNODES];    // 各节点CPU利用率
        u64 memory_pressure[MAX_NUMNODES];      // 各节点内存压力
        int task_distribution[MAX_NUMNODES];    // 任务分布
    };
    
    // 解决方案：负载平衡策略
    struct numa_load_balancing {
        bool enable_numa_balancing;     // 启用NUMA负载平衡
        int rebalance_interval;         // 重平衡间隔
        float imbalance_threshold;      // 不平衡阈值
        
        // 任务迁移策略
        struct task_migration_strategy {
            int migration_cost_factor;  // 迁移成本因子
            bool prefer_local_memory;   // 优先本地内存
            int numa_group_id;          // NUMA组标识
        };
    };
};

// 3. 缓存一致性复杂化
struct numa_cache_coherency {
    // 问题：跨节点缓存同步开销增加
    struct coherency_overhead {
        u64 cache_miss_penalty;         // 缓存未命中惩罚
        int coherency_protocol_cost;    // 一致性协议成本
        float false_sharing_impact;     // 虚假共享影响
    };
    
    // 解决方案：NUMA感知的缓存优化
    struct numa_cache_optimization {
        bool enable_numa_hint_faults;   // 启用NUMA提示错误
        int cache_hot_threshold;        // 缓存热阈值
        bool avoid_cross_node_sharing;  // 避免跨节点共享
        
        // 缓存友好的内存布局
        struct cache_friendly_layout {
            int cache_line_alignment;   // 缓存行对齐
            bool numa_aware_slab;       // NUMA感知slab分配
            int node_local_ratio;       // 节点本地化比率
        };
    };
};

// NUMA问题的综合解决框架
static int numa_problem_mitigation_framework(void)
{
    struct numa_mitigation_config config = {
        // 自适应迁移策略
        .adaptive_migration = {
            .enable = true,
            .cost_threshold = NUMA_MIGRATE_COST_THRESHOLD,
            .scan_delay_ms = 1000,
            .max_scan_window = 256 * 1024 * 1024, // 256MB
        },
        
        // 智能放置策略
        .intelligent_placement = {
            .enable_first_touch = true,
            .enable_next_touch_migrate = true,
            .locality_factor = 85, // 85%本地化目标
        },
        
        // 性能监控和调优
        .performance_monitoring = {
            .enable_perf_events = true,
            .memory_access_sampling = true,
            .cross_node_traffic_monitoring = true,
        }
    };
    
    return numa_apply_mitigation_config(&config);
}

// NUMA优化效果评估
struct numa_optimization_metrics {
    // 性能改进指标
    struct performance_improvement {
        float memory_latency_reduction;  // 内存延迟减少百分比
        float bandwidth_utilization_increase; // 带宽利用率提升
        float overall_performance_gain;  // 总体性能提升
    };
    
    // 系统资源利用率
    struct resource_utilization {
        float average_numa_hit_ratio;    // 平均NUMA命中率
        float cross_node_migration_rate; // 跨节点迁移率
        float load_balance_efficiency;   // 负载平衡效率
    };
    
    // 应用程序影响
    struct application_impact {
        float cpu_bound_app_speedup;     // CPU密集型应用加速
        float memory_bound_app_speedup;  // 内存密集型应用加速
        float mixed_workload_improvement; // 混合工作负载改进
    };
};
```

### **NUMA拓扑发现机制详解**

Linux内核在启动时通过多种途径发现系统的NUMA拓扑：

```c
// NUMA拓扑发现的完整流程 - arch/x86/mm/numa.c

// 1. 硬件拓扑检测入口
static int __init numa_init(void)
{
    int ret = -ENODEV;
    
    // 清理之前的设置
    numa_reset_distance();
    
    // 按优先级顺序尝试不同的发现方法
    
    // 方法1: ACPI SRAT表发现
    if (acpi_disabled)
        goto skip_acpi;
        
    ret = acpi_numa_init();
    if (!ret)
        goto discovery_complete;
        
skip_acpi:
    // 方法2: AMD专用NUMA发现
    ret = amd_numa_init();
    if (!ret)
        goto discovery_complete;
        
    // 方法3: 虚假NUMA用于测试
    if (numa_fake_node)
        ret = fake_numa_init();
        
discovery_complete:
    if (ret) {
        // 发现失败，创建虚拟NUMA节点
        printk(KERN_INFO "No NUMA configuration found, creating fake node\n");
        ret = dummy_numa_init();
    }
    
    // 初始化NUMA距离
    numa_init_distance();
    
    // 设置CPU到节点的映射
    numa_init_cpu_to_node();
    
    // 初始化内存zones
    numa_init_memory_zones();
    
    return ret;
}

// 2. ACPI SRAT表解析
static int __init acpi_numa_init(void)
{
    struct acpi_table_srat *srat;
    struct acpi_srat_mem_affinity *ma;
    struct acpi_srat_cpu_affinity *ca;
    int ret;
    
    // 查找SRAT表
    ret = acpi_get_table(ACPI_SIG_SRAT, 0, (struct acpi_table_header **)&srat);
    if (ACPI_FAILURE(ret))
        return -ENODEV;
        
    // 解析SRAT表条目
    ret = acpi_table_parse_entries(ACPI_SIG_SRAT,
                                  sizeof(struct acpi_table_srat),
                                  ACPI_SRAT_TYPE_CPU_AFFINITY,
                                  srat_parse_cpu_affinity, 0);
    if (ret < 0)
        goto out_err;
        
    ret = acpi_table_parse_entries(ACPI_SIG_SRAT,
                                  sizeof(struct acpi_table_srat),
                                  ACPI_SRAT_TYPE_MEMORY_AFFINITY,
                                  srat_parse_memory_affinity, 0);
    if (ret < 0)
        goto out_err;
        
    // 解析SLIT表（节点间距离）
    ret = acpi_parse_slit();
    if (ret < 0)
        printk(KERN_WARNING "SLIT table not found, using default distances\n");
        
out_err:
    acpi_put_table((struct acpi_table_header *)srat);
    return ret;
}

// 3. CPU亲和性解析
static int __init srat_parse_cpu_affinity(struct acpi_subtable_header *header,
                                         const unsigned long end)
{
    struct acpi_srat_cpu_affinity *cpu_affinity = 
        (struct acpi_srat_cpu_affinity *)header;
    int node_id, cpu_id;
    
    // 检查条目有效性
    if (!(cpu_affinity->flags & ACPI_SRAT_CPU_ENABLED))
        return 0;
        
    node_id = cpu_affinity->proximity_domain_lo |
              (cpu_affinity->proximity_domain_hi[0] << 8) |
              (cpu_affinity->proximity_domain_hi[1] << 16) |
              (cpu_affinity->proximity_domain_hi[2] << 24);
              
    cpu_id = cpu_affinity->apic_id;
    
    // 设置CPU到节点的映射
    if (cpu_id >= NR_CPUS) {
        printk(KERN_WARNING "SRAT: CPU ID %d exceeds maximum\n", cpu_id);
        return -EINVAL;
    }
    
    set_cpu_numa_node(cpu_id, node_id);
    node_set(node_id, numa_nodes_parsed);
    
    printk(KERN_DEBUG "SRAT: CPU %d -> Node %d\n", cpu_id, node_id);
    
    return 0;
}

// 4. 内存亲和性解析
static int __init srat_parse_memory_affinity(struct acpi_subtable_header *header,
                                            const unsigned long end)
{
    struct acpi_srat_mem_affinity *mem_affinity = 
        (struct acpi_srat_mem_affinity *)header;
    int node_id;
    u64 start, length;
    
    // 检查条目有效性
    if (!(mem_affinity->flags & ACPI_SRAT_MEM_ENABLED))
        return 0;
        
    node_id = mem_affinity->proximity_domain;
    start = mem_affinity->base_address;
    length = mem_affinity->length;
    
    // 注册内存范围到指定节点
    if (numa_add_memblk(node_id, start, start + length) < 0) {
        printk(KERN_WARNING "SRAT: Failed to add memory block [%llx-%llx] to node %d\n",
               start, start + length - 1, node_id);
        return -EINVAL;
    }
    
    printk(KERN_DEBUG "SRAT: Memory [%llx-%llx] -> Node %d\n", 
           start, start + length - 1, node_id);
    
    return 0;
}

// 5. 节点间距离初始化
static void __init numa_init_distance(void)
{
    int i, j;
    
    // 分配距离表
    numa_distance = memblock_alloc(nr_node_ids * nr_node_ids, PAGE_SIZE);
    if (!numa_distance) {
        printk(KERN_WARNING "Failed to allocate NUMA distance table\n");
        return;
    }
    
    numa_distance_cnt = nr_node_ids;
    
    // 初始化默认距离
    for (i = 0; i < nr_node_ids; i++) {
        for (j = 0; j < nr_node_ids; j++) {
            numa_distance[i * nr_node_ids + j] = 
                (i == j) ? LOCAL_DISTANCE : REMOTE_DISTANCE;
        }
    }
    
    // 应用SLIT表中的距离信息
    if (slit_distance_table) {
        for (i = 0; i < nr_node_ids; i++) {
            for (j = 0; j < nr_node_ids; j++) {
                numa_distance[i * nr_node_ids + j] = 
                    slit_distance_table[i * nr_node_ids + j];
            }
        }
    }
}

// 6. CPU到节点映射初始化
static void __init numa_init_cpu_to_node(void)
{
    int cpu, node;
    
    // 为未映射的CPU分配节点
    for_each_possible_cpu(cpu) {
        node = early_cpu_to_node(cpu);
        if (node == NUMA_NO_NODE) {
            // 使用第一个有效节点作为默认值
            node = first_node(numa_nodes_parsed);
            set_cpu_numa_node(cpu, node);
        }
    }
    
    // 验证映射的正确性
    for_each_possible_cpu(cpu) {
        node = early_cpu_to_node(cpu);
        if (!node_isset(node, numa_nodes_parsed)) {
            printk(KERN_WARNING "CPU %d mapped to invalid node %d\n", 
                   cpu, node);
        }
    }
}
```

### **NUMA工作时序图**

```mermaid
sequenceDiagram
    participant **BIOS** as **BIOS/UEFI**
    participant **Kernel** as **Linux内核**
    participant **ACPI** as **ACPI子系统**
    participant **MM** as **内存管理**
    participant **Sched** as **进程调度器**
    participant **App** as **用户应用**
    
    Note over **BIOS**,**App**: **NUMA系统完整工作时序流程**
    
    **BIOS**->>**BIOS**: **硬件拓扑检测**
    **BIOS**->>**BIOS**: **生成SRAT/SLIT表**
    **BIOS**->>**Kernel**: **系统启动，传递ACPI表**
    
    activate **Kernel**
    **Kernel**->>**ACPI**: **numa_init()调用**
    activate **ACPI**
    
    **ACPI**->>**ACPI**: **acpi_numa_init()**
    **ACPI**->>**ACPI**: **解析SRAT表**
    Note right of **ACPI**: **CPU亲和性：CPU->节点映射<br/>内存亲和性：内存范围->节点映射**
    
    **ACPI**->>**ACPI**: **解析SLIT表** 
    Note right of **ACPI**: **节点间距离矩阵<br/>本地距离=10，远程距离=20+**
    
    **ACPI**->>**Kernel**: **返回NUMA拓扑信息**
    deactivate **ACPI**
    
    **Kernel**->>**MM**: **numa_init_distance()**
    activate **MM**
    **MM**->>**MM**: **初始化距离表**
    **MM**->>**MM**: **设置zonelist回退顺序**
    **MM**->>**Kernel**: **内存管理器就绪**
    deactivate **MM**
    
    **Kernel**->>**Sched**: **numa_init_cpu_to_node()**
    activate **Sched**
    **Sched**->>**Sched**: **建立CPU-节点映射**
    **Sched**->>**Sched**: **初始化NUMA调度域**
    **Sched**->>**Kernel**: **调度器NUMA感知就绪**
    deactivate **Sched**
    
    **Kernel**->>**Kernel**: **启动per-node kswapd**
    **Kernel**->>**App**: **系统启动完成**
    deactivate **Kernel**
    
    Note over **App**: **运行时NUMA操作**
    
    **App**->>**MM**: **malloc(size)**
    activate **MM**
    
    **MM**->>**MM**: **确定分配策略**
    alt **首次分配(First-touch)**
        **MM**->>**MM**: **分配到当前CPU节点**
        **MM**->>**App**: **返回本地内存地址**
    else **NUMA策略分配**
        **MM**->>**MM**: **检查mempolicy**
        
        alt **MPOL_BIND策略**
            **MM**->>**MM**: **强制指定节点分配**
        else **MPOL_INTERLEAVE策略**
            **MM**->>**MM**: **轮询各节点分配**
        else **MPOL_PREFERRED策略**
            **MM**->>**MM**: **首选节点分配，回退其他节点**
        end
        
        **MM**->>**App**: **返回内存地址**
    end
    
    deactivate **MM**
    
    **App**->>**MM**: **访问内存页面**
    activate **MM**
    
    alt **本地节点访问**
        **MM**->>**App**: **快速访问（~100-200ns）**
    else **远程节点访问**
        **MM**->>**MM**: **NUMA fault检测**
        **MM**->>**Sched**: **触发NUMA balancing**
        
        activate **Sched**
        **Sched**->>**Sched**: **评估迁移收益**
        Note right of **Sched**: **考虑因子：<br/>1. 访问频率<br/>2. 迁移成本<br/>3. 节点负载<br/>4. 任务亲和性**
        
        alt **迁移收益高**
            **Sched**->>**MM**: **migrate_misplaced_page()**
            **MM**->>**MM**: **页面迁移到本地节点**
            **MM**->>**Sched**: **更新任务NUMA统计**
            **Sched**->>**Sched**: **调整任务首选节点**
        else **保持现状**
            **Sched**->>**MM**: **不进行迁移**
        end
        
        deactivate **Sched**
        **MM**->>**App**: **访问完成（可能已迁移）**
    end
    
    deactivate **MM**
    
    **App**->>**Sched**: **fork()创建子进程**
    activate **Sched**
    **Sched**->>**Sched**: **继承父进程NUMA属性**
    **Sched**->>**Sched**: **选择执行节点**
    Note right of **Sched**: **考虑因子：<br/>1. 父进程节点<br/>2. 内存位置<br/>3. 节点负载**
    **Sched**->>**App**: **子进程在最优节点启动**
    deactivate **Sched**
```

## 总结

Linux NUMA架构通过以下核心机制实现高效的非均匀内存访问管理：

**拓扑管理**：
- 自动发现NUMA拓扑结构
- 计算节点间访问距离
- 构建调度域层次结构

**内存分配**：
- 本地节点优先分配策略
- 基于距离的回退机制
- 多种内存分配策略支持

**进程调度**：
- NUMA感知的任务调度
- 自动页面迁移优化
- 内存访问局部性保持

**性能优化**：
- 减少跨节点内存访问
- 优化缓存友好性
- 动态负载均衡

这些机制共同工作，使Linux能够在NUMA系统上实现接近硬件最优的内存访问性能，为现代多核、多插槽服务器提供了强大的可扩展性支持。
