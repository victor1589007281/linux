/* SPDX-License-Identifier: GPL-2.0 */
#ifndef _LINUX_SCHED_LOADAVG_H
#define _LINUX_SCHED_LOADAVG_H

/*
 * These are the constant used to fake the fixed-point load-average
 * counting. Some notes:
 *  - 11 bit fractions expand to 22 bits by the multiplies: this gives
 *    a load-average precision of 10 bits integer + 11 bits fractional
 *  - if you want to count load-averages more often, you need more
 *    precision, or rounding will get you. With 2-second counting freq,
 *    the EXP_n values would be 1981, 2034 and 2043 if still using only
 *    11 bit fractions.
 */
extern unsigned long avenrun[];		/* Load averages */
extern void get_avenrun(unsigned long *loads, unsigned long offset, int shift);

// 精度的位数
#define FSHIFT		11		/* nr of bits of precision */
// 固定点数表示的 1.0
#define FIXED_1		(1<<FSHIFT)	/* 1.0 as fixed-point */
// 5 秒间隔
#define LOAD_FREQ	(5*HZ+1)	/* 5 sec intervals */
// 固定点数表示的 1 分钟负载指数
#define EXP_1		1884		/* 1/exp(5sec/1min) as fixed-point */
// 固定点数表示的 5 分钟负载指数
#define EXP_5		2014		/* 1/exp(5sec/5min) */
// 固定点数表示的 15 分钟负载指数
#define EXP_15		2037		/* 1/exp(5sec/15min) */

/*
 * a1 = a0 * e + a * (1 - e)
 */
/*
EWMA 加权移动平均值
https://zhuanlan.zhihu.com/p/29895933
*/
static inline unsigned long
calc_load(unsigned long load, unsigned long exp, unsigned long active)
{
    unsigned long newload;

    // 计算新的负载值
    newload = load * exp + active * (FIXED_1 - exp);
    if (active >= load)
        newload += FIXED_1-1;

    // 返回新的负载值
    return newload / FIXED_1;
}

extern unsigned long calc_load_n(unsigned long load, unsigned long exp,
				 unsigned long active, unsigned int n);

#define LOAD_INT(x) ((x) >> FSHIFT)
#define LOAD_FRAC(x) LOAD_INT(((x) & (FIXED_1-1)) * 100)

extern void calc_global_load(void);

#endif /* _LINUX_SCHED_LOADAVG_H */
