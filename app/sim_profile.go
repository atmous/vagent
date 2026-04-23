package main

import (
	"hash/fnv"
	"math/rand"
	"strings"
)

type machineRole int

const (
	roleEdge machineRole = iota
	roleService
	roleGPUNode
	roleWorkstation
)

func stableSeed(parts ...string) uint64 {
	h := fnv.New64a()
	for _, part := range parts {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

func newStableRand(parts ...string) *rand.Rand {
	return rand.New(rand.NewSource(int64(stableSeed(parts...))))
}

func inferMachineRole(host Host) machineRole {
	memGB := host.MemTotal / giB
	platform := strings.ToLower(host.Platform)
	hasGPU := len(host.GPU) > 0
	hasConsumerGPU := false
	for _, model := range host.GPU {
		if containsAnyFold(model, "geforce", "radeon", "arc") {
			hasConsumerGPU = true
			break
		}
	}

	switch {
	case hasGPU && (strings.Contains(platform, "windows") || hasConsumerGPU):
		return roleWorkstation
	case hasGPU:
		return roleGPUNode
	case memGB >= 8:
		return roleService
	default:
		return roleEdge
	}
}

func estimateLogicalCores(host Host) float64 {
	memGB := host.MemTotal / giB
	cores := 2
	switch {
	case memGB >= 32:
		cores = 16
	case memGB >= 16:
		cores = 12
	case memGB >= 8:
		cores = 8
	case memGB >= 4:
		cores = 4
	}

	models := append(cloneStrings(host.CPU), cloneStrings(host.GPU)...)
	for _, model := range models {
		switch {
		case containsAnyFold(model, "epyc", "threadripper"):
			if cores < 8 {
				cores = 8
			}
		case containsAnyFold(model, "xeon", "ryzen 9", "core(tm) i9"):
			if cores < 6 {
				cores = 6
			}
		case containsAnyFold(model, "ryzen 7", "core(tm) i7"):
			if cores < 4 {
				cores = 4
			}
		}
	}

	if len(host.GPU) >= 2 {
		cores += 4
	} else if len(host.GPU) == 1 {
		cores += 2
	}
	if cores > 32 {
		cores = 32
	}
	return float64(cores)
}

func containsAnyFold(value string, needles ...string) bool {
	lower := strings.ToLower(value)
	for _, needle := range needles {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}
