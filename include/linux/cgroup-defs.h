/* SPDX-License-Identifier: GPL-2.0 */
/*
 * linux/cgroup-defs.h - basic definitions for cgroup
 *
 * This file provides basic type and interface.  Include this file directly
 * only if necessary to avoid cyclic dependencies.
 */
#ifndef _LINUX_CGROUP_DEFS_H
#define _LINUX_CGROUP_DEFS_H

#include <linux/limits.h>
#include <linux/list.h>
#include <linux/idr.h>
#include <linux/wait.h>
#include <linux/mutex.h>
#include <linux/rcupdate.h>
#include <linux/refcount.h>
#include <linux/percpu-refcount.h>
#include <linux/percpu-rwsem.h>
#include <linux/u64_stats_sync.h>
#include <linux/workqueue.h>
#include <linux/bpf-cgroup-defs.h>
#include <linux/psi_types.h>

#ifdef CONFIG_CGROUPS

struct cgroup;
struct cgroup_root;
struct cgroup_subsys;
struct cgroup_taskset;
struct kernfs_node;
struct kernfs_ops;
struct kernfs_open_file;
struct seq_file;
struct poll_table_struct;

#define MAX_CGROUP_TYPE_NAMELEN 32
#define MAX_CGROUP_ROOT_NAMELEN 64
#define MAX_CFTYPE_NAME		64

/* define the enumeration of all cgroup subsystems */
#define SUBSYS(_x) _x ## _cgrp_id,
enum cgroup_subsys_id {
#include <linux/cgroup_subsys.h>
	CGROUP_SUBSYS_COUNT,
};
#undef SUBSYS

/* bits in struct cgroup_subsys_state flags field */
enum {
	CSS_NO_REF	= (1 << 0), /* no reference counting for this css */
	CSS_ONLINE	= (1 << 1), /* between ->css_online() and ->css_offline() */
	CSS_RELEASED	= (1 << 2), /* refcnt reached zero, released */
	CSS_VISIBLE	= (1 << 3), /* css is visible to userland */
	CSS_DYING	= (1 << 4), /* css is dying */
};

/* bits in struct cgroup flags field */
enum {
	/* Control Group requires release notifications to userspace */
	CGRP_NOTIFY_ON_RELEASE,
	/*
	 * Clone the parent's configuration when creating a new child
	 * cpuset cgroup.  For historical reasons, this option can be
	 * specified at mount time and thus is implemented here.
	 */
	CGRP_CPUSET_CLONE_CHILDREN,

	/* Control group has to be frozen. */
	CGRP_FREEZE,

	/* Cgroup is frozen. */
	CGRP_FROZEN,

	/* Control group has to be killed. */
	CGRP_KILL,
};

/* cgroup_root->flags */
enum {
	CGRP_ROOT_NOPREFIX	= (1 << 1), /* mounted subsystems have no named prefix */
	CGRP_ROOT_XATTR		= (1 << 2), /* supports extended attributes */

	/*
	 * Consider namespaces as delegation boundaries.  If this flag is
	 * set, controller specific interface files in a namespace root
	 * aren't writeable from inside the namespace.
	 */
	CGRP_ROOT_NS_DELEGATE	= (1 << 3),

	/*
	 * Reduce latencies on dynamic cgroup modifications such as task
	 * migrations and controller on/offs by disabling percpu operation on
	 * cgroup_threadgroup_rwsem. This makes hot path operations such as
	 * forks and exits into the slow path and more expensive.
	 *
	 * The static usage pattern of creating a cgroup, enabling controllers,
	 * and then seeding it with CLONE_INTO_CGROUP doesn't require write
	 * locking cgroup_threadgroup_rwsem and thus doesn't benefit from
	 * favordynmod.
	 */
	CGRP_ROOT_FAVOR_DYNMODS = (1 << 4),

	/*
	 * Enable cpuset controller in v1 cgroup to use v2 behavior.
	 */
	CGRP_ROOT_CPUSET_V2_MODE = (1 << 16),

	/*
	 * Enable legacy local memory.events.
	 */
	CGRP_ROOT_MEMORY_LOCAL_EVENTS = (1 << 17),

	/*
	 * Enable recursive subtree protection
	 */
	CGRP_ROOT_MEMORY_RECURSIVE_PROT = (1 << 18),

	/*
	 * Enable hugetlb accounting for the memory controller.
	 */
	CGRP_ROOT_MEMORY_HUGETLB_ACCOUNTING = (1 << 19),

	/*
	 * Enable legacy local pids.events.
	 */
	CGRP_ROOT_PIDS_LOCAL_EVENTS = (1 << 20),
};

