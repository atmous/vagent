package main

import (
	"crypto/md5"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	kiB = 1024
	miB = 1024 * kiB
	giB = 1024 * miB

	defaultReportDelaySeconds = 3
	defaultIPv4ReportPeriod   = 30 * time.Minute
)

var (
	nowFunc = time.Now
)

type Config struct {
	Agents []Agent
}

type Agent struct {
	Name         string
	Server       string
	ClientSecret string
	TLS          bool
	UUID         string
	ReportDelay  time.Duration
	Version      string
	Host         Host
	Metrics      Metrics
}

type Host struct {
	IPv4             string
	Platform         string
	PlatformVersion  string
	CPU              []string
	GPU              []string
	TemperatureNames []string
	MemTotal         uint64
	DiskTotal        uint64
	SwapTotal        uint64
	Arch             string
	Virtualization   string
	BootTime         uint64
}

type Metrics struct {
	CPU             FloatRange `yaml:"cpu"`
	MemUsedPercent  FloatRange `yaml:"mem_used_percent"`
	SwapUsedPercent FloatRange `yaml:"swap_used_percent"`
	DiskUsedPercent FloatRange `yaml:"disk_used_percent"`
	LoadScale       FloatRange `yaml:"load_scale"`
	GPU             FloatRange `yaml:"gpu"`
	Temperature     FloatRange `yaml:"temperature"`
	NetInSpeed      UintRange  `yaml:"net_in_speed"`
	NetOutSpeed     UintRange  `yaml:"net_out_speed"`
	TCPConnCount    UintRange  `yaml:"tcp_conn_count"`
	UDPConnCount    UintRange  `yaml:"udp_conn_count"`
	ProcessCount    UintRange  `yaml:"process_count"`
}

type FloatRange struct {
	Min  float64 `yaml:"min" json:"min"`
	Max  float64 `yaml:"max" json:"max"`
	Step float64 `yaml:"step" json:"step"`
}

type UintRange struct {
	Min  uint64 `yaml:"min" json:"min"`
	Max  uint64 `yaml:"max" json:"max"`
	Step uint64 `yaml:"step" json:"step"`
}

type agentDoc struct {
	Server       string  `yaml:"server"`
	ClientSecret string  `yaml:"client_secret"`
	TLS          *bool   `yaml:"tls"`
	Host         hostDoc `yaml:"host"`
}

type hostDoc struct {
	IPv4            string   `yaml:"ipv4"`
	Platform        string   `yaml:"platform"`
	PlatformVersion string   `yaml:"platform_version"`
	CPU             []string `yaml:"cpu"`
	GPU             []string `yaml:"gpu"`
	MemTotal        *sizeGB  `yaml:"mem_total"`
	DiskTotal       *sizeGB  `yaml:"disk_total"`
	SwapTotal       *sizeGB  `yaml:"swap_total"`
	Arch            string   `yaml:"arch"`
}

type sizeGB uint64

func Load(path string, defaultVersion string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config %s must be a YAML mapping", path)
	}

	mapping := root.Content[0]
	cfg := &Config{Agents: make([]Agent, 0, len(mapping.Content)/2)}
	for i := 0; i < len(mapping.Content); i += 2 {
		name := strings.TrimSpace(mapping.Content[i].Value)
		if name == "" {
			continue
		}

		if err := validateAgentNode(mapping.Content[i+1]); err != nil {
			return nil, fmt.Errorf("node %q: %w", name, err)
		}

		entry := defaultAgentDoc()
		if err := mapping.Content[i+1].Decode(&entry); err != nil {
			return nil, fmt.Errorf("parse node %q: %w", name, err)
		}

		agent, err := resolveAgent(name, entry, i/2, defaultVersion)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", name, err)
		}
		cfg.Agents = append(cfg.Agents, agent)
	}

	if len(cfg.Agents) == 0 {
		return nil, fmt.Errorf("config %s does not define any valid agents", path)
	}
	return cfg, nil
}

