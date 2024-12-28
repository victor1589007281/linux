/* SPDX-License-Identifier: GPL-2.0 */
#ifndef _LINUX_FS_STRUCT_H
#define _LINUX_FS_STRUCT_H

#include <linux/path.h>
#include <linux/spinlock.h>
#include <linux/seqlock.h>

struct fs_struct {
    int users; // 用户数
    spinlock_t lock; // 自旋锁
    seqcount_spinlock_t seq; // 序列计数自旋锁
    int umask; // 文件创建掩码
    int in_exec; // 是否在执行中
    struct path root, pwd; // 根路径和当前工作目录路径
} __randomize_layout; // 随机化布局

extern struct kmem_cache *fs_cachep;

extern void exit_fs(struct task_struct *);
extern void set_fs_root(struct fs_struct *, const struct path *);
extern void set_fs_pwd(struct fs_struct *, const struct path *);
extern struct fs_struct *copy_fs_struct(struct fs_struct *);
extern void free_fs_struct(struct fs_struct *);
extern int unshare_fs_struct(void);

static inline void get_fs_root(struct fs_struct *fs, struct path *root)
{
	spin_lock(&fs->lock);
	*root = fs->root;
	path_get(root);
	spin_unlock(&fs->lock);
}

static inline void get_fs_pwd(struct fs_struct *fs, struct path *pwd)
{
	spin_lock(&fs->lock);
	*pwd = fs->pwd;
	path_get(pwd);
	spin_unlock(&fs->lock);
}

extern bool current_chrooted(void);

#endif /* _LINUX_FS_STRUCT_H */