/* cftype->flags */
enum {
	CFTYPE_ONLY_ON_ROOT	= (1 << 0),	/* only create on root cgrp */
	CFTYPE_NOT_ON_ROOT	= (1 << 1),	/* don't create on root cgrp */
	CFTYPE_NS_DELEGATABLE	= (1 << 2),	/* writeable beyond delegation boundaries */

	CFTYPE_NO_PREFIX	= (1 << 3),	/* (DON'T USE FOR NEW FILES) no subsys prefix */
	CFTYPE_WORLD_WRITABLE	= (1 << 4),	/* (DON'T USE FOR NEW FILES) S_IWUGO */
	CFTYPE_DEBUG		= (1 << 5),	/* create when cgroup_debug */

	/* internal flags, do not use outside cgroup core proper */
	__CFTYPE_ONLY_ON_DFL	= (1 << 16),	/* only on default hierarchy */
	__CFTYPE_NOT_ON_DFL	= (1 << 17),	/* not on default hierarchy */
	__CFTYPE_ADDED		= (1 << 18),
};

/*
 * cgroup_file is the handle for a file instance created in a cgroup which
 * is used, for example, to generate file changed notifications.  This can
 * be obtained by setting cftype->file_offset.
 */
struct cgroup_file {
	/* do not access any fields from outside cgroup core */
	struct kernfs_node *kn;
	unsigned long notified_at;
	struct timer_list notify_timer;
};

/*
 * Per-subsystem/per-cgroup state maintained by the system.  This is the
 * fundamental structural building block that controllers deal with.
 *
 * Fields marked with "PI:" are public and immutable and may be accessed
 * directly without synchronization.
 */
// 系统维护的每个子系统/每个 cgroup 的状态。
// 这是控制器处理的基本结构构建块。
// 标有 "PI:" 的字段是公共且不可变的，可以直接访问而无需同步。
struct cgroup_subsys_state {
    /* PI: the cgroup that this css is attached to */
    // PI: 此 css 所附加的 cgroup
    struct cgroup *cgroup;

    /* PI: the cgroup subsystem that this css is attached to */
    // PI: 此 css 所附加的 cgroup 子系统
    struct cgroup_subsys *ss;

    /* reference count - access via css_[try]get() and css_put() */
    // 引用计数 - 通过 css_[try]get() 和 css_put() 访问
    struct percpu_ref refcnt;

    /*
     * siblings list anchored at the parent's ->children
     *
     * linkage is protected by cgroup_mutex or RCU
     */
    // 兄弟列表锚定在父级的 ->children
    // 链接受 cgroup_mutex 或 RCU 保护
    struct list_head sibling;
    struct list_head children;

    /* flush target list anchored at cgrp->rstat_css_list */
    // 刷新目标列表锚定在 cgrp->rstat_css_list
    struct list_head rstat_css_node;

    /*
     * PI: Subsys-unique ID.  0 is unused and root is always 1.  The
     * matching css can be looked up using css_from_id().
     */
    // PI: 子系统唯一 ID。0 未使用，根始终为 1。可以使用 css_from_id() 查找匹配的 css。
    int id;

    unsigned int flags;

    /*
     * Monotonically increasing unique serial number which defines a
     * uniform order among all csses.  It's guaranteed that all
     * ->children lists are in the ascending order of ->serial_nr and
     * used to allow interrupting and resuming iterations.
     */
    // 单调递增的唯一序列号，定义了所有 css 之间的统一顺序。
    // 保证所有 ->children 列表按 ->serial_nr 的升序排列，并用于允许中断和恢复迭代。
    u64 serial_nr;

    /*
     * Incremented by online self and children.  Used to guarantee that
     * parents are not offlined before their children.
     */
    // 由在线自身和子节点递增。用于保证父节点不会在其子节点之前下线。
    atomic_t online_cnt;

    /* percpu_ref killing and RCU release */
    // percpu_ref 终止和 RCU 释放
    struct work_struct destroy_work;
    struct rcu_work destroy_rwork;

    /*
     * PI: the parent css.	Placed here for cache proximity to following
     * fields of the containing structure.
     */
    // PI: 父 css。放置在此处以便于与包含结构的以下字段的缓存接近。
    struct cgroup_subsys_state *parent;

    /*
     * Keep track of total numbers of visible descendant CSSes.
     * The total number of dying CSSes is tracked in
     * css->cgroup->nr_dying_subsys[ssid].
     * Protected by cgroup_mutex.
     */
    // 跟踪可见后代 CSS 的总数。
    // 正在消亡的 CSS 的总数在 css->cgroup->nr_dying_subsys[ssid] 中跟踪。
    // 受 cgroup_mutex 保护。
    int nr_descendants;
};

/*
 * A css_set is a structure holding pointers to a set of
 * cgroup_subsys_state objects. This saves space in the task struct
 * object and speeds up fork()/exit(), since a single inc/dec and a
 * list_add()/del() can bump the reference count on the entire cgroup
 * set for a task.
 */
