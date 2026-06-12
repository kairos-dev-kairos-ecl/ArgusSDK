//go:build ignore

// gen.go contains the go:generate directive to regenerate the bpf2go skeleton.
//
// Regeneration requires clang + Linux kernel headers (BPF target) and runs in
// CI/maintainer context only.  The generated artifacts (tcpconnect_bpfel.go,
// tcpconnect_bpfeb.go, tcpconnect_bpfel.o, tcpconnect_bpfeb.o) are committed
// to the repository so that go build / the agent binary never needs clang.
//
// To regenerate (run from internal/collector/euc/ on a Linux machine with clang):
//
//	go generate ./...
//
// Or directly:
//
//	go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel,bpfeb -go-package euc \
//	    tcpconnect ./bpf/tcpconnect.c -- -I/usr/include/bpf -D__TARGET_ARCH_x86
//
// The -go-package flag ensures the generated skeleton is in package euc (same
// package as linux.go), so linux.go can call loadTcpconnectObjects and reference
// tcpconnectObjects directly without a subpackage import.

package euc

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel,bpfeb -go-package euc tcpconnect ./bpf/tcpconnect.c -- -I/usr/include/bpf -D__TARGET_ARCH_x86
