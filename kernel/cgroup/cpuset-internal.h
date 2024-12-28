/* SPDX-License-Identifier: GPL-2.0-or-later */

#ifndef __CPUSET_INTERNAL_H
#define __CPUSET_INTERNAL_H

#include <linux/cgroup.h>
#include <linux/cpu.h>
#include <linux/cpumask.h>
#include <linux/cpuset.h>
#include <linux/spinlock.h>
#include <linux/union_find.h>

/* See "Frequency meter" comments, below. */

struct fmeter {
	int cnt;		/* unprocessed events count */
	int val;		/* most recent output value */
	time64_t time;		/* clock (secs) when val computed */
	spinlock_t lock;	/* guards read or write of above */
};

/*
 * Invalid partition error code
 */
enum prs_errcode {
	PERR_NONE = 0,
	PERR_INVCPUS,
	PERR_INVPARENT,
	PERR_NOTPART,
	PERR_NOTEXCL,
	PERR_NOCPUS,
	PERR_HOTPLUG,
	PERR_CPUSEMPTY,
	PERR_HKEEPING,
	PERR_ACCESS,
};

/* bits in struct cpuset flags field */
typedef enum {
	CS_ONLINE,
	CS_CPU_EXCLUSIVE,
	CS_MEM_EXCLUSIVE,
	CS_MEM_HARDWALL,
	CS_MEMORY_MIGRATE,
	CS_SCHED_LOAD_BALANCE,
	CS_SPREAD_PAGE,
	CS_SPREAD_SLAB,
} cpuset_flagbits_t;

/* The various types of files and directories in a cpuset file system */

typedef enum {
	FILE_MEMORY_MIGRATE,
	FILE_CPULIST,
	FILE_MEMLIST,
	FILE_EFFECTIVE_CPULIST,
	FILE_EFFECTIVE_MEMLIST,
	FILE_SUBPARTS_CPULIST,
	FILE_EXCLUSIVE_CPULIST,
	FILE_EFFECTIVE_XCPULIST,
	FILE_ISOLATED_CPULIST,
	FILE_CPU_EXCLUSIVE,
	FILE_MEM_EXCLUSIVE,
	FILE_MEM_HARDWALL,
	FILE_SCHED_LOAD_BALANCE,
	FILE_PARTITION_ROOT,
	FILE_SCHED_RELAX_DOMAIN_LEVEL,
	FILE_MEMORY_PRESSURE_ENABLED,
	FILE_MEMORY_PRESSURE,
	FILE_SPREAD_PAGE,
	FILE_SPREAD_SLAB,
} cpuset_filetype_t;


struct cpuset {
    struct cgroup_subsys_state css; // cgroup 子系统状态

    unsigned long flags; // 标志位，用于位操作

    /*
     * On default hierarchy:
     *
     * The user-configured masks can only be changed by writing to
     * cpuset.cpus and cpuset.mems, and won't be limited by the
     * parent masks.
     *
     * The effective masks is the real masks that apply to the tasks
     * in the cpuset. They may be changed if the configured masks are
     * changed or hotplug happens.
     *
     * effective_mask == configured_mask & parent's effective_mask,
     * and if it ends up empty, it will inherit the parent's mask.
     *
     *
     * On legacy hierarchy:
     *
     * The user-configured masks are always the same with effective masks.
     */

    /* user-configured CPUs and Memory Nodes allow to tasks */
    cpumask_var_t cpus_allowed; // 用户配置的允许使用的 CPU 掩码
    nodemask_t mems_allowed; // 用户配置的允许使用的内存节点掩码

    /* effective CPUs and Memory Nodes allow to tasks */
    cpumask_var_t effective_cpus; // 有效的允许使用的 CPU 掩码
    nodemask_t effective_mems; // 有效的允许使用的内存节点掩码