// css_set 是一个结构体，包含指向一组 cgroup_subsys_state 对象的指针。
// 这节省了 task 结构体中的空间，并加速了 fork()/exit()，因为单个 inc/dec 和 list_add()/del() 可以增加整个任务的 cgroup 集的引用计数。
struct css_set {
    /*
     * Set of subsystem states, one for each subsystem. This array is
     * immutable after creation apart from the init_css_set during
     * subsystem registration (at boot time).
     */
    // 子系统状态集，每个子系统一个。除了在子系统注册期间（在启动时）的 init_css_set 之外，此数组在创建后不可变。
    struct cgroup_subsys_state *subsys[CGROUP_SUBSYS_COUNT];

    /* reference count */
    // 引用计数
    refcount_t refcount;

    /*
     * For a domain cgroup, the following points to self.  If threaded,
     * to the matching cset of the nearest domain ancestor.  The
     * dom_cset provides access to the domain cgroup and its csses to
     * which domain level resource consumptions should be charged.
     */
    // 对于域 cgroup，以下指向自身。如果是线程化的，则指向最近的域祖先的匹配 cset。
    // dom_cset 提供对域 cgroup 及其 css 的访问，域级资源消耗应计入其中。
    struct css_set *dom_cset;

    /* the default cgroup associated with this css_set */
    // 与此 css_set 关联的默认 cgroup
    struct cgroup *dfl_cgrp;

    /* internal task count, protected by css_set_lock */
    // 内部任务计数，受 css_set_lock 保护
    int nr_tasks;

    /*
     * Lists running through all tasks using this cgroup group.
     * mg_tasks lists tasks which belong to this cset but are in the
     * process of being migrated out or in.  Protected by
     * css_set_lock, but, during migration, once tasks are moved to
     * mg_tasks, it can be read safely while holding cgroup_mutex.
     */
    // 列出使用此 cgroup 组的所有任务。
    // mg_tasks 列出属于此 cset 但正在迁出或迁入过程中的任务。受 css_set_lock 保护，但在迁移期间，一旦任务被移动到 mg_tasks，在持有 cgroup_mutex 时可以安全读取。
    struct list_head tasks;
    struct list_head mg_tasks;
    struct list_head dying_tasks;

    /* all css_task_iters currently walking this cset */
    // 当前遍历此 cset 的所有 css_task_iters
    struct list_head task_iters;

    /*
     * On the default hierarchy, ->subsys[ssid] may point to a css
     * attached to an ancestor instead of the cgroup this css_set is
     * associated with.  The following node is anchored at
     * ->subsys[ssid]->cgroup->e_csets[ssid] and provides a way to
     * iterate through all css's attached to a given cgroup.
     */
    // 在默认层次结构中，->subsys[ssid] 可能指向附加到祖先的 css，而不是与此 css_set 关联的 cgroup。
    // 以下节点锚定在 ->subsys[ssid]->cgroup->e_csets[ssid]，提供了一种遍历附加到给定 cgroup 的所有 css 的方法。
    struct list_head e_cset_node[CGROUP_SUBSYS_COUNT];

    /* all threaded csets whose ->dom_cset points to this cset */
    // 所有其 ->dom_cset 指向此 cset 的线程化 cset
    struct list_head threaded_csets;
    struct list_head threaded_csets_node;

    /*
     * List running through all cgroup groups in the same hash
     * slot. Protected by css_set_lock
     */
    // 列出同一哈希槽中的所有 cgroup 组。受 css_set_lock 保护
    struct hlist_node hlist;

    /*
     * List of cgrp_cset_links pointing at cgroups referenced from this
     * css_set.  Protected by css_set_lock.
     */
    // 指向从此 css_set 引用的 cgroup 的 cgrp_cset_links 列表。受 css_set_lock 保护。
    struct list_head cgrp_links;

    /*
     * List of csets participating in the on-going migration either as
     * source or destination.  Protected by cgroup_mutex.
     */
    // 参与正在进行的迁移的 cset 列表，无论是作为源还是目标。受 cgroup_mutex 保护。
    struct list_head mg_src_preload_node;
    struct list_head mg_dst_preload_node;
    struct list_head mg_node;

    /*
     * If this cset is acting as the source of migration the following
     * two fields are set.  mg_src_cgrp and mg_dst_cgrp are
     * respectively the source and destination cgroups of the on-going
     * migration.  mg_dst_cset is the destination cset the target tasks
     * on this cset should be migrated to.  Protected by cgroup_mutex.
     */
    // 如果此 cset 作为迁移的源，则设置以下两个字段。
    // mg_src_cgrp 和 mg_dst_cgrp 分别是正在进行的迁移的源和目标 cgroup。
    // mg_dst_cset 是目标 cset，目标任务应迁移到此 cset。受 cgroup_mutex 保护。
    struct cgroup *mg_src_cgrp;
    struct cgroup *mg_dst_cgrp;
    struct css_set *mg_dst_cset;

