// SPDX-License-Identifier: GPL-2.0 OR BSD-2-Clause
//
// tcpconnect.c — eBPF kprobe program for Shadow-AI EUC observer.
//
// Attaches to tcp_v4_connect and tcp_v6_connect kernel functions and emits
// a fixed-size event record to a BPF ring buffer map named "events" for each
// outbound TCP connect.  The event carries connection METADATA ONLY (5-tuple
// at connect time): remote address, remote port, address family, the
// connecting task's PID, and its comm string (best-effort via
// bpf_get_current_comm).  NO payload, NO general process enumeration.
//
// Regeneration (maintainer/CI only):
//   cd internal/collector/euc
//   go generate    # requires clang + kernel headers; commits output artifacts
//
// The agent binary embeds the precompiled .o produced by bpf2go and never
// invokes clang at build or runtime.
//
// Minimum tested kernel: 5.15 LTS (BPF ring buffer available since 5.8).
// Target capability: CAP_BPF + CAP_PERFMON (not root, not CAP_SYS_ADMIN).

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

// TASK_COMM_LEN matches the kernel constant — max length of the comm string.
#define TASK_COMM_LEN 16

// connect_event is the fixed-size record written to the ring buffer.
// Keep fields naturally aligned to avoid verifier padding issues.
struct connect_event {
	__u32 pid;           // PID of the connecting task
	__u16 dport;         // remote port (network byte order)
	__u16 af;            // address family: AF_INET (2) or AF_INET6 (10)
	__u8  saddr[16];     // local address (4 bytes used for IPv4, 16 for IPv6)
	__u8  daddr[16];     // remote address (4 bytes used for IPv4, 16 for IPv6)
	char  comm[TASK_COMM_LEN]; // connecting task comm (best-effort)
};

// events is the BPF ring buffer map.  bpf2go generates a Go accessor for
// this field using the map name ("Events" after title-casing by bpf2go).
struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 1 << 24); // 16 MiB ring buffer
} events SEC(".maps");

// handle_tcp_v4_connect is the kprobe handler for tcp_v4_connect.
// At probe time, sk->__sk_common.skc_daddr holds the remote IPv4 address
// and sk->__sk_common.skc_dport holds the remote port (big-endian).
SEC("kprobe/tcp_v4_connect")
int BPF_KPROBE(handle_tcp_v4_connect, struct sock *sk)
{
	struct connect_event *e;

	e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
	if (!e)
		return 0;

	e->pid   = bpf_get_current_pid_tgid() >> 32;
	e->af    = AF_INET;
	e->dport = BPF_CORE_READ(sk, __sk_common.skc_dport);

	// Remote IPv4 address into the first 4 bytes of daddr.
	__u32 daddr4 = BPF_CORE_READ(sk, __sk_common.skc_daddr);
	__builtin_memcpy(e->daddr, &daddr4, sizeof(daddr4));

	// Local IPv4 address into the first 4 bytes of saddr.
	__u32 saddr4 = BPF_CORE_READ(sk, __sk_common.skc_rcv_saddr);
	__builtin_memcpy(e->saddr, &saddr4, sizeof(saddr4));

	bpf_get_current_comm(e->comm, sizeof(e->comm));

	bpf_ringbuf_submit(e, 0);
	return 0;
}

// handle_tcp_v6_connect is the kprobe handler for tcp_v6_connect.
// At probe time, sk->__sk_common.skc_v6_daddr holds the remote IPv6 address.
SEC("kprobe/tcp_v6_connect")
int BPF_KPROBE(handle_tcp_v6_connect, struct sock *sk)
{
	struct connect_event *e;

	e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
	if (!e)
		return 0;

	e->pid   = bpf_get_current_pid_tgid() >> 32;
	e->af    = AF_INET6;
	e->dport = BPF_CORE_READ(sk, __sk_common.skc_dport);

	// Remote IPv6 address (16 bytes).
	BPF_CORE_READ_INTO(e->daddr, sk, __sk_common.skc_v6_daddr.in6_u.u6_addr8);

	// Local IPv6 address (16 bytes).
	BPF_CORE_READ_INTO(e->saddr, sk, __sk_common.skc_v6_rcv_saddr.in6_u.u6_addr8);

	bpf_get_current_comm(e->comm, sizeof(e->comm));

	bpf_ringbuf_submit(e, 0);
	return 0;
}

char LICENSE[] SEC("license") = "Dual BSD/GPL";
