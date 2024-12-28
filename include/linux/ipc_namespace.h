/* SPDX-License-Identifier: GPL-2.0 */
#ifndef __IPC_NAMESPACE_H__
#define __IPC_NAMESPACE_H__

#include <linux/err.h>
#include <linux/idr.h>
#include <linux/rwsem.h>
#include <linux/notifier.h>
#include <linux/nsproxy.h>
#include <linux/ns_common.h>
#include <linux/refcount.h>
#include <linux/rhashtable-types.h>
#include <linux/sysctl.h>
#include <linux/percpu_counter.h>

struct user_namespace;

struct ipc_ids {
	int in_use;
	unsigned short seq;
	struct rw_semaphore rwsem;
	struct idr ipcs_idr;
	int max_idx;
	int last_idx;	/* For wrap around detection */
#ifdef CONFIG_CHECKPOINT_RESTORE
	int next_id;
#endif
	struct rhashtable key_ht;
};

//隔离System V IPC对象和POSIX消息队列
struct ipc_namespace {
    struct ipc_ids	ids[3]; // IPC 标识符数组

    int		sem_ctls[4]; // 信号量控制数组
    int		used_sems; // 使用的信号量数量

    unsigned int	msg_ctlmax; // 消息队列的最大消息数
    unsigned int	msg_ctlmnb; // 消息队列的最大字节数
    unsigned int	msg_ctlmni; // 最大消息队列数
    struct percpu_counter percpu_msg_bytes; // 每 CPU 消息字节计数器
    struct percpu_counter percpu_msg_hdrs; // 每 CPU 消息头计数器

    size_t		shm_ctlmax; // 共享内存的最大大小
    size_t		shm_ctlall; // 共享内存的总大小
    unsigned long	shm_tot; // 共享内存的总数
    int		shm_ctlmni; // 最大共享内存段数
    /*
     * Defines whether IPC_RMID is forced for _all_ shm segments regardless
     * of shmctl()
     */
    // 定义是否强制对所有共享内存段执行 IPC_RMID，而不考虑 shmctl()
    int		shm_rmid_forced;

    struct notifier_block ipcns_nb; // IPC 命名空间通知块

    /* The kern_mount of the mqueuefs sb.  We take a ref on it */
    // mqueuefs 超级块的内核挂载。我们对此引用。
    struct vfsmount	*mq_mnt;

    /* # queues in this ns, protected by mq_lock */
    // 此命名空间中的队列数，由 mq_lock 保护
    unsigned int    mq_queues_count;

    /* next fields are set through sysctl */
    // 下一个字段通过 sysctl 设置
    unsigned int    mq_queues_max;   /* initialized to DFLT_QUEUESMAX */
    // 最大队列数，初始化为 DFLT_QUEUESMAX
    unsigned int    mq_msg_max;      /* initialized to DFLT_MSGMAX */
    // 最大消息数，初始化为 DFLT_MSGMAX
    unsigned int    mq_msgsize_max;  /* initialized to DFLT_MSGSIZEMAX */
    // 最大消息大小，初始化为 DFLT_MSGSIZEMAX
    unsigned int    mq_msg_default; // 默认消息数
    unsigned int    mq_msgsize_default; // 默认消息大小

    struct ctl_table_set	mq_set; // 消息队列控制表集合
    struct ctl_table_header	*mq_sysctls; // 消息队列 sysctl 表头

    struct ctl_table_set	ipc_set; // IPC 控制表集合
    struct ctl_table_header	*ipc_sysctls; // IPC sysctl 表头

    /* user_ns which owns the ipc ns */
    // 拥有此 IPC 命名空间的用户命名空间
    struct user_namespace *user_ns;
    struct ucounts *ucounts; // 用户计数

    struct llist_node mnt_llist; // 挂载链表节点

    struct ns_common ns; // 命名空间公共部分
} __randomize_layout; // 随机化布局

extern struct ipc_namespace init_ipc_ns;
extern spinlock_t mq_lock;

#ifdef CONFIG_SYSVIPC
extern void shm_destroy_orphaned(struct ipc_namespace *ns);
#else /* CONFIG_SYSVIPC */
static inline void shm_destroy_orphaned(struct ipc_namespace *ns) {}
#endif /* CONFIG_SYSVIPC */