    /* dead and being drained, ignore for migration */
    // 已死且正在排空，迁移时忽略
    bool dead;

    /* For RCU-protected deletion */
    // 用于 RCU 保护的删除
    struct rcu_head rcu_head;
};

struct cgroup_base_stat {
	struct task_cputime cputime;

#ifdef CONFIG_SCHED_CORE
	u64 forceidle_sum;
#endif
};

/*
 * rstat - cgroup scalable recursive statistics.  Accounting is done
 * per-cpu in cgroup_rstat_cpu which is then lazily propagated up the
 * hierarchy on reads.
 *
 * When a stat gets updated, the cgroup_rstat_cpu and its ancestors are
 * linked into the updated tree.  On the following read, propagation only
 * considers and consumes the updated tree.  This makes reading O(the
 * number of descendants which have been active since last read) instead of
 * O(the total number of descendants).
 *
 * This is important because there can be a lot of (draining) cgroups which
 * aren't active and stat may be read frequently.  The combination can
 * become very expensive.  By propagating selectively, increasing reading
 * frequency decreases the cost of each read.
 *
 * This struct hosts both the fields which implement the above -
 * updated_children and updated_next - and the fields which track basic
 * resource statistics on top of it - bsync, bstat and last_bstat.
 */
struct cgroup_rstat_cpu {
	/*
	 * ->bsync protects ->bstat.  These are the only fields which get
	 * updated in the hot path.
	 */
	struct u64_stats_sync bsync;
	struct cgroup_base_stat bstat;

	/*
	 * Snapshots at the last reading.  These are used to calculate the
	 * deltas to propagate to the global counters.
	 */
	struct cgroup_base_stat last_bstat;

	/*
	 * This field is used to record the cumulative per-cpu time of
	 * the cgroup and its descendants. Currently it can be read via
	 * eBPF/drgn etc, and we are still trying to determine how to
	 * expose it in the cgroupfs interface.
	 */
	struct cgroup_base_stat subtree_bstat;

	/*
	 * Snapshots at the last reading. These are used to calculate the
	 * deltas to propagate to the per-cpu subtree_bstat.
	 */
	struct cgroup_base_stat last_subtree_bstat;

	/*
	 * Child cgroups with stat updates on this cpu since the last read
	 * are linked on the parent's ->updated_children through
	 * ->updated_next.
	 *
	 * In addition to being more compact, singly-linked list pointing
	 * to the cgroup makes it unnecessary for each per-cpu struct to
	 * point back to the associated cgroup.
	 *
	 * Protected by per-cpu cgroup_rstat_cpu_lock.
	 */
	struct cgroup *updated_children;	/* terminated by self cgroup */
	struct cgroup *updated_next;		/* NULL iff not on the list */
};

struct cgroup_freezer_state {
	/* Should the cgroup and its descendants be frozen. */
	bool freeze;

	/* Should the cgroup actually be frozen? */
	int e_freeze;

	/* Fields below are protected by css_set_lock */

	/* Number of frozen descendant cgroups */
	int nr_frozen_descendants;

	/*
	 * Number of tasks, which are counted as frozen:
	 * frozen, SIGSTOPped, and PTRACEd.
	 */
	int nr_frozen_tasks;
};

struct cgroup {
    /* self css with NULL ->ss, points back to this cgroup */
    // 自身的 css，带有 NULL ->ss，指向这个 cgroup
    struct cgroup_subsys_state self;

    unsigned long flags;		/* "unsigned long" so bitops work */
    // 标志位，使用 "unsigned long" 以便位操作

    /*
     * The depth this cgroup is at.  The root is at depth zero and each
     * step down the hierarchy increments the level.  This along with
     * ancestors[] can determine whether a given cgroup is a
     * descendant of another without traversing the hierarchy.
     */
    // 这个 cgroup 所在的深度。根在深度为零的位置，每向下一级增加一个层次。
    // 这与 ancestors[] 一起可以确定一个给定的 cgroup 是否是另一个的后代，而无需遍历层次结构。
    int level;

    /* Maximum allowed descent tree depth */
    // 允许的最大树深度
    int max_depth;

