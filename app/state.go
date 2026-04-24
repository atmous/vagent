package main

import (
	"math"
	"math/rand"
	"time"

	"github.com/niudea/vagent/nezha"
)

type stateProfile struct {
	logicalCores     float64
	activityPhase    float64
	activityFloor    float64
	activitySwing    float64
	cpuIdle          float64
	cpuBusy          float64
	memIdle          float64
	memBusy          float64
	diskLow          float64
	diskHigh         float64
	diskDrift        float64
	swapBusy         float64
	netInIdle        uint64
	netInBusy        uint64
	netOutIdle       uint64
	netOutBusy       uint64
	tcpIdle          uint64
	tcpBusy          uint64
	udpIdle          uint64
	udpBusy          uint64
	processIdle      uint64
	processBusy      uint64
	gpuIdle          float64
	gpuBusy          float64
	cpuTempIdle      float64
	cpuTempBusy      float64
	gpuTempIdle      float64
	gpuTempBusy      float64
	connectionJitter uint64
}

type stateGen struct {
	cfg            Agent
	rng            *rand.Rand
	profile        stateProfile
	hostProto      *nezha.Host
	state          *nezha.State
	cpu            float64
	memPercent     float64
	swapPercent    float64
	diskPercent    float64
	load1          float64
	load5          float64
	load15         float64
	netInSpeed     uint64
	netOutSpeed    uint64
	netInTransfer  uint64
	netOutTransfer uint64
	tcpConnCount   uint64
	udpConnCount   uint64
	processCount   uint64
	gpu            []float64
	temperatures   []float64
}

func newStateGen(cfg Agent) *stateGen {
	rng := newStableRand("state", cfg.Name, cfg.UUID, cfg.Server)
	profile := buildStateProfile(cfg, rng)

	hostProto := &nezha.Host{
		Platform:        cfg.Host.Platform,
		PlatformVersion: cfg.Host.PlatformVersion,
		Cpu:             cloneStrings(cfg.Host.CPU),
		MemTotal:        cfg.Host.MemTotal,
		DiskTotal:       cfg.Host.DiskTotal,
		SwapTotal:       cfg.Host.SwapTotal,
		Arch:            cfg.Host.Arch,
		Virtualization:  cfg.Host.Virtualization,
		BootTime:        cfg.Host.BootTime,
		Version:         cfg.Version,
		Gpu:             cloneStrings(cfg.Host.GPU),
	}

	state := &nezha.State{
		Temperatures: make([]*nezha.State_SensorTemperature, len(cfg.Host.TemperatureNames)),
		Gpu:          make([]float64, len(cfg.Host.GPU)),
	}
	for i, name := range cfg.Host.TemperatureNames {
		state.Temperatures[i] = &nezha.State_SensorTemperature{Name: name}
	}

	g := &stateGen{
		cfg:            cfg,
		rng:            rng,
		profile:        profile,
		hostProto:      hostProto,
		state:          state,
		netInTransfer:  randomUint(rng, UintRange{Min: 256 * miB, Max: 48 * giB}),
		netOutTransfer: randomUint(rng, UintRange{Min: 256 * miB, Max: 48 * giB}),
		gpu:            make([]float64, len(cfg.Host.GPU)),
		temperatures:   make([]float64, len(cfg.Host.TemperatureNames)),
	}

	activity := g.activityLevel(nowFunc())
	g.cpu = lerpFloat(profile.cpuIdle, profile.cpuBusy, activity)
	g.memPercent = lerpFloat(profile.memIdle, profile.memBusy, activity*0.72)
	g.diskPercent = lerpFloat(profile.diskLow, profile.diskHigh, randomScale(rng, 0.1, 0.65))
	g.netInSpeed = lerpUint(profile.netInIdle, profile.netInBusy, activity)
	g.netOutSpeed = lerpUint(profile.netOutIdle, profile.netOutBusy, activity)
	g.tcpConnCount = lerpUint(profile.tcpIdle, profile.tcpBusy, activity)
	g.udpConnCount = lerpUint(profile.udpIdle, profile.udpBusy, activity)
	g.processCount = lerpUint(profile.processIdle, profile.processBusy, activity)

	if cfg.Host.SwapTotal == 0 {
		g.swapPercent = 0
	} else {
		g.swapPercent = clamp(lerpFloat(0, profile.swapBusy, activity*0.45), cfg.Metrics.SwapUsedPercent.Min, cfg.Metrics.SwapUsedPercent.Max)
	}

	if len(g.gpu) > 0 {
		for i := range g.gpu {
			g.gpu[i] = clamp(lerpFloat(profile.gpuIdle, profile.gpuBusy, activity*randomScale(rng, 0.8, 1.15)), cfg.Metrics.GPU.Min, cfg.Metrics.GPU.Max)
		}
	}

	g.temperatures[0] = lerpFloat(profile.cpuTempIdle, profile.cpuTempBusy, g.cpu/100.0)
	for i := 1; i < len(g.temperatures); i++ {
		usage := 0.0
		if gpuIndex := i - 1; gpuIndex < len(g.gpu) {
			usage = g.gpu[gpuIndex]
		}
		g.temperatures[i] = lerpFloat(profile.gpuTempIdle, profile.gpuTempBusy, usage/100.0)
	}

	return g
}

