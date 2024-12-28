/* SPDX-License-Identifier: GPL-2.0 */
#ifndef _LINUX_SCHED_H
#define _LINUX_SCHED_H

/*
 * Define 'struct task_struct' and provide the main scheduler
 * APIs (schedule(), wakeup variants, etc.)
 */

#include <uapi/linux/sched.h>

#include <asm/current.h>
#include <asm/processor.h>
#include <linux/thread_info.h>
#include <linux/preempt.h>
#include <linux/cpumask_types.h>

#include <linux/cache.h>
#include <linux/irqflags_types.h>
#include <linux/smp_types.h>
#include <linux/pid_types.h>
#include <linux/sem_types.h>
#include <linux/shm.h>
#include <linux/kmsan_types.h>
#include <linux/mutex_types.h>
#include <linux/plist_types.h>
#include <linux/hrtimer_types.h>
#include <linux/timer_types.h>
#include <linux/seccomp_types.h>
#include <linux/nodemask_types.h>
#include <linux/refcount_types.h>
#include <linux/resource.h>
#include <linux/latencytop.h>
#include <linux/sched/prio.h>
#include <linux/sched/types.h>
#include <linux/signal_types.h>
#include <linux/syscall_user_dispatch_types.h>
#include <linux/mm_types_task.h>
#include <linux/netdevice_xmit.h>
#include <linux/task_io_accounting.h>
#include <linux/posix-timers_types.h>
#include <linux/restart_block.h>
#include <uapi/linux/rseq.h>
#include <linux/seqlock_types.h>
#include <linux/kcsan.h>
#include <linux/rv.h>
#include <linux/livepatch_sched.h>
#include <linux/uidgid_types.h>
#include <asm/kmap_size.h>

/* task_struct member predeclarations (sorted alphabetically): */
struct audit_context;
struct bio_list;
struct blk_plug;
struct bpf_local_storage;
struct bpf_run_ctx;
struct bpf_net_context;
struct capture_control;
struct cfs_rq;
struct fs_struct;
struct futex_pi_state;
struct io_context;
struct io_uring_task;
struct mempolicy;
struct nameidata;
struct nsproxy;
struct perf_event_context;
struct pid_namespace;
struct pipe_inode_info;
struct rcu_node;
struct reclaim_state;
struct robust_list_head;
struct root_domain;
struct rq;
struct sched_attr;
struct sched_dl_entity;
struct seq_file;
struct sighand_struct;
struct signal_struct;
struct task_delay_info;
struct task_group;
struct task_struct;
struct user_event_mm;

#include <linux/sched/ext.h>

/*
 * Task state bitmask. NOTE! These bits are also
 * encoded in fs/proc/array.c: get_task_state().
 *
 * We have two separate sets of flags: task->__state
 * is about runnability, while task->exit_state are
 * about the task exiting. Confusing, but this way
 * modifying one set can't modify the other one by
 * mistake.
 */
// 任务状态位掩码。注意！这些位也在 fs/proc/array.c 中编码：get_task_state()。
// 我们有两组独立的标志：task->__state 是关于可运行性的，而 task->exit_state 是关于任务退出的。
// 这可能会让人困惑，但这样修改一组标志不会错误地修改另一组标志。

/* Used in tsk->__state: */
// 用于 tsk->__state：
#define TASK_RUNNING			0x00000000 // 任务正在运行
#define TASK_INTERRUPTIBLE		0x00000001 // 任务可中断
#define TASK_UNINTERRUPTIBLE		0x00000002 // 任务不可中断
#define __TASK_STOPPED			0x00000004 // 任务停止
#define __TASK_TRACED			0x00000008 // 任务被跟踪
/* Used in tsk->exit_state: */
// 用于 tsk->exit_state：
#define EXIT_DEAD			0x00000010 // 任务已死
#define EXIT_ZOMBIE			0x00000020 // 任务僵尸
#define EXIT_TRACE			(EXIT_ZOMBIE | EXIT_DEAD) // 任务退出跟踪
/* Used in tsk->__state again: */
// 再次用于 tsk->__state：
#define TASK_PARKED			0x00000040 // 任务暂停
#define TASK_DEAD			0x00000080 // 任务死亡
#define TASK_WAKEKILL			0x00000100 // 任务唤醒杀死
#define TASK_WAKING			0x00000200 // 任务唤醒中
#define TASK_NOLOAD			0x00000400 // 任务不加载
#define TASK_NEW			0x00000800 // 新任务
#define TASK_RTLOCK_WAIT		0x00001000 // 任务实时锁等待
#define TASK_FREEZABLE			0x00002000 // 任务可冻结
#define __TASK_FREEZABLE_UNSAFE	       (0x00004000 * IS_ENABLED(CONFIG_LOCKDEP)) // 任务可冻结不安全
#define TASK_FROZEN			0x00008000 // 任务冻结
#define TASK_STATE_MAX			0x00010000 // 任务状态最大值

#define TASK_ANY			(TASK_STATE_MAX-1) // 任意任务状态

/*
 * DO NOT ADD ANY NEW USERS !
 */
#define TASK_FREEZABLE_UNSAFE		(TASK_FREEZABLE | __TASK_FREEZABLE_UNSAFE)

/* Convenience macros for the sake of set_current_state: */
#define TASK_KILLABLE			(TASK_WAKEKILL | TASK_UNINTERRUPTIBLE)
#define TASK_STOPPED			(TASK_WAKEKILL | __TASK_STOPPED)
#define TASK_TRACED			__TASK_TRACED

#define TASK_IDLE			(TASK_UNINTERRUPTIBLE | TASK_NOLOAD)

/* Convenience macros for the sake of wake_up(): */
#define TASK_NORMAL			(TASK_INTERRUPTIBLE | TASK_UNINTERRUPTIBLE)

/* get_task_state(): */
#define TASK_REPORT			(TASK_RUNNING | TASK_INTERRUPTIBLE | \
					 TASK_UNINTERRUPTIBLE | __TASK_STOPPED | \
					 __TASK_TRACED | EXIT_DEAD | EXIT_ZOMBIE | \
					 TASK_PARKED)

#define task_is_running(task)		(READ_ONCE((task)->__state) == TASK_RUNNING)

#define task_is_traced(task)		((READ_ONCE(task->jobctl) & JOBCTL_TRACED) != 0)
#define task_is_stopped(task)		((READ_ONCE(task->jobctl) & JOBCTL_STOPPED) != 0)
#define task_is_stopped_or_traced(task)	((READ_ONCE(task->jobctl) & (JOBCTL_STOPPED | JOBCTL_TRACED)) != 0)

/*
 * Special states are those that do not use the normal wait-loop pattern. See
 * the comment with set_special_state().
 */
#define is_special_task_state(state)					\
	((state) & (__TASK_STOPPED | __TASK_TRACED | TASK_PARKED |	\
		    TASK_DEAD | TASK_FROZEN))

#ifdef CONFIG_DEBUG_ATOMIC_SLEEP
# define debug_normal_state_change(state_value)				\
	do {								\
		WARN_ON_ONCE(is_special_task_state(state_value));	\
		current->task_state_change = _THIS_IP_;			\
	} while (0)

# define debug_special_state_change(state_value)			\
	do {								\
		WARN_ON_ONCE(!is_special_task_state(state_value));	\
		current->task_state_change = _THIS_IP_;			\
	} while (0)

# define debug_rtlock_wait_set_state()					\
	do {								 \
		current->saved_state_change = current->task_state_change;\
		current->task_state_change = _THIS_IP_;			 \
	} while (0)

# define debug_rtlock_wait_restore_state()				\
	do {								 \
		current->task_state_change = current->saved_state_change;\
	} while (0)

#else
# define debug_normal_state_change(cond)	do { } while (0)
# define debug_special_state_change(cond)	do { } while (0)
# define debug_rtlock_wait_set_state()		do { } while (0)
# define debug_rtlock_wait_restore_state()	do { } while (0)
#endif

/*
 * set_current_state() includes a barrier so that the write of current->__state
 * is correctly serialised wrt the caller's subsequent test of whether to
 * actually sleep:
 *
 *   for (;;) {
 *	set_current_state(TASK_UNINTERRUPTIBLE);
 *	if (CONDITION)
 *	   break;
 *
 *	schedule();
 *   }
 *   __set_current_state(TASK_RUNNING);
 *
 * If the caller does not need such serialisation (because, for instance, the
 * CONDITION test and condition change and wakeup are under the same lock) then
 * use __set_current_state().
 *
 * The above is typically ordered against the wakeup, which does:
 *
 *   CONDITION = 1;
 *   wake_up_state(p, TASK_UNINTERRUPTIBLE);
 *
 * where wake_up_state()/try_to_wake_up() executes a full memory barrier before
 * accessing p->__state.
 *
 * Wakeup will do: if (@state & p->__state) p->__state = TASK_RUNNING, that is,
 * once it observes the TASK_UNINTERRUPTIBLE store the waking CPU can issue a
 * TASK_RUNNING store which can collide with __set_current_state(TASK_RUNNING).
 *
 * However, with slightly different timing the wakeup TASK_RUNNING store can
 * also collide with the TASK_UNINTERRUPTIBLE store. Losing that store is not
 * a problem either because that will result in one extra go around the loop
 * and our @cond test will save the day.
 *
 * Also see the comments of try_to_wake_up().
 */
#define __set_current_state(state_value)				\
	do {								\
		debug_normal_state_change((state_value));		\
		WRITE_ONCE(current->__state, (state_value));		\
	} while (0)

#define set_current_state(state_value)					\
	do {								\
		debug_normal_state_change((state_value));		\
		smp_store_mb(current->__state, (state_value));		\
	} while (0)

/*
 * set_special_state() should be used for those states when the blocking task
 * can not use the regular condition based wait-loop. In that case we must
 * serialize against wakeups such that any possible in-flight TASK_RUNNING
 * stores will not collide with our state change.
 */
#define set_special_state(state_value)					\
	do {								\
		unsigned long flags; /* may shadow */			\
									\
		raw_spin_lock_irqsave(&current->pi_lock, flags);	\
		debug_special_state_change((state_value));		\
		WRITE_ONCE(current->__state, (state_value));		\
		raw_spin_unlock_irqrestore(&current->pi_lock, flags);	\
	} while (0)

/*
 * PREEMPT_RT specific variants for "sleeping" spin/rwlocks
 *
 * RT's spin/rwlock substitutions are state preserving. The state of the
 * task when blocking on the lock is saved in task_struct::saved_state and
 * restored after the lock has been acquired.  These operations are
 * serialized by task_struct::pi_lock against try_to_wake_up(). Any non RT
 * lock related wakeups while the task is blocked on the lock are
 * redirected to operate on task_struct::saved_state to ensure that these
 * are not dropped. On restore task_struct::saved_state is set to
 * TASK_RUNNING so any wakeup attempt redirected to saved_state will fail.
 *
 * The lock operation looks like this:
 *
 *	current_save_and_set_rtlock_wait_state();
 *	for (;;) {
 *		if (try_lock())
 *			break;
 *		raw_spin_unlock_irq(&lock->wait_lock);
 *		schedule_rtlock();
 *		raw_spin_lock_irq(&lock->wait_lock);
 *		set_current_state(TASK_RTLOCK_WAIT);
 *	}
 *	current_restore_rtlock_saved_state();
 */
