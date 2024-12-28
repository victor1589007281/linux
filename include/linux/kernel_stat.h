/* SPDX-License-Identifier: GPL-2.0 */
#ifndef _LINUX_KERNEL_STAT_H
#define _LINUX_KERNEL_STAT_H

#include <linux/smp.h>
#include <linux/threads.h>
#include <linux/percpu.h>
#include <linux/interrupt.h>
#include <linux/sched.h>
#include <linux/vtime.h>
#include <asm/irq.h>

/*
 * 'kernel_stat.h' contains the definitions needed for doing
 * some kernel statistics (CPU usage, context switches ...),
 * used by rstatd/perfmeter
 */

enum cpu_usage_stat {
    CPUTIME_USER,        // 用户态 CPU 时间
    CPUTIME_NICE,        // 低优先级用户态 CPU 时间
    CPUTIME_SYSTEM,      // 内核态 CPU 时间
    CPUTIME_SOFTIRQ,     // 软中断 CPU 时间
    CPUTIME_IRQ,         // 硬中断 CPU 时间
    CPUTIME_IDLE,        // 空闲 CPU 时间
    CPUTIME_IOWAIT,      // IO 等待 CPU 时间
    CPUTIME_STEAL,       // 被窃取的 CPU 时间
    CPUTIME_GUEST,       // 客户机虚拟机的 CPU 时间
    CPUTIME_GUEST_NICE,  // 低优先级客户机虚拟机的 CPU 时间
#ifdef CONFIG_SCHED_CORE
    CPUTIME_FORCEIDLE,   // 强制空闲 CPU 时间
#endif
    NR_STATS,            // CPU 统计项的数量
};

struct kernel_cpustat {
    u64 cpustat[NR_STATS]; // 每个 CPU 统计项的计数
};

struct kernel_stat {
	unsigned long irqs_sum;
	unsigned int softirqs[NR_SOFTIRQS];
};

DECLARE_PER_CPU(struct kernel_stat, kstat);
DECLARE_PER_CPU(struct kernel_cpustat, kernel_cpustat);

/* Must have preemption disabled for this to be meaningful. */
#define kstat_this_cpu this_cpu_ptr(&kstat)
#define kcpustat_this_cpu this_cpu_ptr(&kernel_cpustat)
#define kstat_cpu(cpu) per_cpu(kstat, cpu)
#define kcpustat_cpu(cpu) per_cpu(kernel_cpustat, cpu)

extern unsigned long long nr_context_switches_cpu(int cpu);
extern unsigned long long nr_context_switches(void);

extern unsigned int kstat_irqs_cpu(unsigned int irq, int cpu);
extern void kstat_incr_irq_this_cpu(unsigned int irq);

static inline void kstat_incr_softirqs_this_cpu(unsigned int irq)
{
	__this_cpu_inc(kstat.softirqs[irq]);
}

static inline unsigned int kstat_softirqs_cpu(unsigned int irq, int cpu)
{
       return kstat_cpu(cpu).softirqs[irq];
}

static inline unsigned int kstat_cpu_softirqs_sum(int cpu)
{
	int i;
	unsigned int sum = 0;

	for (i = 0; i < NR_SOFTIRQS; i++)
		sum += kstat_softirqs_cpu(i, cpu);

	return sum;
}

#ifdef CONFIG_GENERIC_IRQ_STAT_SNAPSHOT
extern void kstat_snapshot_irqs(void);
extern unsigned int kstat_get_irq_since_snapshot(unsigned int irq);
#else
static inline void kstat_snapshot_irqs(void) { }
static inline unsigned int kstat_get_irq_since_snapshot(unsigned int irq) { return 0; }
#endif

/*
 * Number of interrupts per specific IRQ source, since bootup
 */
extern unsigned int kstat_irqs_usr(unsigned int irq);

/*
 * Number of interrupts per cpu, since bootup
 */
static inline unsigned long kstat_cpu_irqs_sum(unsigned int cpu)
{
	return kstat_cpu(cpu).irqs_sum;
}

#ifdef CONFIG_VIRT_CPU_ACCOUNTING_GEN
extern u64 kcpustat_field(struct kernel_cpustat *kcpustat,
			  enum cpu_usage_stat usage, int cpu);
extern void kcpustat_cpu_fetch(struct kernel_cpustat *dst, int cpu);
#else
static inline u64 kcpustat_field(struct kernel_cpustat *kcpustat,
				 enum cpu_usage_stat usage, int cpu)
{
	return kcpustat->cpustat[usage];
}

static inline void kcpustat_cpu_fetch(struct kernel_cpustat *dst, int cpu)
{
	*dst = kcpustat_cpu(cpu);
}

#endif

extern void account_user_time(struct task_struct *, u64);
extern void account_guest_time(struct task_struct *, u64);
extern void account_system_time(struct task_struct *, int, u64);
extern void account_system_index_time(struct task_struct *, u64,
				      enum cpu_usage_stat);
extern void account_steal_time(u64);
extern void account_idle_time(u64);
extern u64 get_idle_time(struct kernel_cpustat *kcs, int cpu);

#ifdef CONFIG_VIRT_CPU_ACCOUNTING_NATIVE
static inline void account_process_tick(struct task_struct *tsk, int user)
{
	vtime_flush(tsk);
}
#else
extern void account_process_tick(struct task_struct *, int user);
#endif

extern void account_idle_ticks(unsigned long ticks);

#ifdef CONFIG_SCHED_CORE
extern void __account_forceidle_time(struct task_struct *tsk, u64 delta);
#endif

#endif /* _LINUX_KERNEL_STAT_H */