func defaultAgentDoc() agentDoc {
	return agentDoc{}
}

func resolveAgent(name string, doc agentDoc, index int, defaultVersion string) (Agent, error) {
	server := strings.TrimSpace(doc.Server)
	if server == "" {
		return Agent{}, fmt.Errorf("server is required")
	}
	if err := rejectEnvPlaceholder("server", server); err != nil {
		return Agent{}, err
	}

	clientSecret := strings.TrimSpace(doc.ClientSecret)
	if clientSecret == "" {
		return Agent{}, fmt.Errorf("client_secret is required")
	}
	if err := rejectEnvPlaceholder("client_secret", clientSecret); err != nil {
		return Agent{}, err
	}
	if doc.TLS == nil {
		return Agent{}, fmt.Errorf("tls is required")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("vagent-%02d", index+1)
	}

	version := strings.TrimSpace(defaultVersion)
	if version == "" {
		version = "vagent"
	}

	uuid := deterministicUUID(server + "|" + name)

	host, err := resolveHost(doc.Host, name, server)
	if err != nil {
		return Agent{}, err
	}

	return Agent{
		Name:         name,
		Server:       server,
		ClientSecret: clientSecret,
		TLS:          *doc.TLS,
		UUID:         uuid,
		ReportDelay:  time.Duration(defaultReportDelaySeconds) * time.Second,
		Version:      version,
		Host:         host,
		Metrics:      defaultMetrics(host, name, server),
	}, nil
}

func validateAgentNode(node *yaml.Node) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := strings.TrimSpace(node.Content[i].Value)
		switch key {
		case "server", "client_secret", "tls":
		case "host":
			if err := validateHostNode(node.Content[i+1]); err != nil {
				return err
			}
		default:
			return fmt.Errorf("field %q is not supported", key)
		}
	}
	return nil
}

func validateHostNode(node *yaml.Node) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := strings.TrimSpace(node.Content[i].Value)
		switch key {
		case "platform", "platform_version", "arch", "cpu", "gpu", "mem_total", "disk_total", "swap_total", "ipv4":
		default:
			return fmt.Errorf("host field %q is not supported", key)
		}
	}
	return nil
}

func resolveHost(doc hostDoc, name string, server string) (Host, error) {
	ipv4 := strings.TrimSpace(doc.IPv4)
	if err := rejectEnvPlaceholder("host.ipv4", ipv4); err != nil {
		return Host{}, err
	}
	if ipv4 != "" {
		parsed := net.ParseIP(ipv4)
		if parsed == nil || parsed.To4() == nil {
			return Host{}, fmt.Errorf("host.ipv4 must be a valid IPv4 address")
		}
	}

	if doc.MemTotal != nil && uint64(*doc.MemTotal) == 0 {
		return Host{}, fmt.Errorf("host.mem_total must be greater than 0GB")
	}
	if doc.DiskTotal != nil && uint64(*doc.DiskTotal) == 0 {
		return Host{}, fmt.Errorf("host.disk_total must be greater than 0GB")
	}

	rng := newStableRand("host", server, name)
	platform := defaultHostPlatform(name, doc.Platform)
	gpu := defaultHostGPU(rng, platform, doc.GPU)
	memGB := defaultHostMemTotal(rng, platform, gpu, doc.MemTotal)
	diskGB := defaultHostDiskTotal(rng, memGB, gpu, doc.DiskTotal)
	swapGB := defaultHostSwapTotal(rng, platform, memGB, doc.SwapTotal)
	cpu := defaultHostCPU(rng, platform, memGB, gpu, doc.CPU)

	host := Host{
		IPv4:             ipv4,
		Platform:         platform,
		PlatformVersion:  defaultHostPlatformVersion(rng, platform, doc.PlatformVersion),
		CPU:              cpu,
		GPU:              gpu,
		TemperatureNames: defaultTemperatureNames(gpu),
		MemTotal:         gbToBytes(memGB),
		DiskTotal:        gbToBytes(diskGB),
		SwapTotal:        gbToBytes(swapGB),
		Arch:             defaultHostArch(doc.Arch),
		Virtualization:   defaultHostVirtualizationValue(rng, platform, gpu),
	}
	host.BootTime = defaultHostBootTime(rng, host)
	return host, nil
}

