/* SPDX-License-Identifier: GPL-2.0 */
/*
 * descriptor table internals; you almost certainly want file.h instead.
 */

#ifndef __LINUX_FDTABLE_H
#define __LINUX_FDTABLE_H

#include <linux/posix_types.h>
#include <linux/compiler.h>
#include <linux/spinlock.h>
#include <linux/rcupdate.h>
#include <linux/nospec.h>
#include <linux/types.h>
#include <linux/init.h>
#include <linux/fs.h>

#include <linux/atomic.h>

/*
 * The default fd array needs to be at least BITS_PER_LONG,
 * as this is the granularity returned by copy_fdset().
 */
#define NR_OPEN_DEFAULT BITS_PER_LONG

struct fdtable {
    unsigned int max_fds; // 最大文件描述符数量
    struct file __rcu **fd;      /* current fd array */
    // 当前文件描述符数组
    unsigned long *close_on_exec; // close-on-exec 位图
    unsigned long *open_fds; // 打开文件描述符位图
    unsigned long *full_fds_bits; // 完整文件描述符位图
    struct rcu_head rcu; // RCU 头
};

/*
 * Open file table structure
 */
// 打开文件表结构
struct files_struct {
  /*
   * read mostly part
   */
  // 主要用于读取的部分
    atomic_t count; // 引用计数
    bool resize_in_progress; // 是否正在调整大小
    wait_queue_head_t resize_wait; // 调整大小等待队列

    struct fdtable __rcu *fdt; // 文件描述符表指针
    struct fdtable fdtab; // 文件描述符表
  /*
   * written part on a separate cache line in SMP
   */
  // 在 SMP 中写入部分位于单独的缓存行中
    spinlock_t file_lock ____cacheline_aligned_in_smp; // 自旋锁
    unsigned int next_fd; // 下一个可用的文件描述符
    unsigned long close_on_exec_init[1]; // close-on-exec 初始化位图
    unsigned long open_fds_init[1]; // 打开文件描述符初始化位图
    unsigned long full_fds_bits_init[1]; // 完整文件描述符位图初始化
    struct file __rcu * fd_array[NR_OPEN_DEFAULT]; // 文件指针数组
};

struct file_operations;
struct vfsmount;
struct dentry;

#define rcu_dereference_check_fdtable(files, fdtfd) \
	rcu_dereference_check((fdtfd), lockdep_is_held(&(files)->file_lock))

#define files_fdtable(files) \
	rcu_dereference_check_fdtable((files), (files)->fdt)

/*
 * The caller must ensure that fd table isn't shared or hold rcu or file lock
 */
static inline struct file *files_lookup_fd_raw(struct files_struct *files, unsigned int fd)
{
	struct fdtable *fdt = rcu_dereference_raw(files->fdt);
	unsigned long mask = array_index_mask_nospec(fd, fdt->max_fds);
	struct file *needs_masking;

	/*
	 * 'mask' is zero for an out-of-bounds fd, all ones for ok.
	 * 'fd&mask' is 'fd' for ok, or 0 for out of bounds.
	 *
	 * Accessing fdt->fd[0] is ok, but needs masking of the result.
	 */
	needs_masking = rcu_dereference_raw(fdt->fd[fd&mask]);
	return (struct file *)(mask & (unsigned long)needs_masking);
}

static inline struct file *files_lookup_fd_locked(struct files_struct *files, unsigned int fd)
{
	RCU_LOCKDEP_WARN(!lockdep_is_held(&files->file_lock),
			   "suspicious rcu_dereference_check() usage");
	return files_lookup_fd_raw(files, fd);
}

struct file *lookup_fdget_rcu(unsigned int fd);
struct file *task_lookup_fdget_rcu(struct task_struct *task, unsigned int fd);
struct file *task_lookup_next_fdget_rcu(struct task_struct *task, unsigned int *fd);

static inline bool close_on_exec(unsigned int fd, const struct files_struct *files)
{
	return test_bit(fd, files_fdtable(files)->close_on_exec);
}

struct task_struct;

void put_files_struct(struct files_struct *fs);
int unshare_files(void);
struct fd_range {
	unsigned int from, to;
};
struct files_struct *dup_fd(struct files_struct *, struct fd_range *) __latent_entropy;
void do_close_on_exec(struct files_struct *);
int iterate_fd(struct files_struct *, unsigned,
		int (*)(const void *, struct file *, unsigned),
		const void *);

extern int close_fd(unsigned int fd);
extern int __close_range(unsigned int fd, unsigned int max_fd, unsigned int flags);
extern struct file *file_close_fd(unsigned int fd);

extern struct kmem_cache *files_cachep;

#endif /* __LINUX_FDTABLE_H */
