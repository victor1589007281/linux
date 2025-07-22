# Linux TCP协议实现原理与算法分析

## 目录

1. [概述](#概述)
2. [TCP协议栈架构](#tcp协议栈架构)
3. [TCP状态机](#tcp状态机)
4. [连接建立与关闭](#连接建立与关闭)
5. [数据传输机制](#数据传输机制)
6. [拥塞控制算法](#拥塞控制算法)
7. [流量控制](#流量控制)
8. [重传与超时](#重传与超时)
9. [TCP选项与扩展](#tcp选项与扩展)
10. [性能优化策略](#性能优化策略)
11. [核心数据结构](#核心数据结构)
12. [优点与局限性](#优点与局限性)
13. [总结](#总结)

## 概述

TCP (Transmission Control Protocol)是Internet协议栈中最重要的传输层协议之一，提供可靠的、面向连接的数据传输服务。Linux内核中的TCP实现是世界上最成熟和高性能的TCP协议栈之一，经过数十年的持续优化和改进。

### 核心设计目标

1. **可靠传输**：通过确认、重传机制确保数据无损传输
2. **流量控制**：防止发送方发送速度超过接收方处理能力
3. **拥塞控制**：避免网络拥塞，确保网络稳定性
4. **连接管理**：提供面向连接的服务，管理连接生命周期
5. **性能优化**：在保证可靠性的前提下最大化吞吐量

### TCP协议特性

- **面向连接**：通信前需要建立连接，通信后需要释放连接
- **全双工通信**：连接的双方可以同时发送和接收数据
- **可靠传输**：使用确认机制、重传机制等保证数据可靠传输
- **流控制**：根据接收方的处理能力调整发送速度
- **拥塞控制**：根据网络状况调整发送速度

## TCP协议栈架构

Linux TCP协议栈采用分层设计，与socket接口、IP层紧密集成。

### 协议栈层次结构

```c
// TCP协议栈架构图
/*
 * Linux TCP协议栈结构:
 * 
 * ┌─────────────────────────────────────┐
 * │           应用层                     │
 * │    Socket API (read/write/send)     │
 * └─────────────┬───────────────────────┘
 *               │ 系统调用接口
 * ┌─────────────▼───────────────────────┐
 * │           Socket层                  │
 * │  struct sock, socket缓冲区管理       │
 * └─────────────┬───────────────────────┘
 *               │
 * ┌─────────────▼───────────────────────┐
 * │           TCP层                     │
 * │  ┌─────────────────────────────────┐ │
 * │  │      连接管理                    │ │
 * │  │   (状态机、三次握手)              │ │
 * │  └─────────────────────────────────┘ │
 * │  ┌─────────────────────────────────┐ │
 * │  │      数据传输                    │ │
 * │  │   (分段、重组、确认)              │ │
 * │  └─────────────────────────────────┘ │
 * │  ┌─────────────────────────────────┐ │
 * │  │      拥塞控制                    │ │
 * │  │   (慢启动、拥塞避免)              │ │
 * │  └─────────────────────────────────┘ │
 * │  ┌─────────────────────────────────┐ │
 * │  │      定时器管理                  │ │
 * │  │   (重传、保活、TIME_WAIT)         │ │
 * │  └─────────────────────────────────┘ │
 * └─────────────┬───────────────────────┘
 *               │
 * ┌─────────────▼───────────────────────┐
 * │           IP层                      │
 * │    路由、分片、IPv4/IPv6             │
 * └─────────────┬───────────────────────┘
 *               │
 * ┌─────────────▼───────────────────────┐
 * │        数据链路层                    │
 * │      以太网、设备驱动                │
 * └─────────────────────────────────────┘
 */

// TCP控制块结构 - include/linux/tcp.h
struct tcp_sock {
    struct inet_connection_sock inet_conn; // 连接套接字
    
    // TCP特有字段
    u16 tcp_header_len;                   // TCP头长度
    u16 gso_segs;                         // GSO段数
    
    // 序列号空间
    u32 segs_in;                          // 接收段数
    u32 data_segs_in;                     // 接收数据段数
    u32 segs_out;                         // 发送段数
    u32 data_segs_out;                    // 发送数据段数
    u64 bytes_received;                   // 接收字节数
    u64 bytes_acked;                      // 确认字节数
    
    // 序列号
    u32 rcv_nxt;                          // 下一个期望接收的序列号
    u32 copied_seq;                       // 已拷贝到用户空间的序列号
    u32 rcv_wup;                          // 窗口更新的序列号
    u32 snd_nxt;                          // 下一个要发送的序列号
    u32 snd_una;                          // 未确认的序列号
    u32 snd_sml;                          // 最后小包的序列号
    u32 rcv_tstamp;                       // 接收时间戳
    u32 lsndtime;                         // 最后发送时间
    
    // 窗口
    u32 snd_wl1;                          // 发送窗口更新的序列号
    u32 snd_wnd;                          // 发送窗口大小
    u32 max_window;                       // 最大窗口大小
    u32 mss_cache;                        // MSS缓存
    
    // 拥塞控制
    u32 snd_ssthresh;                     // 慢启动阈值
    u32 snd_cwnd;                         // 拥塞窗口
    u32 snd_cwnd_cnt;                     // 拥塞窗口计数
    u32 snd_cwnd_clamp;                   // 拥塞窗口上限
    u32 snd_cwnd_used;                    // 已使用拥塞窗口
    u32 snd_cwnd_stamp;                   // 拥塞窗口时间戳
    u32 prior_cwnd;                       // 之前的拥塞窗口
    u32 prr_delivered;                    // PRR已交付
    u32 prr_out;                          // PRR输出
    u32 delivered;                        // 已交付包数
    u32 delivered_ce;                     // 已交付CE包数
    u32 lost;                             // 丢失包数
    u32 app_limited;                      // 应用受限标志
    u64 first_tx_mstamp;                  // 首次发送时间戳
    u64 delivered_mstamp;                 // 交付时间戳
    u32 rate_delivered;                   // 交付率
    u32 rate_interval_us;                 // 交付率间隔
    
    // 重传相关
    u32 retrans_stamp;                    // 重传时间戳
    u32 undo_marker;                      // 撤销标记
    int undo_retrans;                     // 撤销重传计数
    u32 total_retrans;                    // 总重传次数
    u32 bytes_retrans;                    // 重传字节数
    
    // 快重传相关
    u32 fackets_out;                      // 前向确认包数
    u32 high_seq;                         // 进入快恢复时的最高序列号
    u32 retrans_out;                      // 重传包数
    u8 do_early_retrans:1;                // 是否早期重传
    u8 early_retrans_delayed:1;           // 早期重传延迟
    
    // SACK相关
    u8 sacked_out;                        // SACK标记的包数
    u8 fackets_out;                       // 前向确认包数
    struct tcp_sack_block duplicate_sack[1]; // 重复SACK块
    struct tcp_sack_block selective_acks[4]; // 选择性确认块
    
    // RTT估算
    u32 srtt_us;                          // 平滑RTT(微秒)
    u32 mdev_us;                          // RTT均方差(微秒)
    u32 mdev_max_us;                      // RTT最大均方差
    u32 rttvar_us;                        // RTT变化量
    u32 rtt_seq;                          // RTT测量序列号
    struct  minmax rtt_min;               // 最小RTT
    
    // 定时器
    u32 packets_out;                      // 发出未确认的包数
    u32 retrans_out;                      // 重传包数
    u16 advmss;                           // 通告MSS
    u8 unused;                            // 未使用
    u8 nonagle;                           // Nagle算法控制
    u8 thin_lto:1;                        // 瘦流线性超时
    u8 recvmsg_inq:1;                     // 接收消息查询
    u8 repair:1;                          // 修复模式
    u8 frto:1;                            // F-RTO标志
    u8 repair_queue;                      // 修复队列
    u8 syn_data:1;                        // SYN数据
    u8 syn_fastopen:1;                    // SYN快速打开
    u8 syn_fastopen_exp:1;                // SYN快速打开实验
    u8 syn_data_acked:1;                  // SYN数据已确认
    u8 save_syn:1;                        // 保存SYN
    u8 is_cwnd_limited:1;                 // 拥塞窗口受限
    
    // 快速打开
    struct tcp_fastopen_request *fastopen_req; // 快速打开请求
    struct request_sock *fastopen_rsk;    // 快速打开请求套接字
    
    u32 *saved_syn;                       // 保存的SYN
};
```

### TCP处理流程

```c
// TCP数据包接收处理主流程 - net/ipv4/tcp_ipv4.c
int tcp_v4_rcv(struct sk_buff *skb)
{
    struct net *net = dev_net(skb->dev);
    struct tcphdr *th;
    const struct iphdr *iph;
    struct sock *sk;
    int ret;
    u32 isn;

    // 检查TCP头部
    if (skb->pkt_type != PACKET_HOST)
        goto discard_it;

    // 获取TCP头
    th = tcp_hdr(skb);

    if (th->doff < sizeof(struct tcphdr) / 4)
        goto bad_packet;

    if (!pskb_may_pull(skb, th->doff * 4))
        goto discard_it;

    // 计算校验和
    th = tcp_hdr(skb);
    iph = ip_hdr(skb);
    
    TCP_SKB_CB(skb)->seq = ntohl(th->seq);
    TCP_SKB_CB(skb)->end_seq = (TCP_SKB_CB(skb)->seq + th->syn + th->fin +
                               skb->len - th->doff * 4);
    TCP_SKB_CB(skb)->ack_seq = ntohl(th->ack_seq);
    TCP_SKB_CB(skb)->tcp_flags = tcp_flag_byte(th);
    TCP_SKB_CB(skb)->tcp_tw_isn = 0;
    TCP_SKB_CB(skb)->ip_dsfield = ipv4_get_dsfield(iph);
    TCP_SKB_CB(skb)->sacked = 0;
    TCP_SKB_CB(skb)->has_rxtstamp = skb->tstamp ? 1 : 0;

    // 查找对应的socket
lookup:
    sk = __inet_lookup_skb(&tcp_hashinfo, skb, __tcp_hdrlen(th), th->source,
                          th->dest, inet_iif(skb), &refcounted);
    if (!sk)
        goto no_tcp_socket;

process:
    if (sk->sk_state == TCP_TIME_WAIT)
        goto do_time_wait;

    if (sk->sk_state == TCP_NEW_SYN_RECV) {
        struct request_sock *req = inet_reqsk(sk);
        bool req_stolen = false;
        struct sock *nsk;

        sk = req->rsk_listener;
        if (unlikely(tcp_v4_inbound_md5_hash(sk, skb, dif, sdif))) {
            sk_drops_add(sk, skb);
            reqsk_put(req);
            goto discard_it;
        }
        if (tcp_checksum_complete(skb)) {
            reqsk_put(req);
            goto csum_error;
        }
        if (unlikely(sk->sk_state != TCP_LISTEN)) {
            nsk = reuseport_migrate_sock(sk, req_to_sk(req), skb);
            if (!nsk) {
                inet_csk_reqsk_queue_drop_and_put(sk, req);
                goto lookup;
            }
            sk = nsk;
            /* reuseport_migrate_sock() has already held one sk_refcnt
             * before returning.
             */
        } else {
            /* We own a reference on the listener, increase it again
             * as we might lose it too soon.
             */
            sock_hold(sk);
        }
        refcounted = true;
        nsk = NULL;
        if (!tcp_filter(sk, skb)) {
            th = (const struct tcphdr *)skb->data;
            iph = ip_hdr(skb);
            tcp_v4_fill_cb(skb, iph, th);
            nsk = tcp_check_req(sk, skb, req, false, &req_stolen);
        }
        if (!nsk) {
            reqsk_put(req);
            if (req_stolen) {
                /* Another cpu got exclusive access to req
                 * and created a full blown socket.
                 * Try to feed this packet to this socket
                 * instead of discarding it.
                 */
                tcp_v4_restore_cb(skb);
                sock_put(sk);
                goto lookup;
            }
            goto discard_and_relse;
        }
        nf_reset_ct(skb);
        if (nsk == sk) {
            reqsk_put(req);
            tcp_v4_restore_cb(skb);
        } else if (tcp_child_process(sk, nsk, skb)) {
            tcp_v4_send_reset(nsk, skb);
            goto discard_and_relse;
        } else {
            sock_put(sk);
            return 0;
        }
    }

    if (static_branch_unlikely(&ip4_min_ttl) &&
        unlikely(iph->ttl < net->ipv4.sysctl_ip_min_ttl)) {
        __NET_INC_STATS(net, LINUX_MIB_TCPMINTTLDROP);
        goto discard_and_relse;
    }

    if (!xfrm4_policy_check(sk, XFRM_POLICY_IN, skb))
        goto discard_and_relse;

    if (tcp_v4_inbound_md5_hash(sk, skb, dif, sdif))
        goto discard_and_relse;

    nf_reset_ct(skb);

    if (tcp_filter(sk, skb))
        goto discard_and_relse;
    th = (const struct tcphdr *)skb->data;
    iph = ip_hdr(skb);
    tcp_v4_fill_cb(skb, iph, th);

    skb->dev = NULL;

    if (sk->sk_state == TCP_LISTEN) {
        ret = tcp_v4_do_rcv(sk, skb);
        goto put_and_return;
    }

    sk_incoming_cpu_update(sk);

    sock_rps_save_rxhash(sk, skb);

    ret = 0;
    if (!sock_owned_by_user(sk)) {
        skb_to_free = sk->sk_rx_skb_cache;
        sk->sk_rx_skb_cache = NULL;
        ret = tcp_v4_do_rcv(sk, skb);
    } else {
        if (tcp_add_backlog(sk, skb))
            goto discard_and_relse;
        skb_to_free = NULL;
    }

put_and_return:
    if (refcounted)
        sock_put(sk);
    if (skb_to_free)
        __kfree_skb(skb_to_free);

    return ret;

no_tcp_socket:
    if (!xfrm4_policy_check(NULL, XFRM_POLICY_IN, skb))
        goto discard_it;

    tcp_v4_fill_cb(skb, iph, th);

    if (tcp_checksum_complete(skb)) {
csum_error:
        __TCP_INC_STATS(net, TCP_MIB_CSUMERRORS);
bad_packet:
        __TCP_INC_STATS(net, TCP_MIB_INERRS);
    } else {
        tcp_v4_send_reset(NULL, skb);
    }

discard_it:
    /* Discard frame. */
    kfree_skb(skb);
    return 0;

discard_and_relse:
    sk_drops_add(sk, skb);
    if (refcounted)
        sock_put(sk);
    goto discard_it;

do_time_wait:
    if (!xfrm4_policy_check(NULL, XFRM_POLICY_IN, skb)) {
        inet_twsk_put(inet_twsk(sk));
        goto discard_it;
    }

    tcp_v4_fill_cb(skb, iph, th);

    if (tcp_checksum_complete(skb)) {
        inet_twsk_put(inet_twsk(sk));
        goto csum_error;
    }
    switch (tcp_timewait_state_process(inet_twsk(sk), skb, th)) {
    case TCP_TW_SYN: {
        struct sock *sk2 = inet_lookup_listener(dev_net(skb->dev),
                                               &tcp_hashinfo, skb,
                                               __tcp_hdrlen(th),
                                               iph->saddr, th->source,
                                               iph->daddr, th->dest,
                                               inet_iif(skb),
                                               sdif);
        if (sk2) {
            inet_twsk_deschedule_put(inet_twsk(sk));
            sk = sk2;
            tcp_v4_restore_cb(skb);
            refcounted = false;
            goto process;
        }
    }
    /* to ACK */
    fallthrough;
    case TCP_TW_ACK:
        tcp_v4_timewait_ack(sk, skb);
        break;
    case TCP_TW_RST:
        tcp_v4_send_reset(sk, skb);
        inet_twsk_deschedule_put(inet_twsk(sk));
        goto discard_it;
    case TCP_TW_SUCCESS:
        ;
    }
    goto discard_it;
}
```

## TCP状态机

TCP协议使用有限状态机来管理连接的生命周期，包含11个状态和相应的状态转换。

### TCP状态定义

```c
// TCP状态定义 - include/net/tcp_states.h
enum {
    TCP_ESTABLISHED = 1,    // 已建立连接
    TCP_SYN_SENT,          // 已发送SYN，等待对方SYN
    TCP_SYN_RECV,          // 已接收SYN，已发送SYN+ACK
    TCP_FIN_WAIT1,         // 已发送FIN，等待对方ACK
    TCP_FIN_WAIT2,         // 收到对方对FIN的ACK，等待对方FIN
    TCP_TIME_WAIT,         // 已收到对方FIN并发送ACK，等待2MSL
    TCP_CLOSE,             // 连接关闭
    TCP_CLOSE_WAIT,        // 收到对方FIN，已发送ACK，等待本地close
    TCP_LAST_ACK,          // 已发送FIN，等待最后的ACK
    TCP_LISTEN,            // 监听连接请求
    TCP_CLOSING,           // 同时发起关闭
    TCP_NEW_SYN_RECV,      // 新的SYN_RECV状态(快速打开)
    
    TCP_MAX_STATES         // 状态数量上限
};

// TCP状态转换表
static const unsigned char tcp_state_transition[TCP_MAX_STATES][TCP_MAX_STATES] = {
    /* from-to:  ESTABLISHED, SYN_SENT, SYN_RECV, FIN_WAIT1, FIN_WAIT2,
     *           TIME_WAIT,   CLOSE,     CLOSE_WAIT, LAST_ACK, LISTEN,
     *           CLOSING,     NEW_SYN_RECV
     */
    [TCP_ESTABLISHED] = {
        [TCP_ESTABLISHED] = TCP_ESTABLISHED,
        [TCP_FIN_WAIT1]   = TCP_FIN_WAIT1,
        [TCP_CLOSE_WAIT]  = TCP_CLOSE_WAIT,
        [TCP_CLOSE]       = TCP_CLOSE,
    },
    [TCP_SYN_SENT] = {
        [TCP_ESTABLISHED] = TCP_ESTABLISHED,
        [TCP_SYN_RECV]    = TCP_SYN_RECV,
        [TCP_CLOSE]       = TCP_CLOSE,
    },
    [TCP_SYN_RECV] = {
        [TCP_ESTABLISHED] = TCP_ESTABLISHED,
        [TCP_FIN_WAIT1]   = TCP_FIN_WAIT1,
        [TCP_CLOSE]       = TCP_CLOSE,
    },
    [TCP_FIN_WAIT1] = {
        [TCP_FIN_WAIT2]   = TCP_FIN_WAIT2,
        [TCP_TIME_WAIT]   = TCP_TIME_WAIT,
        [TCP_CLOSING]     = TCP_CLOSING,
        [TCP_CLOSE]       = TCP_CLOSE,
    },
    [TCP_FIN_WAIT2] = {
        [TCP_TIME_WAIT]   = TCP_TIME_WAIT,
        [TCP_CLOSE]       = TCP_CLOSE,
    },
    [TCP_CLOSE_WAIT] = {
        [TCP_LAST_ACK]    = TCP_LAST_ACK,
        [TCP_CLOSE]       = TCP_CLOSE,
    },
    [TCP_LAST_ACK] = {
        [TCP_CLOSE]       = TCP_CLOSE,
    },
    [TCP_LISTEN] = {
        [TCP_SYN_RECV]    = TCP_SYN_RECV,
        [TCP_CLOSE]       = TCP_CLOSE,
    },
    [TCP_CLOSING] = {
        [TCP_TIME_WAIT]   = TCP_TIME_WAIT,
        [TCP_CLOSE]       = TCP_CLOSE,
    },
    [TCP_NEW_SYN_RECV] = {
        [TCP_ESTABLISHED] = TCP_ESTABLISHED,
        [TCP_CLOSE]       = TCP_CLOSE,
    },
};

// 状态名称字符串
static const char * const tcp_state_names[] = {
    [TCP_ESTABLISHED]  = "ESTABLISHED",
    [TCP_SYN_SENT]     = "SYN-SENT",
    [TCP_SYN_RECV]     = "SYN-RECV",
    [TCP_FIN_WAIT1]    = "FIN-WAIT-1",
    [TCP_FIN_WAIT2]    = "FIN-WAIT-2", 
    [TCP_TIME_WAIT]    = "TIME-WAIT",
    [TCP_CLOSE]        = "CLOSE",
    [TCP_CLOSE_WAIT]   = "CLOSE-WAIT",
    [TCP_LAST_ACK]     = "LAST-ACK",
    [TCP_LISTEN]       = "LISTEN",
    [TCP_CLOSING]      = "CLOSING",
    [TCP_NEW_SYN_RECV] = "NEW-SYN-RECV",
};
```

### 状态转换逻辑

```c
// TCP状态转换处理 - net/ipv4/tcp_input.c
void tcp_set_state(struct sock *sk, int state)
{
    int oldstate = sk->sk_state;

    // 状态转换跟踪
    trace_tcp_set_state(sk, oldstate, state);

    switch (state) {
    case TCP_ESTABLISHED:
        if (oldstate != TCP_ESTABLISHED)
            TCP_INC_STATS(sock_net(sk), TCP_MIB_CURRESTAB);
        break;

    case TCP_CLOSE:
        if (oldstate == TCP_CLOSE_WAIT || oldstate == TCP_ESTABLISHED)
            TCP_INC_STATS(sock_net(sk), TCP_MIB_ESTABRESETS);

        sk->sk_prot->unhash(sk);
        if (inet_csk(sk)->icsk_bind_hash &&
            !(sk->sk_userlocks & SOCK_BINDPORT_LOCK))
            inet_put_port(sk);
        /* fall through */
    default:
        if (oldstate == TCP_ESTABLISHED)
            TCP_DEC_STATS(sock_net(sk), TCP_MIB_CURRESTAB);
    }

    /* Change state AFTER socket is unhashed to avoid closed
     * socket sitting in hash tables.
     */
    inet_sk_state_store(sk, state);

#ifdef STATE_TRACE
    SOCK_DEBUG(sk, "TCP sk=%p, State %s -> %s\n", sk, 
               tcp_state_name[oldstate], tcp_state_name[state]);
#endif
}

// 接收到SYN包的处理
static int tcp_rcv_synsent_state_process(struct sock *sk, struct sk_buff *skb,
                                        const struct tcphdr *th)
{
    struct inet_connection_sock *icsk = inet_csk(sk);
    struct tcp_sock *tp = tcp_sk(sk);
    struct tcp_fastopen_cookie foc = { .len = -1 };
    int saved_clamp = tp->rx_opt.mss_clamp;
    bool fastopen_fail;

    tcp_parse_options(sock_net(sk), skb, &tp->rx_opt, 0, &foc);

    if (th->ack) {
        /* rfc793:
         * "If the state is SYN-SENT then
         *    first check the ACK bit
         *      If the ACK bit is set
         *        If SEG.ACK =< ISS or SEG.ACK > SND.NXT, send
         *        a reset (unless the RST bit is set, if so drop
         *        the segment and return)"
         */
        if (!after(TCP_SKB_CB(skb)->ack_seq, tp->snd_una) ||
            after(TCP_SKB_CB(skb)->ack_seq, tp->snd_nxt))
            goto reset_and_undo;

        if (tp->rx_opt.saw_tstamp && tp->rx_opt.rcv_tsecr &&
            !between(tp->rx_opt.rcv_tsecr, tp->retrans_stamp,
                     tcp_time_stamp(tp))) {
            NET_INC_STATS(sock_net(sk), LINUX_MIB_PAWSACTIVEREJECTED);
            goto reset_and_undo;
        }

        /* Now ACK is acceptable.
         *
         * "If the RST bit is set
         *    If the ACK was acceptable then signal the user "error:
         *    connection reset", drop the segment, enter CLOSED state,
         *    delete TCB, and return."
         */

        if (th->rst) {
            tcp_reset(sk);
            goto discard;
        }

        /* rfc793:
         *   "fifth, if neither of the SYN or RST bits is set then
         *    drop the segment and return."
         *
         *    See note below!
         *                                        --ANK(990513)
         */
        if (!th->syn)
            goto discard_and_undo;

        /* rfc793:
         *   "If the SYN bit is on ...
         *    are acceptable then ...
         *    (our SYN has been ACKed), change the connection
         *    state to ESTABLISHED..."
         */

        tcp_ecn_rcv_synack(tp, th);

        tcp_init_wl(tp, TCP_SKB_CB(skb)->seq);
        tcp_ack(sk, skb, FLAG_SLOWPATH);

        /* Ok.. it's good. Set up sequence numbers and
         * move to established.
         */
        tp->rcv_nxt = TCP_SKB_CB(skb)->seq + 1;
        tp->rcv_wup = TCP_SKB_CB(skb)->seq + 1;

        /* RFC1323: The window in SYN & SYN/ACK segments is
         * never scaled.
         */
        tp->snd_wnd = ntohs(th->window);

        if (!tp->rx_opt.wscale_ok) {
            tp->rx_opt.snd_wscale = tp->rx_opt.rcv_wscale = 0;
            tp->window_clamp = min(tp->window_clamp, 65535U);
        }

        if (tp->rx_opt.saw_tstamp) {
            tp->rx_opt.tstamp_ok       = 1;
            tp->tcp_header_len =
                sizeof(struct tcphdr) + TCPOLEN_TSTAMP_ALIGNED;
            tp->advmss             -= TCPOLEN_TSTAMP_ALIGNED;
            tcp_store_ts_recent(tp);
        } else {
            tp->tcp_header_len = sizeof(struct tcphdr);
        }

        tcp_sync_mss(sk, icsk->icsk_pmtu_cookie);
        tcp_initialize_rcv_mss(sk);

        /* Remember, tcp_poll() does not lock socket!
         * Change state from SYN-SENT only after copied_seq
         * is initialized. */
        tp->copied_seq = tp->rcv_nxt;

        smp_mb();

        tcp_finish_connect(sk, skb);

        fastopen_fail = (tp->syn_fastopen || tp->syn_data) &&
                       tcp_rcv_fastopen_synack(sk, skb, &foc);

        if (!fastopen_fail) {
            if (sk->sk_write_pending ||
                icsk->icsk_accept_queue.rskq_defer_accept ||
                icsk->icsk_ack.pingpong) {
                /* Save one ACK. Data will be ready after
                 * several ticks, if write_pending is set.
                 *
                 * It may be deleted, but with this feature tcpdumps
                 * look so _wonderfully_ clever, that I was not able
                 * to stand against the temptation 8)     --ANK
                 */
                inet_csk_schedule_ack(sk);
                tcp_enter_quickack_mode(sk, TCP_MAX_QUICKACKS);
                inet_csk_reset_xmit_timer(sk, ICSK_TIME_DACK,
                                         TCP_DELACK_MAX, TCP_RTO_MAX);

discard:
                tcp_drop(sk, skb);
                return 0;
            } else {
                tcp_send_ack(sk);
            }
            return -1;
        }

        /* An initial SYN ACK */
        if (fastopen_fail) {
            tcp_fastopen_undo_data(sk, skb);
            tcp_fastopen_cache_set(sk, tp->rx_opt.mss_clamp, &foc, false);
        }

        if (tcp_is_sack(tp) && sock_net(sk)->ipv4.sysctl_tcp_fack)
            tcp_enable_fack(tp);

        goto discard;
    }

    /* SYNSENT state receives SYN packet without ACK */
    if (th->syn) {
        tcp_ecn_rcv_syn(tp, th);

        tcp_mtup_init(sk);
        tcp_sync_mss(sk, icsk->icsk_pmtu_cookie);
        tcp_initialize_rcv_mss(sk);

        tcp_send_synack(sk);
#if 0
        /* Note, we could accept data and URG from this segment.
         * There are no obstacles to make this (except that we must
         * either change tcp_recvmsg() to prevent it from returning data
         * before 3WHS completes per RFC793, or employ TCP Fast Open).
         *
         * However, if we ignore data in ACKless segments sometimes,
         * we have no reasons to accept it sometimes.
         * Also, seems the code doing it in step6 of tcp_rcv_state_process
         * is not flawless. So, discard packet for sanity.
         * Uncomment this return to process the data.
         */
        return -1;
#else
        goto discard;
#endif
    }
    /* "fifth, if neither of the SYN or RST bits is set then
     * drop the segment and return."
     */

discard_and_undo:
    tcp_clear_options(&tp->rx_opt);
    tp->rx_opt.mss_clamp = saved_clamp;
    goto discard;

reset_and_undo:
    tcp_clear_options(&tp->rx_opt);
    tp->rx_opt.mss_clamp = saved_clamp;
    return 1;
}
```

## 连接建立与关闭

TCP使用三次握手建立连接，四次挥手关闭连接，确保双方都能够正确地建立和释放连接。

### 三次握手过程

```c
// 客户端发起连接 - net/ipv4/tcp_output.c
int tcp_connect(struct sock *sk)
{
    struct tcp_sock *tp = tcp_sk(sk);
    struct sk_buff *buff;
    int err;

    tcp_call_bpf(sk, BPF_SOCK_OPS_TCP_CONNECT_CB, 0, NULL);

    if (inet_csk(sk)->icsk_af_ops->rebuild_header(sk))
        return -EHOSTUNREACH; /* Routing failure or similar. */

    tcp_connect_init(sk);

    if (unlikely(tp->repair)) {
        tcp_finish_connect(sk, NULL);
        return 0;
    }

    buff = tcp_stream_alloc_skb(sk, 0, sk->sk_allocation, true);
    if (unlikely(!buff))
        return -ENOBUFS;

    tcp_init_nondata_skb(buff, tp->write_seq++, TCPHDR_SYN);
    tcp_mstamp_refresh(tp);
    tp->retrans_stamp = tcp_time_stamp(tp);
    tcp_connect_queue_skb(sk, buff);
    tcp_ecn_send_syn(sk, buff);
    tcp_rbtree_insert(&sk->tcp_rtx_queue, buff);

    /* Send off SYN; include data in Fast Open. */
    err = tp->fastopen_req ? tcp_send_syn_data(sk, buff) :
          tcp_transmit_skb(sk, buff, 1, sk->sk_allocation);
    if (err == -ECONNREFUSED)
        return err;

    /* We change tp->snd_nxt after the tcp_transmit_skb() call
     * in order to make this packet get counted in tcpOutSegs.
     */
    tp->snd_nxt = tp->write_seq;
    tp->pushed_seq = tp->write_seq;
    buff = tcp_send_head(sk);
    if (unlikely(buff)) {
        tp->snd_nxt	= TCP_SKB_CB(buff)->seq;
        tp->pushed_seq	= TCP_SKB_CB(buff)->seq;
    }
    TCP_INC_STATS(sock_net(sk), TCP_MIB_ACTIVEOPENS);

    /* Timer for retransmission of the first SYN */
    inet_csk_reset_xmit_timer(sk, ICSK_TIME_RETRANS,
                             inet_csk(sk)->icsk_rto, TCP_RTO_MAX);
    return 0;
}

// 服务器端监听和接受连接 - net/ipv4/tcp_ipv4.c
int tcp_v4_do_rcv(struct sock *sk, struct sk_buff *skb)
{
    struct sock *rsk;

    if (sk->sk_state == TCP_ESTABLISHED) { /* Fast path */
        struct dst_entry *dst = sk->sk_rx_dst;

        sock_rps_save_rxhash(sk, skb);
        sk_mark_napi_id(sk, skb);
        if (dst) {
            if (inet_sk(sk)->rx_dst_ifindex != skb->skb_iif ||
                !dst->ops->check(dst, 0)) {
                dst_release(dst);
                sk->sk_rx_dst = NULL;
            }
        }
        tcp_rcv_established(sk, skb);
        return 0;
    }

    if (tcp_checksum_complete(skb))
        goto csum_err;

    if (sk->sk_state == TCP_LISTEN) {
        struct sock *nsk = tcp_v4_cookie_check(sk, skb);

        if (!nsk)
            goto discard;
        if (nsk != sk) {
            if (tcp_child_process(sk, nsk, skb)) {
                rsk = nsk;
                goto reset;
            }
            return 0;
        }
    } else
        sock_rps_save_rxhash(sk, skb);

    if (tcp_rcv_state_process(sk, skb)) {
        rsk = sk;
        goto reset;
    }
    return 0;

reset:
    tcp_v4_send_reset(rsk, skb);
discard:
    kfree_skb(skb);
    /* Be careful here. If this function gets more complicated and
     * gcc suffers from register pressure on the x86, sk (in %ebx)
     * might be destroyed here. This current version compiles correctly,
     * but you have been warned.
     */
    return 0;

csum_err:
    TCP_INC_STATS(sock_net(sk), TCP_MIB_CSUMERRORS);
    TCP_INC_STATS(sock_net(sk), TCP_MIB_INERRS);
    goto discard;
}

// 处理SYN包，创建连接请求 - net/ipv4/tcp_input.c
struct sock *tcp_check_req(struct sock *sk, struct sk_buff *skb,
                          struct request_sock *req,
                          bool fastopen, bool *req_stolen)
{
    struct tcp_options_received tmp_opt;
    struct sock *child;
    const struct tcphdr *th = tcp_hdr(skb);
    __be32 flg = tcp_flag_word(th) & (TCP_FLAG_RST|TCP_FLAG_SYN|TCP_FLAG_ACK);
    bool paws_reject = false;
    bool own_req;

    tmp_opt.saw_tstamp = 0;
    if (th->doff > (sizeof(struct tcphdr)>>2)) {
        tcp_parse_options(sock_net(sk), skb, &tmp_opt, 0, NULL);

        if (tmp_opt.saw_tstamp) {
            tmp_opt.ts_recent = req->ts_recent;
            if (tmp_opt.rcv_tsecr)
                tmp_opt.rcv_tsecr -= tcp_rsk(req)->ts_off;
            /* We do not store true stamp, but it is not required,
             * it can be estimated (approximately)
             * from another data.
             */
            tmp_opt.ts_recent_stamp = ktime_get_seconds() - ((TCP_TIMEOUT_INIT/HZ)<<tcp_rsk(req)->retrans);
            paws_reject = tcp_paws_reject(&tmp_opt, th->rst);
        }
    }

    /* Check for pure retransmitted SYN. */
    if (TCP_SKB_CB(skb)->seq == tcp_rsk(req)->rcv_isn &&
        flg == TCP_FLAG_SYN &&
        !paws_reject) {
        /*
         * RFC793 draws (Incorrectly! It was fixed in RFC1122)
         * this case on figure 6 and figure 8, but formal
         * protocol description says NOTHING.
         * To be more exact, it says that we should send ACK,
         * because this segment (at least, if it has no data)
         * is out of window.
         *
         *  CONCLUSION: RFC793 (even with RFC1122) DOES NOT
         *  describe SYN-RECV state. All the description
         *  is wrong, we cannot believe to it and should
         *  rely only on common sense and implementation
         *  experience.
         *
         * Enforce "SYN-ACK" according to figure 8, figure 6
         * of RFC793, fixed by RFC1122.
         *
         * Note that even if there is new data in the SYN packet
         * they will be thrown away too.
         *
         * Reset timer after retransmitting SYNACK, similar to
         * the idea of fast retransmit in recovery.
         */
        if (!tcp_oow_rate_limited(sock_net(sk), skb,
                                 LINUX_MIB_TCPSYNCHALLENGE,
                                 &tcp_rsk(req)->last_oow_ack_time) &&

            !inet_rtx_syn_ack(sk, req)) {
            unsigned long expires = jiffies;

            expires += min(TCP_TIMEOUT_INIT << tcp_rsk(req)->retrans,
                          TCP_RTO_MAX);
            if (!fastopen)
                mod_timer_pending(&req->rsk_timer, expires);
            else
                req->rsk_timer.expires = expires;
        }

        return NULL;
    }

    /* Further reproduces section "SEGMENT ARRIVES"
       for state SYN-RECEIVED of RFC793.
       It is broken, however, it does not work only
       when SYNs are crossed.

       You would think that SYN crossing is impossible here, since
       we should have a SYN_SENT socket (from connect()) on our end,
       but this is not true if the crossed SYNs were sent to both
       ends by a malicious third party.  We must defend against this,
       and to do that we first verify the ACK (as per RFC793, page
       36) and reset if it is invalid.  Is this a true full defense?
       To convince ourselves, let us consider a way in which the ACK
       test can still pass in this 'malicious crossed SYNs' case.
       Malicious sender sends identical SYNs (and thus identical sequence
       numbers) to both A and B:

        A: gets SYN, sends SYN-ACK
        B: gets SYN, sends SYN-ACK
        A: gets SYN-ACK, sends ACK
        B: gets SYN-ACK, sends ACK

       By the time the ACK reaches each endpoint, the endpoints consider
       the other endpoint to be 'valid' as per the ACK test. But actually,
       these ACKs are identical and therefore not valid.

       Checking for crossed SYNs properly would require 3WHS termination.

       NOTE: Actually, we could implement a solution using timestamps, 
       since those are the same on packets sent by the same host.  But
       PAWS would still not help here, since PAWS does not help when
       sequence numbers are identical (timestamp mechanism only works
       if the sequence number is in the past).

       Actually, this whole business is a bit more nuanced than I first
       let on.  If we could guarantee that truly malicious double SYNs
       would always have crossed sequence numbers, then we could use the
       PAWS mechanism.  As a general rule, crossed SYNs will have
       identical sequence numbers.  Standard connect() implementations
       almost universally use struct timeval to seed the random number
       generator, so identical SYNs are very likely.  If we detect
       identical SYNs, we could set a flag to check timestamps more
       closely, but this might require a change to the PAWS algorithm.

       Actually, the reason for the crossed SYN case is that the Linux
       implementation of connect() picks a sequence number and then
       immediately sends a SYN.  If two machines do this in tandem, they
       pick the same sequence number (very likely due to the clock-based
       seed), and the result is that we see crossed SYNs with identical
       sequence numbers.

       Note that RFC793 only mentions crossed SYNs when two listen
       sockets are in play.  There's no discussion of the crossed SYNs
       that occur when a connect() is attempted from both ends.

       With crossed SYNs, we should note that the timestamp will NOT
       be the same on both SYNs (barring clock smearing) since sys_call
       time will likely differ, so timestamps might actually be useful
       here.  Of course, the question is whether the time difference
       would be reliably greater than the offset.

       One approach would be to explicitly check for this case when
       processing SYN in SYN_SENT state, and send a challenge ACK
       instead of entering SYN_RECV state.

       Actually, the challenge ACK approach might make sense here,
       since this case by definition means that the 4-tuple is already
       an established connection.
     */

    if (flg == (TCP_FLAG_ACK|TCP_FLAG_SYN)) {
        /* Invalid, unless it is retransmitted SYN/ACK.  */
        if (TCP_SKB_CB(skb)->seq != tcp_rsk(req)->rcv_isn + 1)
            return sk;
    } else if (flg == TCP_FLAG_ACK) {
        /* Invalid ACK */
        if (TCP_SKB_CB(skb)->ack_seq != tcp_rsk(req)->snt_isn + 1)
            return sk;

        /* OK, ACK is valid, create big socket and
         * feed this segment to it. It will repeat all
         * the tests. THIS SEGMENT MUST MOVE SOCKET TO
         * ESTABLISHED STATE. If it will be dropped after
         * socket is created, wait for troubles.
         */
        child = inet_csk(sk)->icsk_af_ops->syn_recv_sock(sk, skb, req, NULL,
                                                        req, &own_req);
        if (!child)
            goto listen_overflow;

        sock_rps_save_rxhash(child, skb);
        tcp_synack_rtt_meas(child, req);
        *req_stolen = !own_req;
        return inet_csk_complete_hashdance(sk, child, req, own_req);
    } else {
        /* Only RST or bare SYN left. */

        if (flg & TCP_FLAG_RST) {
            /* RFC793 page 70: "segment should not be
             * checked for old duplicate SYNs (page 71)
             * nor dropped for old duplicate ACKs (page 73)
             * because they will be RSTs."
             */
            goto embryonic_reset;
        }

        /* ACK bit is clear. If SYN is set, we have no RST. */
        if (!(flg & TCP_FLAG_SYN))
            return sk;

        /* Fragment overlaps with SYN. Duplicate SYN or our out-of-order SYN? */
        if (TCP_SKB_CB(skb)->seq != tcp_rsk(req)->rcv_isn)
            goto syn_challenge;

        /* From tcp_input.c:tcp_rcv_synsent_state_process() */
        TCP_ECN_rcv_syn(sk, th);

        tcp_mtup_init(child);
        tcp_sync_mss(child, tcp_mss_to_mtu(child, tcp_sk(child)->rx_opt.mss_clamp));
        tcp_initialize_rcv_mss(child);

        tcp_send_synack(child);
        /* note that the child socket is not yet in established state */
    }
    return NULL;

listen_overflow:
    if (!net->ipv4.sysctl_tcp_abort_on_overflow) {
        inet_rsk(req)->acked = 1;
        return NULL;
    }

embryonic_reset:
    if (!(flg & TCP_FLAG_RST)) {
        /* Received a bad SYN pkt.  */
        if (flg & TCP_FLAG_ACK)
            tcp_v4_send_synack(sk, NULL, &TCP_SKB_CB(skb)->header.h4.opt,
                              skb, 0, NULL, TCP_FLAG_RST, tcp_rsk(req)->ts_off);
        else
            tcp_v4_send_reset(sk, skb);
    } else if (fastopen) { /* received RST pkt */
        tcp_fastopen_active_disable(sk);
        tcp_fastopen_cache_set(sk, 0, NULL, true, 0);
    }
    return sk;

syn_challenge:
    if (syn_inerr)
        TCP_INC_STATS(sock_net(sk), LINUX_MIB_TCPSYNCHALLENGE);
    return sk;
}
```

## 数据传输机制

TCP通过可靠的数据传输机制确保数据的完整性和顺序性。

### 数据分段与重组

```c
// 数据发送分段 - net/ipv4/tcp_output.c
static int tcp_write_xmit(struct sock *sk, unsigned int mss_now, int nonagle,
                         int push_one, gfp_t gfp)
{
    struct tcp_sock *tp = tcp_sk(sk);
    struct sk_buff *skb;
    unsigned int tso_segs, sent_pkts;
    int cwnd_quota;
    int result;
    bool is_cwnd_limited = false, is_rwnd_limited = false;
    u32 max_segs;

    sent_pkts = 0;

    tcp_mstamp_refresh(tp);
    if (!push_one) {
        /* Do MTU probing. */
        result = tcp_mtu_probe(sk);
        if (!result) {
            return 0;
        } else if (result > 0) {
            sent_pkts = 1;
        }
    }

    max_segs = tcp_tso_segs(sk, mss_now);
    while ((skb = tcp_send_head(sk))) {
        unsigned int limit;

        if (unlikely(tp->repair) && tp->repair_queue == TCP_SEND_QUEUE) {
            /* "skb_mstamp_ns" is used as a start point for the retransmit timer */
            tcp_update_skb_after_send(sk, skb, tp->tcp_wstamp_ns);
            goto repair; /* Skip network transmission */
        }

        if (tcp_pacing_check(sk))
            break;

        tso_segs = tcp_init_tso_segs(skb, mss_now);
        BUG_ON(!tso_segs);

        cwnd_quota = tcp_cwnd_test(tp, skb);
        if (!cwnd_quota) {
            if (push_one == 2)
                /* Force out a loss probe pkt. */
                cwnd_quota = 1;
            else
                break;
        }

        if (unlikely(!tcp_snd_wnd_test(tp, skb, mss_now))) {
            is_rwnd_limited = true;
            break;
        }

        if (tso_segs == 1) {
            if (unlikely(!tcp_nagle_test(tp, skb, mss_now,
                                        (tcp_skb_is_last(sk, skb) ?
                                         nonagle : TCP_NAGLE_PUSH))))
                break;
        } else {
            if (!push_one &&
                tcp_tso_should_defer(sk, skb, &is_cwnd_limited,
                                   &is_rwnd_limited, max_segs))
                break;
        }

        limit = mss_now;
        if (tso_segs > 1 && !tcp_urg_mode(tp))
            limit = tcp_mss_split_point(sk, skb, mss_now,
                                       min_t(unsigned int,
                                             cwnd_quota,
                                             max_segs),
                                       nonagle);

        if (skb->len > limit &&
            unlikely(tso_fragment(sk, TCP_FRAG_IN_WRITE_QUEUE,
                                 skb, limit, mss_now, gfp)))
            break;

        if (test_bit(TCP_TSQ_DEFERRED, &sk->sk_tsq_flags))
            clear_bit(TCP_TSQ_DEFERRED, &sk->sk_tsq_flags);
        if (tcp_small_queue_check(sk, skb, 0))
            break;

        /* TCP Small Queues :
         * Control number of packets in qdisc/devices to fight bufferbloat.
         * This is done per-CPU to avoid contention on a global spinlock.
         * For each CPU, we try to maintain a target queue of ~2ms worth of data.
         */
        if (refcount_read(&sk->sk_wmem_alloc) >= sk->sk_sndbuf) {
            /* It is possible TX completion already happened
             * before we set TSQ_THROTTLED, so we must
             * test again the condition.
             */
            smp_mb__before_atomic();
            if (refcount_read(&sk->sk_wmem_alloc) >= sk->sk_sndbuf) {
                set_bit(TSQ_THROTTLED, &sk->sk_tsq_flags);
                /* It is possible TX completion already happened
                 * before we set TSQ_THROTTLED, so we must
                 * test again the condition.
                 */
                smp_mb__after_atomic();
                if (refcount_read(&sk->sk_wmem_alloc) < sk->sk_sndbuf)
                    tasklet_schedule(&per_cpu(tsq_tasklet, smp_processor_id()));
                break;
            }
        }

        if (unlikely(tcp_transmit_skb(sk, skb, 1, gfp)))
            break;

repair:
        /* Advance the send_head.  This one is sent out.
         * This call will increment packets_out.
         */
        tcp_event_new_data_sent(sk, skb);

        tcp_minshall_update(tp, mss_now, skb);
        sent_pkts += tcp_skb_pcount(skb);

        if (push_one)
            break;
    }

    if (is_rwnd_limited)
        tcp_chrono_start(sk, TCP_CHRONO_RWND_LIMITED);
    else
        tcp_chrono_stop(sk, TCP_CHRONO_RWND_LIMITED);

    if (is_cwnd_limited)
        tp->is_cwnd_limited = 1;
    else
        tp->is_cwnd_limited = 0;

    if (likely(sent_pkts || is_cwnd_limited))
        tcp_cwnd_validate(sk, is_cwnd_limited);

    if (likely(sent_pkts)) {
        if (tcp_in_cwnd_reduction(sk))
            tp->prr_out += sent_pkts;

        /* Send one loss probe per tail loss episode. */
        if (push_one != 2)
            tcp_schedule_loss_probe(sk, false);
        is_cwnd_limited |= (tcp_packets_in_flight(tp) >= tp->snd_cwnd);
        tcp_cwnd_validate(sk, is_cwnd_limited);
        return 0;
    }
    return !tp->packets_out && tcp_send_head(sk);
}

// 数据接收重组 - net/ipv4/tcp_input.c
static void tcp_data_queue(struct sock *sk, struct sk_buff *skb)
{
    struct tcp_sock *tp = tcp_sk(sk);
    bool fragstolen;
    int eaten;

    if (sk_is_mptcp(sk))
        mptcp_incoming_options(sk, skb);

    if (TCP_SKB_CB(skb)->seq == tp->rcv_nxt) {
        if (tcp_receive_window(tp) == 0) {
            NET_INC_STATS(sock_net(sk), LINUX_MIB_TCPZEROWINDOWDROP);
            goto out_of_window;
        }

        /* Ok. In sequence. In window. */
queue_and_out:
        if (tcp_try_rmem_schedule(sk, skb, skb->truesize)) {
            /* TODO: maybe ratelimit these WIN 0 ACK ? */
            inet_csk(sk)->icsk_ack.pending |= ICSK_ACK_NOW;
            inet_csk_schedule_ack(sk);
            sk->sk_data_ready(sk);

            if (skb_queue_len(&sk->sk_receive_queue) == 0)
                sk_forced_mem_schedule(sk, skb->truesize);
            else if (tcp_try_rmem_schedule(sk, skb, skb->truesize)) {
                NET_INC_STATS(sock_net(sk), LINUX_MIB_TCPRCVQDROP);
                sk_drops_add(sk, skb);
                __kfree_skb(skb);
                return;
            }
        }

        eaten = tcp_queue_rcv(sk, skb, &fragstolen);
        if (skb->len)
            tcp_event_data_recv(sk, skb);
        if (TCP_SKB_CB(skb)->fin)
            tcp_fin(sk);

        if (!RB_EMPTY_ROOT(&tp->out_of_order_queue)) {
            tcp_ofo_queue(sk);

            /* RFC5681. 4.2. SHOULD send immediate ACK, when
             * gap in queue is filled.
             */
            if (RB_EMPTY_ROOT(&tp->out_of_order_queue))
                inet_csk(sk)->icsk_ack.pending |= ICSK_ACK_NOW;
        }

        if (tp->rx_opt.num_sacks)
            tcp_sack_remove(tp);

        tcp_fast_path_check(sk);

        if (eaten > 0)
            kfree_skb_partial(skb, fragstolen);
        if (!sock_flag(sk, SOCK_DEAD))
            sk->sk_data_ready(sk);
        return;
    }

    if (!after(TCP_SKB_CB(skb)->end_seq, tp->rcv_nxt)) {
        tcp_rcv_spurious_retrans(sk, skb);
        /* A retransmit, 2nd most common case.  Force an immediate ack. */
        NET_INC_STATS(sock_net(sk), LINUX_MIB_DELAYEDACKLOST);
        tcp_dsack_set(sk, TCP_SKB_CB(skb)->seq, TCP_SKB_CB(skb)->end_seq);

out_of_window:
        tcp_enter_quickack_mode(sk, TCP_MAX_QUICKACKS);
        inet_csk_schedule_ack(sk);
drop:
        tcp_drop(sk, skb);
        return;
    }

    /* Out of window. F.e. zero window probe. */
    if (!before(TCP_SKB_CB(skb)->seq, tp->rcv_nxt + tcp_receive_window(tp)))
        goto out_of_window;

    if (before(TCP_SKB_CB(skb)->seq, tp->rcv_nxt)) {
        /* Partial packet, seq < rcv_next < end_seq */
        TCP_INC_STATS(sock_net(sk), TCP_MIB_INERRS);

        /* If window is closed, drop tail of packet. But after
         * remembering D-SACK for its head made in previous line.
         */
        if (!tcp_receive_window(tp)) {
            NET_INC_STATS(sock_net(sk), LINUX_MIB_TCPZEROWINDOWDROP);
            goto out_of_window;
        }
        goto queue_and_out;
    }

    tcp_data_queue_ofo(sk, skb);
}
```

## 拥塞控制算法

TCP拥塞控制是防止网络拥塞的关键机制，Linux实现了多种拥塞控制算法。

### CUBIC拥塞控制算法

```c
// CUBIC算法实现 - net/ipv4/tcp_cubic.c
struct bictcp {
    u32 cnt;                     // 拥塞窗口增长计数
    u32 last_max_cwnd;           // 最后最大拥塞窗口
    u32 last_cwnd;               // 最后拥塞窗口
    u32 last_time;               // 最后时间
    u32 bic_origin_point;        // 起始点
    u32 bic_K;                   // K值(时间到达W_max的时间)
    u32 delay_min;               // 最小延迟
    u32 epoch_start;             // 纪元开始时间
    u32 ack_cnt;                 // ACK计数
    u32 tcp_cwnd;                // TCP拥塞窗口
    u16 unused;
    u8 sample_cnt;               // 样本计数
    u8 found;                    // 找到标志
    u32 round_start;             // 轮次开始
    u32 end_seq;                 // 结束序列号
    u32 last_ack;                // 最后ACK
    u32 curr_rtt;                // 当前RTT
};

// CUBIC窗口增长函数
static inline void bictcp_update(struct bictcp *ca, u32 cwnd, u32 acked)
{
    u32 delta, bic_target, max_cnt;
    u64 offs, t;

    ca->ack_cnt += acked;    /* count the number of ACKed packets */

    if (ca->last_cwnd == cwnd &&
        (s32)(tcp_jiffies32 - ca->last_time) <= HZ / 32)
        return;

    /* The CUBIC function can update ca->cnt at most once per jiffy.
     * On all cwnd reduction events, ca->epoch_start is set to 0,
     * which will force a recalculation of ca->cnt.
     */
    if (ca->epoch_start && tcp_jiffies32 == ca->last_time)
        goto tcp_friendliness;

    ca->last_cwnd = cwnd;
    ca->last_time = tcp_jiffies32;

    if (ca->epoch_start == 0) {
        ca->epoch_start = tcp_jiffies32;       /* record beginning */
        ca->ack_cnt = acked;                   /* start counting */
        ca->tcp_cwnd = cwnd;                   /* syn with cubic */

        if (ca->last_max_cwnd <= cwnd) {
            ca->bic_K = 0;
            ca->bic_origin_point = cwnd;
        } else {
            /* Compute new K based on
             * (wmax-cwnd) * (srtt>>3 / HZ) / c * 2^(3*bictcp_HZ)
             */
            ca->bic_K = cubic_root(cube_factor
                                  * (ca->last_max_cwnd - cwnd));
            ca->bic_origin_point = ca->last_max_cwnd;
        }
    }

    /* cubic function - calc*/
    /* calculate c * time^3 / rtt,
     *  while considering overflow in calculation of time^3
     * (so time^3 is done by using 64 bit)
     * and without the support of division of 64bit numbers
     * (so all divisions are done by using 32 bit)
     *  also NOTE the unit of those veriables
     *        time  = (t - K) / 2^bictcp_HZ
     *        c = bic_scale >> 10
     * rtt  = (srtt >> 3) / HZ
     * !!! The following code does not have overflow problems,
     * if the cwnd < 1 million packets !!!
     */

    t = (s32)(tcp_jiffies32 - ca->epoch_start);
    t += msecs_to_jiffies(ca->delay_min >> 3);
    /* change the unit from HZ to bictcp_HZ */
    t <<= BICTCP_HZ;
    do_div(t, HZ);

    if (t < ca->bic_K)		/* t - K */
        offs = ca->bic_K - t;
    else
        offs = t - ca->bic_K;

    /* c/rtt * (t-K)^3 */
    delta = (cube_rtt_scale * offs * offs * offs) >> (10+3*BICTCP_HZ);
    if (t < ca->bic_K)                            /* below origin*/
        bic_target = ca->bic_origin_point - delta;
    else                                          /* above origin*/
        bic_target = ca->bic_origin_point + delta;

    /* cubic function - calc bictcp_cnt*/
    if (bic_target > cwnd) {
        ca->cnt = cwnd / (bic_target - cwnd);
    } else {
        ca->cnt = 100 * cwnd;              /* very small increment*/
    }

    /*
     * The initial growth of cubic function may be too conservative
     * when the available bandwidth is still unknown.
     */
    if (ca->last_max_cwnd == 0 && ca->cnt > 20)
        ca->cnt = 20;   /* increase cwnd 5% per RTT */

tcp_friendliness:
    /* TCP Friendly */
    if (tcp_friendliness) {
        u32 scale = beta_scale;

        delta = (cwnd * scale) >> 3;
        while (ca->ack_cnt > delta) {               /* update tcp cwnd */
            ca->ack_cnt -= delta;
            ca->tcp_cwnd++;
        }

        if (ca->tcp_cwnd > cwnd) {      /* if bic is slower than tcp */
            delta = ca->tcp_cwnd - cwnd;
            max_cnt = cwnd / delta;
            if (ca->cnt > max_cnt)
                ca->cnt = max_cnt;
        }
    }

    /* The maximum rate of cwnd increase CUBIC allows is 1 packet per
     * 2 packets ACKed, meaning cwnd grows at 1.5x per RTT.
     */
    ca->cnt = max(ca->cnt, 2U);
}

// CUBIC拥塞事件处理
static u32 bictcp_recalc_ssthresh(struct sock *sk)
{
    const struct tcp_sock *tp = tcp_sk(sk);
    struct bictcp *ca = inet_csk_ca(sk);

    ca->epoch_start = 0;        /* end of epoch */

    /* Wmax and fast convergence */
    if (tp->snd_cwnd < ca->last_max_cwnd && fast_convergence)
        ca->last_max_cwnd = (tp->snd_cwnd * (BICTCP_BETA_SCALE + beta))
                           / (2 * BICTCP_BETA_SCALE);
    else
        ca->last_max_cwnd = tp->snd_cwnd;

    return max((tp->snd_cwnd * beta) / BICTCP_BETA_SCALE, 2U);
}
```

### BBR拥塞控制算法

```c
// BBR算法核心结构 - net/ipv4/tcp_bbr.c
struct bbr {
    u32 min_rtt_us;             // 最小RTT
    u32 min_rtt_stamp;          // 最小RTT时间戳
    u32 probe_rtt_done_stamp;   // 探测RTT完成时间戳
    struct minmax bw;           // 带宽估计
    u32 rtt_cnt;               // RTT计数
    u32 next_rtt_delivered;    // 下次RTT交付
    u64 cycle_mstamp;          // 周期时间戳
    u32 mode:3,                // 模式
        prev_ca_state:3,       // 先前拥塞状态
        packet_conservation:1, // 包守恒
        round_start:1,         // 轮次开始
        idle_restart:1,        // 空闲重启
        probe_rtt_round_done:1,// 探测RTT轮次完成
        unused:13,
        lt_is_sampling:1,      // 长期取样
        lt_rtt_cnt:7,          // 长期RTT计数
        lt_use_bw:1;           // 长期使用带宽
    u32 lt_bw;                 // 长期带宽
    u32 lt_last_delivered;     // 长期最后交付
    u32 lt_last_stamp;         // 长期最后时间戳
    u32 lt_last_lost;          // 长期最后丢失
    u32 pacing_gain:10,        // 调步增益
        cwnd_gain:10,          // 拥塞窗口增益
        full_bw_reached:1,     // 达到满带宽
        full_bw_cnt:2,         // 满带宽计数
        cycle_idx:3,           // 周期索引
        has_seen_rtt:1,        // 已见RTT
        unused_b:5;
    u32 prior_cwnd;            // 先前拥塞窗口
    u32 full_bw;               // 满带宽
};

// BBR主要函数：更新模型和控制参数
static void bbr_main(struct sock *sk, const struct rate_sample *rs)
{
    struct bbr *bbr = inet_csk_ca(sk);
    u32 bw;

    bbr_update_model(sk, rs);

    bw = bbr_bw(sk);
    bbr_set_pacing_rate(sk, bw, bbr->pacing_gain);
    bbr_set_cwnd(sk, rs, rs->acked_sacked, bw, bbr->cwnd_gain);
}

// BBR带宽和RTT估计
static void bbr_update_bw(struct sock *sk, const struct rate_sample *rs)
{
    struct tcp_sock *tp = tcp_sk(sk);
    struct bbr *bbr = inet_csk_ca(sk);
    u64 bw;

    bbr->round_start = 0;
    if (rs->delivered < 0 || rs->interval_us <= 0)
        return; /* Not a valid observation */

    /* See if we've reached the next RTT */
    if (!before(rs->prior_delivered, bbr->next_rtt_delivered)) {
        bbr->next_rtt_delivered = tp->delivered;
        bbr->rtt_cnt++;
        bbr->round_start = 1;
        bbr->packet_conservation = 0;
    }

    /* Divide delivered by the interval to find a (lower bound) bottleneck
     * bandwidth sample. Delivered is in packets and interval_us in uS and
     * ratio will be <<1 for most connections. So delivered is first scaled.
     */
    bw = div64_long((u64)rs->delivered * BW_UNIT, rs->interval_us);

    /* If this sample is application-limited, it is likely to have a very
     * low delivered count that represents application behavior rather than
     * the available network rate. Such a sample could drag down estimated
     * bw, causing needless slow-down. Thus, to continue to send at the
     * last measured network rate, we filter out app-limited samples unless
     * they describe the path bw at least as well as our bw model.
     *
     * So the goal during app-limited phase is to proceed with the best
     * network rate no matter how long. We automatically leave this
     * phase when app writes faster than the network can deliver :)
     */
    if (!rs->is_app_limited || bw >= bbr_max_bw(sk)) {
        /* Incorporate new sample into our max bw filter. */
        minmax_running_max(&bbr->bw, bbr_bw_rtts, bbr->rtt_cnt, bw);
    }
}

static void bbr_update_min_rtt(struct sock *sk, const struct rate_sample *rs)
{
    struct bbr *bbr = inet_csk_ca(sk);
    bool filter_expired;

    /* Track min RTT seen in the min_rtt_win_sec filter window: */
    filter_expired = after(tcp_jiffies32,
                          bbr->min_rtt_stamp + bbr_min_rtt_win_sec * HZ);
    if (rs->rtt_us >= 0 &&
        (rs->rtt_us < bbr->min_rtt_us ||
         (filter_expired && !rs->is_ack_delayed))) {
        bbr->min_rtt_us = rs->rtt_us;
        bbr->min_rtt_stamp = tcp_jiffies32;
    }

    if (bbr_probe_rtt_mode_ms > 0 && filter_expired &&
        !bbr->idle_restart && bbr->mode != BBR_PROBE_RTT) {
        bbr->mode = BBR_PROBE_RTT;
        bbr_save_cwnd(sk);
        bbr->probe_rtt_done_stamp = 0;
    }

    if (bbr->mode == BBR_PROBE_RTT) {
        /* Ignore low rate samples during this mode. */
        tp->app_limited =
            (tp->delivered + tcp_packets_in_flight(tp)) ? : 1;
        /* Maintain min packets in flight for max(200 ms, 1 round). */
        if (!bbr->probe_rtt_done_stamp &&
            tcp_packets_in_flight(tp) <= bbr_cwnd_min_target) {
            bbr->probe_rtt_done_stamp = tcp_jiffies32 +
                                       msecs_to_jiffies(bbr_probe_rtt_mode_ms);
            bbr->probe_rtt_round_done = 0;
            bbr->next_rtt_delivered = tp->delivered;
        } else if (bbr->probe_rtt_done_stamp) {
            if (bbr->round_start)
                bbr->probe_rtt_round_done = 1;
            if (bbr->probe_rtt_round_done)
                bbr_check_probe_rtt_done(sk);
        }
    }
    /* Restart after idle ends only once we process a new S/ACK for data */
    if (rs->delivered > 0)
        bbr->idle_restart = 0;
}
```

## 总结

Linux TCP协议实现代表了现代网络协议栈的巅峰之作，通过精心设计的状态机、拥塞控制算法和性能优化机制，实现了可靠、高效的数据传输。其核心优势包括：

### 技术成就

1. **完整的协议实现**：严格遵循RFC标准，同时加入了大量创新优化
2. **先进的拥塞控制**：从经典的慢启动到现代的BBR算法，持续演进
3. **高性能优化**：通过TSO、GRO、零拷贝等技术实现极高性能
4. **可扩展架构**：模块化设计支持新算法和特性的无缝集成

### 发展方向

1. **更智能的拥塞控制**：基于机器学习的自适应算法
2. **更好的多路径支持**：MPTCP的进一步完善
3. **用户态协议栈**：支持DPDK等高性能框架
4. **新兴网络环境适配**：5G、卫星网络等新场景的优化

Linux TCP的成功证明了开源协作在构建复杂系统方面的巨大潜力，为全球互联网的稳定运行提供了坚实的基础。
