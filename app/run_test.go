package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niudea/vagent/nezha"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// fakeNezhaServer records what the probe reports and answers receipts.
type fakeNezhaServer struct {
	nezha.UnimplementedNezhaServiceServer
	mu          sync.Mutex
	hosts       []*nezha.Host
	geoIPs      []*nezha.GeoIP
	states      []*nezha.State
	metaSecrets []string
	metaUUIDs   []string
	infoErr     error // if set, ReportSystemInfo2 returns this error
}

func (f *fakeNezhaServer) ReportSystemInfo2(ctx context.Context, h *nezha.Host) (*nezha.Uint64Receipt, error) {
	if f.infoErr != nil {
		return nil, f.infoErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = append(f.hosts, h)
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		f.metaSecrets = append(f.metaSecrets, md.Get("client_secret")...)
		f.metaUUIDs = append(f.metaUUIDs, md.Get("client_uuid")...)
	}
	return &nezha.Uint64Receipt{Data: 1}, nil
}

func (f *fakeNezhaServer) ReportGeoIP(ctx context.Context, g *nezha.GeoIP) (*nezha.GeoIP, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.geoIPs = append(f.geoIPs, g)
	return &nezha.GeoIP{CountryCode: "CN"}, nil
}

func (f *fakeNezhaServer) ReportSystemState(stream nezha.NezhaService_ReportSystemStateServer) error {
	for {
		st, err := stream.Recv()
		if err != nil {
			return err
		}
		f.mu.Lock()
		f.states = append(f.states, st)
		f.mu.Unlock()
		if err := stream.Send(&nezha.Receipt{Proced: true}); err != nil {
			return err
		}
	}
}

func startFakeServer(t *testing.T, srv *fakeNezhaServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	nezha.RegisterNezhaServiceServer(s, srv)
	go s.Serve(lis)
	t.Cleanup(s.Stop)
	return lis.Addr().String()
}

func loadProbeAgent(t *testing.T, server string, tlsFlag bool) Agent {
	t.Helper()
	content := "Linux:\n  client_secret: \"testsecret123\"\n  server: \"" + server + "\"\n  tls: " + map[bool]string{true: "true", false: "false"}[tlsFlag] + "\n  host:\n    ipv4: \"41.0.0.1\"\n    mem_total: 4GB\n    disk_total: 40GB\n"
	cfg, err := Load(cfgSourcePath(t, content), "test")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg.Agents[0]
}

func TestRunSessionReportsHostGeoIPAndStates(t *testing.T) {
	fake := &fakeNezhaServer{}
	addr := startFakeServer(t, fake)
	agent := loadProbeAgent(t, addr, false)
	p := newProbe(agent, io.Discard, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.runSession(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("runSession returned early: %v", err)
	case <-time.After(6 * time.Second):
	}
	cancel()
	<-done

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.hosts) == 0 {
		t.Fatal("fake server got no ReportSystemInfo2 call")
	}
	h := fake.hosts[0]
	if h.Platform != "Linux" || h.MemTotal != 4*giB || h.DiskTotal != 40*giB {
		t.Fatalf("host payload = %+v", h)
	}
	if len(fake.geoIPs) == 0 || fake.geoIPs[0].GetIp().GetIpv4() != "41.0.0.1" {
		t.Fatalf("geoip payload = %+v", fake.geoIPs)
	}
	if len(fake.states) == 0 {
		t.Fatal("fake server got no ReportSystemState messages")
	}
	st := fake.states[0]
	if st.MemUsed == 0 || st.DiskUsed == 0 || st.Uptime == 0 {
		t.Fatalf("state payload looks empty: %+v", st)
	}
	if len(fake.metaSecrets) == 0 || fake.metaSecrets[0] != "testsecret123" {
		t.Fatalf("auth metadata secret = %v", fake.metaSecrets)
	}
	if len(fake.metaUUIDs) == 0 || fake.metaUUIDs[0] == "" {
		t.Fatalf("auth metadata uuid = %v", fake.metaUUIDs)
	}
	t.Logf("host reports=%d geoip=%d states=%d (first state cpu=%.1f mem_used=%d uptime=%d)",
		len(fake.hosts), len(fake.geoIPs), len(fake.states), st.Cpu, st.MemUsed, st.Uptime)
}

func TestRunSessionTLSHandshakeFailure(t *testing.T) {
	fake := &fakeNezhaServer{}
	addr := startFakeServer(t, fake)       // plaintext server
	agent := loadProbeAgent(t, addr, true) // probe wants TLS
	p := newProbe(agent, io.Discard, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := p.runSession(ctx)
	if err == nil {
		t.Fatal("runSession returned nil, want TLS handshake error")
	}
	t.Logf("TLS failure error: %v", err)
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "tls") && !strings.Contains(msg, "handshake") && !strings.Contains(msg, "first record does not look like a tls") && !strings.Contains(msg, "unavailab") {
		t.Fatalf("error does not look like a TLS failure: %v", err)
	}
}

func TestRunSessionAuthFailureSurfaces(t *testing.T) {
	fake := &fakeNezhaServer{infoErr: status.Error(codes.Unauthenticated, "bad client_secret")}
	addr := startFakeServer(t, fake)
	agent := loadProbeAgent(t, addr, false)
	p := newProbe(agent, io.Discard, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := p.runSession(ctx)
	if err == nil {
		t.Fatal("runSession returned nil, want Unauthenticated error")
	}
	t.Logf("auth failure error: %v", err)
	if status.Code(err) != codes.Unauthenticated {
		var st interface{ GRPCStatus() *status.Status }
		if errors.As(err, &st) {
			if st.GRPCStatus().Code() != codes.Unauthenticated {
				t.Fatalf("code = %v, want Unauthenticated", st.GRPCStatus().Code())
			}
		} else {
			t.Fatalf("error is not a gRPC status error: %v", err)
		}
	}
}

func TestDialIsLazy(t *testing.T) {
	// grpc.NewClient is lazy: dial() succeeds even for a dead server.
	agent := loadProbeAgent(t, "127.0.0.1:59999", false)
	p := newProbe(agent, io.Discard, 0)
	conn, _, err := p.dial()
	if err != nil {
		t.Fatalf("dial returned error for dead server: %v", err)
	}
	if conn == nil {
		t.Fatal("dial returned nil conn")
	}
	conn.Close()
	// Sanity: a TLS config is used when tls=true.
	agentTLS := loadProbeAgent(t, "127.0.0.1:59999", true)
	pTLS := newProbe(agentTLS, io.Discard, 0)
	conn2, _, err := pTLS.dial()
	if err != nil {
		t.Fatalf("tls dial returned error: %v", err)
	}
	conn2.Close()
	_ = tls.VersionTLS12
	_ = insecure.NewCredentials
}

func TestReconnectBackoffGrowsAndCaps(t *testing.T) {
	want := []time.Duration{
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		80 * time.Second,
		160 * time.Second,
		300 * time.Second, // capped at 5m
		300 * time.Second,
		300 * time.Second,
	}
	for i, w := range want {
		if got := reconnectBackoff(i + 1); got != w {
			t.Errorf("reconnectBackoff(%d) = %s, want %s", i+1, got, w)
		}
	}
	if got := reconnectBackoff(0); got != 5*time.Second {
		t.Errorf("reconnectBackoff(0) = %s, want 5s", got)
	}
	if got := reconnectBackoff(-3); got != 5*time.Second {
		t.Errorf("reconnectBackoff(-3) = %s, want 5s", got)
	}
}
