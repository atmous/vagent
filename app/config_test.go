package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadAppliesDefaultsAndNestedOverrides(t *testing.T) {
	setNowForTest(t, 1700003600)

	cfg := loadTestConfig(t, `
Linux:
  client_secret: "secret"
  server: "dashboard.example.com:5555"
  tls: true
  host:
    platform: Linux
    ipv4: "41.0.0.1"
    mem_total: 4GB
    disk_total: 80GB
    swap_total: 2GB
windows:
  client_secret: "secret"
  server: "dashboard.example.com:5555"
  tls: false
  host:
    platform: Windows
    gpu:
      - "Tesla T4"
`)

	if got, want := len(cfg.Agents), 2; got != want {
		t.Fatalf("agent count = %d, want %d", got, want)
	}

	alpha := cfg.Agents[0]
	if alpha.Name != "Linux" {
		t.Fatalf("alpha name = %q, want Linux", alpha.Name)
	}
	if alpha.Host.Platform != "Linux" {
		t.Fatalf("alpha platform = %q, want Linux", alpha.Host.Platform)
	}
	if got := alpha.Host.TemperatureNames; len(got) != 1 || got[0] != "CPU Package" {
		t.Fatalf("alpha auto temperature names = %#v", got)
	}
	if got := alpha.Host.CPU; len(got) != 1 || got[0] == "" {
		t.Fatalf("alpha inherited CPU = %#v", got)
	}
	if alpha.Host.IPv4 != "41.0.0.1" {
		t.Fatalf("alpha host ipv4 = %q, want 41.0.0.1", alpha.Host.IPv4)
	}
	if alpha.Host.MemTotal != 4*giB {
		t.Fatalf("alpha mem total = %d, want %d", alpha.Host.MemTotal, 4*giB)
	}
	if alpha.Host.DiskTotal != 80*giB {
		t.Fatalf("alpha disk total = %d, want %d", alpha.Host.DiskTotal, 80*giB)
	}
	if alpha.Host.SwapTotal != 2*giB {
		t.Fatalf("alpha swap total = %d, want %d", alpha.Host.SwapTotal, 2*giB)
	}
	if alpha.Host.BootTime == 0 || alpha.Host.BootTime >= 1700003600 {
		t.Fatalf("alpha boot time = %d, want realistic generated value", alpha.Host.BootTime)
	}
	if alpha.UUID == "" {
		t.Fatal("alpha UUID should be generated")
	}
	if alpha.Version != "build" {
		t.Fatalf("alpha version = %q, want build", alpha.Version)
	}
	if got := alpha.Metrics.CPU; got.Min <= 0 || got.Max < got.Min || got.Step <= 0 {
		t.Fatalf("alpha cpu range = %#v, want realistic generated range", got)
	}

	beta := cfg.Agents[1]
	if beta.Name != "windows" {
		t.Fatalf("beta name = %q, want windows", beta.Name)
	}
	if beta.TLS {
		t.Fatal("beta TLS should be false")
	}
	if beta.Host.Platform != "Windows" {
		t.Fatalf("beta platform = %q, want Windows", beta.Host.Platform)
	}
	if got := beta.Host.TemperatureNames; len(got) != 2 || got[0] != "CPU Package" || got[1] != "GPU Core" {
		t.Fatalf("beta auto temperature names = %#v", got)
	}
	if beta.ReportDelay != 3*time.Second {
		t.Fatalf("beta report delay = %s, want 3s", beta.ReportDelay)
	}
	if beta.Version != "build" {
		t.Fatalf("beta version = %q, want build", beta.Version)
	}
	if beta.UUID == "" {
		t.Fatal("beta UUID should be generated")
	}
	if beta.Host.BootTime == 0 || beta.Host.BootTime >= 1700003600 {
		t.Fatalf("beta boot time = %d, want realistic generated value", beta.Host.BootTime)
	}
	if beta.UUID == alpha.UUID {
		t.Fatalf("beta UUID should differ from alpha UUID, got %q", beta.UUID)
	}
}

func TestLoadSupportsMultipleServers(t *testing.T) {
	cfg := loadTestConfig(t, `
Linux:
  client_secret: "secret-a"
  server: "default.example.com:443"
  tls: true
windows:
  client_secret: "secret-b"
  server: "hk.example.com:443"
  tls: true
windows-hk:
  client_secret: "secret-b"
  server: "hk.example.com:443"
  tls: true
`)

	if got, want := len(cfg.Agents), 3; got != want {
		t.Fatalf("agent count = %d, want %d", got, want)
	}
	if cfg.Agents[0].Name != "Linux" || cfg.Agents[0].Server != "default.example.com:443" || cfg.Agents[0].ClientSecret != "secret-a" {
		t.Fatalf("first group agent = %#v", cfg.Agents[0])
	}
	if cfg.Agents[1].Name != "windows" || cfg.Agents[1].Server != "hk.example.com:443" || cfg.Agents[1].ClientSecret != "secret-b" {
		t.Fatalf("second group agent = %#v", cfg.Agents[1])
	}
	if cfg.Agents[2].Name != "windows-hk" || cfg.Agents[2].Server != "hk.example.com:443" || cfg.Agents[2].ClientSecret != "secret-b" {
		t.Fatalf("third group agent = %#v", cfg.Agents[2])
	}
}