func (g *stateGen) host() *nezha.Host {
	return g.hostProto
}

func (g *stateGen) next(interval time.Duration) *nezha.State {
	now := nowFunc()
	activity := clamp(g.activityLevel(now)+randomScale(g.rng, -0.06, 0.06), 0.03, 1)

	cpuTarget := clamp(
		lerpFloat(g.profile.cpuIdle, g.profile.cpuBusy, activity)+randomScale(g.rng, -4.5, 4.5),
		g.cfg.Metrics.CPU.Min,
		g.cfg.Metrics.CPU.Max,
	)
	g.cpu = moveFloatToward(g.cpu, cpuTarget, 4.8, g.cfg.Metrics.CPU)

	memTarget := clamp(
		lerpFloat(g.profile.memIdle, g.profile.memBusy, activity*0.7+(g.cpu/100.0)*0.3)+randomScale(g.rng, -2.5, 2.5),
		g.cfg.Metrics.MemUsedPercent.Min,
		g.cfg.Metrics.MemUsedPercent.Max,
	)
	g.memPercent = moveFloatToward(g.memPercent, memTarget, 2.4, g.cfg.Metrics.MemUsedPercent)

	diskTarget := clamp(
		g.diskPercent+randomScale(g.rng, -g.profile.diskDrift, g.profile.diskDrift),
		maxFloat(g.cfg.Metrics.DiskUsedPercent.Min, g.profile.diskLow),
		minFloat(g.cfg.Metrics.DiskUsedPercent.Max, g.profile.diskHigh),
	)
	g.diskPercent = moveFloatToward(g.diskPercent, diskTarget, g.profile.diskDrift, g.cfg.Metrics.DiskUsedPercent)

	if g.cfg.Host.SwapTotal == 0 {
		g.swapPercent = 0
	} else {
		swapPressure := clamp((g.memPercent-52)/38, 0, 1)
		swapTarget := clamp(
			lerpFloat(0, g.profile.swapBusy, swapPressure)+randomScale(g.rng, -1.5, 1.5),
			g.cfg.Metrics.SwapUsedPercent.Min,
			g.cfg.Metrics.SwapUsedPercent.Max,
		)
		g.swapPercent = moveFloatToward(g.swapPercent, swapTarget, 2.1, g.cfg.Metrics.SwapUsedPercent)
	}

	netInTarget := moveUintTarget(g.profile.netInIdle, g.profile.netInBusy, activity, g.profile.connectionJitter, g.rng)
	netOutTarget := moveUintTarget(g.profile.netOutIdle, g.profile.netOutBusy, activity*randomScale(g.rng, 0.92, 1.08), g.profile.connectionJitter, g.rng)
	g.netInSpeed = moveUintToward(g.netInSpeed, netInTarget, maxUint(g.cfg.Metrics.NetInSpeed.Step, 24*kiB), g.cfg.Metrics.NetInSpeed)
	g.netOutSpeed = moveUintToward(g.netOutSpeed, netOutTarget, maxUint(g.cfg.Metrics.NetOutSpeed.Step, 24*kiB), g.cfg.Metrics.NetOutSpeed)

	g.processCount = moveUintToward(
		g.processCount,
		moveUintTarget(g.profile.processIdle, g.profile.processBusy, activity, 8, g.rng),
		maxUint(g.cfg.Metrics.ProcessCount.Step, 6),
		g.cfg.Metrics.ProcessCount,
	)
	g.tcpConnCount = moveUintToward(
		g.tcpConnCount,
		moveUintTarget(g.profile.tcpIdle, g.profile.tcpBusy, activity, 12, g.rng),
		maxUint(g.cfg.Metrics.TCPConnCount.Step, 6),
		g.cfg.Metrics.TCPConnCount,
	)
	g.udpConnCount = moveUintToward(
		g.udpConnCount,
		moveUintTarget(g.profile.udpIdle, g.profile.udpBusy, activity, 4, g.rng),
		maxUint(g.cfg.Metrics.UDPConnCount.Step, 2),
		g.cfg.Metrics.UDPConnCount,
	)

	if len(g.gpu) > 0 {
		for i := range g.gpu {
			gpuActivity := clamp(activity*randomScale(g.rng, 0.84, 1.18), 0, 1)
			gpuTarget := clamp(
				lerpFloat(g.profile.gpuIdle, g.profile.gpuBusy, gpuActivity)+float64(i)*randomScale(g.rng, 0.8, 2.4),
				g.cfg.Metrics.GPU.Min,
				g.cfg.Metrics.GPU.Max,
			)
			g.gpu[i] = moveFloatToward(g.gpu[i], gpuTarget, 6.5, g.cfg.Metrics.GPU)
		}
	}

	cpuTempTarget := clamp(
		lerpFloat(g.profile.cpuTempIdle, g.profile.cpuTempBusy, g.cpu/100.0)+randomScale(g.rng, -0.8, 0.8),
		g.cfg.Metrics.Temperature.Min,
		g.cfg.Metrics.Temperature.Max,
	)
	g.temperatures[0] = moveFloatToward(g.temperatures[0], cpuTempTarget, 2.2, g.cfg.Metrics.Temperature)

	for i := 1; i < len(g.temperatures); i++ {
		usage := 0.0
		if gpuIndex := i - 1; gpuIndex < len(g.gpu) {
			usage = g.gpu[gpuIndex]
		}
		tempTarget := clamp(
			lerpFloat(g.profile.gpuTempIdle, g.profile.gpuTempBusy, usage/100.0)+randomScale(g.rng, -0.8, 0.8),
			g.cfg.Metrics.Temperature.Min,
			g.cfg.Metrics.Temperature.Max,
		)
		g.temperatures[i] = moveFloatToward(g.temperatures[i], tempTarget, 2.6, g.cfg.Metrics.Temperature)
	}

	seconds := interval.Seconds()
	if seconds <= 0 {
		seconds = 1
	}
	g.netInTransfer += uint64(float64(g.netInSpeed) * seconds)
	g.netOutTransfer += uint64(float64(g.netOutSpeed) * seconds)

	loadTarget := clamp((g.cpu/100.0)*g.profile.logicalCores*randomScale(g.rng, 0.78, 1.16), 0, g.profile.logicalCores*2.6)
	g.load1 = smooth(g.load1, loadTarget, 0.34)
	g.load5 = smooth(g.load5, g.load1, 0.12)
	g.load15 = smooth(g.load15, g.load5, 0.05)

	g.state.Cpu = round(g.cpu, 1)
	g.state.MemUsed = percentOf(g.cfg.Host.MemTotal, g.memPercent)
	g.state.SwapUsed = percentOf(g.cfg.Host.SwapTotal, g.swapPercent)
	g.state.DiskUsed = percentOf(g.cfg.Host.DiskTotal, g.diskPercent)
	g.state.NetInTransfer = g.netInTransfer
	g.state.NetOutTransfer = g.netOutTransfer
	g.state.NetInSpeed = g.netInSpeed
	g.state.NetOutSpeed = g.netOutSpeed
	g.state.Uptime = uptimeSince(g.cfg.Host.BootTime, now)
	g.state.Load1 = round(g.load1, 2)
	g.state.Load5 = round(g.load5, 2)
	g.state.Load15 = round(g.load15, 2)
	g.state.TcpConnCount = g.tcpConnCount
	g.state.UdpConnCount = g.udpConnCount
	g.state.ProcessCount = g.processCount

	for i := range g.state.Temperatures {
		g.state.Temperatures[i].Temperature = round(g.temperatures[i], 1)
	}
	for i := range g.state.Gpu {
		g.state.Gpu[i] = round(g.gpu[i], 1)
	}

	return g.state
}