#ifdef CONFIG_POSIX_MQUEUE
extern int mq_init_ns(struct ipc_namespace *ns);
/*
 * POSIX Message Queue default values:
 *
 * MIN_*: Lowest value an admin can set the maximum unprivileged limit to
 * DFLT_*MAX: Default values for the maximum unprivileged limits
 * DFLT_{MSG,MSGSIZE}: Default values used when the user doesn't supply
 *   an attribute to the open call and the queue must be created
 * HARD_*: Highest value the maximums can be set to.  These are enforced
 *   on CAP_SYS_RESOURCE apps as well making them inviolate (so make them
 *   suitably high)
 *
 * POSIX Requirements:
 *   Per app minimum openable message queues - 8.  This does not map well
 *     to the fact that we limit the number of queues on a per namespace
 *     basis instead of a per app basis.  So, make the default high enough
 *     that no given app should have a hard time opening 8 queues.
 *   Minimum maximum for HARD_MSGMAX - 32767.  I bumped this to 65536.
 *   Minimum maximum for HARD_MSGSIZEMAX - POSIX is silent on this.  However,
 *     we have run into a situation where running applications in the wild
 *     require this to be at least 5MB, and preferably 10MB, so I set the
 *     value to 16MB in hopes that this user is the worst of the bunch and
 *     the new maximum will handle anyone else.  I may have to revisit this
 *     in the future.
 */
#define DFLT_QUEUESMAX		      256
#define MIN_MSGMAX			1
#define DFLT_MSG		       10U
#define DFLT_MSGMAX		       10
#define HARD_MSGMAX		    65536
#define MIN_MSGSIZEMAX		      128
#define DFLT_MSGSIZE		     8192U
#define DFLT_MSGSIZEMAX		     8192
#define HARD_MSGSIZEMAX	    (16*1024*1024)
#else
static inline int mq_init_ns(struct ipc_namespace *ns) { return 0; }
#endif

#if defined(CONFIG_IPC_NS)
extern struct ipc_namespace *copy_ipcs(unsigned long flags,
	struct user_namespace *user_ns, struct ipc_namespace *ns);

static inline struct ipc_namespace *get_ipc_ns(struct ipc_namespace *ns)
{
	if (ns)
		refcount_inc(&ns->ns.count);
	return ns;
}

static inline struct ipc_namespace *get_ipc_ns_not_zero(struct ipc_namespace *ns)
{
	if (ns) {
		if (refcount_inc_not_zero(&ns->ns.count))
			return ns;
	}

	return NULL;
}

extern void put_ipc_ns(struct ipc_namespace *ns);
#else
static inline struct ipc_namespace *copy_ipcs(unsigned long flags,
	struct user_namespace *user_ns, struct ipc_namespace *ns)
{
	if (flags & CLONE_NEWIPC)
		return ERR_PTR(-EINVAL);

	return ns;
}

static inline struct ipc_namespace *get_ipc_ns(struct ipc_namespace *ns)
{
	return ns;
}

static inline struct ipc_namespace *get_ipc_ns_not_zero(struct ipc_namespace *ns)
{
	return ns;
}

static inline void put_ipc_ns(struct ipc_namespace *ns)
{
}
#endif

#ifdef CONFIG_POSIX_MQUEUE_SYSCTL

void retire_mq_sysctls(struct ipc_namespace *ns);
bool setup_mq_sysctls(struct ipc_namespace *ns);

#else /* CONFIG_POSIX_MQUEUE_SYSCTL */

static inline void retire_mq_sysctls(struct ipc_namespace *ns)
{
}

static inline bool setup_mq_sysctls(struct ipc_namespace *ns)
{
	return true;
}

#endif /* CONFIG_POSIX_MQUEUE_SYSCTL */

#ifdef CONFIG_SYSVIPC_SYSCTL

bool setup_ipc_sysctls(struct ipc_namespace *ns);
void retire_ipc_sysctls(struct ipc_namespace *ns);

#else /* CONFIG_SYSVIPC_SYSCTL */

static inline void retire_ipc_sysctls(struct ipc_namespace *ns)
{
}

static inline bool setup_ipc_sysctls(struct ipc_namespace *ns)
{
	return true;
}

#endif /* CONFIG_SYSVIPC_SYSCTL */
#endif