func TestLoadRejectsInvalidHostIPv4(t *testing.T) {
	_, err := Load(cfgSourcePath(t, `
Linux:
  client_secret: "secret"
  server: "dashboard.example.com:5555"
  tls: true
  host:
    ipv4: "not-an-ip"
`), "build")
	if err == nil {
		t.Fatal("Load returned nil error, want invalid host ipv4 error")
	}
	if !strings.Contains(err.Error(), "host.ipv4 must be a valid IPv4 address") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsUnsupportedFields(t *testing.T) {
	_, err := Load(cfgSourcePath(t, `
Linux:
  client_secret: "secret"
  server: "dashboard.example.com:5555"
  tls: true
  uuid: "2e3a27f1-3f07-4ca2-bd26-bf4284f8f001"
`), "build")
	if err == nil {
		t.Fatal("Load returned nil error, want unsupported uuid error")
	}
	if !strings.Contains(err.Error(), `field "uuid" is not supported`) {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = Load(cfgSourcePath(t, `
Linux:
  client_secret: "secret"
  server: "dashboard.example.com:5555"
  tls: true
  host:
    boot_time: 1700000000
`), "build")
	if err == nil {
		t.Fatal("Load returned nil error, want unsupported host.boot_time error")
	}
	if !strings.Contains(err.Error(), `host field "boot_time" is not supported`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureCreatesDefaultConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")

	created, err := Ensure(path)
	if err != nil {
		t.Fatalf("Ensure returned error: %v", err)
	}
	if !created {
		t.Fatal("Ensure should report created=true")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(content), "Linux:") {
		t.Fatalf("default config does not contain group header: %s", string(content))
	}
	if !strings.Contains(string(content), `server: "${NZ_SERVER}"`) {
		t.Fatalf("default config does not contain NZ_SERVER placeholder: %s", string(content))
	}
	if strings.Contains(string(content), "ipv4:") {
		t.Fatalf("default config should not contain optional ipv4: %s", string(content))
	}

	created, err = Ensure(path)
	if err != nil {
		t.Fatalf("Ensure returned error on existing file: %v", err)
	}
	if created {
		t.Fatal("Ensure should not recreate an existing file")
	}
}

func TestLoadAcceptsSizeStringsWithGBSuffix(t *testing.T) {
	cfg := loadTestConfig(t, `
Linux:
  client_secret: "secret"
  server: "dashboard.example.com:5555"
  tls: true
  host:
    mem_total: 4GB
    disk_total: 80 GB
    swap_total: "2gb"
`)

	agent := cfg.Agents[0]
	if agent.Host.MemTotal != 4*giB {
		t.Fatalf("mem total = %d, want %d", agent.Host.MemTotal, 4*giB)
	}
	if agent.Host.DiskTotal != 80*giB {
		t.Fatalf("disk total = %d, want %d", agent.Host.DiskTotal, 80*giB)
	}
	if agent.Host.SwapTotal != 2*giB {
		t.Fatalf("swap total = %d, want %d", agent.Host.SwapTotal, 2*giB)
	}
}

func TestLoadRejectsBareNumericSizeValues(t *testing.T) {
	_, err := Load(cfgSourcePath(t, `
Linux:
  client_secret: "secret"
  server: "dashboard.example.com:5555"
  tls: true
  host:
    mem_total: 4
`), "build")
	if err == nil {
		t.Fatal("Load returned nil error, want invalid mem_total error")
	}
	if !strings.Contains(err.Error(), "40GB") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadGeneratesStableUUIDAndBootTime(t *testing.T) {
	setNowForTest(t, 1700003600)

	content := `
Linux:
  client_secret: "secret"
  server: "dashboard.example.com:5555"
  tls: true
`

	first := loadTestConfig(t, content).Agents[0]
	second := loadTestConfig(t, content).Agents[0]
	if first.UUID != second.UUID {
		t.Fatalf("UUID mismatch: %q vs %q", first.UUID, second.UUID)
	}
	if first.Host.BootTime != second.Host.BootTime {
		t.Fatalf("boot time mismatch: %d vs %d", first.Host.BootTime, second.Host.BootTime)
	}
}

func TestLoadRequiresTLS(t *testing.T) {
	_, err := Load(cfgSourcePath(t, `
Linux:
  client_secret: "secret"
  server: "dashboard.example.com:5555"
`), "build")
	if err == nil {
		t.Fatal("Load returned nil error, want missing tls error")
	}
	if !strings.Contains(err.Error(), "tls is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func loadTestConfig(t *testing.T, content string) *Config {
	t.Helper()

	cfg, err := Load(cfgSourcePath(t, content), "build")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	return cfg
}

func cfgSourcePath(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func setNowForTest(t *testing.T, unixSeconds int64) {
	t.Helper()

	oldNow := nowFunc
	nowFunc = func() time.Time { return time.Unix(unixSeconds, 0) }
	t.Cleanup(func() {
		nowFunc = oldNow
	})
}