func buildStateProfile(cfg Agent, rng *rand.Rand) stateProfile {
	role := inferMachineRole(cfg.Host)
	profile := stateProfile{
		logicalCores:     estimateLogicalCores(cfg.Host),
		activityPhase:    randomScale(rng, 0, 2*math.Pi),
		connectionJitter: maxUint(cfg.Metrics.TCPConnCount.Step/2, 4),
	}

	switch role {
	case roleWorkstation:
		profile.activityFloor = randomScale(rng, 0.12, 0.24)
		profile.activitySwing = randomScale(rng, 0.48, 0.72)
		profile.cpuIdle = percentile(gcfg(cfg.Metrics.CPU), 0.12, 0.26)
		profile.cpuBusy = percentile(gcfg(cfg.Metrics.CPU), 0.6, 0.9)
		profile.memIdle = percentile(gcfg(cfg.Metrics.MemUsedPercent), 0.22, 0.38)
		profile.memBusy = percentile(gcfg(cfg.Metrics.MemUsedPercent), 0.68, 0.92)
		profile.gpuIdle = percentile(gcfg(cfg.Metrics.GPU), 0.04, 0.12)
		profile.gpuBusy = percentile(gcfg(cfg.Metrics.GPU), 0.62, 0.92)
		profile.cpuTempIdle = percentile(gcfg(cfg.Metrics.Temperature), 0.12, 0.28)
		profile.cpuTempBusy = percentile(gcfg(cfg.Metrics.Temperature), 0.66, 0.9)
		profile.gpuTempIdle = percentile(gcfg(cfg.Metrics.Temperature), 0.08, 0.24)
		profile.gpuTempBusy = percentile(gcfg(cfg.Metrics.Temperature), 0.68, 0.92)
	case roleGPUNode:
		profile.activityFloor = randomScale(rng, 0.18, 0.32)
		profile.activitySwing = randomScale(rng, 0.34, 0.56)
		profile.cpuIdle = percentile(gcfg(cfg.Metrics.CPU), 0.18, 0.32)
		profile.cpuBusy = percentile(gcfg(cfg.Metrics.CPU), 0.58, 0.84)
		profile.memIdle = percentile(gcfg(cfg.Metrics.MemUsedPercent), 0.28, 0.42)
		profile.memBusy = percentile(gcfg(cfg.Metrics.MemUsedPercent), 0.7, 0.94)
		profile.gpuIdle = percentile(gcfg(cfg.Metrics.GPU), 0.08, 0.16)
		profile.gpuBusy = percentile(gcfg(cfg.Metrics.GPU), 0.56, 0.86)
		profile.cpuTempIdle = percentile(gcfg(cfg.Metrics.Temperature), 0.18, 0.32)
		profile.cpuTempBusy = percentile(gcfg(cfg.Metrics.Temperature), 0.62, 0.86)
		profile.gpuTempIdle = percentile(gcfg(cfg.Metrics.Temperature), 0.16, 0.3)
		profile.gpuTempBusy = percentile(gcfg(cfg.Metrics.Temperature), 0.66, 0.9)
	case roleService:
		profile.activityFloor = randomScale(rng, 0.22, 0.36)
		profile.activitySwing = randomScale(rng, 0.24, 0.46)
		profile.cpuIdle = percentile(gcfg(cfg.Metrics.CPU), 0.18, 0.34)
		profile.cpuBusy = percentile(gcfg(cfg.Metrics.CPU), 0.58, 0.88)
		profile.memIdle = percentile(gcfg(cfg.Metrics.MemUsedPercent), 0.32, 0.46)
		profile.memBusy = percentile(gcfg(cfg.Metrics.MemUsedPercent), 0.68, 0.9)
		profile.gpuIdle = percentile(gcfg(cfg.Metrics.GPU), 0, 0.02)
		profile.gpuBusy = percentile(gcfg(cfg.Metrics.GPU), 0.04, 0.18)
		profile.cpuTempIdle = percentile(gcfg(cfg.Metrics.Temperature), 0.1, 0.24)
		profile.cpuTempBusy = percentile(gcfg(cfg.Metrics.Temperature), 0.56, 0.82)
		profile.gpuTempIdle = profile.cpuTempIdle
		profile.gpuTempBusy = profile.cpuTempBusy
	default:
		profile.activityFloor = randomScale(rng, 0.08, 0.18)
		profile.activitySwing = randomScale(rng, 0.24, 0.42)
		profile.cpuIdle = percentile(gcfg(cfg.Metrics.CPU), 0.08, 0.22)
		profile.cpuBusy = percentile(gcfg(cfg.Metrics.CPU), 0.52, 0.78)
		profile.memIdle = percentile(gcfg(cfg.Metrics.MemUsedPercent), 0.2, 0.34)
		profile.memBusy = percentile(gcfg(cfg.Metrics.MemUsedPercent), 0.6, 0.82)
		profile.gpuIdle = percentile(gcfg(cfg.Metrics.GPU), 0, 0.02)
		profile.gpuBusy = percentile(gcfg(cfg.Metrics.GPU), 0.04, 0.12)
		profile.cpuTempIdle = percentile(gcfg(cfg.Metrics.Temperature), 0.08, 0.2)
		profile.cpuTempBusy = percentile(gcfg(cfg.Metrics.Temperature), 0.48, 0.72)
		profile.gpuTempIdle = profile.cpuTempIdle
		profile.gpuTempBusy = profile.cpuTempBusy
	}

	profile.diskLow = percentile(gcfg(cfg.Metrics.DiskUsedPercent), 0.18, 0.42)
	profile.diskHigh = percentile(gcfg(cfg.Metrics.DiskUsedPercent), 0.55, 0.88)
	profile.diskDrift = randomScale(rng, 0.15, 0.55)
	profile.swapBusy = percentile(gcfg(cfg.Metrics.SwapUsedPercent), 0.36, 0.82)
	profile.netInIdle = percentileUint(cfg.Metrics.NetInSpeed, 0.12, 0.28)
	profile.netInBusy = percentileUint(cfg.Metrics.NetInSpeed, 0.62, 0.94)
	profile.netOutIdle = percentileUint(cfg.Metrics.NetOutSpeed, 0.12, 0.28)
	profile.netOutBusy = percentileUint(cfg.Metrics.NetOutSpeed, 0.62, 0.94)
	profile.tcpIdle = percentileUint(cfg.Metrics.TCPConnCount, 0.18, 0.34)
	profile.tcpBusy = percentileUint(cfg.Metrics.TCPConnCount, 0.62, 0.9)
	profile.udpIdle = percentileUint(cfg.Metrics.UDPConnCount, 0.18, 0.34)
	profile.udpBusy = percentileUint(cfg.Metrics.UDPConnCount, 0.62, 0.9)
	profile.processIdle = percentileUint(cfg.Metrics.ProcessCount, 0.22, 0.38)
	profile.processBusy = percentileUint(cfg.Metrics.ProcessCount, 0.64, 0.92)
	return profile
}