func rejectEnvPlaceholder(field string, value string) error {
	if strings.Contains(value, "${") && strings.Contains(value, "}") {
		return fmt.Errorf("%s must be a literal value in config; environment variable placeholders are not supported", field)
	}
	return nil
}

func defaultMetrics(host Host, name string, server string) Metrics {
	rng := newStableRand("metrics", server, name, host.Platform, host.PlatformVersion, strings.Join(host.CPU, ","), strings.Join(host.GPU, ","))
	memGB := maxUint(host.MemTotal/giB, 1)
	role := inferMachineRole(host)

	var metrics Metrics
	switch role {
	case roleWorkstation:
		metrics = Metrics{
			CPU:             randomMetricFloat(rng, 2, 8, 24, 58, 2, 6),
			MemUsedPercent:  randomMetricFloat(rng, 30, 42, 62, 84, 3, 7),
			SwapUsedPercent: randomMetricFloat(rng, 0, 4, 10, 28, 2, 5),
			DiskUsedPercent: randomMetricFloat(rng, 18, 34, 44, 72, 2, 5),
			LoadScale:       randomMetricFloat(rng, 0.35, 0.65, 1.0, 1.55, 0.08, 0.22),
			GPU:             randomMetricFloat(rng, 2, 10, 55, 94, 6, 14),
			Temperature:     randomMetricFloat(rng, 34, 40, 74, 84, 2, 5),
			NetInSpeed:      randomMetricUint(rng, 24*kiB, 96*kiB, 256*kiB, 2*miB, 16*kiB, 192*kiB),
			NetOutSpeed:     randomMetricUint(rng, 24*kiB, 96*kiB, 256*kiB, 2*miB, 16*kiB, 192*kiB),
			TCPConnCount:    randomMetricUint(rng, 18, 48, 120, 260, 8, 36),
			UDPConnCount:    randomMetricUint(rng, 2, 8, 16, 44, 2, 8),
			ProcessCount:    randomMetricUint(rng, 72, 110, 140, 240, 8, 24),
		}
	case roleGPUNode:
		metrics = Metrics{
			CPU:             randomMetricFloat(rng, 4, 10, 28, 66, 3, 7),
			MemUsedPercent:  randomMetricFloat(rng, 36, 48, 64, 88, 3, 7),
			SwapUsedPercent: randomMetricFloat(rng, 0, 4, 8, 24, 2, 5),
			DiskUsedPercent: randomMetricFloat(rng, 20, 36, 42, 68, 2, 5),
			LoadScale:       randomMetricFloat(rng, 0.45, 0.7, 1.0, 1.65, 0.08, 0.22),
			GPU:             randomMetricFloat(rng, 4, 12, 48, 88, 5, 12),
			Temperature:     randomMetricFloat(rng, 38, 44, 70, 82, 2, 5),
			NetInSpeed:      randomMetricUint(rng, 48*kiB, 160*kiB, 384*kiB, 1536*kiB, 32*kiB, 192*kiB),
			NetOutSpeed:     randomMetricUint(rng, 48*kiB, 160*kiB, 384*kiB, 1536*kiB, 32*kiB, 192*kiB),
			TCPConnCount:    randomMetricUint(rng, 28, 72, 180, 360, 12, 40),
			UDPConnCount:    randomMetricUint(rng, 4, 10, 18, 54, 2, 8),
			ProcessCount:    randomMetricUint(rng, 88, 128, 180, 280, 10, 28),
		}
	case roleService:
		metrics = Metrics{
			CPU:             randomMetricFloat(rng, 4, 12, 32, 72, 3, 7),
			MemUsedPercent:  randomMetricFloat(rng, 34, 48, 62, 86, 3, 7),
			SwapUsedPercent: randomMetricFloat(rng, 0, 3, 6, 18, 1, 4),
			DiskUsedPercent: randomMetricFloat(rng, 22, 38, 48, 76, 2, 5),
			LoadScale:       randomMetricFloat(rng, 0.45, 0.75, 1.05, 1.7, 0.08, 0.22),
			GPU:             randomMetricFloat(rng, 0, 2, 0, 12, 2, 6),
			Temperature:     randomMetricFloat(rng, 34, 40, 64, 76, 2, 4),
			NetInSpeed:      randomMetricUint(rng, 64*kiB, 192*kiB, 512*kiB, 2*miB, 32*kiB, 256*kiB),
			NetOutSpeed:     randomMetricUint(rng, 64*kiB, 192*kiB, 512*kiB, 2*miB, 32*kiB, 256*kiB),
			TCPConnCount:    randomMetricUint(rng, 36, 96, 220, 520, 16, 52),
			UDPConnCount:    randomMetricUint(rng, 4, 12, 20, 72, 2, 10),
			ProcessCount:    randomMetricUint(rng, 92, 140, 180, 320, 12, 32),
		}
	default:
		metrics = Metrics{
			CPU:             randomMetricFloat(rng, 1, 6, 18, 44, 2, 5),
			MemUsedPercent:  randomMetricFloat(rng, 24, 36, 52, 74, 3, 6),
			SwapUsedPercent: randomMetricFloat(rng, 0, 2, 4, 14, 1, 4),
			DiskUsedPercent: randomMetricFloat(rng, 16, 28, 36, 58, 2, 5),
			LoadScale:       randomMetricFloat(rng, 0.3, 0.55, 0.9, 1.35, 0.08, 0.2),
			GPU:             randomMetricFloat(rng, 0, 1, 0, 8, 2, 4),
			Temperature:     randomMetricFloat(rng, 32, 38, 58, 70, 2, 4),
			NetInSpeed:      randomMetricUint(rng, 24*kiB, 96*kiB, 256*kiB, 768*kiB, 16*kiB, 128*kiB),
			NetOutSpeed:     randomMetricUint(rng, 24*kiB, 96*kiB, 256*kiB, 768*kiB, 16*kiB, 128*kiB),
			TCPConnCount:    randomMetricUint(rng, 16, 42, 90, 220, 8, 32),
			UDPConnCount:    randomMetricUint(rng, 1, 6, 12, 32, 2, 6),
			ProcessCount:    randomMetricUint(rng, 48, 84, 120, 200, 8, 22),
		}
	}

	if memGB >= 16 {
		metrics.TCPConnCount.Max += 90
		metrics.ProcessCount.Max += 40
		metrics.NetInSpeed.Max += 256 * kiB
		metrics.NetOutSpeed.Max += 256 * kiB
	}
	return normalizeMetrics(metrics)
}