#define current_save_and_set_rtlock_wait_state()			\
	do {								\
		lockdep_assert_irqs_disabled();				\
		raw_spin_lock(&current->pi_lock);			\
		current->saved_state = current->__state;		\
		debug_rtlock_wait_set_state();				\
		WRITE_ONCE(current->__state, TASK_RTLOCK_WAIT);		\
		raw_spin_unlock(&current->pi_lock);			\
	} while (0);

#define current_restore_rtlock_saved_state()				\
	do {								\
		lockdep_assert_irqs_disabled();				\
		raw_spin_lock(&current->pi_lock);			\
		debug_rtlock_wait_restore_state();			\
		WRITE_ONCE(current->__state, current->saved_state);	\
		current->saved_state = TASK_RUNNING;			\
		raw_spin_unlock(&current->pi_lock);			\
	} while (0);

#define get_current_state()	READ_ONCE(current->__state)

/*
 * Define the task command name length as enum, then it can be visible to
 * BPF programs.
 */
enum {
	TASK_COMM_LEN = 16,
};

extern void sched_tick(void);

#define	MAX_SCHEDULE_TIMEOUT		LONG_MAX

extern long schedule_timeout(long timeout);
extern long schedule_timeout_interruptible(long timeout);
extern long schedule_timeout_killable(long timeout);
extern long schedule_timeout_uninterruptible(long timeout);
extern long schedule_timeout_idle(long timeout);
asmlinkage void schedule(void);
extern void schedule_preempt_disabled(void);
asmlinkage void preempt_schedule_irq(void);
#ifdef CONFIG_PREEMPT_RT
 extern void schedule_rtlock(void);
#endif

extern int __must_check io_schedule_prepare(void);
extern void io_schedule_finish(int token);
extern long io_schedule_timeout(long timeout);
extern void io_schedule(void);

/**
 * struct prev_cputime - snapshot of system and user cputime
 * @utime: time spent in user mode
 * @stime: time spent in system mode
 * @lock: protects the above two fields
 *
 * Stores previous user/system time values such that we can guarantee
 * monotonicity.
 */
struct prev_cputime {
#ifndef CONFIG_VIRT_CPU_ACCOUNTING_NATIVE
	u64				utime;
	u64				stime;
	raw_spinlock_t			lock;
#endif
};

enum vtime_state {
	/* Task is sleeping or running in a CPU with VTIME inactive: */
	VTIME_INACTIVE = 0,
	/* Task is idle */
	VTIME_IDLE,
	/* Task runs in kernelspace in a CPU with VTIME active: */
	VTIME_SYS,
	/* Task runs in userspace in a CPU with VTIME active: */
	VTIME_USER,
	/* Task runs as guests in a CPU with VTIME active: */
	VTIME_GUEST,
};

struct vtime {
	seqcount_t		seqcount;
	unsigned long long	starttime;
	enum vtime_state	state;
	unsigned int		cpu;
	u64			utime;
	u64			stime;
	u64			gtime;
};

/*
 * Utilization clamp constraints.
 * @UCLAMP_MIN:	Minimum utilization
 * @UCLAMP_MAX:	Maximum utilization
 * @UCLAMP_CNT:	Utilization clamp constraints count
 */
enum uclamp_id {
	UCLAMP_MIN = 0,
	UCLAMP_MAX,
	UCLAMP_CNT
};

#ifdef CONFIG_SMP
extern struct root_domain def_root_domain;
extern struct mutex sched_domains_mutex;
#endif

struct sched_param {
	int sched_priority;
};

struct sched_info {
#ifdef CONFIG_SCHED_INFO
	/* Cumulative counters: */

	/* # of times we have run on this CPU: */
	unsigned long			pcount;

	/* Time spent waiting on a runqueue: */
	unsigned long long		run_delay;

	/* Timestamps: */

	/* When did we last run on a CPU? */
	unsigned long long		last_arrival;

	/* When were we last queued to run? */
	unsigned long long		last_queued;

#endif /* CONFIG_SCHED_INFO */
};

/*
 * Integer metrics need fixed point arithmetic, e.g., sched/fair
 * has a few: load, load_avg, util_avg, freq, and capacity.
 *
 * We define a basic fixed point arithmetic range, and then formalize
 * all these metrics based on that basic range.
 */
# define SCHED_FIXEDPOINT_SHIFT		10
# define SCHED_FIXEDPOINT_SCALE		(1L << SCHED_FIXEDPOINT_SHIFT)

/* Increase resolution of cpu_capacity calculations */
# define SCHED_CAPACITY_SHIFT		SCHED_FIXEDPOINT_SHIFT
# define SCHED_CAPACITY_SCALE		(1L << SCHED_CAPACITY_SHIFT)

struct load_weight {
    unsigned long			weight; // 负载权重
    u32				inv_weight; // 负载权重的倒数
};

/*
 * The load/runnable/util_avg accumulates an infinite geometric series
 * (see __update_load_avg_cfs_rq() in kernel/sched/pelt.c).
 *
 * [load_avg definition]
 *
 *   load_avg = runnable% * scale_load_down(load)
 *
 * [runnable_avg definition]
 *
 *   runnable_avg = runnable% * SCHED_CAPACITY_SCALE
 *
 * [util_avg definition]
 *
 *   util_avg = running% * SCHED_CAPACITY_SCALE
 *
 * where runnable% is the time ratio that a sched_entity is runnable and
 * running% the time ratio that a sched_entity is running.
 *
 * For cfs_rq, they are the aggregated values of all runnable and blocked
 * sched_entities.
 *
 * The load/runnable/util_avg doesn't directly factor frequency scaling and CPU
 * capacity scaling. The scaling is done through the rq_clock_pelt that is used
 * for computing those signals (see update_rq_clock_pelt())
 *
 * N.B., the above ratios (runnable% and running%) themselves are in the
 * range of [0, 1]. To do fixed point arithmetics, we therefore scale them
 * to as large a range as necessary. This is for example reflected by
 * util_avg's SCHED_CAPACITY_SCALE.
 *
 * [Overflow issue]
 *
 * The 64-bit load_sum can have 4353082796 (=2^64/47742/88761) entities
 * with the highest load (=88761), always runnable on a single cfs_rq,
 * and should not overflow as the number already hits PID_MAX_LIMIT.
 *
 * For all other cases (including 32-bit kernels), struct load_weight's
 * weight will overflow first before we do, because:
 *
 *    Max(load_avg) <= Max(load.weight)
 *
 * Then it is the load_weight's responsibility to consider overflow
 * issues.
 */
struct sched_avg {
	u64				last_update_time;
	u64				load_sum;
	u64				runnable_sum;
	u32				util_sum;
	u32				period_contrib;
	unsigned long			load_avg;
	unsigned long			runnable_avg;
	unsigned long			util_avg;
	unsigned int			util_est;
} ____cacheline_aligned;

/*
 * The UTIL_AVG_UNCHANGED flag is used to synchronize util_est with util_avg
 * updates. When a task is dequeued, its util_est should not be updated if its
 * util_avg has not been updated in the meantime.
 * This information is mapped into the MSB bit of util_est at dequeue time.
 * Since max value of util_est for a task is 1024 (PELT util_avg for a task)
 * it is safe to use MSB.
 */
#define UTIL_EST_WEIGHT_SHIFT		2
#define UTIL_AVG_UNCHANGED		0x80000000

struct sched_statistics {
#ifdef CONFIG_SCHEDSTATS
	u64				wait_start;
	u64				wait_max;
	u64				wait_count;
	u64				wait_sum;
	u64				iowait_count;
	u64				iowait_sum;

	u64				sleep_start;
	u64				sleep_max;
	s64				sum_sleep_runtime;

	u64				block_start;
	u64				block_max;
	s64				sum_block_runtime;

	s64				exec_max;
	u64				slice_max;

	u64				nr_migrations_cold;
	u64				nr_failed_migrations_affine;
	u64				nr_failed_migrations_running;
	u64				nr_failed_migrations_hot;
	u64				nr_forced_migrations;

	u64				nr_wakeups;
	u64				nr_wakeups_sync;
	u64				nr_wakeups_migrate;
	u64				nr_wakeups_local;
	u64				nr_wakeups_remote;
	u64				nr_wakeups_affine;
	u64				nr_wakeups_affine_attempts;
	u64				nr_wakeups_passive;
	u64				nr_wakeups_idle;

#ifdef CONFIG_SCHED_CORE
	u64				core_forceidle_sum;
#endif
#endif /* CONFIG_SCHEDSTATS */
} ____cacheline_aligned;

struct sched_entity {
    /* For load-balancing: */
    // 用于负载平衡
    struct load_weight		load; // 负载权重
    struct rb_node			run_node; // 运行节点
    u64				deadline; // 截止时间
    u64				min_vruntime; // 最小虚拟运行时间
    u64				min_slice; // 最小时间片

    struct list_head		group_node; // 组节点
    unsigned char			on_rq; // 是否在运行队列上
    unsigned char			sched_delayed; // 调度延迟
    unsigned char			rel_deadline; // 相对截止时间
    unsigned char			custom_slice; // 自定义时间片
                    /* hole */

    u64				exec_start; // 执行开始时间
    u64				sum_exec_runtime; // 执行时间总和
    u64				prev_sum_exec_runtime; // 上一次执行时间总和
    u64				vruntime; // 虚拟运行时间
    s64				vlag; // 虚拟滞后时间
    u64				slice; // 时间片

    u64				nr_migrations; // 迁移次数

#ifdef CONFIG_FAIR_GROUP_SCHED
    int				depth; // 深度
    struct sched_entity		*parent; // 父实体
    /* rq on which this entity is (to be) queued: */
    // 此实体（将要）排队的运行队列
    struct cfs_rq			*cfs_rq;
    /* rq "owned" by this entity/group: */
    // 此实体/组“拥有”的运行队列
    struct cfs_rq			*my_q;
    /* cached value of my_q->h_nr_running */
    // my_q->h_nr_running 的缓存值
    unsigned long			runnable_weight; // 可运行权重
#endif

#ifdef CONFIG_SMP
    /*
     * Per entity load average tracking.
     *
     * Put into separate cache line so it does not
     * collide with read-mostly values above.
     */
    // 每个实体的负载平均跟踪。
    // 放入单独的缓存行中，以免与上面的主要读取值冲突。
    struct sched_avg		avg; // 调度平均值
#endif
};

struct sched_rt_entity {
	struct list_head		run_list;
	unsigned long			timeout;
	unsigned long			watchdog_stamp;
	unsigned int			time_slice;
	unsigned short			on_rq;
	unsigned short			on_list;

	struct sched_rt_entity		*back;
#ifdef CONFIG_RT_GROUP_SCHED
	struct sched_rt_entity		*parent;
	/* rq on which this entity is (to be) queued: */
	struct rt_rq			*rt_rq;
	/* rq "owned" by this entity/group: */
	struct rt_rq			*my_q;
#endif
} __randomize_layout;

typedef bool (*dl_server_has_tasks_f)(struct sched_dl_entity *);
typedef struct task_struct *(*dl_server_pick_f)(struct sched_dl_entity *);

struct sched_dl_entity {
	struct rb_node			rb_node;

	/*
	 * Original scheduling parameters. Copied here from sched_attr
	 * during sched_setattr(), they will remain the same until
	 * the next sched_setattr().
	 */
	u64				dl_runtime;	/* Maximum runtime for each instance	*/
	u64				dl_deadline;	/* Relative deadline of each instance	*/
	u64				dl_period;	/* Separation of two instances (period) */
	u64				dl_bw;		/* dl_runtime / dl_period		*/
	u64				dl_density;	/* dl_runtime / dl_deadline		*/