func (g *stateGen) activityLevel(now time.Time) float64 {
	hour := float64(now.UTC().Hour()) + float64(now.UTC().Minute())/60 + float64(now.UTC().Second())/3600
	cycle := (math.Sin((hour/24.0)*2*math.Pi+g.profile.activityPhase) + 1) / 2
	level := g.profile.activityFloor + cycle*g.profile.activitySwing
	switch now.UTC().Weekday() {
	case time.Saturday, time.Sunday:
		level *= 0.88
	}
	return clamp(level, 0.03, 1)
}

func gcfg(r FloatRange) FloatRange {
	return r
}

func percentile(r FloatRange, low, high float64) float64 {
	if r.Max <= r.Min {
		return r.Min
	}
	return lerpFloat(r.Min, r.Max, clamp((low+high)/2, 0, 1))
}

func percentileUint(r UintRange, low, high float64) uint64 {
	if r.Max <= r.Min {
		return r.Min
	}
	return lerpUint(r.Min, r.Max, clamp((low+high)/2, 0, 1))
}

func percentOf(total uint64, percent float64) uint64 {
	if total == 0 || percent <= 0 {
		return 0
	}
	return uint64(float64(total) * percent / 100.0)
}

func uptimeSince(bootTime uint64, now time.Time) uint64 {
	if bootTime == 0 {
		return 0
	}
	current := now.Unix()
	if current <= int64(bootTime) {
		return 0
	}
	return uint64(current - int64(bootTime))
}