func normalizeMetrics(metrics Metrics) Metrics {
	metrics.CPU = normalizeFloatRange(metrics.CPU)
	metrics.MemUsedPercent = normalizeFloatRange(metrics.MemUsedPercent)
	metrics.SwapUsedPercent = normalizeFloatRange(metrics.SwapUsedPercent)
	metrics.DiskUsedPercent = normalizeFloatRange(metrics.DiskUsedPercent)
	metrics.LoadScale = normalizeFloatRange(metrics.LoadScale)
	metrics.GPU = normalizeFloatRange(metrics.GPU)
	metrics.Temperature = normalizeFloatRange(metrics.Temperature)
	metrics.NetInSpeed = normalizeUintRange(metrics.NetInSpeed)
	metrics.NetOutSpeed = normalizeUintRange(metrics.NetOutSpeed)
	metrics.TCPConnCount = normalizeUintRange(metrics.TCPConnCount)
	metrics.UDPConnCount = normalizeUintRange(metrics.UDPConnCount)
	metrics.ProcessCount = normalizeUintRange(metrics.ProcessCount)
	return metrics
}

func normalizeFloatRange(r FloatRange) FloatRange {
	if r.Max < r.Min {
		r.Max = r.Min
	}
	return r
}

func normalizeUintRange(r UintRange) UintRange {
	if r.Max < r.Min {
		r.Max = r.Min
	}
	return r
}