    /*
     * Exclusive CPUs dedicated to current cgroup (default hierarchy only)
     *
     * The effective_cpus of a valid partition root comes solely from its
     * effective_xcpus and some of the effective_xcpus may be distributed
     * to sub-partitions below & hence excluded from its effective_cpus.
     * For a valid partition root, its effective_cpus have no relationship
     * with cpus_allowed unless its exclusive_cpus isn't set.
     *
     * This value will only be set if either exclusive_cpus is set or
     * when this cpuset becomes a local partition root.
     */
    cpumask_var_t effective_xcpus; // 当前 cgroup 专用的有效 CPU 掩码（仅默认层次结构）

    /*
     * Exclusive CPUs as requested by the user (default hierarchy only)
     *
     * Its value is independent of cpus_allowed and designates the set of
     * CPUs that can be granted to the current cpuset or its children when
     * it becomes a valid partition root. The effective set of exclusive
     * CPUs granted (effective_xcpus) depends on whether those exclusive
     * CPUs are passed down by its ancestors and not yet taken up by
     * another sibling partition root along the way.
     *
     * If its value isn't set, it defaults to cpus_allowed.
     */
    cpumask_var_t exclusive_cpus; // 用户请求的专用 CPU 掩码（仅默认层次结构）

    /*
     * This is old Memory Nodes tasks took on.
     *
     * - top_cpuset.old_mems_allowed is initialized to mems_allowed.
     * - A new cpuset's old_mems_allowed is initialized when some
     *   task is moved into it.
     * - old_mems_allowed is used in cpuset_migrate_mm() when we change
     *   cpuset.mems_allowed and have tasks' nodemask updated, and
     *   then old_mems_allowed is updated to mems_allowed.
     */
    nodemask_t old_mems_allowed; // 任务使用的旧内存节点掩码

    struct fmeter fmeter; // 内存压力过滤器

    /*
     * Tasks are being attached to this cpuset.  Used to prevent
     * zeroing cpus/mems_allowed between ->can_attach() and ->attach().
     */
    int attach_in_progress; // 正在附加任务

    /* for custom sched domain */
    int relax_domain_level; // 自定义调度域的级别

    /* number of valid local child partitions */
    int nr_subparts; // 有效的本地子分区数量

    /* partition root state */
    int partition_root_state; // 分区根状态

    /*
     * number of SCHED_DEADLINE tasks attached to this cpuset, so that we
     * know when to rebuild associated root domain bandwidth information.
     */
    int nr_deadline_tasks; // 附加到此 cpuset 的 SCHED_DEADLINE 任务数量
    int nr_migrate_dl_tasks; // 迁移的 SCHED_DEADLINE 任务数量
    u64 sum_migrate_dl_bw; // 迁移的 SCHED_DEADLINE 任务的带宽总和

    /* Invalid partition error code, not lock protected */
    enum prs_errcode prs_err; // 无效分区错误代码，不受锁保护

    /* Handle for cpuset.cpus.partition */
    struct cgroup_file partition_file; // cpuset.cpus.partition 的句柄

    /* Remote partition silbling list anchored at remote_children */
    struct list_head remote_sibling; // 远程分区兄弟列表，锚定在 remote_children

    /* Used to merge intersecting subsets for generate_sched_domains */
    struct uf_node node; // 用于合并交叉子集以生成调度域
};

static inline struct cpuset *css_cs(struct cgroup_subsys_state *css)
{
	return css ? container_of(css, struct cpuset, css) : NULL;
}

/* Retrieve the cpuset for a task */
static inline struct cpuset *task_cs(struct task_struct *task)
{
	return css_cs(task_css(task, cpuset_cgrp_id));
}

static inline struct cpuset *parent_cs(struct cpuset *cs)
{
	return css_cs(cs->css.parent);
}

/* convenient tests for these bits */
static inline bool is_cpuset_online(struct cpuset *cs)
{
	return test_bit(CS_ONLINE, &cs->flags) && !css_is_dying(&cs->css);
}

static inline int is_cpu_exclusive(const struct cpuset *cs)
{
	return test_bit(CS_CPU_EXCLUSIVE, &cs->flags);
}

static inline int is_mem_exclusive(const struct cpuset *cs)
{
	return test_bit(CS_MEM_EXCLUSIVE, &cs->flags);
}