func moveFloatToward(current, target, maxStep float64, bounds FloatRange) float64 {
	if current == 0 {
		current = target
	}
	if maxStep <= 0 {
		maxStep = 1
	}
	delta := clamp(target-current, -maxStep, maxStep)
	return clamp(current+delta, bounds.Min, bounds.Max)
}

func moveUintTarget(min, max uint64, factor float64, jitter uint64, rng *rand.Rand) uint64 {
	target := lerpUint(min, max, clamp(factor, 0, 1))
	if jitter > 0 {
		offset := int64(rng.Intn(int(jitter*2)+1)) - int64(jitter)
		next := int64(target) + offset
		if next < int64(min) {
			next = int64(min)
		}
		if next > int64(max) {
			next = int64(max)
		}
		return uint64(next)
	}
	return target
}

func moveUintToward(current, target, maxStep uint64, bounds UintRange) uint64 {
	if current == 0 {
		current = target
	}
	if maxStep == 0 {
		maxStep = 1
	}
	var next uint64
	switch {
	case target > current:
		delta := minUint(target-current, maxStep)
		next = current + delta
	case current > target:
		delta := minUint(current-target, maxStep)
		next = current - delta
	default:
		next = current
	}
	if next < bounds.Min {
		next = bounds.Min
	}
	if next > bounds.Max {
		next = bounds.Max
	}
	return next
}

func lerpFloat(min, max, factor float64) float64 {
	if max <= min {
		return min
	}
	return min + (max-min)*clamp(factor, 0, 1)
}

func lerpUint(min, max uint64, factor float64) uint64 {
	if max <= min {
		return min
	}
	return min + uint64(float64(max-min)*clamp(factor, 0, 1))
}

func smooth(previous, current, factor float64) float64 {
	if previous == 0 {
		return current
	}
	return previous*(1-factor) + current*factor
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func randomScale(rng *rand.Rand, min, max float64) float64 {
	if max <= min {
		return min
	}
	return min + rng.Float64()*(max-min)
}

func randomUint(rng *rand.Rand, r UintRange) uint64 {
	if r.Max <= r.Min {
		return r.Min
	}
	return r.Min + uint64(rng.Float64()*float64(r.Max-r.Min))
}

func minUint(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}

func maxUint(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}

func minFloat(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

func round(value float64, digits int) float64 {
	pow := math.Pow10(digits)
	return math.Round(value*pow) / pow
}