    /*
     * Keep track of total numbers of visible and dying descent cgroups.
     * Dying cgroups are cgroups which were deleted by a user,
     * but are still existing because someone else is holding a reference.
     * max_descendants is a maximum allowed number of descent cgroups.
     *
     * nr_descendants and nr_dying_descendants are protected
     * by cgroup_mutex and css_set_lock. It's fine to read them holding
     * any of cgroup_mutex and css_set_lock; for writing both locks
     * should be held.
     */
    // 跟踪可见和正在消亡的子 cgroup 的总数。
    // 正在消亡的 cgroup 是用户删除的 cgroup，但由于其他人持有引用，它们仍然存在。
    // max_descendants 是允许的最大子 cgroup 数量。
    //
    // nr_descendants 和 nr_dying_descendants 受 cgroup_mutex 和 css_set_lock 保护。
    // 在持有 cgroup_mutex 和 css_set_lock 中的任何一个时读取它们是可以的；写入时应同时持有这两个锁。
    int nr_descendants;
    int nr_dying_descendants;
    int max_descendants;

    /*
     * Each non-empty css_set associated with this cgroup contributes
     * one to nr_populated_csets.  The counter is zero iff this cgroup
     * doesn't have any tasks.
     *
     * All children which have non-zero nr_populated_csets and/or
     * nr_populated_children of their own contribute one to either
     * nr_populated_domain_children or nr_populated_threaded_children
     * depending on their type.  Each counter is zero iff all cgroups
     * of the type in the subtree proper don't have any tasks.
     */
    // 与此 cgroup 关联的每个非空 css_set 都会使 nr_populated_csets 增加一个。
    // 计数器为零当且仅当此 cgroup 没有任何任务。
    //
    // 所有具有非零 nr_populated_csets 和/或 nr_populated_children 的子节点根据其类型分别使 nr_populated_domain_children 或 nr_populated_threaded_children 增加一个。
    // 当且仅当子树中的所有此类型的 cgroup 没有任何任务时，每个计数器为零。
    int nr_populated_csets;
    int nr_populated_domain_children;
    int nr_populated_threaded_children;

    int nr_threaded_children;	/* # of live threaded child cgroups */
    // 活跃的线程子 cgroup 数量

    struct kernfs_node *kn;		/* cgroup kernfs entry */
    // cgroup kernfs 条目
    struct cgroup_file procs_file;	/* handle for "cgroup.procs" */
    // "cgroup.procs" 的句柄
    struct cgroup_file events_file;	/* handle for "cgroup.events" */
    // "cgroup.events" 的句柄

    /* handles for "{cpu,memory,io,irq}.pressure" */
    // "{cpu,memory,io,irq}.pressure" 的句柄
    struct cgroup_file psi_files[NR_PSI_RESOURCES];

    /*
     * The bitmask of subsystems enabled on the child cgroups.
     * ->subtree_control is the one configured through
     * "cgroup.subtree_control" while ->subtree_ss_mask is the effective
     * one which may have more subsystems enabled.  Controller knobs
     * are made available iff it's enabled in ->subtree_control.
     */
    // 在子 cgroup 上启用的子系统的位掩码。
    // ->subtree_control 是通过 "cgroup.subtree_control" 配置的，而 ->subtree_ss_mask 是有效的位掩码，可能启用了更多的子系统。
    // 控制器旋钮仅在 ->subtree_control 中启用时可用。
    u16 subtree_control;
    u16 subtree_ss_mask;
    u16 old_subtree_control;
    u16 old_subtree_ss_mask;

    /* Private pointers for each registered subsystem */
    // 每个注册子系统的私有指针
    struct cgroup_subsys_state __rcu *subsys[CGROUP_SUBSYS_COUNT];

    /*
     * Keep track of total number of dying CSSes at and below this cgroup.
     * Protected by cgroup_mutex.
     */
    // 跟踪此 cgroup 及其以下的正在消亡的 CSS 的总数。受 cgroup_mutex 保护。
    int nr_dying_subsys[CGROUP_SUBSYS_COUNT];

    struct cgroup_root *root;

    /*
     * List of cgrp_cset_links pointing at css_sets with tasks in this
     * cgroup.  Protected by css_set_lock.
     */
    // 指向此 cgroup 中具有任务的 css_set 的 cgrp_cset_links 列表。受 css_set_lock 保护。
    struct list_head cset_links;

    /*
     * On the default hierarchy, a css_set for a cgroup with some
     * susbsys disabled will point to css's which are associated with
     * the closest ancestor which has the subsys enabled.  The
     * following lists all css_sets which point to this cgroup's css
     * for the given subsystem.
     */
    // 在默认层次结构中，某些子系统禁用的 cgroup 的 css_set 将指向与启用了该子系统的最近祖先关联的 css。
    // 以下列出了所有指向此 cgroup 的给定子系统的 css 的 css_set。
    struct list_head e_csets[CGROUP_SUBSYS_COUNT];