func gbToBytes(total uint64) uint64 {
	return total * giB
}

func defaultHostPlatform(name string, explicit string) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if strings.Contains(lower, "win") {
		return "Windows"
	}
	return "Linux"
}

func defaultHostPlatformVersion(rng interface{ Intn(int) int }, platform string, explicit string) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	switch {
	case containsAnyFold(platform, "windows"):
		return pickWeightedString(rng, []string{"Server 2022", "Server 2019", "11 Pro"}, []int{6, 3, 1})
	case containsAnyFold(platform, "ubuntu"):
		return pickWeightedString(rng, []string{"24.04", "22.04"}, []int{4, 6})
	case containsAnyFold(platform, "debian"):
		return pickWeightedString(rng, []string{"12", "11"}, []int{7, 3})
	default:
		return pickWeightedString(rng, []string{"6.1", "5.15", "6.6"}, []int{5, 3, 2})
	}
}

func defaultHostArch(explicit string) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	return "amd64"
}

func defaultHostCPU(rng interface{ Intn(int) int }, platform string, memGB uint64, gpu []string, explicit []string) []string {
	if len(explicit) > 0 {
		return cloneStrings(explicit)
	}

	role := inferMachineRole(Host{
		Platform: platform,
		GPU:      gpu,
		MemTotal: gbToBytes(memGB),
	})
	switch role {
	case roleWorkstation:
		return []string{pickWeightedString(rng, []string{
			"Intel(R) Core(TM) i7-12700KF CPU @ 3.60GHz",
			"Intel(R) Core(TM) i9-13900K CPU @ 3.00GHz",
			"AMD Ryzen 7 5800X 8-Core Processor",
			"AMD Ryzen 9 7900X 12-Core Processor",
		}, []int{3, 2, 3, 2})}
	case roleGPUNode, roleService:
		return []string{pickWeightedString(rng, []string{
			"AMD EPYC 7B12 64-Core Processor",
			"Intel(R) Xeon(R) Platinum 8370C CPU @ 2.80GHz",
			"AMD EPYC 7543 32-Core Processor",
			"Intel(R) Xeon(R) Gold 6138 CPU @ 2.00GHz",
		}, []int{4, 4, 1, 1})}
	default:
		return []string{pickWeightedString(rng, []string{
			"AMD EPYC 7B12 64-Core Processor",
			"Intel(R) Xeon(R) Platinum 8370C CPU @ 2.80GHz",
			"Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz",
		}, []int{4, 4, 2})}
	}
}

func defaultHostGPU(rng interface{ Intn(int) int }, platform string, explicit []string) []string {
	if len(explicit) > 0 {
		return cloneStrings(explicit)
	}

	switch {
	case containsAnyFold(platform, "windows"):
		switch pickWeightedInt(rng, []int{70, 22, 8}) {
		case 1:
			return []string{pickWeightedString(rng, []string{
				"NVIDIA GeForce RTX 3060",
				"NVIDIA GeForce RTX 4070 SUPER",
				"NVIDIA GeForce RTX 4080 SUPER",
				"NVIDIA GeForce RTX 4090",
			}, []int{3, 3, 2, 2})}
		case 2:
			return []string{"NVIDIA GeForce RTX 4090", "NVIDIA GeForce RTX 4080 SUPER"}
		default:
			return nil
		}
	default:
		switch pickWeightedInt(rng, []int{84, 13, 3}) {
		case 1:
			return []string{pickWeightedString(rng, []string{
				"NVIDIA Tesla T4",
				"NVIDIA L4",
				"NVIDIA A10",
			}, []int{5, 2, 1})}
		case 2:
			return []string{"NVIDIA Tesla T4", "NVIDIA Tesla T4"}
		default:
			return nil
		}
	}
}