	/*
	 * Actual scheduling parameters. Initialized with the values above,
	 * they are continuously updated during task execution. Note that
	 * the remaining runtime could be < 0 in case we are in overrun.
	 */
	s64				runtime;	/* Remaining runtime for this instance	*/
	u64				deadline;	/* Absolute deadline for this instance	*/
	unsigned int			flags;		/* Specifying the scheduler behaviour	*/

	/*
	 * Some bool flags:
	 *
	 * @dl_throttled tells if we exhausted the runtime. If so, the
	 * task has to wait for a replenishment to be performed at the
	 * next firing of dl_timer.
	 *
	 * @dl_yielded tells if task gave up the CPU before consuming
	 * all its available runtime during the last job.
	 *
	 * @dl_non_contending tells if the task is inactive while still
	 * contributing to the active utilization. In other words, it
	 * indicates if the inactive timer has been armed and its handler
	 * has not been executed yet. This flag is useful to avoid race
	 * conditions between the inactive timer handler and the wakeup
	 * code.
	 *
	 * @dl_overrun tells if the task asked to be informed about runtime
	 * overruns.
	 *
	 * @dl_server tells if this is a server entity.
	 *
	 * @dl_defer tells if this is a deferred or regular server. For
	 * now only defer server exists.
	 *
	 * @dl_defer_armed tells if the deferrable server is waiting
	 * for the replenishment timer to activate it.
	 *
	 * @dl_defer_running tells if the deferrable server is actually
	 * running, skipping the defer phase.
	 */
	unsigned int			dl_throttled      : 1;
	unsigned int			dl_yielded        : 1;
	unsigned int			dl_non_contending : 1;
	unsigned int			dl_overrun	  : 1;
	unsigned int			dl_server         : 1;
	unsigned int			dl_defer	  : 1;
	unsigned int			dl_defer_armed	  : 1;
	unsigned int			dl_defer_running  : 1;

	/*
	 * Bandwidth enforcement timer. Each -deadline task has its
	 * own bandwidth to be enforced, thus we need one timer per task.
	 */
	struct hrtimer			dl_timer;

	/*
	 * Inactive timer, responsible for decreasing the active utilization
	 * at the "0-lag time". When a -deadline task blocks, it contributes
	 * to GRUB's active utilization until the "0-lag time", hence a
	 * timer is needed to decrease the active utilization at the correct
	 * time.
	 */
	struct hrtimer			inactive_timer;

	/*
	 * Bits for DL-server functionality. Also see the comment near
	 * dl_server_update().
	 *
	 * @rq the runqueue this server is for
	 *
	 * @server_has_tasks() returns true if @server_pick return a
	 * runnable task.
	 */
	struct rq			*rq;
	dl_server_has_tasks_f		server_has_tasks;
	dl_server_pick_f		server_pick_task;

#ifdef CONFIG_RT_MUTEXES
	/*
	 * Priority Inheritance. When a DEADLINE scheduling entity is boosted
	 * pi_se points to the donor, otherwise points to the dl_se it belongs
	 * to (the original one/itself).
	 */
	struct sched_dl_entity *pi_se;
#endif
};

#ifdef CONFIG_UCLAMP_TASK
/* Number of utilization clamp buckets (shorter alias) */
#define UCLAMP_BUCKETS CONFIG_UCLAMP_BUCKETS_COUNT

/*
 * Utilization clamp for a scheduling entity
 * @value:		clamp value "assigned" to a se
 * @bucket_id:		bucket index corresponding to the "assigned" value
 * @active:		the se is currently refcounted in a rq's bucket
 * @user_defined:	the requested clamp value comes from user-space
 *
 * The bucket_id is the index of the clamp bucket matching the clamp value
 * which is pre-computed and stored to avoid expensive integer divisions from
 * the fast path.
 *
 * The active bit is set whenever a task has got an "effective" value assigned,
 * which can be different from the clamp value "requested" from user-space.
 * This allows to know a task is refcounted in the rq's bucket corresponding
 * to the "effective" bucket_id.
 *
 * The user_defined bit is set whenever a task has got a task-specific clamp
 * value requested from userspace, i.e. the system defaults apply to this task
 * just as a restriction. This allows to relax default clamps when a less
 * restrictive task-specific value has been requested, thus allowing to
 * implement a "nice" semantic. For example, a task running with a 20%
 * default boost can still drop its own boosting to 0%.
 */
struct uclamp_se {
	unsigned int value		: bits_per(SCHED_CAPACITY_SCALE);
	unsigned int bucket_id		: bits_per(UCLAMP_BUCKETS);
	unsigned int active		: 1;
	unsigned int user_defined	: 1;
};
#endif /* CONFIG_UCLAMP_TASK */

union rcu_special {
	struct {
		u8			blocked;
		u8			need_qs;
		u8			exp_hint; /* Hint for performance. */
		u8			need_mb; /* Readers need smp_mb(). */
	} b; /* Bits. */
	u32 s; /* Set of bits. */
};

enum perf_event_task_context {
	perf_invalid_context = -1,
	perf_hw_context = 0,
	perf_sw_context,
	perf_nr_task_contexts,
};

/*
 * Number of contexts where an event can trigger:
 *      task, softirq, hardirq, nmi.
 */
#define PERF_NR_CONTEXTS	4

struct wake_q_node {
	struct wake_q_node *next;
};

struct kmap_ctrl {
#ifdef CONFIG_KMAP_LOCAL
	int				idx;
	pte_t				pteval[KM_MAX_IDX];
#endif
};


struct task_struct {
#ifdef CONFIG_THREAD_INFO_IN_TASK
    /*
     * For reasons of header soup (see current_thread_info()), this
     * must be the first element of task_struct.
     */
    // 由于头文件的复杂性（见 current_thread_info()），这必须是 task_struct 的第一个元素。
    struct thread_info		thread_info;
#endif
    unsigned int			__state; // 任务状态

    /* saved state for "spinlock sleepers" */
    // "自旋锁睡眠者"的保存状态
    unsigned int			saved_state;

    /*
     * This begins the randomizable portion of task_struct. Only
     * scheduling-critical items should be added above here.
     */
    // 这开始了 task_struct 的可随机化部分。只有调度关键项应添加在此上方。
    randomized_struct_fields_start

    void				*stack; // 任务栈指针
    refcount_t			usage; // 引用计数
    /* Per task flags (PF_*), defined further below: */
    // 每个任务的标志（PF_*），在下面进一步定义：
    unsigned int			flags;
    unsigned int			ptrace; // ptrace 标志

#ifdef CONFIG_MEM_ALLOC_PROFILING
    struct alloc_tag		*alloc_tag; // 内存分配标签
#endif

#ifdef CONFIG_SMP
    int				on_cpu; // 当前运行的 CPU
    struct __call_single_node	wake_entry; // 唤醒条目
    unsigned int			wakee_flips; // 唤醒翻转计数
    unsigned long			wakee_flip_decay_ts; // 唤醒翻转衰减时间戳
    struct task_struct		*last_wakee; // 最后唤醒的任务

    /*
     * recent_used_cpu is initially set as the last CPU used by a task
     * that wakes affine another task. Waker/wakee relationships can
     * push tasks around a CPU where each wakeup moves to the next one.
     * Tracking a recently used CPU allows a quick search for a recently
     * used CPU that may be idle.
     */
    // recent_used_cpu 最初设置为唤醒另一个任务的任务最后使用的 CPU。唤醒者/被唤醒者关系可以将任务推到一个 CPU 上，每次唤醒移动到下一个。跟踪最近使用的 CPU 允许快速搜索可能空闲的最近使用的 CPU。
    int				recent_used_cpu; // 最近使用的 CPU
    int				wake_cpu; // 唤醒的 CPU
#endif
    int				on_rq; // 是否在运行队列上

    int				prio; // 优先级
    int				static_prio; // 静态优先级
    int				normal_prio; // 正常优先级
    unsigned int			rt_priority; // 实时优先级

    struct sched_entity		se; // 调度实体
    struct sched_rt_entity		rt; // 实时调度实体
    struct sched_dl_entity		dl; // 调度截止时间实体
    struct sched_dl_entity		*dl_server; // 调度截止时间服务器
#ifdef CONFIG_SCHED_CLASS_EXT
    struct sched_ext_entity		scx; // 扩展调度实体
#endif
    const struct sched_class	*sched_class; // 调度类

#ifdef CONFIG_SCHED_CORE
    struct rb_node			core_node; // 核心节点
    unsigned long			core_cookie; // 核心 cookie
    unsigned int			core_occupation; // 核心占用
#endif

#ifdef CONFIG_CGROUP_SCHED
    struct task_group		*sched_task_group; // 调度任务组
#endif

#ifdef CONFIG_UCLAMP_TASK
    /*
     * Clamp values requested for a scheduling entity.
     * Must be updated with task_rq_lock() held.
     */
    // 调度实体请求的钳位值。必须在持有 task_rq_lock() 时更新。
    struct uclamp_se		uclamp_req[UCLAMP_CNT];
    /*
     * Effective clamp values used for a scheduling entity.
     * Must be updated with task_rq_lock() held.
     */
    // 调度实体使用的有效钳位值。必须在持有 task_rq_lock() 时更新。
    struct uclamp_se		uclamp[UCLAMP_CNT];
#endif

    struct sched_statistics         stats; // 调度统计信息

#ifdef CONFIG_PREEMPT_NOTIFIERS
    /* List of struct preempt_notifier: */
    // 预占通知器列表
    struct hlist_head		preempt_notifiers;
#endif

#ifdef CONFIG_BLK_DEV_IO_TRACE
    unsigned int			btrace_seq; // 块设备 IO 跟踪序列
#endif

    unsigned int			policy; // 调度策略
    unsigned long			max_allowed_capacity; // 最大允许容量
    int				nr_cpus_allowed; // 允许的 CPU 数量
    const cpumask_t			*cpus_ptr; // CPU 掩码指针
    cpumask_t			*user_cpus_ptr; // 用户 CPU 掩码指针
    cpumask_t			cpus_mask; // CPU 掩码
    void				*migration_pending; // 迁移挂起
#ifdef CONFIG_SMP
    unsigned short			migration_disabled; // 禁用迁移
#endif
    unsigned short			migration_flags; // 迁移标志

#ifdef CONFIG_PREEMPT_RCU
    int				rcu_read_lock_nesting; // RCU 读锁嵌套计数
    union rcu_special		rcu_read_unlock_special; // RCU 读解锁特殊标志
    struct list_head		rcu_node_entry; // RCU 节点条目
    struct rcu_node			*rcu_blocked_node; // RCU 阻塞节点
#endif /* #ifdef CONFIG_PREEMPT_RCU */

#ifdef CONFIG_TASKS_RCU
    unsigned long			rcu_tasks_nvcsw; // RCU 任务上下文切换计数
    u8				rcu_tasks_holdout; // RCU 任务保持
    u8				rcu_tasks_idx; // RCU 任务索引
    int				rcu_tasks_idle_cpu; // RCU 任务空闲 CPU
    struct list_head		rcu_tasks_holdout_list; // RCU 任务保持列表
    int				rcu_tasks_exit_cpu; // RCU 任务退出 CPU
    struct list_head		rcu_tasks_exit_list; // RCU 任务退出列表
#endif /* #ifdef CONFIG_TASKS_RCU */

#ifdef CONFIG_TASKS_TRACE_RCU
    int				trc_reader_nesting; // 跟踪 RCU 读者嵌套计数
    int				trc_ipi_to_cpu; // 跟踪 RCU IPI 到 CPU
    union rcu_special		trc_reader_special; // 跟踪 RCU 读者特殊标志
    struct list_head		trc_holdout_list; // 跟踪 RCU 保持列表
    struct list_head		trc_blkd_node; // 跟踪 RCU 阻塞节点
    int				trc_blkd_cpu; // 跟踪 RCU 阻塞 CPU
#endif /* #ifdef CONFIG_TASKS_TRACE_RCU */