    /*
     * If !threaded, self.  If threaded, it points to the nearest
     * domain ancestor.  Inside a threaded subtree, cgroups are exempt
     * from process granularity and no-internal-task constraint.
     * Domain level resource consumptions which aren't tied to a
     * specific task are charged to the dom_cgrp.
     */
    // 如果不是线程化的，则指向自身。如果是线程化的，则指向最近的域祖先。
    // 在线程化子树中，cgroup 不受进程粒度和无内部任务约束的限制。
    // 与特定任务无关的域级资源消耗计入 dom_cgrp。
    struct cgroup *dom_cgrp;
    struct cgroup *old_dom_cgrp;		/* used while enabling threaded */
    // 启用线程化时使用

    /* per-cpu recursive resource statistics */
    // 每 CPU 递归资源统计
    struct cgroup_rstat_cpu __percpu *rstat_cpu;
    struct list_head rstat_css_list;

    /*
     * Add padding to separate the read mostly rstat_cpu and
     * rstat_css_list into a different cacheline from the following
     * rstat_flush_next and *bstat fields which can have frequent updates.
     */
    // 添加填充以将主要读取的 rstat_cpu 和 rstat_css_list 与以下可能频繁更新的 rstat_flush_next 和 *bstat 字段分隔到不同的缓存行中。
    CACHELINE_PADDING(_pad_);

    /*
     * A singly-linked list of cgroup structures to be rstat flushed.
     * This is a scratch field to be used exclusively by
     * cgroup_rstat_flush_locked() and protected by cgroup_rstat_lock.
     */
    // 要进行 rstat 刷新的 cgroup 结构的单链表。
    // 这是一个仅供 cgroup_rstat_flush_locked() 使用的临时字段，并受 cgroup_rstat_lock 保护。
    struct cgroup	*rstat_flush_next;

    /* cgroup basic resource statistics */
    // cgroup 基本资源统计
    struct cgroup_base_stat last_bstat;
    struct cgroup_base_stat bstat;
    struct prev_cputime prev_cputime;	/* for printing out cputime */
    // 用于打印 cputime

    /*
     * list of pidlists, up to two for each namespace (one for procs, one
     * for tasks); created on demand.
     */
    // pidlists 列表，每个命名空间最多两个（一个用于进程，一个用于任务）；按需创建。
    struct list_head pidlists;
    struct mutex pidlist_mutex;

    /* used to wait for offlining of csses */
    // 用于等待 css 下线
    wait_queue_head_t offline_waitq;

    /* used to schedule release agent */
    // 用于调度释放代理
    struct work_struct release_agent_work;

    /* used to track pressure stalls */
    // 用于跟踪压力停滞
    struct psi_group *psi;

    /* used to store eBPF programs */
    // 用于存储 eBPF 程序
    struct cgroup_bpf bpf;

    /* Used to store internal freezer state */
    // 用于存储内部冻结状态
    struct cgroup_freezer_state freezer;

#ifdef CONFIG_BPF_SYSCALL
    struct bpf_local_storage __rcu  *bpf_cgrp_storage;
#endif

    /* All ancestors including self */
    // 包括自身在内的所有祖先
    struct cgroup *ancestors[];
};

/*
 * A cgroup_root represents the root of a cgroup hierarchy, and may be
 * associated with a kernfs_root to form an active hierarchy.  This is
 * internal to cgroup core.  Don't access directly from controllers.
 */
struct cgroup_root {
	struct kernfs_root *kf_root;

	/* The bitmask of subsystems attached to this hierarchy */
	unsigned int subsys_mask;

	/* Unique id for this hierarchy. */
	int hierarchy_id;

	/* A list running through the active hierarchies */
	struct list_head root_list;
	struct rcu_head rcu;	/* Must be near the top */

	/*
	 * The root cgroup. The containing cgroup_root will be destroyed on its
	 * release. cgrp->ancestors[0] will be used overflowing into the
	 * following field. cgrp_ancestor_storage must immediately follow.
	 */
	struct cgroup cgrp;

	/* must follow cgrp for cgrp->ancestors[0], see above */
	struct cgroup *cgrp_ancestor_storage;

	/* Number of cgroups in the hierarchy, used only for /proc/cgroups */
	atomic_t nr_cgrps;

	/* Hierarchy-specific flags */
	unsigned int flags;

	/* The path to use for release notifications. */
	char release_agent_path[PATH_MAX];

	/* The name for this hierarchy - may be empty */
	char name[MAX_CGROUP_ROOT_NAMELEN];
};

/*
 * struct cftype: handler definitions for cgroup control files
 *
 * When reading/writing to a file:
 *	- the cgroup to use is file->f_path.dentry->d_parent->d_fsdata
 *	- the 'cftype' of the file is file->f_path.dentry->d_fsdata
 */
struct cftype {
	/*
	 * By convention, the name should begin with the name of the
	 * subsystem, followed by a period.  Zero length string indicates
	 * end of cftype array.
	 */
	char name[MAX_CFTYPE_NAME];
	unsigned long private;

