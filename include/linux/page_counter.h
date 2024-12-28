/* SPDX-License-Identifier: GPL-2.0 */
#ifndef _LINUX_PAGE_COUNTER_H
#define _LINUX_PAGE_COUNTER_H

#include <linux/atomic.h>
#include <linux/cache.h>
#include <linux/limits.h>
#include <asm/page.h>

struct page_counter {
    /*
     * Make sure 'usage' does not share cacheline with any other field. The
     * memcg->memory.usage is a hot member of struct mem_cgroup.
     */
    // 确保 'usage' 不与任何其他字段共享缓存行。memcg->memory.usage 是 struct mem_cgroup 的一个热点成员。
    atomic_long_t usage;
    CACHELINE_PADDING(_pad1_);

    /* effective memory.min and memory.min usage tracking */
    // 有效的 memory.min 和 memory.min 使用跟踪
    unsigned long emin;
    atomic_long_t min_usage;
    atomic_long_t children_min_usage;

    /* effective memory.low and memory.low usage tracking */
    // 有效的 memory.low 和 memory.low 使用跟踪
    unsigned long elow;
    atomic_long_t low_usage;
    atomic_long_t children_low_usage;

    unsigned long watermark;
    /* Latest cg2 reset watermark */
    // 最新的 cg2 重置水位线
    unsigned long local_watermark;
    unsigned long failcnt;

    /* Keep all the read most fields in a separete cacheline. */
    // 将所有读取最多的字段保存在单独的缓存行中。
    CACHELINE_PADDING(_pad2_);

    bool protection_support; // 保护支持
    unsigned long min; // 最小值
    unsigned long low; // 低值
    unsigned long high; // 高值
    unsigned long max; // 最大值
    struct page_counter *parent; // 父计数器
} ____cacheline_internodealigned_in_smp;

#if BITS_PER_LONG == 32
#define PAGE_COUNTER_MAX LONG_MAX
#else
#define PAGE_COUNTER_MAX (LONG_MAX / PAGE_SIZE)
#endif

/*
 * Protection is supported only for the first counter (with id 0).
 */
static inline void page_counter_init(struct page_counter *counter,
				     struct page_counter *parent,
				     bool protection_support)
{
	counter->usage = (atomic_long_t)ATOMIC_LONG_INIT(0);
	counter->max = PAGE_COUNTER_MAX;
	counter->parent = parent;
	counter->protection_support = protection_support;
}

static inline unsigned long page_counter_read(struct page_counter *counter)
{
	return atomic_long_read(&counter->usage);
}

void page_counter_cancel(struct page_counter *counter, unsigned long nr_pages);
void page_counter_charge(struct page_counter *counter, unsigned long nr_pages);
bool page_counter_try_charge(struct page_counter *counter,
			     unsigned long nr_pages,
			     struct page_counter **fail);
void page_counter_uncharge(struct page_counter *counter, unsigned long nr_pages);
void page_counter_set_min(struct page_counter *counter, unsigned long nr_pages);
void page_counter_set_low(struct page_counter *counter, unsigned long nr_pages);

static inline void page_counter_set_high(struct page_counter *counter,
					 unsigned long nr_pages)
{
	WRITE_ONCE(counter->high, nr_pages);
}

int page_counter_set_max(struct page_counter *counter, unsigned long nr_pages);
int page_counter_memparse(const char *buf, const char *max,
			  unsigned long *nr_pages);

static inline void page_counter_reset_watermark(struct page_counter *counter)
{
	unsigned long usage = page_counter_read(counter);

	/*
	 * Update local_watermark first, so it's always <= watermark
	 * (modulo CPU/compiler re-ordering)
	 */
	counter->local_watermark = usage;
	counter->watermark = usage;
}

#ifdef CONFIG_MEMCG
void page_counter_calculate_protection(struct page_counter *root,
				       struct page_counter *counter,
				       bool recursive_protection);
#else
static inline void page_counter_calculate_protection(struct page_counter *root,
						     struct page_counter *counter,
						     bool recursive_protection) {}
#endif

#endif /* _LINUX_PAGE_COUNTER_H */