    struct sched_info		sched_info; // 调度信息

    struct list_head		tasks; // 任务列表
#ifdef CONFIG_SMP
    struct plist_node		pushable_tasks; // 可推送任务
    struct rb_node			pushable_dl_tasks; // 可推送的截止时间任务
#endif

    struct mm_struct		*mm; // 内存管理结构
    struct mm_struct		*active_mm; // 活动内存管理结构
    struct address_space		*faults_disabled_mapping; // 禁用故障映射

    int				exit_state; // 退出状态
    int				exit_code; // 退出代码
    int				exit_signal; // 退出信号
    /* The signal sent when the parent dies: */
    // 父进程死亡时发送的信号
    int				pdeath_signal;
    /* JOBCTL_*, siglock protected: */
    // JOBCTL_*，受 siglock 保护
    unsigned long			jobctl;

    /* Used for emulating ABI behavior of previous Linux versions: */
    // 用于模拟以前 Linux 版本的 ABI 行为
    unsigned int			personality;

    /* Scheduler bits, serialized by scheduler locks: */
    // 调度器位，由调度器锁序列化
    unsigned			sched_reset_on_fork:1;
    unsigned			sched_contributes_to_load:1;
    unsigned			sched_migrated:1;

    /* Force alignment to the next boundary: */
    // 强制对齐到下一个边界
    unsigned			:0;

    /* Unserialized, strictly 'current' */
    // 未序列化，严格的 'current'

    /*
     * This field must not be in the scheduler word above due to wakelist
     * queueing no longer being serialized by p->on_cpu. However:
     *
     * p->XXX = X;			ttwu()
     * schedule()			  if (p->on_rq && ..) // false
     *   smp_mb__after_spinlock();	  if (smp_load_acquire(&p->on_cpu) && //true
     *   deactivate_task()		      ttwu_queue_wakelist())
     *     p->on_rq = 0;			p->sched_remote_wakeup = Y;
     *
     * guarantees all stores of 'current' are visible before
     * ->sched_remote_wakeup gets used, so it can be in this word.
     */
    // 由于 wakelist 排队不再由 p->on_cpu 序列化，因此此字段不得位于上面的调度器字中。然而：
    // 保证所有 'current' 的存储在 ->sched_remote_wakeup 被使用之前都是可见的，因此它可以在这个字中。
    unsigned			sched_remote_wakeup:1;
#ifdef CONFIG_RT_MUTEXES
    unsigned			sched_rt_mutex:1;
#endif

    /* Bit to tell TOMOYO we're in execve(): */
    // 告诉 TOMOYO 我们在 execve() 中的位
    unsigned			in_execve:1;
    unsigned			in_iowait:1;
#ifndef TIF_RESTORE_SIGMASK
    unsigned			restore_sigmask:1;
#endif
#ifdef CONFIG_MEMCG_V1
    unsigned			in_user_fault:1;
#endif
#ifdef CONFIG_LRU_GEN
    /* whether the LRU algorithm may apply to this access */
    // LRU 算法是否可以应用于此访问
    unsigned			in_lru_fault:1;
#endif
#ifdef CONFIG_COMPAT_BRK
    unsigned			brk_randomized:1;
#endif
#ifdef CONFIG_CGROUPS
    /* disallow userland-initiated cgroup migration */
    // 禁止用户态发起的 cgroup 迁移
    unsigned			no_cgroup_migration:1;
    /* task is frozen/stopped (used by the cgroup freezer) */
    // 任务被冻结/停止（由 cgroup 冷冻器使用）
    unsigned			frozen:1;
#endif
#ifdef CONFIG_BLK_CGROUP
    unsigned			use_memdelay:1;
#endif
#ifdef CONFIG_PSI
    /* Stalled due to lack of memory */
    // 由于内存不足而停滞
    unsigned			in_memstall:1;
#endif
#ifdef CONFIG_PAGE_OWNER
    /* Used by page_owner=on to detect recursion in page tracking. */
    // 由 page_owner=on 用于检测页面跟踪中的递归。
    unsigned			in_page_owner:1;
#endif
#ifdef CONFIG_EVENTFD
    /* Recursion prevention for eventfd_signal() */
    // eventfd_signal() 的递归预防
    unsigned			in_eventfd:1;
#endif
#ifdef CONFIG_ARCH_HAS_CPU_PASID
    unsigned			pasid_activated:1;
#endif
#ifdef	CONFIG_CPU_SUP_INTEL
    unsigned			reported_split_lock:1;
#endif
#ifdef CONFIG_TASK_DELAY_ACCT
    /* delay due to memory thrashing */
    // 由于内存抖动导致的延迟
    unsigned                        in_thrashing:1;
#endif
#ifdef CONFIG_PREEMPT_RT
    struct netdev_xmit		net_xmit; // 网络传输
#endif
    unsigned long			atomic_flags; /* Flags requiring atomic access. */
    // 需要原子访问的标志

    struct restart_block		restart_block; // 重启块

    pid_t				pid; // 进程 ID
    pid_t				tgid; // 线程组 ID

#ifdef CONFIG_STACKPROTECTOR
    /* Canary value for the -fstack-protector GCC feature: */
    // -fstack-protector GCC 特性的金丝雀值
    unsigned long			stack_canary;
#endif
    /*
     * Pointers to the (original) parent process, youngest child, younger sibling,
     * older sibling, respectively.  (p->father can be replaced with
     * p->real_parent->pid)
     */
    // 指向（原始）父进程、最小的子进程、较小的兄弟姐妹、较大的兄弟姐妹的指针。（p->father 可以用 p->real_parent->pid 替换）

    /* Real parent process: */
    // 真实的父进程
    struct task_struct __rcu	*real_parent;

    /* Recipient of SIGCHLD, wait4() reports: */
    // SIGCHLD 的接收者，wait4() 报告：
    struct task_struct __rcu	*parent;

    /*
     * Children/sibling form the list of natural children:
     */
    // 子进程/兄弟姐妹形成自然子进程列表：
    struct list_head		children;
    struct list_head		sibling;
    struct task_struct		*group_leader; // 组长

    /*
     * 'ptraced' is the list of tasks this task is using ptrace() on.
     *
     * This includes both natural children and PTRACE_ATTACH targets.
     * 'ptrace_entry' is this task's link on the p->parent->ptraced list.
     */
    // 'ptraced' 是此任务正在使用 ptrace() 的任务列表。
    // 这包括自然子进程和 PTRACE_ATTACH 目标。
    // 'ptrace_entry' 是此任务在 p->parent->ptraced 列表上的链接。
    struct list_head		ptraced;
    struct list_head		ptrace_entry;

    /* PID/PID hash table linkage. */
    // PID/PID 哈希表链接。
    struct pid			*thread_pid;
    struct hlist_node		pid_links[PIDTYPE_MAX];
    struct list_head		thread_node;

    struct completion		*vfork_done; // vfork 完成

    /* CLONE_CHILD_SETTID: */
    int __user			*set_child_tid; // 设置子线程 ID

    /* CLONE_CHILD_CLEARTID: */
    int __user			*clear_child_tid; // 清除子线程 ID

    /* PF_KTHREAD | PF_IO_WORKER */
    void				*worker_private; // 工作线程私有数据

    u64				utime; // 用户时间
    u64				stime; // 系统时间
#ifdef CONFIG_ARCH_HAS_SCALED_CPUTIME
    u64				utimescaled; // 缩放的用户时间
    u64				stimescaled; // 缩放的系统时间
#endif
    u64				gtime; // 全局时间
    struct prev_cputime		prev_cputime; // 先前的 CPU 时间
#ifdef CONFIG_VIRT_CPU_ACCOUNTING_GEN
    struct vtime			vtime; // 虚拟时间
#endif

#ifdef CONFIG_NO_HZ_FULL
    atomic_t			tick_dep_mask; // 时钟依赖掩码
#endif
    /* Context switch counts: */
    // 上下文切换计数：
    unsigned long			nvcsw; // 自愿上下文切换计数
    unsigned long			nivcsw; // 非自愿上下文切换计数

    /* Monotonic time in nsecs: */
    // 单调时间（纳秒）
    u64				start_time;

    /* Boot based time in nsecs: */
    // 基于启动的时间（纳秒）
    u64				start_boottime;

    /* MM fault and swap info: this can arguably be seen as either mm-specific or thread-specific: */
    // MM 故障和交换信息：这可以被认为是特定于 mm 或特定于线程的：
    unsigned long			min_flt; // 次要故障计数
    unsigned long			maj_flt; // 主要故障计数

    /* Empty if CONFIG_POSIX_CPUTIMERS=n */
    // 如果 CONFIG_POSIX_CPUTIMERS=n 则为空
    struct posix_cputimers		posix_cputimers;

#ifdef CONFIG_POSIX_CPU_TIMERS_TASK_WORK
    struct posix_cputimers_work	posix_cputimers_work; // POSIX CPU 计时器工作
#endif

    /* Process credentials: */
    // 进程凭据：

    /* Tracer's credentials at attach: */
    // 附加时跟踪器的凭据：
    const struct cred __rcu		*ptracer_cred;

    /* Objective and real subjective task credentials (COW): */
    // 客观和真实的主观任务凭据（COW）：
    const struct cred __rcu		*real_cred;

    /* Effective (overridable) subjective task credentials (COW): */
    // 有效（可覆盖）的主观任务凭据（COW）：
    const struct cred __rcu		*cred;

#ifdef CONFIG_KEYS
    /* Cached requested key. */
    // 缓存的请求密钥。
    struct key			*cached_requested_key;
#endif

    /*
     * executable name, excluding path.
     *
     * - normally initialized setup_new_exec()
     * - access it with [gs]et_task_comm()
     * - lock it with task_lock()
     */
    // 可执行文件名，不包括路径。
    // - 通常在 setup_new_exec() 中初始化
    // - 使用 [gs]et_task_comm() 访问它
    // - 使用 task_lock() 锁定它
    char				comm[TASK_COMM_LEN];

    struct nameidata		*nameidata; // 名称数据

#ifdef CONFIG_SYSVIPC
    struct sysv_sem			sysvsem; // SYSV 信号量
    struct sysv_shm			sysvshm; // SYSV 共享内存
#endif
#ifdef CONFIG_DETECT_HUNG_TASK
    unsigned long			last_switch_count; // 最后一次切换计数
    unsigned long			last_switch_time; // 最后一次切换时间
#endif
    /* Filesystem information: */
    // 文件系统信息：
    struct fs_struct		*fs;

    /* Open file information: */
    // 打开文件信息：
    struct files_struct		*files;

#ifdef CONFIG_IO_URING
    struct io_uring_task		*io_uring; // IO uring 任务
#endif

    /* Namespaces: */
    // 命名空间
    struct nsproxy *nsproxy;
    