	/*
	 * The maximum length of string, excluding trailing nul, that can
	 * be passed to write.  If < PAGE_SIZE-1, PAGE_SIZE-1 is assumed.
	 */
	size_t max_write_len;

	/* CFTYPE_* flags */
	unsigned int flags;

	/*
	 * If non-zero, should contain the offset from the start of css to
	 * a struct cgroup_file field.  cgroup will record the handle of
	 * the created file into it.  The recorded handle can be used as
	 * long as the containing css remains accessible.
	 */
	unsigned int file_offset;

	/*
	 * Fields used for internal bookkeeping.  Initialized automatically
	 * during registration.
	 */
	struct cgroup_subsys *ss;	/* NULL for cgroup core files */
	struct list_head node;		/* anchored at ss->cfts */
	struct kernfs_ops *kf_ops;

	int (*open)(struct kernfs_open_file *of);
	void (*release)(struct kernfs_open_file *of);

	/*
	 * read_u64() is a shortcut for the common case of returning a
	 * single integer. Use it in place of read()
	 */
	u64 (*read_u64)(struct cgroup_subsys_state *css, struct cftype *cft);
	/*
	 * read_s64() is a signed version of read_u64()
	 */
	s64 (*read_s64)(struct cgroup_subsys_state *css, struct cftype *cft);

	/* generic seq_file read interface */
	int (*seq_show)(struct seq_file *sf, void *v);

	/* optional ops, implement all or none */
	void *(*seq_start)(struct seq_file *sf, loff_t *ppos);
	void *(*seq_next)(struct seq_file *sf, void *v, loff_t *ppos);
	void (*seq_stop)(struct seq_file *sf, void *v);

	/*
	 * write_u64() is a shortcut for the common case of accepting
	 * a single integer (as parsed by simple_strtoull) from
	 * userspace. Use in place of write(); return 0 or error.
	 */
	int (*write_u64)(struct cgroup_subsys_state *css, struct cftype *cft,
			 u64 val);
	/*
	 * write_s64() is a signed version of write_u64()
	 */
	int (*write_s64)(struct cgroup_subsys_state *css, struct cftype *cft,
			 s64 val);

	/*
	 * write() is the generic write callback which maps directly to
	 * kernfs write operation and overrides all other operations.
	 * Maximum write size is determined by ->max_write_len.  Use
	 * of_css/cft() to access the associated css and cft.
	 */
	ssize_t (*write)(struct kernfs_open_file *of,
			 char *buf, size_t nbytes, loff_t off);

	__poll_t (*poll)(struct kernfs_open_file *of,
			 struct poll_table_struct *pt);

	struct lock_class_key	lockdep_key;
};

/*
 * Control Group subsystem type.
 * See Documentation/admin-guide/cgroup-v1/cgroups.rst for details
 */
struct cgroup_subsys {
	struct cgroup_subsys_state *(*css_alloc)(struct cgroup_subsys_state *parent_css);
	int (*css_online)(struct cgroup_subsys_state *css);
	void (*css_offline)(struct cgroup_subsys_state *css);
	void (*css_released)(struct cgroup_subsys_state *css);
	void (*css_free)(struct cgroup_subsys_state *css);
	void (*css_reset)(struct cgroup_subsys_state *css);
	void (*css_rstat_flush)(struct cgroup_subsys_state *css, int cpu);
	int (*css_extra_stat_show)(struct seq_file *seq,
				   struct cgroup_subsys_state *css);
	int (*css_local_stat_show)(struct seq_file *seq,
				   struct cgroup_subsys_state *css);

	int (*can_attach)(struct cgroup_taskset *tset);
	void (*cancel_attach)(struct cgroup_taskset *tset);
	void (*attach)(struct cgroup_taskset *tset);
	void (*post_attach)(void);
	int (*can_fork)(struct task_struct *task,
			struct css_set *cset);
	void (*cancel_fork)(struct task_struct *task, struct css_set *cset);
	void (*fork)(struct task_struct *task);
	void (*exit)(struct task_struct *task);
	void (*release)(struct task_struct *task);
	void (*bind)(struct cgroup_subsys_state *root_css);

	bool early_init:1;

	/*
	 * If %true, the controller, on the default hierarchy, doesn't show
	 * up in "cgroup.controllers" or "cgroup.subtree_control", is
	 * implicitly enabled on all cgroups on the default hierarchy, and
	 * bypasses the "no internal process" constraint.  This is for
	 * utility type controllers which is transparent to userland.
	 *
	 * An implicit controller can be stolen from the default hierarchy
	 * anytime and thus must be okay with offline csses from previous
	 * hierarchies coexisting with csses for the current one.
	 */
	bool implicit_on_dfl:1;