func defaultHostMemTotal(rng interface{ Intn(int) int }, platform string, gpu []string, explicit *sizeGB) uint64 {
	if explicit != nil {
		return uint64(*explicit)
	}
	switch {
	case len(gpu) >= 2:
		return pickWeightedUint64(rng, []uint64{16, 32, 64}, []int{4, 4, 2})
	case len(gpu) == 1:
		return pickWeightedUint64(rng, []uint64{4, 8, 16, 32}, []int{1, 5, 3, 1})
	case containsAnyFold(platform, "windows"):
		return pickWeightedUint64(rng, []uint64{4, 8, 16}, []int{4, 4, 2})
	default:
		return pickWeightedUint64(rng, []uint64{2, 4, 8, 16}, []int{5, 4, 2, 1})
	}
}

func defaultHostDiskTotal(rng interface{ Intn(int) int }, memGB uint64, gpu []string, explicit *sizeGB) uint64 {
	if explicit != nil {
		return uint64(*explicit)
	}
	minDisk := maxUint(20, memGB*6)
	if len(gpu) > 0 && minDisk < 80 {
		minDisk = 80
	}
	choices := []uint64{20, 40, 60, 80, 120, 160, 240, 320}
	index := 0
	for index < len(choices)-1 && choices[index] < minDisk {
		index++
	}
	extra := 0
	if remain := len(choices) - index; remain > 1 {
		extra = pickWeightedInt(rng, []int{6, 3, 1})
		if extra >= remain {
			extra = remain - 1
		}
	}
	return choices[index+extra]
}

func defaultHostSwapTotal(rng interface{ Intn(int) int }, platform string, memGB uint64, explicit *sizeGB) uint64 {
	if explicit != nil {
		return uint64(*explicit)
	}
	switch {
	case containsAnyFold(platform, "windows") && memGB >= 16:
		return pickWeightedUint64(rng, []uint64{2, 4, 8}, []int{3, 4, 3})
	case containsAnyFold(platform, "windows"):
		return pickWeightedUint64(rng, []uint64{0, 2, 4}, []int{2, 5, 3})
	case memGB >= 8:
		return pickWeightedUint64(rng, []uint64{0, 2, 4}, []int{5, 3, 2})
	default:
		return pickWeightedUint64(rng, []uint64{0, 1, 2}, []int{6, 2, 2})
	}
}

func defaultHostVirtualizationValue(rng interface{ Intn(int) int }, platform string, gpu []string) string {
	switch {
	case containsAnyFold(platform, "windows"):
		return pickWeightedString(rng, []string{"hyper-v", "vmware", "kvm"}, []int{5, 3, 2})
	case len(gpu) > 0:
		return pickWeightedString(rng, []string{"kvm", "vmware", "qemu"}, []int{6, 2, 2})
	default:
		return pickWeightedString(rng, []string{"kvm", "qemu", "vmware", "virtualbox"}, []int{6, 2, 1, 1})
	}
}

func defaultHostBootTime(rng interface{ Intn(int) int }, host Host) uint64 {
	var minHours int
	var maxHours int
	switch inferMachineRole(host) {
	case roleWorkstation:
		minHours, maxHours = 12, 24*21
	case roleGPUNode:
		minHours, maxHours = 24, 24*45
	case roleService:
		minHours, maxHours = 24*5, 24*180
	default:
		minHours, maxHours = 24, 24*75
	}
	if containsAnyFold(host.Platform, "windows") && maxHours > 24*45 {
		maxHours = 24 * 45
	}
	hours := minHours
	if maxHours > minHours {
		hours += rng.Intn(maxHours - minHours + 1)
	}
	minutes := rng.Intn(60)
	return uint64(nowFunc().Add(-time.Duration(hours)*time.Hour - time.Duration(minutes)*time.Minute).Unix())
}