    /* Signal handlers: */
    // 信号处理程序
    struct signal_struct *signal;
    struct sighand_struct __rcu *sighand;
    sigset_t blocked; // 被阻塞的信号集
    sigset_t real_blocked; // 实际被阻塞的信号集
    /* Restored if set_restore_sigmask() was used: */
    // 如果使用了 set_restore_sigmask()，则恢复
    sigset_t saved_sigmask;
    struct sigpending pending; // 挂起的信号
    unsigned long sas_ss_sp; // 信号栈指针
    size_t sas_ss_size; // 信号栈大小
    unsigned int sas_ss_flags; // 信号栈标志
    
    struct callback_head *task_works; // 任务工作回调
    
#ifdef CONFIG_AUDIT
#ifdef CONFIG_AUDITSYSCALL
    struct audit_context *audit_context; // 审计上下文
#endif
    kuid_t loginuid; // 登录用户 ID
    unsigned int sessionid; // 会话 ID
#endif
    struct seccomp seccomp; // seccomp 结构
    struct syscall_user_dispatch syscall_dispatch; // 系统调用用户调度
    
    /* Thread group tracking: */
    // 线程组跟踪
    u64 parent_exec_id; // 父执行 ID
    u64 self_exec_id; // 自身执行 ID
    
    /* Protection against (de-)allocation: mm, files, fs, tty, keyrings, mems_allowed, mempolicy: */
    // 保护 mm、文件、fs、tty、密钥环、mems_allowed、mempolicy 的分配和释放
    spinlock_t alloc_lock;
    
    /* Protection of the PI data structures: */
    // 保护 PI 数据结构
    raw_spinlock_t pi_lock;
    
    struct wake_q_node wake_q; // 唤醒队列节点
    
#ifdef CONFIG_RT_MUTEXES
    /* PI waiters blocked on a rt_mutex held by this task: */
    // 被此任务持有的 rt_mutex 阻塞的 PI 等待者
    struct rb_root_cached pi_waiters;
    /* Updated under owner's pi_lock and rq lock */
    // 在所有者的 pi_lock 和 rq 锁下更新
    struct task_struct *pi_top_task;
    /* Deadlock detection and priority inheritance handling: */
    // 死锁检测和优先级继承处理
    struct rt_mutex_waiter *pi_blocked_on;
#endif

#ifdef CONFIG_DEBUG_MUTEXES
    /* Mutex deadlock detection: */
    // 互斥锁死锁检测
    struct mutex_waiter *blocked_on;
#endif

#ifdef CONFIG_DEBUG_ATOMIC_SLEEP
    int non_block_count; // 非阻塞计数
#endif

#ifdef CONFIG_TRACE_IRQFLAGS
    struct irqtrace_events irqtrace; // 中断跟踪事件
    unsigned int hardirq_threaded; // 硬中断线程化
    u64 hardirq_chain_key; // 硬中断链键
    int softirqs_enabled; // 软中断启用
    int softirq_context; // 软中断上下文
    int irq_config; // 中断配置
#endif
#ifdef CONFIG_PREEMPT_RT
    int softirq_disable_cnt; // 软中断禁用计数
#endif

#ifdef CONFIG_LOCKDEP
    # define MAX_LOCK_DEPTH 48UL
    u64 curr_chain_key; // 当前链键
    int lockdep_depth; // 锁依赖深度
    unsigned int lockdep_recursion; // 锁依赖递归
    struct held_lock held_locks[MAX_LOCK_DEPTH]; // 持有的锁
#endif

#if defined(CONFIG_UBSAN) && !defined(CONFIG_UBSAN_TRAP)
    unsigned int in_ubsan; // 在 UBSAN 中
#endif

    /* Journalling filesystem info: */
    // 日志文件系统信息
    void *journal_info;
    
    /* Stacked block device info: */
    // 堆叠块设备信息
    struct bio_list *bio_list;
    
    /* Stack plugging: */
    // 堆栈插入
    struct blk_plug *plug;
    
    /* VM state: */
    // 虚拟机状态
    struct reclaim_state *reclaim_state;
    
    struct io_context *io_context; // IO 上下文
    
#ifdef CONFIG_COMPACTION
    struct capture_control *capture_control; // 捕获控制
#endif
    /* Ptrace state: */
    // Ptrace 状态
    unsigned long ptrace_message;
    kernel_siginfo_t *last_siginfo; // 最后一个信号信息
    
    struct task_io_accounting ioac; // 任务 IO 计费
#ifdef CONFIG_PSI
    /* Pressure stall state */
    // 压力停滞状态
    unsigned int psi_flags;
#endif
#ifdef CONFIG_TASK_XACCT
    /* Accumulated RSS usage: */
    // 累积的 RSS 使用量
    u64 acct_rss_mem1;
    /* Accumulated virtual memory usage: */
    // 累积的虚拟内存使用量
    u64 acct_vm_mem1;
    /* stime + utime since last update: */
    // 自上次更新以来的 stime + utime
    u64 acct_timexpd;
#endif
#ifdef CONFIG_CPUSETS
    /* Protected by ->alloc_lock: */
    // 受 alloc_lock 保护
    nodemask_t mems_allowed;
    /* Sequence number to catch updates: */
    // 捕获更新的序列号
    seqcount_spinlock_t mems_allowed_seq;
    int cpuset_mem_spread_rotor; // cpuset 内存扩展转子
#endif
#ifdef CONFIG_CGROUPS
    /* Control Group info protected by css_set_lock: */
    // 受 css_set_lock 保护的控制组信息
    struct css_set __rcu *cgroups;
    /* cg_list protected by css_set_lock and tsk->alloc_lock: */
    // 受 css_set_lock 和 tsk->alloc_lock 保护的 cg_list
    struct list_head cg_list;
#endif
#ifdef CONFIG_X86_CPU_RESCTRL
u32 closid; // 关闭 ID
u32 rmid; // RMID
#endif
#ifdef CONFIG_FUTEX
    struct robust_list_head __user *robust_list; // 健壮列表
    #ifdef CONFIG_COMPAT
        struct compat_robust_list_head __user *compat_robust_list; // 兼容健壮列表
    #endif
    struct list_head pi_state_list; // PI 状态列表
    struct futex_pi_state *pi_state_cache; // PI 状态缓存
    struct mutex futex_exit_mutex; // futex 退出互斥锁
    unsigned int futex_state; // futex 状态
#endif
#ifdef CONFIG_PERF_EVENTS
    u8 perf_recursion[PERF_NR_CONTEXTS]; // 性能递归
    struct perf_event_context *perf_event_ctxp; // 性能事件上下文
    struct mutex perf_event_mutex; // 性能事件互斥锁
    struct list_head perf_event_list; // 性能事件列表
#endif
#ifdef CONFIG_DEBUG_PREEMPT
    unsigned long preempt_disable_ip; // 禁用抢占 IP
#endif
#ifdef CONFIG_NUMA
    /* Protected by alloc_lock: */
    // 受 alloc_lock 保护
    struct mempolicy *mempolicy;
    short il_prev; // 上一个 IL
    u8 il_weight; // IL 权重
    short pref_node_fork; // 首选节点分叉
#endif
#ifdef CONFIG_NUMA_BALANCING
    int numa_scan_seq; // NUMA 扫描序列
    unsigned int numa_scan_period; // NUMA 扫描周期
    unsigned int numa_scan_period_max; // NUMA 扫描最大周期
    int numa_preferred_nid; // NUMA 首选节点 ID
    unsigned long numa_migrate_retry; // NUMA 迁移重试
    /* Migration stamp: */
    // 迁移戳记
    u64 node_stamp;
    u64 last_task_numa_placement; // 上次任务 NUMA 放置
    u64 last_sum_exec_runtime; // 上次执行运行时间总和
    struct callback_head numa_work; // NUMA 工作回调
    
    /*
     * This pointer is only modified for current in syscall and
     * pagefault context (and for tasks being destroyed), so it can be read
     * from any of the following contexts:
     *  - RCU read-side critical section
     *  - current->numa_group from everywhere
     *  - task's runqueue locked, task not running
     */
    // 这个指针只在系统调用和页面错误上下文中（以及任务被销毁时）修改，因此可以从以下任何上下文中读取：
    // - RCU 读取侧临界区
    // - current->numa_group 从任何地方
    // - 任务的运行队列被锁定，任务未运行
    struct numa_group __rcu *numa_group;
    
    /*
     * numa_faults is an array split into four regions:
     * faults_memory, faults_cpu, faults_memory_buffer, faults_cpu_buffer
     * in this precise order.
     *
     * faults_memory: Exponential decaying average of faults on a per-node
     * basis. Scheduling placement decisions are made based on these
     * counts. The values remain static for the duration of a PTE scan.
     * faults_cpu: Track the nodes the process was running on when a NUMA
     * hinting fault was incurred.
     * faults_memory_buffer and faults_cpu_buffer: Record faults per node
     * during the current scan window. When the scan completes, the counts
     * in faults_memory and faults_cpu decay and these values are copied.
     */
    // numa_faults 是一个数组，分为四个区域：
    // faults_memory, faults_cpu, faults_memory_buffer, faults_cpu_buffer
    // 按照这个精确的顺序。
    //
    // faults_memory: 每个节点的故障指数衰减平均值。调度放置决策基于这些计数。值在 PTE 扫描期间保持静态。
    // faults_cpu: 跟踪进程在发生 NUMA 提示故障时运行的节点。
    // faults_memory_buffer 和 faults_cpu_buffer: 在当前扫描窗口期间记录每个节点的故障。当扫描完成时，faults_memory 和 faults_cpu 中的计数衰减并复制这些值。
    unsigned long *numa_faults;
    unsigned long total_numa_faults; // NUMA 故障总数
    
    /*
     * numa_faults_locality tracks if faults recorded during the last
     * scan window were remote/local or failed to migrate. The task scan
     * period is adapted based on the locality of the faults with different
     * weights depending on whether they were shared or private faults
     */
    // numa_faults_locality 跟踪在最后一个扫描窗口期间记录的故障是远程/本地还是迁移失败。任务扫描周期根据故障的局部性进行调整，不同的权重取决于它们是共享故障还是私有故障
    unsigned long numa_faults_locality[3];
    
    unsigned long numa_pages_migrated; // NUMA 迁移的页面数
#endif /* CONFIG_NUMA_BALANCING */

#ifdef CONFIG_RSEQ
    struct rseq __user *rseq; // rseq 结构
    u32 rseq_len; // rseq 长度
    u32 rseq_sig; // rseq 信号
    /*
     * RmW on rseq_event_mask must be performed atomically
     * with respect to preemption.
     */
    // rseq_event_mask 上的 RmW 必须相对于抢占原子执行
    unsigned long rseq_event_mask;
#endif

#ifdef CONFIG_SCHED_MM_CID
    int mm_cid; // 当前 mm 中的 CID
    int last_mm_cid; // 最近的 mm 中的 CID
    int migrate_from_cpu; // 从 CPU 迁移
    int mm_cid_active; // CID 位图是否激活
    struct callback_head cid_work; // CID 工作回调
#endif

    struct tlbflush_unmap_batch tlb_ubc; // TLB 刷新取消映射批处理
    
    /* Cache last used pipe for splice(): */
    // 缓存最后使用的管道用于 splice()
    struct pipe_inode_info *splice_pipe;
    