	/*
	 * If %true, the controller, supports threaded mode on the default
	 * hierarchy.  In a threaded subtree, both process granularity and
	 * no-internal-process constraint are ignored and a threaded
	 * controllers should be able to handle that.
	 *
	 * Note that as an implicit controller is automatically enabled on
	 * all cgroups on the default hierarchy, it should also be
	 * threaded.  implicit && !threaded is not supported.
	 */
	bool threaded:1;

	/* the following two fields are initialized automatically during boot */
	int id;
	const char *name;

	/* optional, initialized automatically during boot if not set */
	const char *legacy_name;

	/* link to parent, protected by cgroup_lock() */
	struct cgroup_root *root;

	/* idr for css->id */
	struct idr css_idr;

	/*
	 * List of cftypes.  Each entry is the first entry of an array
	 * terminated by zero length name.
	 */
	struct list_head cfts;

	/*
	 * Base cftypes which are automatically registered.  The two can
	 * point to the same array.
	 */
	struct cftype *dfl_cftypes;	/* for the default hierarchy */
	struct cftype *legacy_cftypes;	/* for the legacy hierarchies */

	/*
	 * A subsystem may depend on other subsystems.  When such subsystem
	 * is enabled on a cgroup, the depended-upon subsystems are enabled
	 * together if available.  Subsystems enabled due to dependency are
	 * not visible to userland until explicitly enabled.  The following
	 * specifies the mask of subsystems that this one depends on.
	 */
	unsigned int depends_on;
};

extern struct percpu_rw_semaphore cgroup_threadgroup_rwsem;

struct cgroup_of_peak {
	unsigned long		value;
	struct list_head	list;
};

/**
 * cgroup_threadgroup_change_begin - threadgroup exclusion for cgroups
 * @tsk: target task
 *
 * Allows cgroup operations to synchronize against threadgroup changes
 * using a percpu_rw_semaphore.
 */
static inline void cgroup_threadgroup_change_begin(struct task_struct *tsk)
{
	percpu_down_read(&cgroup_threadgroup_rwsem);
}

/**
 * cgroup_threadgroup_change_end - threadgroup exclusion for cgroups
 * @tsk: target task
 *
 * Counterpart of cgroup_threadcgroup_change_begin().
 */
static inline void cgroup_threadgroup_change_end(struct task_struct *tsk)
{
	percpu_up_read(&cgroup_threadgroup_rwsem);
}

#else	/* CONFIG_CGROUPS */

#define CGROUP_SUBSYS_COUNT 0

static inline void cgroup_threadgroup_change_begin(struct task_struct *tsk)
{
	might_sleep();
}

static inline void cgroup_threadgroup_change_end(struct task_struct *tsk) {}

#endif	/* CONFIG_CGROUPS */

#ifdef CONFIG_SOCK_CGROUP_DATA

/*
 * sock_cgroup_data is embedded at sock->sk_cgrp_data and contains
 * per-socket cgroup information except for memcg association.
 *
 * On legacy hierarchies, net_prio and net_cls controllers directly
 * set attributes on each sock which can then be tested by the network
 * layer. On the default hierarchy, each sock is associated with the
 * cgroup it was created in and the networking layer can match the
 * cgroup directly.
 */
struct sock_cgroup_data {
	struct cgroup	*cgroup; /* v2 */
#ifdef CONFIG_CGROUP_NET_CLASSID
	u32		classid; /* v1 */
#endif
#ifdef CONFIG_CGROUP_NET_PRIO
	u16		prioidx; /* v1 */
#endif
};

static inline u16 sock_cgroup_prioidx(const struct sock_cgroup_data *skcd)
{
#ifdef CONFIG_CGROUP_NET_PRIO
	return READ_ONCE(skcd->prioidx);
#else
	return 1;
#endif
}

static inline u32 sock_cgroup_classid(const struct sock_cgroup_data *skcd)
{
#ifdef CONFIG_CGROUP_NET_CLASSID
	return READ_ONCE(skcd->classid);
#else
	return 0;
#endif
}

static inline void sock_cgroup_set_prioidx(struct sock_cgroup_data *skcd,
					   u16 prioidx)
{
#ifdef CONFIG_CGROUP_NET_PRIO
	WRITE_ONCE(skcd->prioidx, prioidx);
#endif
}

static inline void sock_cgroup_set_classid(struct sock_cgroup_data *skcd,
					   u32 classid)
{
#ifdef CONFIG_CGROUP_NET_CLASSID
	WRITE_ONCE(skcd->classid, classid);
#endif
}

#else	/* CONFIG_SOCK_CGROUP_DATA */

struct sock_cgroup_data {
};

#endif	/* CONFIG_SOCK_CGROUP_DATA */

#endif	/* _LINUX_CGROUP_DEFS_H */
