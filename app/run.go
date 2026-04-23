package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/niuniuo/vagent/nezha"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	reconnectDelay     = 5 * time.Second
	reportHostInterval = 10 * time.Minute
	rpcTimeout         = 10 * time.Second
)

type probe struct {
	cfg   Agent
	log   *log.Logger
	state *stateGen
}

type sessionReceipts struct {
	receiptCh chan *nezha.Receipt
	errCh     chan error
}

type authCredentials struct {
	clientSecret string
	clientUUID   string
}

func newProbe(cfg Agent, writer io.Writer, flags int) *probe {
	return &probe{
		cfg:   cfg,
		log:   log.New(writer, fmt.Sprintf("[%s] ", cfg.Name), flags),
		state: newStateGen(cfg),
	}
}

func (p *probe) run(ctx context.Context) {
	p.log.Printf("starting node, target=%s uuid=%s", p.cfg.Server, p.cfg.UUID)
	for {
		if err := p.runSession(ctx); err != nil && ctx.Err() == nil {
			p.log.Printf("session ended: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

func (p *probe) runSession(ctx context.Context) error {
	conn, client, err := p.dial()
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := p.reportHost(ctx, client); err != nil {
		return fmt.Errorf("report host: %w", err)
	}
	if p.cfg.Host.HasIPv4() {
		if err := p.reportIPv4(ctx, client); err != nil {
			p.log.Printf("report ipv4 failed: %v", err)
		}
	}

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := client.ReportSystemState(streamCtx)
	if err != nil {
		return fmt.Errorf("open state stream: %w", err)
	}
	receipts := startSessionReceipts(stream)

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.reportLoop(streamCtx, client, stream, receipts)
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func (p *probe) dial() (*grpc.ClientConn, nezha.NezhaServiceClient, error) {
	security := grpc.WithTransportCredentials(insecure.NewCredentials())
	if p.cfg.TLS {
		security = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
		}))
	}

	conn, err := grpc.NewClient(
		p.cfg.Server,
		security,
		grpc.WithPerRPCCredentials(&authCredentials{
			clientSecret: p.cfg.ClientSecret,
			clientUUID:   p.cfg.UUID,
		}),
	)
	if err != nil {
		return nil, nil, err
	}
	return conn, nezha.NewNezhaServiceClient(conn), nil
}

func (a *authCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{
		"client_secret": a.clientSecret,
		"client_uuid":   a.clientUUID,
	}, nil
}

func (a *authCredentials) RequireTransportSecurity() bool {
	return false
}

func (p *probe) reportLoop(ctx context.Context, client nezha.NezhaServiceClient, stream nezha.NezhaService_ReportSystemStateClient, receipts *sessionReceipts) error {
	ticker := time.NewTicker(p.cfg.ReportDelay)
	defer ticker.Stop()

	lastHostReport := time.Now()
	var ipv4Ticker *time.Ticker
	var ipv4Ch <-chan time.Time
	if p.cfg.Host.HasIPv4() {
		ipv4Ticker = time.NewTicker(defaultIPv4ReportPeriod)
		defer ipv4Ticker.Stop()
		ipv4Ch = ipv4Ticker.C
	}

	for {
		if err := p.sendState(ctx, stream, receipts); err != nil {
			return err
		}
		if time.Since(lastHostReport) >= reportHostInterval {
			if err := p.reportHost(ctx, client); err != nil {
				return err
			}
			lastHostReport = time.Now()
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-ipv4Ch:
			if err := p.reportIPv4(ctx, client); err != nil {
				p.log.Printf("report ipv4 failed: %v", err)
			}
		}
	}
}

func (p *probe) sendState(ctx context.Context, stream nezha.NezhaService_ReportSystemStateClient, receipts *sessionReceipts) error {
	if err := stream.Send(p.state.next(p.cfg.ReportDelay)); err != nil {
		return err
	}
	_, err := recvReceipt(ctx, receipts)
	return err
}

func (p *probe) reportHost(ctx context.Context, client nezha.NezhaServiceClient) error {
	_, err := withTimeoutValue(ctx, rpcTimeout, func(callCtx context.Context) (*nezha.Uint64Receipt, error) {
		return client.ReportSystemInfo2(callCtx, p.state.host())
	})
	return err
}

func (p *probe) reportIPv4(ctx context.Context, client nezha.NezhaServiceClient) error {
	resp, err := withTimeoutValue(ctx, rpcTimeout, func(callCtx context.Context) (*nezha.GeoIP, error) {
		return client.ReportGeoIP(callCtx, &nezha.GeoIP{
			Ip: &nezha.IP{
				Ipv4: p.cfg.Host.IPv4,
			},
		})
	})
	if err != nil {
		return err
	}
	if resp.GetCountryCode() != "" {
		p.log.Printf("reported ipv4 display ipv4=%s country=%s", p.cfg.Host.IPv4, resp.GetCountryCode())
	}
	return nil
}

func startSessionReceipts(stream nezha.NezhaService_ReportSystemStateClient) *sessionReceipts {
	ret := &sessionReceipts{
		receiptCh: make(chan *nezha.Receipt, 1),
		errCh:     make(chan error, 1),
	}
	go func() {
		for {
			receipt, err := stream.Recv()
			if err != nil {
				ret.errCh <- err
				return
			}
			ret.receiptCh <- receipt
		}
	}()
	return ret
}

func recvReceipt(ctx context.Context, receipts *sessionReceipts) (*nezha.Receipt, error) {
	timer := time.NewTimer(rpcTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, context.DeadlineExceeded
	case err := <-receipts.errCh:
		return nil, err
	case receipt := <-receipts.receiptCh:
		return receipt, nil
	}
}

func withTimeoutValue[T any](ctx context.Context, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return fn(callCtx)
}
