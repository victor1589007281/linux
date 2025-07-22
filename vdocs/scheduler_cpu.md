# Linux CPU调度器原理与实现分析

## 目录

1. [概述](#概述)
2. [调度器架构](#调度器架构)
3. [调度类体系](#调度类体系)
4. [CFS调度算法详解](#cfs调度算法详解)
5. [核心数据结构](#核心数据结构)
6. [SMP负载均衡](#smp负载均衡)
7. [实时调度](#实时调度)
8. [调度域与拓扑](#调度域与拓扑)
9. [性能优化机制](#性能优化机制)
10. [扩展调度框架](#扩展调度框架)
11. [优点与局限性](#优点与局限性)
12. [总结](#总结)

## 概述

Linux CPU调度器是内核的核心组件之一，负责在多个可运行任务之间分配CPU时间，确保系统的公平性、响应性和整体性能。现代Linux调度器采用分层的调度类架构，支持多种调度策略，能够满足从桌面交互到高性能计算等各种场景的需求。

### 核心设计目标

1. **公平性**：确保每个任务获得公平的CPU时间份额
2. **响应性**：保证交互式任务的低延迟响应
3. **吞吐量**：最大化系统整体的处理能力
4. **实时性**：支持硬实时和软实时任务的确定性调度
5. **可扩展性**：在大规模SMP系统上保持良好性能

### 发展历程

- **Linux 2.4及之前**：简单的时间片轮转调度器
- **Linux 2.6.0-2.6.22**：O(1)调度器，引入优先级数组
- **Linux 2.6.23+**：CFS(完全公平调度器)成为默认调度器
- **Linux 3.14+**：引入Deadline调度器支持硬实时任务
- **Linux 6.6+**：开始过渡到EEVDF(最早合格虚拟截止时间优先)算法
- **Linux 6.12+**：引入sched_ext可扩展调度框架

## 调度器架构

Linux调度器采用分层的调度类(Scheduling Class)架构，每个调度类实现特定的调度策略。

### 调度器层次结构

```c
// 调度器核心架构图
/*
 * 调度器层次结构:
 * 
 * ┌─────────────────────────────────────────┐
 * │           调度器核心(Core)                │
 * │         __schedule()                   │
 * └─────────┬───────────────────────────────┘
 *           │
 * ┌─────────▼───────────────────────────────┐
 * │          调度类选择                      │
 * │      pick_next_task()                  │
 * └─┬───┬───┬───┬───┬───┬───────────────────┘
 *   │   │   │   │   │   │
 *   ▼   ▼   ▼   ▼   ▼   ▼
 *  stop rt  dl fair idle scx
 *  调度 实时 截止 公平 空闲 扩展
 *  类   类   期类  类  类   类
 */

// 调度类优先级（按优先级从高到低排列）
extern const struct sched_class stop_sched_class;      // 最高优先级，停止任务
extern const struct sched_class dl_sched_class;        // Deadline调度类
extern const struct sched_class rt_sched_class;        // 实时调度类
extern const struct sched_class fair_sched_class;      // 公平调度类(CFS)
extern const struct sched_class idle_sched_class;      // 空闲调度类
```

### 任务选择流程

```c
// 核心任务选择逻辑 - kernel/sched/core.c
static inline struct task_struct *
__pick_next_task(struct rq *rq, struct task_struct *prev, struct rq_flags *rf)
{
    const struct sched_class *class;
    struct task_struct *p;

    // CFS快速路径优化：如果所有任务都在fair类中
    if (likely(!sched_class_above(prev->sched_class, &fair_sched_class) &&
               rq->nr_running == rq->cfs.h_nr_running)) {
        p = pick_next_task_fair(rq, prev, rf);
        if (unlikely(p == RETRY_TASK))
            goto restart;

        if (!p) {
            p = pick_task_idle(rq);
            put_prev_set_next_task(rq, prev, p);
        }
        return p;
    }

restart:
    prev_balance(rq, prev, rf);

    // 按优先级遍历所有调度类
    for_each_active_class(class) {
        if (class->pick_next_task) {
            p = class->pick_next_task(rq, prev);
            if (p)
                return p;
        } else {
            p = class->pick_task(rq);
            if (p) {
                put_prev_set_next_task(rq, prev, p);
                return p;
            }
        }
    }

    BUG(); /* idle类应该总是有可运行任务 */
}
```

## 调度类体系

### 调度类抽象接口

```c
// 调度类结构体 - kernel/sched/sched.h
struct sched_class {
    // 任务入队和出队
    void (*enqueue_task) (struct rq *rq, struct task_struct *p, int flags);
    bool (*dequeue_task) (struct rq *rq, struct task_struct *p, int flags);
    
    // 主动让出CPU
    void (*yield_task)   (struct rq *rq);
    bool (*yield_to_task)(struct rq *rq, struct task_struct *p);

    // 抢占检查
    void (*wakeup_preempt)(struct rq *rq, struct task_struct *p, int flags);

    // 负载均衡
    int (*balance)(struct rq *rq, struct task_struct *prev, struct rq_flags *rf);
    
    // 任务选择
    struct task_struct *(*pick_task)(struct rq *rq);
    struct task_struct *(*pick_next_task)(struct rq *rq, struct task_struct *prev);

    // 任务放置和设置
    void (*put_prev_task)(struct rq *rq, struct task_struct *p, struct task_struct *next);
    void (*set_next_task)(struct rq *rq, struct task_struct *p, bool first);

#ifdef CONFIG_SMP
    // SMP相关操作
    int  (*select_task_rq)(struct task_struct *p, int task_cpu, int flags);
    void (*migrate_task_rq)(struct task_struct *p, int new_cpu);
    void (*task_woken)(struct rq *this_rq, struct task_struct *task);
    void (*set_cpus_allowed)(struct task_struct *p, struct affinity_context *ctx);
    void (*rq_online)(struct rq *rq);
    void (*rq_offline)(struct rq *rq);
    struct rq *(*find_lock_rq)(struct task_struct *p, struct rq *rq);
#endif

    // 时钟节拍处理
    void (*task_tick)(struct rq *rq, struct task_struct *p, int queued);
    void (*task_fork)(struct task_struct *p);
    void (*task_dead)(struct task_struct *p);

    // 调度类切换
    void (*switching_to) (struct rq *this_rq, struct task_struct *task);
    void (*switched_from)(struct rq *this_rq, struct task_struct *task);
};
```

### 调度策略映射

```c
// 调度策略定义 - include/uapi/linux/sched.h
#define SCHED_NORMAL    0   // 普通任务(CFS)
#define SCHED_FIFO      1   // 实时FIFO
#define SCHED_RR        2   // 实时轮转
#define SCHED_BATCH     3   // 批处理任务(CFS)
#define SCHED_IDLE      5   // 空闲任务
#define SCHED_DEADLINE  6   // 截止期任务
#define SCHED_EXT       7   // 扩展调度类

// 策略到调度类的映射
static const struct sched_class *sched_class_map[NR_SCHED_CLASSES] = {
    [SCHED_NORMAL]   = &fair_sched_class,
    [SCHED_FIFO]     = &rt_sched_class,
    [SCHED_RR]       = &rt_sched_class,
    [SCHED_BATCH]    = &fair_sched_class,
    [SCHED_IDLE]     = &idle_sched_class,
    [SCHED_DEADLINE] = &dl_sched_class,
    [SCHED_EXT]      = &ext_sched_class,
};
```

## CFS调度算法详解

CFS(Completely Fair Scheduler)是Linux的默认调度器，实现了一个"理想多任务CPU"的概念。

### 核心原理

CFS基于虚拟运行时间(Virtual Runtime)的概念，将CPU时间公平地分配给所有任务：

```c
// 虚拟运行时间计算公式
virtual_runtime = actual_runtime * (NICE_0_LOAD / task_weight)

// 其中：
// - actual_runtime：实际运行时间
// - NICE_0_LOAD：nice值为0的标准权重(1024)
// - task_weight：任务权重(由nice值决定)
```

### CFS数据结构

```c
// CFS运行队列 - kernel/sched/sched.h
struct cfs_rq {
    struct load_weight  load;              // 总负载权重
    unsigned int        nr_running;        // 运行任务数
    unsigned int        h_nr_running;      // 层级运行任务数
    unsigned int        idle_nr_running;   // IDLE任务数
    
    s64                 avg_vruntime;      // 平均虚拟运行时间
    u64                 avg_load;          // 平均负载
    u64                 min_vruntime;      // 最小虚拟运行时间
    
    struct rb_root_cached tasks_timeline;  // 红黑树时间线
    
    struct sched_entity *curr;             // 当前运行的调度实体
    struct sched_entity *next;             // 下一个调度实体

#ifdef CONFIG_SMP
    struct sched_avg    avg;               // SMP负载追踪
#endif

#ifdef CONFIG_CFS_BANDWIDTH
    s64                 runtime_remaining; // 剩余运行时间
    u64                 throttled_clock;   // 限流时钟
    int                 throttled;         // 限流标志
    struct list_head    throttled_list;    // 限流列表
#endif
};

// 调度实体 - include/linux/sched.h
struct sched_entity {
    struct load_weight  load;              // 负载权重
    struct rb_node      run_node;          // 红黑树节点
    u64                 deadline;          // 截止时间
    u64                 min_vruntime;      // 最小虚拟运行时间
    u64                 min_slice;         // 最小时间片

    struct list_head    group_node;        // 组节点
    unsigned char       on_rq;             // 是否在运行队列
    unsigned char       sched_delayed;     // 调度延迟标志

    u64                 exec_start;        // 执行开始时间
    u64                 sum_exec_runtime;  // 累计执行时间
    u64                 prev_sum_exec_runtime; // 上一次累计时间
    u64                 vruntime;          // 虚拟运行时间
    s64                 vlag;              // 虚拟滞后
    u64                 slice;             // 时间片

    u64                 nr_migrations;     // 迁移次数

#ifdef CONFIG_FAIR_GROUP_SCHED
    int                 depth;             // 层次深度
    struct sched_entity *parent;          // 父调度实体
    struct cfs_rq       *cfs_rq;           // 所属CFS运行队列
    struct cfs_rq       *my_q;             // 拥有的CFS队列
    unsigned long       runnable_weight;   // 可运行权重
#endif

#ifdef CONFIG_SMP
    struct sched_avg    avg;               // 平均负载追踪
#endif
};
```

### CFS调度流程

```c
// CFS任务选择 - kernel/sched/fair.c
static struct task_struct *
pick_next_task_fair(struct rq *rq, struct task_struct *prev, struct rq_flags *rf)
{
    struct cfs_rq *cfs_rq = &rq->cfs;
    struct sched_entity *se;
    struct task_struct *p;

    // 如果没有可运行任务
    if (!cfs_rq->nr_running)
        goto idle;

    // 处理先前任务
    put_prev_task_balance(rq, prev, rf);

    do {
        se = pick_next_entity(cfs_rq);
        cfs_rq = group_cfs_rq(se);
    } while (cfs_rq);

    p = task_of(se);

done: __maybe_unused;
    if (hrtick_enabled_fair(rq))
        hrtick_start_fair(rq, p);

    util_est_update(&rq->cfs, p, true);

    return p;

idle:
    if (!rf)
        return NULL;

    new_tasks = sched_balance_newidle(rq, rf);
    if (new_tasks)
        return RETRY_TASK;

    return NULL;
}

// 选择下一个调度实体
static struct sched_entity *pick_next_entity(struct cfs_rq *cfs_rq)
{
    struct sched_entity *se = __pick_first_entity(cfs_rq);
    struct sched_entity *left = __pick_first_entity(cfs_rq);

    // 从红黑树最左侧选择vruntime最小的任务
    if (left) {
        se = left;
        // 检查是否需要抢占
        if (sched_feat(NEXT_BUDDY) && cfs_rq->next && wakeup_preempt_entity(cfs_rq->next, left) < 1)
            se = cfs_rq->next;
    }

    // 清除下一个提示
    cfs_rq->next = NULL;

    return se;
}
```

### 虚拟运行时间更新

```c
// 更新当前任务的虚拟运行时间
static void update_curr(struct cfs_rq *cfs_rq)
{
    struct sched_entity *curr = cfs_rq->curr;
    u64 now = rq_clock_task(rq_of(cfs_rq));
    u64 delta_exec;

    if (unlikely(!curr))
        return;

    // 计算执行时间增量
    delta_exec = now - curr->exec_start;
    if (unlikely((s64)delta_exec <= 0))
        return;

    curr->exec_start = now;

    if (schedstat_enabled()) {
        struct sched_statistics *stats = __schedstat_from_se(curr);
        __schedstat_set(stats->exec_max,
                       max(delta_exec, stats->exec_max));
    }

    curr->sum_exec_runtime += delta_exec;
    schedstat_add(cfs_rq->exec_clock, delta_exec);

    // 更新虚拟运行时间
    curr->vruntime += calc_delta_fair(delta_exec, curr);
    update_min_vruntime(cfs_rq);

    if (entity_is_task(curr))
        update_curr_task(task_of(curr), delta_exec);

    account_cfs_rq_runtime(cfs_rq, delta_exec);
}

// 计算公平的时间增量
static u64 calc_delta_fair(u64 delta, struct sched_entity *se)
{
    if (unlikely(se->load.weight != NICE_0_LOAD))
        delta = __calc_delta(delta, NICE_0_LOAD, &se->load);

    return delta;
}
```

### 红黑树维护

```c
// 将调度实体加入红黑树
static void
enqueue_entity(struct cfs_rq *cfs_rq, struct sched_entity *se, int flags)
{
    bool renorm = !(flags & ENQUEUE_WAKEUP) || (flags & ENQUEUE_MIGRATED);
    bool curr = cfs_rq->curr == se;

    // 如果不是当前运行任务且需要重新规范化
    if (renorm && !curr)
        se->vruntime += cfs_rq->min_vruntime;

    update_curr(cfs_rq);

    // 更新统计信息
    if (flags & ENQUEUE_WAKEUP)
        enqueue_sleeper(cfs_rq, se);

    account_entity_enqueue(cfs_rq, se);

    // 如果不是当前任务，加入红黑树
    if (!curr)
        __enqueue_entity(cfs_rq, se);
    
    se->on_rq = 1;

    if (cfs_rq->nr_running == 1) {
        check_enqueue_throttle(cfs_rq);
        if (!throttled_hierarchy(cfs_rq))
            list_add_leaf_cfs_rq(cfs_rq);
    }
}

// 红黑树插入操作
static void __enqueue_entity(struct cfs_rq *cfs_rq, struct sched_entity *se)
{
    struct rb_node **link = &cfs_rq->tasks_timeline.rb_root.rb_node;
    struct rb_node *parent = NULL;
    struct sched_entity *entry;
    bool leftmost = true;

    // 找到插入位置
    while (*link) {
        parent = *link;
        entry = rb_entry(parent, struct sched_entity, run_node);

        if (entity_before(se, entry)) {
            link = &parent->rb_left;
        } else {
            link = &parent->rb_right;
            leftmost = false;
        }
    }

    // 插入节点
    rb_link_node(&se->run_node, parent, link);
    rb_insert_color_cached(&se->run_node, &cfs_rq->tasks_timeline, leftmost);
}
```

## 核心数据结构

### 运行队列(struct rq)

运行队列是每个CPU的核心调度数据结构：

```c
// 主运行队列结构 - kernel/sched/sched.h
struct rq {
    raw_spinlock_t      __lock;            // 运行队列锁

    unsigned int        nr_running;        // 运行任务总数
    u64                 nr_switches;       // 上下文切换次数

    // 各调度类的运行队列
    struct cfs_rq       cfs;               // CFS运行队列
    struct rt_rq        rt;                // RT运行队列
    struct dl_rq        dl;                // DL运行队列
    struct scx_rq       scx;               // SCX运行队列

    // 当前运行任务
    struct task_struct __rcu *curr;        // 当前任务
    struct task_struct  *idle;             // 空闲任务
    struct task_struct  *stop;             // 停止任务

    // 时钟相关
    unsigned int        clock_update_flags;
    u64                 clock;             // 运行队列时钟
    u64                 clock_task;        // 任务时钟
    u64                 clock_pelt;        // PELT时钟
    u64                 clock_idle;        // 空闲时钟

    atomic_t            nr_iowait;         // IO等待任务数

#ifdef CONFIG_SMP
    struct root_domain      *rd;           // 根域
    struct sched_domain __rcu *sd;         // 调度域

    unsigned long       cpu_capacity;      // CPU容量
    
    struct balance_callback *balance_callback; // 均衡回调

    // 负载均衡相关
    unsigned char       nohz_idle_balance;
    unsigned char       idle_balance;
    unsigned long       misfit_task_load;

    // 主动均衡
    int                 active_balance;
    int                 push_cpu;
    struct cpu_stop_work active_balance_work;

    int                 cpu;               // CPU编号
    int                 online;            // 是否在线

    struct list_head    cfs_tasks;         // CFS任务列表

    // 平均负载统计
    struct sched_avg    avg_rt;            // RT平均负载
    struct sched_avg    avg_dl;            // DL平均负载
    struct sched_avg    avg_irq;           // IRQ平均负载

    u64                 idle_stamp;        // 空闲时间戳
    u64                 avg_idle;          // 平均空闲时间
#endif

    // 高精度时钟tick
#ifdef CONFIG_SCHED_HRTICK
    struct hrtimer      hrtick_timer;
#endif

    // 调度统计
#ifdef CONFIG_SCHEDSTATS
    struct sched_info   rq_sched_info;
    unsigned long long  rq_cpu_time;
    
    unsigned int        yld_count;         // yield计数
    unsigned int        sched_count;       // 调度计数
    unsigned int        sched_goidle;      // 空闲调度计数
    unsigned int        ttwu_count;        // 唤醒计数
    unsigned int        ttwu_local;        // 本地唤醒计数
#endif
};
```

### 任务结构(struct task_struct)

任务结构中的调度相关字段：

```c
// 任务调度相关字段 - include/linux/sched.h
struct task_struct {
    // 调度相关状态
    unsigned int            __state;       // 任务状态
    int                     on_rq;         // 是否在运行队列
    int                     on_cpu;        // 是否在CPU上运行

    // 优先级
    int                     prio;          // 动态优先级
    int                     static_prio;   // 静态优先级
    int                     normal_prio;   // 正常优先级
    unsigned int            rt_priority;   // 实时优先级

    // 调度实体
    struct sched_entity     se;            // CFS调度实体
    struct sched_rt_entity  rt;            // RT调度实体
    struct sched_dl_entity  dl;            // DL调度实体
    struct sched_ext_entity scx;           // SCX调度实体

    const struct sched_class *sched_class; // 调度类

#ifdef CONFIG_SMP
    // SMP相关
    int                     wake_cpu;      // 唤醒CPU
    int                     recent_used_cpu; // 最近使用的CPU
    unsigned int            wakee_flips;   // 唤醒翻转次数
    struct task_struct      *last_wakee;   // 最后唤醒的任务
#endif

#ifdef CONFIG_CGROUP_SCHED
    struct task_group       *sched_task_group; // 任务组
#endif

    // CPU亲和性
    const struct cpumask    *cpus_ptr;     // CPU掩码指针
    cpumask_t               cpus_mask;     // CPU掩码
    int                     nr_cpus_allowed; // 允许的CPU数量

    // 统计信息
    u64                     nvcsw;         // 自愿上下文切换
    u64                     nivcsw;        // 非自愿上下文切换
};
```

## SMP负载均衡

Linux调度器在SMP系统上实现了复杂的负载均衡机制，确保工作负载在多个CPU之间的公平分布。

### 负载均衡架构

```c
// 负载均衡的基本原理
/*
 * 负载均衡目标：实现公平的CPU时间分配
 * 
 * 公式：W_i,n/P_i == W_j,n/P_j for all i,j
 * 
 * 其中：
 * - W_i,n：CPU i的第n次权重平均
 * - P_i：CPU i的处理能力
 * 
 * 不平衡度量：
 * imb_i,j = max{avg(W/C), W_i/C_i} - min{avg(W/C), W_j/C_j}
 */

// 负载均衡触发时机
enum load_balance_trigger {
    LB_TICK,           // 周期性负载均衡
    LB_NEWIDLE,        // 新空闲负载均衡  
    LB_WAKEUP,         // 唤醒时负载均衡
    LB_FORK,           // fork时负载均衡
    LB_EXEC,           // exec时负载均衡
};
```

### 调度域层次结构

```c
// 调度域结构 - kernel/sched/topology.c
struct sched_domain {
    struct sched_domain __rcu *parent;     // 父调度域
    struct sched_domain_shared *shared;   // 共享数据
    struct sched_group *groups;           // 调度组链表
    
    unsigned long min_interval;           // 最小均衡间隔
    unsigned long max_interval;           // 最大均衡间隔
    unsigned int busy_factor;             // 忙碌因子
    unsigned int imbalance_pct;           // 不平衡百分比
    
    unsigned int cache_nice_tries;        // 缓存友好尝试次数
    unsigned int flags;                   // 调度域标志
    int level;                            // 层次级别
    
    // 最后均衡时间戳
    unsigned long last_balance;
    
    // 均衡失败计数
    unsigned int balance_interval;
    unsigned int nr_balance_failed;
    
    // 空闲CPU掩码
    unsigned long max_newidle_lb_cost;
    unsigned long next_decay_max_lb_cost;
    
    char *name;                           // 调度域名称

#ifdef CONFIG_SCHED_DEBUG
    // 调试统计
    u64 max_newidle_lb_cost;
    unsigned long next_decay_max_lb_cost;
#endif
};

// 调度组结构
struct sched_group {
    struct sched_group *next;             // 下一个组
    atomic_t ref;                         // 引用计数
    unsigned int group_weight;            // 组权重
    unsigned long group_capacity;         // 组容量
    unsigned long group_util;             // 组利用率
    unsigned int group_type;              // 组类型
    unsigned int group_asym_packing;      // 非对称打包
    unsigned long cpumask[];              // CPU掩码
};
```

### 负载均衡算法

```c
// 主要负载均衡函数 - kernel/sched/fair.c
static int sched_balance_rq(int this_cpu, struct rq *this_rq,
                           struct sched_domain *sd, enum cpu_idle_type idle,
                           int *continue_balancing)
{
    int ld_moved, active_balance = 0;
    struct sched_domain_statistics sds;
    struct sched_group *group;
    struct rq *busiest;
    struct rq_flags rf;
    
    // 收集调度域统计信息
    update_sd_statistics(&sds, sd, this_cpu);
    group = find_busiest_group(&sds, this_cpu, &imbalance, idle);
    
    if (!group)
        goto out_balanced;
    
    // 找到最忙的运行队列
    busiest = find_busiest_queue(&sds, group, idle, imbalance, this_cpu);
    
    if (!busiest)
        goto out_balanced;
    
    BUG_ON(busiest == this_rq);
    
    // 执行负载均衡
    schedstat_add(sd->lb_count[idle], 1);
    
    ld_moved = 0;
    
    // 尝试获取busiest运行队列的锁
    if (busiest->nr_running > 1) {
        rq_lock_irqsave(busiest, &rf);
        
        // 迁移任务
        ld_moved = move_tasks(this_rq, this_cpu, busiest,
                             imbalance, idle, &rf);
        
        rq_unlock(busiest, &rf);
        
        if (ld_moved)
            schedstat_inc(sd->lb_gained[idle]);
    }
    
    // 如果没有迁移任务，尝试主动均衡
    if (!ld_moved && !sd_numa) {
        if (idle != CPU_NOT_IDLE ||
            time_after(jiffies, busiest->next_balance)) {
            
            // 设置主动均衡
            raw_spin_rq_lock(busiest);
            
            if (!busiest->active_balance &&
                (busiest->nr_running > 1) &&
                (this_rq->avg_idle > sysctl_sched_migration_cost ||
                 idle != CPU_NOT_IDLE)) {
                
                busiest->active_balance = 1;
                busiest->push_cpu = this_cpu;
                active_balance = 1;
            }
            raw_spin_rq_unlock(busiest);
            
            if (active_balance) {
                stop_one_cpu_nowait(cpu_of(busiest),
                                   active_load_balance_cpu_stop,
                                   busiest, &busiest->active_balance_work);
            }
        }
    }
    
    return ld_moved;

out_balanced:
    schedstat_inc(sd->lb_balanced[idle]);
    return 0;
}

// 任务迁移函数
static int move_tasks(struct rq *this_rq, int this_cpu, struct rq *busiest,
                     unsigned long imbalance, enum cpu_idle_type idle,
                     struct rq_flags *rf)
{
    struct cfs_rq *busy_cfs_rq;
    struct list_head *tasks = &busiest->cfs_tasks;
    struct task_struct *p;
    int pulled = 0;
    
    if (imbalance <= 0)
        return 0;
    
    while (!list_empty(tasks)) {
        p = list_first_entry(tasks, struct task_struct, se.group_node);
        
        // 检查是否可以迁移此任务
        if (!can_migrate_task(p, this_cpu, idle))
            goto next;
        
        // 尝试获取任务
        if (task_running(busiest, p))
            goto next;
        
        // 迁移任务
        dequeue_task(busiest, p, DEQUEUE_NOCLOCK);
        set_task_cpu(p, this_cpu);
        enqueue_task(this_rq, p, ENQUEUE_NOCLOCK);
        
        pulled++;
        imbalance -= task_h_load(p);
        
        if (imbalance <= 0)
            break;

next:
        list_move_tail(&p->se.group_node, tasks);
    }
    
    return pulled;
}
```

### CPU选择策略

```c
// 为唤醒任务选择CPU - kernel/sched/fair.c
static int
select_task_rq_fair(struct task_struct *p, int prev_cpu, int wake_flags)
{
    int sync = (wake_flags & WF_SYNC) && !(current->flags & PF_EXITING);
    struct sched_domain *tmp, *sd = NULL;
    int cpu = smp_processor_id();
    int new_cpu = prev_cpu;
    int want_affine = 0;
    int sd_flag = wake_flags & 0xF;

    // 快速路径：如果要求在当前CPU上运行
    if ((wake_flags & WF_CURRENT_CPU) &&
        cpumask_test_cpu(cpu, p->cpus_ptr))
        return cpu;

    // 能效感知调度
    if (!is_rd_overutilized(this_rq()->rd)) {
        new_cpu = find_energy_efficient_cpu(p, prev_cpu);
        if (new_cpu >= 0)
            return new_cpu;
        new_cpu = prev_cpu;
    }

    // CPU亲和性检查
    want_affine = !wake_wide(p) && cpumask_test_cpu(cpu, p->cpus_ptr);

    rcu_read_lock();
    
    // 遍历调度域层次
    for_each_domain(cpu, tmp) {
        if (want_affine && (tmp->flags & SD_WAKE_AFFINE) &&
            cpumask_test_cpu(prev_cpu, sched_domain_span(tmp))) {
            
            if (cpu != prev_cpu)
                new_cpu = wake_affine(tmp, p, cpu, prev_cpu, sync);
            
            sd = NULL;
            break;
        }

        if (tmp->flags & sd_flag)
            sd = tmp;
        else if (!want_affine)
            break;
    }

    // 在选定的调度域中选择CPU
    if (sd) {
        new_cpu = find_idlest_cpu(sd, p, cpu, prev_cpu, sd_flag);
    } else if (wake_flags & WF_TTWU) {
        // Try to find an idle sibling CPU
        if (want_affine)
            new_cpu = select_idle_sibling(p, prev_cpu, cpu);
    }
    
    rcu_read_unlock();

    return new_cpu;
}

// 查找空闲兄弟CPU
static int select_idle_sibling(struct task_struct *p, int prev, int target)
{
    struct sched_domain *sd;
    unsigned long time = cpu_clock(this) >> 10;
    int i, recent_used_cpu;

    // 检查target是否空闲
    if ((unsigned long)time - p->recent_used_cpu > 3*sysctl_sched_migration_cost ||
        p->recent_used_cpu == prev)
        recent_used_cpu = -1;
    else
        recent_used_cpu = p->recent_used_cpu;

    if (target == recent_used_cpu)
        return target;

    // 检查prev CPU是否空闲
    if (cpumask_test_cpu(prev, p->cpus_ptr) && idle_cpu(prev) &&
        (recent_used_cpu != prev || time - p->recent_used_cpu > sysctl_sched_migration_cost))
        return prev;

    // 检查recent_used_cpu是否空闲
    if (recent_used_cpu != prev && recent_used_cpu != target &&
        cpumask_test_cpu(recent_used_cpu, p->cpus_ptr) &&
        idle_cpu(recent_used_cpu) &&
        cpumask_test_cpu(recent_used_cpu, sched_domain_span(sd)))
        return recent_used_cpu;

    // 在LLC域中查找空闲CPU
    sd = rcu_dereference(per_cpu(sd_llc, target));
    for_each_cpu_wrap(i, sched_domain_span(sd), target + 1) {
        if (!cpumask_test_cpu(i, p->cpus_ptr))
            continue;
        if (idle_cpu(i))
            return i;
    }

    return target;
}
```

## 实时调度

Linux支持两类实时调度：SCHED_FIFO和SCHED_RR，提供确定性的调度保证。

### 实时调度器结构

```c
// RT运行队列 - kernel/sched/sched.h  
struct rt_rq {
    struct rt_prio_array    active;           // 活动优先级数组
    unsigned int            rt_nr_running;    // RT任务数量
    unsigned int            rr_nr_running;    // RR任务数量

    struct {
        int                 curr;             // 当前优先级
        int                 next;             // 下一个优先级
    } highest_prio;

    u64                     rt_time;          // RT时间
    u64                     rt_runtime;       // RT运行时间
    raw_spinlock_t          rt_runtime_lock;  // RT运行时锁

#ifdef CONFIG_RT_GROUP_SCHED
    struct rq              *rq;               // 所属运行队列
    struct task_group      *tg;               // 任务组
#endif
};

// RT优先级数组
struct rt_prio_array {
    DECLARE_BITMAP(bitmap, MAX_RT_PRIO+1);    // 优先级位图
    struct list_head queue[MAX_RT_PRIO];      // 优先级队列数组
};

// RT调度实体
struct sched_rt_entity {
    struct list_head        run_list;         // 运行列表
    unsigned long           timeout;          // 超时时间
    unsigned long           watchdog_stamp;   // 看门狗时间戳
    unsigned int            time_slice;       // 时间片
    unsigned short          on_rq;            // 在队列标志
    unsigned short          on_list;          // 在列表标志

    struct sched_rt_entity  *back;            // 后向指针
#ifdef CONFIG_RT_GROUP_SCHED
    struct sched_rt_entity  *parent;          // 父RT实体
    struct rt_rq            *rt_rq;           // RT运行队列
    struct rt_rq            *my_q;            // 拥有的队列
#endif
};
```

### RT调度算法

```c
// RT任务选择 - kernel/sched/rt.c
static struct task_struct *_pick_next_task_rt(struct rq *rq)
{
    struct sched_rt_entity *rt_se;
    struct task_struct *p;
    struct rt_rq *rt_rq = &rq->rt;

    do {
        rt_se = pick_next_rt_entity(rt_rq);
        BUG_ON(!rt_se);
        rt_rq = group_rt_rq(rt_se);
    } while (rt_rq);

    p = rt_task_of(rt_se);
    p->se.exec_start = rq_clock_task(rq);

    return p;
}

// 选择下一个RT实体
static struct sched_rt_entity *pick_next_rt_entity(struct rt_rq *rt_rq)
{
    struct rt_prio_array *array = &rt_rq->active;
    struct sched_rt_entity *next = NULL;
    struct list_head *queue;
    int idx;

    // 找到最高优先级
    idx = sched_find_first_bit(array->bitmap);
    BUG_ON(idx >= MAX_RT_PRIO);

    queue = array->queue + idx;
    next = list_entry(queue->next, struct sched_rt_entity, run_list);

    return next;
}

// RT任务入队
static void enqueue_task_rt(struct rq *rq, struct task_struct *p, int flags)
{
    struct sched_rt_entity *rt_se = &p->rt;

    if (flags & ENQUEUE_WAKEUP)
        rt_se->timeout = 0;

    check_schedstat_required();
    update_stats_wait_start_rt(rt_rq_of_se(rt_se), rt_se);

    enqueue_rt_entity(rt_se, flags);

    if (!task_current(rq, p) && tsk_nr_cpus_allowed(p) > 1)
        enqueue_pushable_task(rq, p);
}

// RT实体入队
static void enqueue_rt_entity(struct sched_rt_entity *rt_se, int flags)
{
    struct rq *rq = rq_of_rt_se(rt_se);
    struct rt_rq *rt_rq = rt_rq_of_se(rt_se);
    struct rt_prio_array *array = &rt_rq->active;
    struct list_head *queue = array->queue + rt_se_prio(rt_se);

    // 如果是组调度实体，需要递归处理
    if (group_rt_rq && (rt_rq_throttled(group_rt_rq) ||
                        !rt_rq_has_rt_tasks(group_rt_rq)))
        return;

    if (flags & ENQUEUE_HEAD)
        list_add(&rt_se->run_list, queue);
    else
        list_add_tail(&rt_se->run_list, queue);
    
    // 设置优先级位图
    __set_bit(rt_se_prio(rt_se), array->bitmap);

    add_rt_entity_load(rt_se);
    inc_rt_tasks(rt_se, rt_rq);
    inc_rt_group(rt_se, rt_rq);
}
```

### RT带宽控制

```c
// RT带宽结构
struct rt_bandwidth {
    raw_spinlock_t      rt_runtime_lock;     // RT运行时锁
    ktime_t             rt_period;           // RT周期
    u64                 rt_runtime;          // RT运行时间
    struct hrtimer      rt_period_timer;     // 周期定时器
    unsigned int        rt_period_active;    // 周期激活标志
};

// RT带宽检查
static int do_sched_rt_period_timer(struct rt_bandwidth *rt_b, int overrun)
{
    int i, idle = 1, throttled = 0;
    const struct cpumask *span;

    span = sched_rt_period_mask();
    
    // 为每个CPU补充运行时间
    for_each_cpu(i, span) {
        struct rt_rq *rt_rq = sched_rt_period_rt_rq(rt_b, i);
        struct rq *rq = rq_of_rt_rq(rt_rq);
        struct rq_flags rf;
        int skip;

        rq_lock(rq, &rf);

        skip = !rt_rq->rt_time && !rt_rq->rt_nr_running;
        if (skip) {
            rq_unlock(rq, &rf);
            continue;
        }

        raw_spin_lock(&rt_rq->rt_runtime_lock);
        
        if (rt_rq->rt_throttled)
            balance_runtime(rt_rq);
        
        // 重新填充运行时间
        rt_rq->rt_time = 0;
        rt_rq->rt_throttled = 0;
        
        raw_spin_unlock(&rt_rq->rt_runtime_lock);

        rq_unlock(rq, &rf);
    }

    return idle;
}

// RT限流检查
static void update_curr_rt(struct rq *rq)
{
    struct task_struct *curr = rq->curr;
    struct sched_rt_entity *rt_se = &curr->rt;
    u64 delta_exec, runtime;

    if (curr->sched_class != &rt_sched_class)
        return;

    delta_exec = rq_clock_task(rq) - curr->se.exec_start;
    if (unlikely((s64)delta_exec <= 0))
        return;

    curr->se.sum_exec_runtime += delta_exec;
    account_group_exec_runtime(curr, delta_exec);

    curr->se.exec_start = rq_clock_task(rq);
    cgroup_account_cputime(curr, delta_exec);

    if (!rt_bandwidth_enabled())
        return;

    for_each_sched_rt_entity(rt_se) {
        struct rt_rq *rt_rq = rt_rq_of_se(rt_se);

        raw_spin_lock(&rt_rq->rt_runtime_lock);
        rt_rq->rt_time += delta_exec;
        
        // 检查是否超出运行时间
        if (sched_rt_runtime_exceeded(rt_rq))
            resched_curr(rq);
        
        raw_spin_unlock(&rt_rq->rt_runtime_lock);
    }
}
```

## 调度域与拓扑

Linux调度器使用调度域来表示系统的CPU拓扑结构，实现层次化的负载均衡。

### 调度域构建

```c
// 调度域拓扑级别定义
static struct sched_domain_topology_level default_topology[] = {
#ifdef CONFIG_SCHED_SMT
    { cpu_smt_mask, cpu_smt_flags, SD_INIT_NAME(SMT) },
#endif
#ifdef CONFIG_SCHED_MC
    { cpu_coregroup_mask, cpu_core_flags, SD_INIT_NAME(MC) },
#endif
#ifdef CONFIG_SCHED_BOOK
    { cpu_book_mask, SD_INIT_NAME(BOOK) },
#endif
#ifdef CONFIG_SCHED_DRAWER
    { cpu_drawer_mask, SD_INIT_NAME(DRAWER) },
#endif
    { cpu_cpu_mask, SD_INIT_NAME(NODE) },
    { NULL, },
};

// 调度域标志
enum {
    SD_LOAD_BALANCE         = 0x0001,    // 负载均衡
    SD_BALANCE_NEWIDLE      = 0x0002,    // 新空闲均衡
    SD_BALANCE_EXEC         = 0x0004,    // exec时均衡
    SD_BALANCE_FORK         = 0x0008,    // fork时均衡
    SD_BALANCE_WAKE         = 0x0010,    // 唤醒时均衡
    SD_WAKE_AFFINE          = 0x0020,    // 唤醒亲和
    SD_ASYM_CPUCAPACITY     = 0x0040,    // 非对称CPU容量
    SD_SHARE_CPUCAPACITY    = 0x0080,    // 共享CPU容量
    SD_SHARE_PKG_RESOURCES  = 0x0200,    // 共享包资源
    SD_SERIALIZE            = 0x0400,    // 串行化
    SD_ASYM_PACKING         = 0x0800,    // 非对称打包
    SD_PREFER_SIBLING       = 0x1000,    // 优选兄弟
    SD_OVERLAP              = 0x2000,    // 重叠
    SD_NUMA                 = 0x4000,    // NUMA
};

// 构建调度域
static struct sched_domain *
build_sched_domain(struct sched_domain_topology_level *tl,
                  const struct cpumask *cpu_map, struct sched_domain_attr *attr,
                  struct sched_domain *child, int dflags, int cpu)
{
    struct sched_domain *sd = sd_init(tl, cpu_map, child, dflags, cpu);

    if (child) {
        sd->level = child->level + 1;
        sched_domain_level_max = max(sched_domain_level_max, sd->level);
        child->parent = sd;

        if (!cpumask_subset(sched_domain_span(child), sched_domain_span(sd))) {
            pr_err("BUG: arch topology broken\n");
        }
    }
    
    set_domain_attribute(sd, attr);

    return sd;
}

// 调度域初始化
static struct sched_domain *sd_init(struct sched_domain_topology_level *tl,
                                   const struct cpumask *cpu_map,
                                   struct sched_domain *child, int dflags, int cpu)
{
    struct sd_data *sdd = &tl->data;
    struct sched_domain *sd = *per_cpu_ptr(sdd->sd, cpu);
    int sd_id, sd_size, fls = 0;

    // 初始化调度域基本参数
    sd_size = cpumask_weight(tl->mask(cpu));

    if (fls)
        sd->cache_nice_tries = fls;

    sd->flags = tl->flags | dflags;
    sd->private = &tl->data;

    // 设置均衡间隔
    sd->balance_interval = sd_size;
    sd->max_interval = 2*sd->balance_interval;
    sd->busy_factor = 32;
    sd->imbalance_pct = 125;

    // 为特定拓扑级别设置参数
    if (sd->flags & SD_SHARE_CPUCAPACITY) {
        sd->imbalance_pct = 110;
        sd->cache_nice_tries = 0;

    } else if (sd->flags & SD_SHARE_PKG_RESOURCES) {
        sd->cache_nice_tries = 1;
        sd->busy_factor = 64;
        sd->imbalance_pct = 117;

    } else if (sd->flags & SD_NUMA) {
        sd->cache_nice_tries = 2;
        sd->busy_factor = 64;
        sd->imbalance_pct = 125;

        sd->flags |= SD_SERIALIZE;
        if (sched_domains_numa_distance[tl->numa_level] > RECLAIM_DISTANCE) {
            sd->flags &= ~(SD_BALANCE_EXEC | SD_BALANCE_FORK | SD_WAKE_AFFINE);
        }
    }

    return sd;
}
```

### NUMA感知调度

```c
// NUMA平衡器
struct numa_group {
    refcount_t refcount;
    spinlock_t lock;
    int nr_tasks;
    pid_t gid;
    int active_nodes;
    struct rcu_head rcu;
    unsigned long total_faults;
    unsigned long max_faults_cpu;
    unsigned long *faults_cpu;
    unsigned long faults[];
};

// NUMA故障统计
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

    // 分配NUMA统计结构
    if (unlikely(!p->numa_faults)) {
        int size = sizeof(*p->numa_faults) * NR_NUMA_HINT_FAULT_STATS * nr_node_ids;
        p->numa_faults = kzalloc(size, GFP_KERNEL | __GFP_NOWARN);
        if (!p->numa_faults)
            return;

        p->total_numa_faults = 0;
        p->numa_faults_locality[0] = 0;
        p->numa_faults_locality[1] = 0;
    }

    // 记录故障
    task_numa_placement(p);

    // 更新NUMA组
    if (time_after(jiffies, p->numa_migrate_retry)) {
        task_numa_migrate(p);
    }
}

// NUMA任务迁移
static void task_numa_migrate(struct task_struct *p)
{
    struct migration_arg arg = { p, -1 };
    int dest_cpu;

    spin_lock_irq(&p->pi_lock);
    dest_cpu = p->numa_preferred_nid;
    spin_unlock_irq(&p->pi_lock);

    if (dest_cpu == -1)
        return;

    // 尝试迁移到首选NUMA节点
    if (cpumask_any_and(cpumask_of_node(dest_cpu), p->cpus_ptr) < nr_cpu_ids) {
        arg.dest_cpu = cpumask_any_and(cpumask_of_node(dest_cpu), p->cpus_ptr);
        stop_one_cpu(task_cpu(p), migration_cpu_stop, &arg);
    }
}
```

## 性能优化机制

### CPU缓存友好调度

```c
// 亲和性检查 - kernel/sched/fair.c
static int wake_affine(struct sched_domain *sd, struct task_struct *p,
                      int this_cpu, int prev_cpu, int sync)
{
    int target = nr_cpumask_bits;

    if (sched_feat(WA_IDLE))
        target = wake_affine_idle(this_cpu, prev_cpu, sync);

    if (sched_feat(WA_WEIGHT) && target == nr_cpumask_bits)
        target = wake_affine_weight(sd, p, this_cpu, prev_cpu, sync);

    schedstat_inc(p->stats.nr_wakeups_affine_attempts);
    
    if (target != this_cpu)
        return prev_cpu;

    schedstat_inc(p->stats.nr_wakeups_affine);
    schedstat_inc(sd->ttwu_move_affine);

    return target;
}

// 权重感知亲和性
static int
wake_affine_weight(struct sched_domain *sd, struct task_struct *p,
                  int this_cpu, int prev_cpu, int sync)
{
    s64 this_eff_load, prev_eff_load;
    unsigned long task_load;

    this_eff_load = cpu_load(cpu_rq(this_cpu));

    if (sync) {
        unsigned long current_load = task_h_load(current);

        if (current_load > this_eff_load)
            return this_cpu;

        this_eff_load -= current_load;
    }

    task_load = task_h_load(p);
    this_eff_load += task_load;
    
    prev_eff_load = cpu_load(cpu_rq(prev_cpu));
    prev_eff_load -= task_load;

    return this_eff_load <= prev_eff_load ? this_cpu : nr_cpumask_bits;
}
```

### 能效感知调度

```c
// 能效感知CPU选择
static int find_energy_efficient_cpu(struct task_struct *p, int prev_cpu)
{
    struct root_domain *rd = this_rq()->rd;
    int cpu, best_energy_cpu, target = -1;
    int prev_delta = INT_MAX, best_delta = INT_MAX;
    struct sched_domain *sd;
    struct perf_domain *pd;
    
    rcu_read_lock();
    pd = rcu_dereference(rd->pd);
    if (!pd || READ_ONCE(rd->overutilized))
        goto unlock;

    // 遍历性能域
    for (; pd; pd = pd->next) {
        unsigned long cpu_cap, util, base_energy = 0;
        unsigned long prev_delta_pwr = 0, best_delta_pwr = 0;
        int max_spare_cap = -1, max_spare_cap_cpu = -1;

        // 计算当前能耗
        for_each_cpu_and(cpu, perf_domain_span(pd), sched_domain_span(sd)) {
            if (!cpumask_test_cpu(cpu, p->cpus_ptr))
                continue;

            util = cpu_util_next(cpu, p, cpu);
            cpu_cap = capacity_of(cpu);
            
            // 计算备用容量
            if (cpu_cap > util) {
                int spare_cap = cpu_cap - util;
                if (spare_cap > max_spare_cap) {
                    max_spare_cap = spare_cap;
                    max_spare_cap_cpu = cpu;
                }
            }
        }

        if (max_spare_cap_cpu < 0)
            continue;

        // 计算能耗影响
        prev_delta_pwr = compute_energy(pd, prev_cpu, p);
        best_delta_pwr = compute_energy(pd, max_spare_cap_cpu, p);

        // 选择能效最佳的CPU
        if (best_delta_pwr < prev_delta_pwr) {
            prev_delta = prev_delta_pwr;
            best_delta = best_delta_pwr;
            best_energy_cpu = max_spare_cap_cpu;
            target = max_spare_cap_cpu;
        }
    }

unlock:
    rcu_read_unlock();

    return target;
}

// 计算能耗
static unsigned long compute_energy(struct perf_domain *pd, int dst_cpu,
                                  struct task_struct *p)
{
    unsigned long max_util = 0, sum_util = 0, energy = 0;
    int cpu;

    for_each_cpu(cpu, perf_domain_span(pd)) {
        unsigned long cpu_util, util_running = cpu_util_cfs(cpu_rq(cpu));
        unsigned long util_freq = util_running;
        unsigned long util_cap = util_running;

        if (cpu == dst_cpu) {
            // 添加迁移任务的影响
            if (p) {
                util_freq += task_util_est(p);
                util_cap += task_util_est(p);
            }
        }

        cpu_util = effective_cpu_util(cpu, util_freq, ENERGY_UTIL, NULL);
        
        sum_util += min(cpu_util, capacity_orig_of(cpu));
        
        // 记录最大利用率
        max_util = max(max_util, min(cpu_util, capacity_orig_of(cpu)));
    }

    return em_cpu_energy(pd->em_pd, max_util, sum_util);
}
```

### 高精度tick优化

```c
// 高精度tick处理 - kernel/sched/fair.c
#ifdef CONFIG_SCHED_HRTICK
static void hrtick_start_fair(struct rq *rq, struct task_struct *p)
{
    struct sched_entity *se = &p->se;
    struct cfs_rq *cfs_rq = cfs_rq_of(se);

    SCHED_WARN_ON(task_rq(p) != rq);

    if (rq->cfs.h_nr_running > 1) {
        u64 slice = sched_slice(cfs_rq, se);
        u64 ran = se->sum_exec_runtime - se->prev_sum_exec_runtime;
        s64 delta = slice - ran;

        if (delta < 0) {
            if (task_current(rq, p))
                resched_curr(rq);
            return;
        }
        hrtick_start(rq, delta);
    }
}

// 计算时间片
static u64 sched_slice(struct cfs_rq *cfs_rq, struct sched_entity *se)
{
    unsigned int nr_running = cfs_rq->nr_running;
    u64 slice;

    if (sched_feat(ALT_PERIOD))
        nr_running = rq_of(cfs_rq)->cfs.h_nr_running;

    slice = __sched_period(nr_running + !se->on_rq) * se->load.weight;
    do_div(slice, cfs_rq->load.weight);

    if (sched_feat(BASE_SLICE)) {
        if (se_is_idle(se))
            slice = max_t(u64, slice, NSEC_PER_SEC/HZ);
        else
            slice = max_t(u64, slice, sysctl_sched_base_slice);
    }

    return slice;
}
#endif
```

## 扩展调度框架

Linux 6.12引入了sched_ext框架，允许通过BPF程序实现自定义调度策略。

### SCX架构

```c
// SCX运行队列 - kernel/sched/ext.c
struct scx_rq {
    struct scx_dispatch_q   local_dsq;              // 本地调度队列
    struct list_head        runnable_list;          // 可运行任务列表
    struct list_head        ddsp_deferred_locals;   // 延迟调度列表
    
    unsigned long           ops_qseq;               // 操作序列号
    u64                     extra_enq_flags;        // 额外入队标志
    u32                     nr_running;             // 运行任务数
    u32                     flags;                  // 标志位
    u32                     cpuperf_target;         // CPU性能目标
    
    bool                    cpu_released;           // CPU释放标志
    cpumask_var_t           cpus_to_kick;           // 需要唤醒的CPU
    cpumask_var_t           cpus_to_kick_if_idle;   // 空闲时唤醒的CPU
    cpumask_var_t           cpus_to_preempt;        // 需要抢占的CPU
    cpumask_var_t           cpus_to_wait;           // 需要等待的CPU
    
    unsigned long           pnt_seq;                // 抢占通知序列
    struct balance_callback deferred_bal_cb;        // 延迟均衡回调
    struct irq_work         deferred_irq_work;      // 延迟IRQ工作
    struct irq_work         kick_cpus_irq_work;     // 唤醒CPU IRQ工作
};

// SCX调度实体
struct sched_ext_entity {
    struct scx_dispatch_q   *dsq;                   // 调度队列
    struct scx_dsq_list_node dsq_list;             // 调度队列列表节点
    struct rb_node          dsq_priq;              // 优先级队列节点
    
    u32                     dsq_seq;               // 队列序列号
    u32                     dsq_flags;             // 队列标志
    u32                     flags;                 // SCX标志
    u32                     weight;                // 权重
    s32                     sticky_cpu;            // 粘性CPU
    s32                     holding_cpu;           // 持有CPU
    
    u32                     kf_mask;               // 内核函数掩码
    struct task_struct      *kf_tasks[2];          // 内核函数任务
    atomic_long_t           ops_state;             // 操作状态
    
    struct list_head        runnable_node;         // 可运行节点
    unsigned long           runnable_at;           // 可运行时间
    
    u64                     slice;                 // 时间片
    u64                     dsq_vtime;             // 虚拟时间
};
```

### BPF调度器接口

```c
// BPF调度器操作集 - include/linux/sched/ext.h
struct sched_ext_ops {
    int (*select_cpu)(struct task_struct *p, s32 prev_cpu, u64 wake_flags);
    void (*enqueue)(struct task_struct *p, u64 enq_flags);
    void (*dequeue)(struct task_struct *p, u64 deq_flags);
    void (*dispatch)(s32 cpu, struct task_struct *prev);
    void (*tick)(struct task_struct *p);
    void (*runnable)(struct task_struct *p, u64 enq_flags);
    void (*running)(struct task_struct *p);
    void (*stopping)(struct task_struct *p, bool runnable);
    void (*quiescent)(struct task_struct *p, u64 deq_flags);
    bool (*yield)(struct task_struct *from, struct task_struct *to);
    bool (*core_sched_before)(struct task_struct *a, struct task_struct *b);
    void (*set_weight)(struct task_struct *p, u32 weight);
    void (*set_cpumask)(struct task_struct *p, const struct cpumask *cpumask);
    void (*update_idle)(s32 cpu, bool idle);
    void (*cpu_acquire)(s32 cpu, struct scx_cpu_acquire_args *args);
    void (*cpu_release)(s32 cpu, struct scx_cpu_release_args *args);
    void (*init_task)(struct task_struct *p, struct scx_init_task_args *args);
    void (*exit_task)(struct task_struct *p, struct scx_exit_task_args *args);
    void (*enable)(struct task_struct *p);
    void (*disable)(struct task_struct *p);
    s32 (*init)(void);
    void (*exit)(struct scx_exit_info *info);
    void (*dump)(struct scx_dump_ctx *ctx);
    void (*dump_task)(struct scx_dump_ctx *ctx, struct task_struct *p);
    
    u64                     flags;
    const char              name[SCX_OPS_NAME_LEN];
    u32                     timeout_ms;
};

// SCX调度类实现示例
BPF_STRUCT_OPS(scx_example_ops) = {
    .select_cpu     = (void *)scx_example_select_cpu,
    .enqueue        = (void *)scx_example_enqueue,
    .dispatch       = (void *)scx_example_dispatch,
    .running        = (void *)scx_example_running,
    .stopping       = (void *)scx_example_stopping,
    .enable         = (void *)scx_example_enable,
    .name           = "example",
};
```

## 优点与局限性

### 技术优势

1. **模块化设计**
   - 调度类架构支持多种调度策略
   - 插件式扩展机制(sched_ext)
   - 清晰的抽象接口设计

2. **公平性保证**
   - CFS实现O(log n)复杂度的公平调度
   - 红黑树保证任务选择效率
   - 虚拟运行时间确保长期公平性

3. **实时支持**
   - 硬实时调度类(SCHED_DEADLINE)
   - 软实时调度类(SCHED_FIFO/RR)
   - 优先级继承防止优先级倒置

4. **SMP优化**
   - 层次化负载均衡
   - NUMA感知调度
   - CPU亲和性优化
   - 能效感知调度

5. **性能优化**
   - 高精度时钟tick
   - 缓存友好的任务放置
   - 批量操作减少开销
   - 无锁快速路径

### 设计局限

1. **复杂性管理**
   - 多层调度逻辑增加调试难度
   - 各类调度器交互复杂
   - 参数调优困难

2. **延迟敏感性**
   - 负载均衡可能引入延迟
   - 多核竞争影响响应时间
   - tick中断影响实时性能

3. **能耗权衡**
   - 性能与功耗的平衡困难
   - 频繁迁移增加功耗
   - 动态电压频率调节延迟

4. **可预测性**
   - CFS公平性与响应性冲突
   - 负载均衡引入不确定性
   - 多级缓存影响性能预测

5. **扩展性瓶颈**
   - 大规模系统负载均衡开销大
   - 全局状态同步成本高
   - NUMA拓扑复杂度增长

### 应用场景分析

1. **桌面/移动设备**
   - ✅ 交互响应性好
   - ✅ 能效调度优化电池续航
   - ❌ 复杂性对轻量级设备过度

2. **服务器应用**
   - ✅ 多核扩展性良好
   - ✅ 负载均衡提升吞吐量
   - ❌ 延迟敏感应用需要特殊优化

3. **实时系统**
   - ✅ 硬实时调度器支持确定性
   - ❌ 非实时调度器干扰实时性能
   - ❌ 负载均衡引入不可预测延迟

4. **高性能计算**
   - ✅ CPU亲和性减少迁移开销
   - ✅ NUMA感知优化内存访问
   - ❌ 公平调度可能影响计算密集型任务

## 总结

Linux CPU调度器作为操作系统内核的核心组件，经过多年演进已发展成为一个功能完善、性能优秀的调度系统。其分层的调度类架构既保证了系统的灵活性，也满足了不同场景的性能需求。

### 核心技术成就

1. **CFS算法创新**：基于虚拟运行时间的公平调度算法实现了O(log n)的高效任务选择，在保证公平性的同时提供良好的交互性能。

2. **多级调度架构**：从stop调度类到idle调度类的分层设计，为不同优先级和类型的任务提供了合适的调度策略。

3. **SMP负载均衡**：基于调度域的层次化负载均衡机制，有效利用多核系统的处理能力，同时考虑了NUMA拓扑和缓存局部性。

4. **实时调度支持**：Deadline调度器为硬实时任务提供了确定性保证，满足了对时间敏感的应用需求。

5. **能效感知优化**：在移动设备日益普及的今天，调度器的能效感知特性对延长电池续航时间具有重要意义。

6. **可扩展框架**：sched_ext框架的引入为用户空间定制调度策略提供了强大的工具，展现了Linux调度器的前瞻性设计。

### 发展趋势展望

随着计算系统的不断演进，Linux调度器也将继续发展：

1. **异构计算支持**：针对CPU+GPU、大小核等异构架构的调度优化
2. **机器学习增强**：利用AI技术优化调度决策和参数调优
3. **云原生优化**：更好地支持容器和虚拟化环境的调度需求
4. **边缘计算适配**：为资源受限的边缘设备提供轻量级调度方案

Linux调度器的成功在于其在公平性、响应性、吞吐量和实时性之间找到了良好的平衡点，为现代操作系统的高效运行提供了坚实的基础。对于系统开发者和研究人员而言，深入理解Linux调度器的设计原理和实现机制，对于构建高性能系统和优化应用程序性能都具有重要的指导意义。