    struct page_frag task_frag; // 页面碎片
    
#ifdef CONFIG_TASK_DELAY_ACCT
    struct task_delay_info *delays; // 任务延迟信息
#endif

#ifdef CONFIG_FAULT_INJECTION
    int make_it_fail; // 使其失败
    unsigned int fail_nth; // 第 n 次失败
#endif
    /*
     * When (nr_dirtied >= nr_dirtied_pause), it's time to call
     * balance_dirty_pages() for a dirty throttling pause:
     */
    // 当 (nr_dirtied >= nr_dirtied_pause) 时，是时候调用 balance_dirty_pages() 进行脏节流暂停了
    int nr_dirtied; // 脏页数
    int nr_dirtied_pause; // 脏页暂停数
    /* Start of a write-and-pause period: */
    // 写入和暂停周期的开始
    unsigned long dirty_paused_when;
    
#ifdef CONFIG_LATENCYTOP
    int latency_record_count; // 延迟记录计数
    struct latency_record latency_record[LT_SAVECOUNT]; // 延迟记录
#endif
    /*
     * Time slack values; these are used to round up poll() and
     * select() etc timeout values. These are in nanoseconds.
     */
    // 时间松弛值；这些用于四舍五入 poll() 和 select() 等超时值。这些值以纳秒为单位。
    u64 timer_slack_ns;
    u64 default_timer_slack_ns;
    
#if defined(CONFIG_KASAN_GENERIC) || defined(CONFIG_KASAN_SW_TAGS)
    unsigned int kasan_depth; // KASAN 深度
#endif

#ifdef CONFIG_KCSAN
    struct kcsan_ctx kcsan_ctx; // KCSAN 上下文
    #ifdef CONFIG_TRACE_IRQFLAGS
        struct irqtrace_events kcsan_save_irqtrace; // KCSAN 保存的中断跟踪事件
    #endif
    #ifdef CONFIG_KCSAN_WEAK_MEMORY
        int kcsan_stack_depth; // KCSAN 堆栈深度
    #endif
#endif

#ifdef CONFIG_KMSAN
    struct kmsan_ctx kmsan_ctx; // KMSAN 上下文
#endif

#if IS_ENABLED(CONFIG_KUNIT)
    struct kunit *kunit_test; // KUnit 测试
#endif

#ifdef CONFIG_FUNCTION_GRAPH_TRACER
    /* Index of current stored address in ret_stack: */
    // ret_stack 中当前存储地址的索引
    int curr_ret_stack;
    int curr_ret_depth; // 当前返回深度
    
    /* Stack of return addresses for return function tracing: */
    // 返回函数跟踪的返回地址堆栈
    unsigned long *ret_stack;
    
    /* Timestamp for last schedule: */
    // 上次调度的时间戳
    unsigned long long ftrace_timestamp;
    
    /*
     * Number of functions that haven't been traced
     * because of depth overrun:
     */
    // 由于深度超限而未被跟踪的函数数量
    atomic_t trace_overrun;
    
    /* Pause tracing: */
    // 暂停跟踪
    atomic_t tracing_graph_pause;
#endif

#ifdef CONFIG_TRACING
    /* Bitmask and counter of trace recursion: */
    // 跟踪递归的位掩码和计数器
    unsigned long trace_recursion;
#endif /* CONFIG_TRACING */

#ifdef CONFIG_KCOV
    /* See kernel/kcov.c for more details. */
    // 参见 kernel/kcov.c 获取更多细节
    
    /* Coverage collection mode enabled for this task (0 if disabled): */
    // 为此任务启用的覆盖收集模式（如果禁用则为 0）
    unsigned int kcov_mode;
    
    /* Size of the kcov_area: */
    // kcov_area 的大小
    unsigned int kcov_size;
    
    /* Buffer for coverage collection: */
    // 覆盖收集缓冲区
    void *kcov_area;
    
    /* KCOV descriptor wired with this task or NULL: */
    // 与此任务连接的 KCOV 描述符或 NULL
    struct kcov *kcov;
    
    /* KCOV common handle for remote coverage collection: */
    // 远程覆盖收集的 KCOV 通用句柄
    u64 kcov_handle;
    
    /* KCOV sequence number: */
    // KCOV 序列号
    int kcov_sequence;
    
    /* Collect coverage from softirq context: */
    // 从软中断上下文收集覆盖
    unsigned int kcov_softirq;
#endif

#ifdef CONFIG_MEMCG_V1
    struct mem_cgroup *memcg_in_oom; // OOM 中的内存控制组
#endif

#ifdef CONFIG_MEMCG
    /* Number of pages to reclaim on returning to userland: */
    // 返回用户态时要回收的页面数
    unsigned int memcg_nr_pages_over_high;
    
    /* Used by memcontrol for targeted memcg charge: */
    // 由内存控制用于目标 memcg 充电
    struct mem_cgroup *active_memcg;
    
    /* Cache for current->cgroups->memcg->objcg lookups: */
    // 当前->cgroups->memcg->objcg 查找的缓存
    struct obj_cgroup *objcg;
#endif

#ifdef CONFIG_BLK_CGROUP
    struct gendisk *throttle_disk; // 节流磁盘
#endif

#ifdef CONFIG_UPROBES
    struct uprobe_task *utask; // uprobe 任务
#endif
#if defined(CONFIG_BCACHE) || defined(CONFIG_BCACHE_MODULE)
    unsigned int sequential_io; // 顺序 IO
    unsigned int sequential_io_avg; // 顺序 IO 平均值
#endif
    struct kmap_ctrl kmap_ctrl; // kmap 控制
#ifdef CONFIG_DEBUG_ATOMIC_SLEEP
    unsigned long task_state_change; // 任务状态变化
# ifdef CONFIG_PREEMPT_RT
    unsigned long saved_state_change; // 保存的状态变化
# endif
#endif
    struct rcu_head rcu; // RCU 头
    refcount_t rcu_users; // RCU 用户计数
    int pagefault_disabled; // 页面错误禁用
#ifdef CONFIG_MMU
    struct task_struct *oom_reaper_list; // OOM 收割者列表
    struct timer_list oom_reaper_timer; // OOM 收割者定时器
#endif
#ifdef CONFIG_VMAP_STACK
    struct vm_struct *stack_vm_area; // 堆栈虚拟内存区域
#endif
#ifdef CONFIG_THREAD_INFO_IN_TASK
    /* A live task holds one reference: */
    // 活动任务持有一个引用
    refcount_t stack_refcount;
#endif
#ifdef CONFIG_LIVEPATCH
    int patch_state; // 补丁状态
#endif
#ifdef CONFIG_SECURITY
    /* Used by LSM modules for access restriction: */
    // 由 LSM 模块用于访问限制
    void *security;
#endif
#ifdef CONFIG_BPF_SYSCALL
    /* Used by BPF task local storage */
    // 由 BPF 任务本地存储使用
    struct bpf_local_storage __rcu *bpf_storage;
    /* Used for BPF run context */
    // 用于 BPF 运行上下文
    struct bpf_run_ctx *bpf_ctx;
#endif
    /* Used by BPF for per-TASK xdp storage */
    // 由 BPF 用于每个任务的 XDP 存储
    struct bpf_net_context *bpf_net_context;
    
#ifdef CONFIG_GCC_PLUGIN_STACKLEAK
    unsigned long lowest_stack; // 最低堆栈
    unsigned long prev_lowest_stack; // 之前的最低堆栈
#endif

#ifdef CONFIG_X86_MCE
    void __user *mce_vaddr; // MCE 虚拟地址
    __u64 mce_kflags; // MCE 内核标志
    u64 mce_addr; // MCE 地址
    __u64 mce_ripv : 1, // MCE RIPV
    					mce_whole_page : 1,
    					__mce_reserved : 62;
    	struct callback_head		mce_kill_me;
    	int				mce_count;
#endif

#ifdef CONFIG_KRETPROBES
    struct llist_head               kretprobe_instances; // kretprobe 实例
#endif
#ifdef CONFIG_RETHOOK
    struct llist_head               rethooks; // rethook 实例
#endif

#ifdef CONFIG_ARCH_HAS_PARANOID_L1D_FLUSH
    /*
     * If L1D flush is supported on mm context switch
     * then we use this callback head to queue kill work
     * to kill tasks that are not running on SMT disabled
     * cores
     */
    // 如果在 mm 上下文切换时支持 L1D 刷新，那么我们使用这个回调头来排队杀死那些没有在禁用 SMT 的核心上运行的任务
    struct callback_head		l1d_flush_kill;
#endif

#ifdef CONFIG_RV
    /*
     * Per-task RV monitor. Nowadays fixed in RV_PER_TASK_MONITORS.
     * If we find justification for more monitors, we can think
     * about adding more or developing a dynamic method. So far,
     * none of these are justified.
     */
    // 每个任务的 RV 监视器。如今固定在 RV_PER_TASK_MONITORS 中。
    // 如果我们找到更多监视器的理由，我们可以考虑添加更多或开发一种动态方法。到目前为止，这些都没有理由。
    union rv_task_monitor		rv[RV_PER_TASK_MONITORS];
#endif

#ifdef CONFIG_USER_EVENTS
    struct user_event_mm		*user_event_mm; // 用户事件内存管理
#endif

    /*
     * New fields for task_struct should be added above here, so that
     * they are included in the randomized portion of task_struct.
     */
    // task_struct 的新字段应添加在此处上方，以便它们包含在 task_struct 的随机化部分中。
    randomized_struct_fields_end

    /* CPU-specific state of this task: */
    // 此任务的特定 CPU 状态
    struct thread_struct		thread;

    /*
     * WARNING: on x86, 'thread_struct' contains a variable-sized
     * structure.  It *MUST* be at the end of 'task_struct'.
     *
     * Do not put anything below here!
     */
    // 警告：在 x86 上，'thread_struct' 包含一个可变大小的结构。它必须位于 'task_struct' 的末尾。
    // 不要在此处下面放置任何内容！
};

#define TASK_REPORT_IDLE	(TASK_REPORT + 1)
#define TASK_REPORT_MAX		(TASK_REPORT_IDLE << 1)

static inline unsigned int __task_state_index(unsigned int tsk_state,
					      unsigned int tsk_exit_state)
{
	unsigned int state = (tsk_state | tsk_exit_state) & TASK_REPORT;

	BUILD_BUG_ON_NOT_POWER_OF_2(TASK_REPORT_MAX);

	if ((tsk_state & TASK_IDLE) == TASK_IDLE)
		state = TASK_REPORT_IDLE;

	/*
	 * We're lying here, but rather than expose a completely new task state
	 * to userspace, we can make this appear as if the task has gone through
	 * a regular rt_mutex_lock() call.
	 */
	if (tsk_state & TASK_RTLOCK_WAIT)
		state = TASK_UNINTERRUPTIBLE;

	return fls(state);
}

static inline unsigned int task_state_index(struct task_struct *tsk)
{
	return __task_state_index(READ_ONCE(tsk->__state), tsk->exit_state);
}

static inline char task_index_to_char(unsigned int state)
{
	static const char state_char[] = "RSDTtXZPI";

	BUILD_BUG_ON(TASK_REPORT_MAX * 2 != 1 << (sizeof(state_char) - 1));

	return state_char[state];
}

static inline char task_state_to_char(struct task_struct *tsk)
{
	return task_index_to_char(task_state_index(tsk));
}

extern struct pid *cad_pid;

/*
 * Per process flags
 */