static inline int is_mem_hardwall(const struct cpuset *cs)
{
	return test_bit(CS_MEM_HARDWALL, &cs->flags);
}

static inline int is_sched_load_balance(const struct cpuset *cs)
{
	return test_bit(CS_SCHED_LOAD_BALANCE, &cs->flags);
}

static inline int is_memory_migrate(const struct cpuset *cs)
{
	return test_bit(CS_MEMORY_MIGRATE, &cs->flags);
}

static inline int is_spread_page(const struct cpuset *cs)
{
	return test_bit(CS_SPREAD_PAGE, &cs->flags);
}

static inline int is_spread_slab(const struct cpuset *cs)
{
	return test_bit(CS_SPREAD_SLAB, &cs->flags);
}

/**
 * cpuset_for_each_child - traverse online children of a cpuset
 * @child_cs: loop cursor pointing to the current child
 * @pos_css: used for iteration
 * @parent_cs: target cpuset to walk children of
 *
 * Walk @child_cs through the online children of @parent_cs.  Must be used
 * with RCU read locked.
 */
#define cpuset_for_each_child(child_cs, pos_css, parent_cs)		\
	css_for_each_child((pos_css), &(parent_cs)->css)		\
		if (is_cpuset_online(((child_cs) = css_cs((pos_css)))))

/**
 * cpuset_for_each_descendant_pre - pre-order walk of a cpuset's descendants
 * @des_cs: loop cursor pointing to the current descendant
 * @pos_css: used for iteration
 * @root_cs: target cpuset to walk ancestor of
 *
 * Walk @des_cs through the online descendants of @root_cs.  Must be used
 * with RCU read locked.  The caller may modify @pos_css by calling
 * css_rightmost_descendant() to skip subtree.  @root_cs is included in the
 * iteration and the first node to be visited.
 */
#define cpuset_for_each_descendant_pre(des_cs, pos_css, root_cs)	\
	css_for_each_descendant_pre((pos_css), &(root_cs)->css)		\
		if (is_cpuset_online(((des_cs) = css_cs((pos_css)))))

void rebuild_sched_domains_locked(void);
void cpuset_callback_lock_irq(void);
void cpuset_callback_unlock_irq(void);
void cpuset_update_tasks_cpumask(struct cpuset *cs, struct cpumask *new_cpus);
void cpuset_update_tasks_nodemask(struct cpuset *cs);
int cpuset_update_flag(cpuset_flagbits_t bit, struct cpuset *cs, int turning_on);
ssize_t cpuset_write_resmask(struct kernfs_open_file *of,
				    char *buf, size_t nbytes, loff_t off);
int cpuset_common_seq_show(struct seq_file *sf, void *v);

/*
 * cpuset-v1.c
 */
#ifdef CONFIG_CPUSETS_V1
extern struct cftype cpuset1_files[];
void fmeter_init(struct fmeter *fmp);
void cpuset1_update_task_spread_flags(struct cpuset *cs,
					struct task_struct *tsk);
void cpuset1_update_tasks_flags(struct cpuset *cs);
void cpuset1_hotplug_update_tasks(struct cpuset *cs,
			    struct cpumask *new_cpus, nodemask_t *new_mems,
			    bool cpus_updated, bool mems_updated);
int cpuset1_validate_change(struct cpuset *cur, struct cpuset *trial);
#else
static inline void fmeter_init(struct fmeter *fmp) {}
static inline void cpuset1_update_task_spread_flags(struct cpuset *cs,
					struct task_struct *tsk) {}
static inline void cpuset1_update_tasks_flags(struct cpuset *cs) {}
static inline void cpuset1_hotplug_update_tasks(struct cpuset *cs,
			    struct cpumask *new_cpus, nodemask_t *new_mems,
			    bool cpus_updated, bool mems_updated) {}
static inline int cpuset1_validate_change(struct cpuset *cur,
				struct cpuset *trial) { return 0; }
#endif /* CONFIG_CPUSETS_V1 */

#endif /* __CPUSET_INTERNAL_H */