func randomMetricFloat(rng interface{ Intn(int) int }, minLow, minHigh, maxLow, maxHigh, stepLow, stepHigh float64) FloatRange {
	ret := FloatRange{
		Min:  randomFloatRange(rng, minLow, minHigh),
		Max:  randomFloatRange(rng, maxLow, maxHigh),
		Step: randomFloatRange(rng, stepLow, stepHigh),
	}
	if ret.Max < ret.Min {
		ret.Max = ret.Min
	}
	return ret
}

func randomMetricUint(rng interface{ Intn(int) int }, minLow, minHigh, maxLow, maxHigh, stepLow, stepHigh uint64) UintRange {
	ret := UintRange{
		Min:  randomUintRange(rng, minLow, minHigh),
		Max:  randomUintRange(rng, maxLow, maxHigh),
		Step: randomUintRange(rng, stepLow, stepHigh),
	}
	if ret.Max < ret.Min {
		ret.Max = ret.Min
	}
	return ret
}

func randomFloatRange(rng interface{ Intn(int) int }, min, max float64) float64 {
	if max <= min {
		return min
	}
	const scale = 100
	imin := int(min * scale)
	imax := int(max * scale)
	if imax <= imin {
		return min
	}
	return float64(imin+rng.Intn(imax-imin+1)) / scale
}

func randomUintRange(rng interface{ Intn(int) int }, min, max uint64) uint64 {
	if max <= min {
		return min
	}
	return min + uint64(rng.Intn(int(max-min+1)))
}

func pickWeightedString(rng interface{ Intn(int) int }, values []string, weights []int) string {
	return values[pickWeightedInt(rng, weights)]
}

func pickWeightedUint64(rng interface{ Intn(int) int }, values []uint64, weights []int) uint64 {
	return values[pickWeightedInt(rng, weights)]
}

func pickWeightedInt(rng interface{ Intn(int) int }, weights []int) int {
	total := 0
	for _, weight := range weights {
		if weight > 0 {
			total += weight
		}
	}
	if total == 0 {
		return 0
	}
	draw := rng.Intn(total)
	acc := 0
	for i, weight := range weights {
		if weight <= 0 {
			continue
		}
		acc += weight
		if draw < acc {
			return i
		}
	}
	return len(weights) - 1
}

func defaultTemperatureNames(gpu []string) []string {
	names := []string{"CPU Package"}
	switch len(gpu) {
	case 0:
		return names
	case 1:
		return append(names, "GPU Core")
	default:
		for i := range gpu {
			names = append(names, fmt.Sprintf("GPU Core %d", i+1))
		}
		return names
	}
}

func (h Host) HasIPv4() bool {
	return h.IPv4 != ""
}

func deterministicUUID(input string) string {
	sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(input))))
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	ret := make([]string, len(values))
	copy(ret, values)
	return ret
}

func (s *sizeGB) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		value, err := parseSizeGB(node.Value)
		if err != nil {
			return err
		}
		*s = sizeGB(value)
		return nil
	}
	return fmt.Errorf("must be a string GB value like 40GB")
}

func parseSizeGB(value string) (uint64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("must not be empty")
	}

	normalized := strings.ToUpper(trimmed)
	if !strings.HasSuffix(normalized, "GB") {
		return 0, fmt.Errorf("must use GB suffix like 40GB")
	}
	normalized = strings.TrimSpace(strings.TrimSuffix(normalized, "GB"))

	size, err := strconv.ParseUint(normalized, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("must be a GB value like 40GB: %w", err)
	}
	return size, nil
}
