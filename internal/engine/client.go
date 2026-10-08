package engine

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"
)

// dialResult is the outcome of one dial in dialRace: a connection or an error
type dialResult struct {
	conn net.Conn
	err  error
}

// dialTimeout bounds a single TCP connection attempt to one IP
const dialTimeout = 10 * time.Second

// dialOne dials a single address and delivers the outcome to results. If the
// race is already over (ctx cancelled) it abandons the attempt, closing any
// connection it managed to open so nothing leaks.
func dialOne(ctx context.Context, dialer *net.Dialer, network, address string, results chan<- dialResult) {
	conn, err := dialer.DialContext(ctx, network, address)
	select {
	case results <- dialResult{conn, err}:
	case <-ctx.Done():
		if conn != nil {
			conn.Close()
		}
	}
}

// dialRace is a DialContext that connects to a host by racing ALL of its
// resolved IP addresses in parallel, returning the first connection to succeed
// and cancelling the rest
func dialRace(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan dialResult)
	dialer := net.Dialer{Timeout: dialTimeout}

	for _, ip := range ips {
		go dialOne(ctx, &dialer, network, net.JoinHostPort(ip.IP.String(), port), results)
	}

	var firstErr error
	for range ips {
		r := <-results
		if r.err == nil {
			return r.conn, nil
		}
		if firstErr == nil {
			firstErr = r.err
		}
	}

	return nil, firstErr
}

// newClient returns a new http.Client which is then attached to a worker
func newClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:         dialRace,
			MaxIdleConnsPerHost: 1,
			DisableKeepAlives:   false,
			IdleConnTimeout:     10 * time.Second,
			ForceAttemptHTTP2:   false,
			TLSNextProto:        map[string]func(authority string, c *tls.Conn) http.RoundTripper{},
			ReadBufferSize:      64 << 10,
			WriteBufferSize:     64 << 10,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			if len(via) > 0 {
				req.Header.Set("Range", via[0].Header.Get("Range"))
			}
			return nil
		},
	}
}