#define PF_VCPU			0x00000001	/* I'm a virtual CPU */
#define PF_IDLE			0x00000002	/* I am an IDLE thread */
#define PF_EXITING		0x00000004	/* Getting shut down */
#define PF_POSTCOREDUMP		0x00000008	/* Coredumps should ignore this task */
#define PF_IO_WORKER		0x00000010	/* Task is an IO worker */
#define PF_WQ_WORKER		0x00000020	/* I'm a workqueue worker */
#define PF_FORKNOEXEC		0x00000040	/* Forked but didn't exec */
#define PF_MCE_PROCESS		0x00000080      /* Process policy on mce errors */
#define PF_SUPERPRIV		0x00000100	/* Used super-user privileges */
#define PF_DUMPCORE		0x00000200	/* Dumped core */
#define PF_SIGNALED		0x00000400	/* Killed by a signal */
#define PF_MEMALLOC		0x00000800	/* Allocating memory to free memory. See memalloc_noreclaim_save() */
#define PF_NPROC_EXCEEDED	0x00001000	/* set_user() noticed that RLIMIT_NPROC was exceeded */
#define PF_USED_MATH		0x00002000	/* If unset the fpu must be initialized before use */
#define PF_USER_WORKER		0x00004000	/* Kernel thread cloned from userspace thread */
#define PF_NOFREEZE		0x00008000	/* This thread should not be frozen */
#define PF__HOLE__00010000	0x00010000
#define PF_KSWAPD		0x00020000	/* I am kswapd */
#define PF_MEMALLOC_NOFS	0x00040000	/* All allocations inherit GFP_NOFS. See memalloc_nfs_save() */
#define PF_MEMALLOC_NOIO	0x00080000	/* All allocations inherit GFP_NOIO. See memalloc_noio_save() */
#define PF_LOCAL_THROTTLE	0x00100000	/* Throttle writes only against the bdi I write to,
						 * I am cleaning dirty pages from some other bdi. */
#define PF_KTHREAD		0x00200000	/* I am a kernel thread */
#define PF_RANDOMIZE		0x00400000	/* Randomize virtual address space */
#define PF__HOLE__00800000	0x00800000
#define PF__HOLE__01000000	0x01000000
#define PF__HOLE__02000000	0x02000000
#define PF_NO_SETAFFINITY	0x04000000	/* Userland is not allowed to meddle with cpus_mask */
#define PF_MCE_EARLY		0x08000000      /* Early kill for mce process policy */
#define PF_MEMALLOC_PIN		0x10000000	/* Allocations constrained to zones which allow long term pinning.
						 * See memalloc_pin_save() */
#define PF_BLOCK_TS		0x20000000	/* plug has ts that needs updating */
#define PF__HOLE__40000000	0x40000000
#define PF_SUSPEND_TASK		0x80000000      /* This thread called freeze_processes() and should not be frozen */

/*
 * Only the _current_ task can read/write to tsk->flags, but other
 * tasks can access tsk->flags in readonly mode for example
 * with tsk_used_math (like during threaded core dumping).
 * There is however an exception to this rule during ptrace
 * or during fork: the ptracer task is allowed to write to the
 * child->flags of its traced child (same goes for fork, the parent
 * can write to the child->flags), because we're guaranteed the
 * child is not running and in turn not changing child->flags
 * at the same time the parent does it.
 */
#define clear_stopped_child_used_math(child)	do { (child)->flags &= ~PF_USED_MATH; } while (0)
#define set_stopped_child_used_math(child)	do { (child)->flags |= PF_USED_MATH; } while (0)
#define clear_used_math()			clear_stopped_child_used_math(current)
#define set_used_math()				set_stopped_child_used_math(current)

#define conditional_stopped_child_used_math(condition, child) \
	do { (child)->flags &= ~PF_USED_MATH, (child)->flags |= (condition) ? PF_USED_MATH : 0; } while (0)

#define conditional_used_math(condition)	conditional_stopped_child_used_math(condition, current)

#define copy_to_stopped_child_used_math(child) \
	do { (child)->flags &= ~PF_USED_MATH, (child)->flags |= current->flags & PF_USED_MATH; } while (0)

/* NOTE: this will return 0 or PF_USED_MATH, it will never return 1 */
#define tsk_used_math(p)			((p)->flags & PF_USED_MATH)
#define used_math()				tsk_used_math(current)

static __always_inline bool is_percpu_thread(void)
{
#ifdef CONFIG_SMP
	return (current->flags & PF_NO_SETAFFINITY) &&
		(current->nr_cpus_allowed  == 1);
#else
	return true;
#endif
}

/* Per-process atomic flags. */
#define PFA_NO_NEW_PRIVS		0	/* May not gain new privileges. */
#define PFA_SPREAD_PAGE			1	/* Spread page cache over cpuset */
#define PFA_SPREAD_SLAB			2	/* Spread some slab caches over cpuset */
#define PFA_SPEC_SSB_DISABLE		3	/* Speculative Store Bypass disabled */
#define PFA_SPEC_SSB_FORCE_DISABLE	4	/* Speculative Store Bypass force disabled*/
#define PFA_SPEC_IB_DISABLE		5	/* Indirect branch speculation restricted */
#define PFA_SPEC_IB_FORCE_DISABLE	6	/* Indirect branch speculation permanently restricted */
#define PFA_SPEC_SSB_NOEXEC		7	/* Speculative Store Bypass clear on execve() */

