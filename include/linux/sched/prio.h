/* SPDX-License-Identifier: GPL-2.0 */
#ifndef _LINUX_SCHED_PRIO_H
#define _LINUX_SCHED_PRIO_H

#define MAX_NICE	19 // nice 值的最大值
#define MIN_NICE	-20 // nice 值的最小值
#define NICE_WIDTH	(MAX_NICE - MIN_NICE + 1) // nice 值的范围宽度

/*
 * Priority of a process goes from 0..MAX_PRIO-1, valid RT
 * priority is 0..MAX_RT_PRIO-1, and SCHED_NORMAL/SCHED_BATCH
 * tasks are in the range MAX_RT_PRIO..MAX_PRIO-1. Priority
 * values are inverted: lower p->prio value means higher priority.
 */
// 进程的优先级范围是 0..MAX_PRIO-1，有效的实时优先级是 0..MAX_RT_PRIO-1，
// SCHED_NORMAL/SCHED_BATCH 任务的优先级范围是 MAX_RT_PRIO..MAX_PRIO-1。
// 优先级值是反转的：较低的 p->prio 值表示较高的优先级。

#define MAX_RT_PRIO		100 // 实时优先级的最大值
#define MAX_DL_PRIO		0 // 调度截止时间优先级的最大值

#define MAX_PRIO		(MAX_RT_PRIO + NICE_WIDTH) // 最大优先级
#define DEFAULT_PRIO		(MAX_RT_PRIO + NICE_WIDTH / 2) // 默认优先级

/*
 * Convert user-nice values [ -20 ... 0 ... 19 ]
 * to static priority [ MAX_RT_PRIO..MAX_PRIO-1 ],
 * and back.
 */
// 将用户 nice 值 [ -20 ... 0 ... 19 ] 转换为静态优先级 [ MAX_RT_PRIO..MAX_PRIO-1 ]，反之亦然。
#define NICE_TO_PRIO(nice)	((nice) + DEFAULT_PRIO) // 将 nice 值转换为优先级
#define PRIO_TO_NICE(prio)	((prio) - DEFAULT_PRIO) // 将优先级转换为 nice 值

/*
 * Convert nice value [19,-20] to rlimit style value [1,40].
 */
static inline long nice_to_rlimit(long nice)
{
	return (MAX_NICE - nice + 1);
}

/*
 * Convert rlimit style value [1,40] to nice value [-20, 19].
 */
static inline long rlimit_to_nice(long prio)
{
	return (MAX_NICE - prio + 1);
}

#endif /* _LINUX_SCHED_PRIO_H */