#define TASK_PFA_TEST(name, func)					\
	static inline bool task_##func(struct task_struct *p)		\
	{ return test_bit(PFA_##name, &p->atomic_flags); }

#define TASK_PFA_SET(name, func)					\
	static inline void task_set_##func(struct task_struct *p)	\
	{ set_bit(PFA_##name, &p->atomic_flags); }

#define TASK_PFA_CLEAR(name, func)					\
	static inline void task_clear_##func(struct task_struct *p)	\
	{ clear_bit(PFA_##name, &p->atomic_flags); }

TASK_PFA_TEST(NO_NEW_PRIVS, no_new_privs)
TASK_PFA_SET(NO_NEW_PRIVS, no_new_privs)

TASK_PFA_TEST(SPREAD_PAGE, spread_page)
TASK_PFA_SET(SPREAD_PAGE, spread_page)
TASK_PFA_CLEAR(SPREAD_PAGE, spread_page)

TASK_PFA_TEST(SPREAD_SLAB, spread_slab)
TASK_PFA_SET(SPREAD_SLAB, spread_slab)
TASK_PFA_CLEAR(SPREAD_SLAB, spread_slab)

TASK_PFA_TEST(SPEC_SSB_DISABLE, spec_ssb_disable)
TASK_PFA_SET(SPEC_SSB_DISABLE, spec_ssb_disable)
TASK_PFA_CLEAR(SPEC_SSB_DISABLE, spec_ssb_disable)

TASK_PFA_TEST(SPEC_SSB_NOEXEC, spec_ssb_noexec)
TASK_PFA_SET(SPEC_SSB_NOEXEC, spec_ssb_noexec)
TASK_PFA_CLEAR(SPEC_SSB_NOEXEC, spec_ssb_noexec)

TASK_PFA_TEST(SPEC_SSB_FORCE_DISABLE, spec_ssb_force_disable)
TASK_PFA_SET(SPEC_SSB_FORCE_DISABLE, spec_ssb_force_disable)

TASK_PFA_TEST(SPEC_IB_DISABLE, spec_ib_disable)
TASK_PFA_SET(SPEC_IB_DISABLE, spec_ib_disable)
TASK_PFA_CLEAR(SPEC_IB_DISABLE, spec_ib_disable)

TASK_PFA_TEST(SPEC_IB_FORCE_DISABLE, spec_ib_force_disable)
TASK_PFA_SET(SPEC_IB_FORCE_DISABLE, spec_ib_force_disable)

static inline void
current_restore_flags(unsigned long orig_flags, unsigned long flags)
{
	current->flags &= ~flags;
	current->flags |= orig_flags & flags;
}

extern int cpuset_cpumask_can_shrink(const struct cpumask *cur, const struct cpumask *trial);
extern int task_can_attach(struct task_struct *p);
extern int dl_bw_alloc(int cpu, u64 dl_bw);
extern void dl_bw_free(int cpu, u64 dl_bw);
#ifdef CONFIG_SMP

/* do_set_cpus_allowed() - consider using set_cpus_allowed_ptr() instead */
extern void do_set_cpus_allowed(struct task_struct *p, const struct cpumask *new_mask);

/**
 * set_cpus_allowed_ptr - set CPU affinity mask of a task
 * @p: the task
 * @new_mask: CPU affinity mask
 *
 * Return: zero if successful, or a negative error code
 */
extern int set_cpus_allowed_ptr(struct task_struct *p, const struct cpumask *new_mask);
extern int dup_user_cpus_ptr(struct task_struct *dst, struct task_struct *src, int node);
extern void release_user_cpus_ptr(struct task_struct *p);
extern int dl_task_check_affinity(struct task_struct *p, const struct cpumask *mask);
extern void force_compatible_cpus_allowed_ptr(struct task_struct *p);
extern void relax_compatible_cpus_allowed_ptr(struct task_struct *p);
#else
static inline void do_set_cpus_allowed(struct task_struct *p, const struct cpumask *new_mask)
{
}
static inline int set_cpus_allowed_ptr(struct task_struct *p, const struct cpumask *new_mask)
{
	/* Opencoded cpumask_test_cpu(0, new_mask) to avoid dependency on cpumask.h */
	if ((*cpumask_bits(new_mask) & 1) == 0)
		return -EINVAL;
	return 0;
}
static inline int dup_user_cpus_ptr(struct task_struct *dst, struct task_struct *src, int node)
{
	if (src->user_cpus_ptr)
		return -EINVAL;
	return 0;
}
static inline void release_user_cpus_ptr(struct task_struct *p)
{
	WARN_ON(p->user_cpus_ptr);
}

static inline int dl_task_check_affinity(struct task_struct *p, const struct cpumask *mask)
{
	return 0;
}
#endif

extern int yield_to(struct task_struct *p, bool preempt);
extern void set_user_nice(struct task_struct *p, long nice);
extern int task_prio(const struct task_struct *p);

/**
 * task_nice - return the nice value of a given task.
 * @p: the task in question.
 *
 * Return: The nice value [ -20 ... 0 ... 19 ].
 */
// task_nice - 返回给定任务的 nice 值。
// @p: 相关任务。
//
// 返回值：nice 值 [ -20 ... 0 ... 19 ]。
static inline int task_nice(const struct task_struct *p)
{
    return PRIO_TO_NICE((p)->static_prio); // 将静态优先级转换为 nice 值
}

extern int can_nice(const struct task_struct *p, const int nice);
extern int task_curr(const struct task_struct *p);
extern int idle_cpu(int cpu);
extern int available_idle_cpu(int cpu);
extern int sched_setscheduler(struct task_struct *, int, const struct sched_param *);
extern int sched_setscheduler_nocheck(struct task_struct *, int, const struct sched_param *);
extern void sched_set_fifo(struct task_struct *p);
extern void sched_set_fifo_low(struct task_struct *p);
extern void sched_set_normal(struct task_struct *p, int nice);
extern int sched_setattr(struct task_struct *, const struct sched_attr *);
extern int sched_setattr_nocheck(struct task_struct *, const struct sched_attr *);
extern struct task_struct *idle_task(int cpu);

/**
 * is_idle_task - is the specified task an idle task?
 * @p: the task in question.
 *
 * Return: 1 if @p is an idle task. 0 otherwise.
 */
static __always_inline bool is_idle_task(const struct task_struct *p)
{
	return !!(p->flags & PF_IDLE);
}

extern struct task_struct *curr_task(int cpu);
extern void ia64_set_curr_task(int cpu, struct task_struct *p);

void yield(void);

union thread_union {
	struct task_struct task;
#ifndef CONFIG_THREAD_INFO_IN_TASK
	struct thread_info thread_info;
#endif
	unsigned long stack[THREAD_SIZE/sizeof(long)];
};

#ifndef CONFIG_THREAD_INFO_IN_TASK
extern struct thread_info init_thread_info;
#endif

extern unsigned long init_stack[THREAD_SIZE / sizeof(unsigned long)];

#ifdef CONFIG_THREAD_INFO_IN_TASK
# define task_thread_info(task)	(&(task)->thread_info)
#elif !defined(__HAVE_THREAD_FUNCTIONS)
# define task_thread_info(task)	((struct thread_info *)(task)->stack)
#endif

/*
 * find a task by one of its numerical ids
 *
 * find_task_by_pid_ns():
 *      finds a task by its pid in the specified namespace
 * find_task_by_vpid():
 *      finds a task by its virtual pid
 *
 * see also find_vpid() etc in include/linux/pid.h
 */

extern struct task_struct *find_task_by_vpid(pid_t nr);
extern struct task_struct *find_task_by_pid_ns(pid_t nr, struct pid_namespace *ns);

/*
 * find a task by its virtual pid and get the task struct
 */
extern struct task_struct *find_get_task_by_vpid(pid_t nr);

extern int wake_up_state(struct task_struct *tsk, unsigned int state);
extern int wake_up_process(struct task_struct *tsk);
extern void wake_up_new_task(struct task_struct *tsk);

#ifdef CONFIG_SMP
extern void kick_process(struct task_struct *tsk);
#else
static inline void kick_process(struct task_struct *tsk) { }
#endif

extern void __set_task_comm(struct task_struct *tsk, const char *from, bool exec);

static inline void set_task_comm(struct task_struct *tsk, const char *from)
{
	__set_task_comm(tsk, from, false);
}

extern char *__get_task_comm(char *to, size_t len, struct task_struct *tsk);
#define get_task_comm(buf, tsk) ({			\
	BUILD_BUG_ON(sizeof(buf) != TASK_COMM_LEN);	\
	__get_task_comm(buf, sizeof(buf), tsk);		\
})

#ifdef CONFIG_SMP
static __always_inline void scheduler_ipi(void)
{
	/*
	 * Fold TIF_NEED_RESCHED into the preempt_count; anybody setting
	 * TIF_NEED_RESCHED remotely (for the first time) will also send
	 * this IPI.
	 */
	preempt_fold_need_resched();
}
#else
static inline void scheduler_ipi(void) { }
#endif

extern unsigned long wait_task_inactive(struct task_struct *, unsigned int match_state);

/*
 * Set thread flags in other task's structures.
 * See asm/thread_info.h for TIF_xxxx flags available:
 */
static inline void set_tsk_thread_flag(struct task_struct *tsk, int flag)
{
	set_ti_thread_flag(task_thread_info(tsk), flag);
}

static inline void clear_tsk_thread_flag(struct task_struct *tsk, int flag)
{
	clear_ti_thread_flag(task_thread_info(tsk), flag);
}

static inline void update_tsk_thread_flag(struct task_struct *tsk, int flag,
					  bool value)
{
	update_ti_thread_flag(task_thread_info(tsk), flag, value);
}

static inline int test_and_set_tsk_thread_flag(struct task_struct *tsk, int flag)
{
	return test_and_set_ti_thread_flag(task_thread_info(tsk), flag);
}

static inline int test_and_clear_tsk_thread_flag(struct task_struct *tsk, int flag)
{
	return test_and_clear_ti_thread_flag(task_thread_info(tsk), flag);
}

static inline int test_tsk_thread_flag(struct task_struct *tsk, int flag)
{
	return test_ti_thread_flag(task_thread_info(tsk), flag);
}

static inline void set_tsk_need_resched(struct task_struct *tsk)
{
	set_tsk_thread_flag(tsk,TIF_NEED_RESCHED);
}

static inline void clear_tsk_need_resched(struct task_struct *tsk)
{
	clear_tsk_thread_flag(tsk,TIF_NEED_RESCHED);
}

static inline int test_tsk_need_resched(struct task_struct *tsk)
{
	return unlikely(test_tsk_thread_flag(tsk,TIF_NEED_RESCHED));
}

/*
 * cond_resched() and cond_resched_lock(): latency reduction via
 * explicit rescheduling in places that are safe. The return
 * value indicates whether a reschedule was done in fact.
 * cond_resched_lock() will drop the spinlock before scheduling,
 */
#if !defined(CONFIG_PREEMPTION) || defined(CONFIG_PREEMPT_DYNAMIC)
extern int __cond_resched(void);

#if defined(CONFIG_PREEMPT_DYNAMIC) && defined(CONFIG_HAVE_PREEMPT_DYNAMIC_CALL)

void sched_dynamic_klp_enable(void);
void sched_dynamic_klp_disable(void);

DECLARE_STATIC_CALL(cond_resched, __cond_resched);

static __always_inline int _cond_resched(void)
{
	return static_call_mod(cond_resched)();
}

#elif defined(CONFIG_PREEMPT_DYNAMIC) && defined(CONFIG_HAVE_PREEMPT_DYNAMIC_KEY)

extern int dynamic_cond_resched(void);

static __always_inline int _cond_resched(void)
{
	return dynamic_cond_resched();
}

#else /* !CONFIG_PREEMPTION */

static inline int _cond_resched(void)
{
	klp_sched_try_switch();
	return __cond_resched();
}

#endif /* PREEMPT_DYNAMIC && CONFIG_HAVE_PREEMPT_DYNAMIC_CALL */

#else /* CONFIG_PREEMPTION && !CONFIG_PREEMPT_DYNAMIC */

static inline int _cond_resched(void)
{
	klp_sched_try_switch();
	return 0;
}

#endif /* !CONFIG_PREEMPTION || CONFIG_PREEMPT_DYNAMIC */

#define cond_resched() ({			\
	__might_resched(__FILE__, __LINE__, 0);	\
	_cond_resched();			\
})

extern int __cond_resched_lock(spinlock_t *lock);
extern int __cond_resched_rwlock_read(rwlock_t *lock);
extern int __cond_resched_rwlock_write(rwlock_t *lock);

#define MIGHT_RESCHED_RCU_SHIFT		8
#define MIGHT_RESCHED_PREEMPT_MASK	((1U << MIGHT_RESCHED_RCU_SHIFT) - 1)

#ifndef CONFIG_PREEMPT_RT
/*
 * Non RT kernels have an elevated preempt count due to the held lock,
 * but are not allowed to be inside a RCU read side critical section
 */
# define PREEMPT_LOCK_RESCHED_OFFSETS	PREEMPT_LOCK_OFFSET
#else
/*
 * spin/rw_lock() on RT implies rcu_read_lock(). The might_sleep() check in
 * cond_resched*lock() has to take that into account because it checks for
 * preempt_count() and rcu_preempt_depth().
 */
# define PREEMPT_LOCK_RESCHED_OFFSETS	\
	(PREEMPT_LOCK_OFFSET + (1U << MIGHT_RESCHED_RCU_SHIFT))
#endif

#define cond_resched_lock(lock) ({						\
	__might_resched(__FILE__, __LINE__, PREEMPT_LOCK_RESCHED_OFFSETS);	\
	__cond_resched_lock(lock);						\
})

#define cond_resched_rwlock_read(lock) ({					\
	__might_resched(__FILE__, __LINE__, PREEMPT_LOCK_RESCHED_OFFSETS);	\
	__cond_resched_rwlock_read(lock);					\
})

#define cond_resched_rwlock_write(lock) ({					\
	__might_resched(__FILE__, __LINE__, PREEMPT_LOCK_RESCHED_OFFSETS);	\
	__cond_resched_rwlock_write(lock);					\
})

static __always_inline bool need_resched(void)
{
	return unlikely(tif_need_resched());
}

/*
 * Wrappers for p->thread_info->cpu access. No-op on UP.
 */
#ifdef CONFIG_SMP

static inline unsigned int task_cpu(const struct task_struct *p)
{
	return READ_ONCE(task_thread_info(p)->cpu);
}

extern void set_task_cpu(struct task_struct *p, unsigned int cpu);

#else

static inline unsigned int task_cpu(const struct task_struct *p)
{
	return 0;
}

static inline void set_task_cpu(struct task_struct *p, unsigned int cpu)
{
}

#endif /* CONFIG_SMP */

static inline bool task_is_runnable(struct task_struct *p)
{
	return p->on_rq && !p->se.sched_delayed;
}

extern bool sched_task_on_rq(struct task_struct *p);
extern unsigned long get_wchan(struct task_struct *p);
extern struct task_struct *cpu_curr_snapshot(int cpu);

#include <linux/spinlock.h>

/*
 * In order to reduce various lock holder preemption latencies provide an
 * interface to see if a vCPU is currently running or not.
 *
 * This allows us to terminate optimistic spin loops and block, analogous to
 * the native optimistic spin heuristic of testing if the lock owner task is
 * running or not.
 */
#ifndef vcpu_is_preempted
static inline bool vcpu_is_preempted(int cpu)
{
	return false;
}
#endif

extern long sched_setaffinity(pid_t pid, const struct cpumask *new_mask);
extern long sched_getaffinity(pid_t pid, struct cpumask *mask);

#ifndef TASK_SIZE_OF
#define TASK_SIZE_OF(tsk)	TASK_SIZE
#endif

#ifdef CONFIG_SMP
static inline bool owner_on_cpu(struct task_struct *owner)
{
	/*
	 * As lock holder preemption issue, we both skip spinning if
	 * task is not on cpu or its cpu is preempted
	 */
	return READ_ONCE(owner->on_cpu) && !vcpu_is_preempted(task_cpu(owner));
}

/* Returns effective CPU energy utilization, as seen by the scheduler */
unsigned long sched_cpu_util(int cpu);
#endif /* CONFIG_SMP */

#ifdef CONFIG_SCHED_CORE
extern void sched_core_free(struct task_struct *tsk);
extern void sched_core_fork(struct task_struct *p);
extern int sched_core_share_pid(unsigned int cmd, pid_t pid, enum pid_type type,
				unsigned long uaddr);
extern int sched_core_idle_cpu(int cpu);
#else
static inline void sched_core_free(struct task_struct *tsk) { }
static inline void sched_core_fork(struct task_struct *p) { }
static inline int sched_core_idle_cpu(int cpu) { return idle_cpu(cpu); }
#endif

extern void sched_set_stop_task(int cpu, struct task_struct *stop);

#ifdef CONFIG_MEM_ALLOC_PROFILING
static __always_inline struct alloc_tag *alloc_tag_save(struct alloc_tag *tag)
{
	swap(current->alloc_tag, tag);
	return tag;
}

static __always_inline void alloc_tag_restore(struct alloc_tag *tag, struct alloc_tag *old)
{
#ifdef CONFIG_MEM_ALLOC_PROFILING_DEBUG
	WARN(current->alloc_tag != tag, "current->alloc_tag was changed:\n");
#endif
	current->alloc_tag = old;
}
#else
#define alloc_tag_save(_tag)			NULL
#define alloc_tag_restore(_tag, _old)		do {} while (0)
#endif

#endif
