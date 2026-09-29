// Command stress is the operator tool that triggers the test application's
// internal CPU stress (POST /admin/stress) on one or more instances. It runs
// on the controller host and calls the instances' private IPs directly; it
// is deliberately a separate binary from the controller, which never
// generates its own load (docs/spec/app.md §3, REQ-CONSTRAINT-5).
//
// Usage:
//
//	stress -targets 10.0.1.12,10.0.2.34 [-port 8080] [-duration 5m]
//
// Only private IP addresses are accepted, so the tool can never target the
// public load balancer. It exits with status 1 if any target rejected the
// request.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const requestTimeout = 5 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, http.DefaultTransport)
	stop()
	os.Exit(code)
}

type result struct {
	target string
	status int
	body   string
	err    error
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, transport http.RoundTripper) int {
	fs := flag.NewFlagSet("stress", flag.ContinueOnError)
	fs.SetOutput(stderr)
	targetsFlag := fs.String("targets", "", "comma-separated private IPs of the instances to stress (required)")
	port := fs.Int("port", 8080, "test application port")
	duration := fs.Duration("duration", 5*time.Minute, "stress duration per instance")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	targets, err := parseTargets(*targetsFlag)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *port < 1 || *port > 65535 {
		fmt.Fprintf(stderr, "invalid port %d\n", *port)
		return 2
	}
	if *duration <= 0 {
		fmt.Fprintln(stderr, "duration must be positive")
		return 2
	}

	client := &http.Client{Timeout: requestTimeout, Transport: transport}
	results := make([]result, len(targets))
	var wg sync.WaitGroup
	for i, ip := range targets {
		wg.Go(func() { results[i] = trigger(ctx, client, ip, *port, *duration) })
	}
	wg.Wait()

	failed := 0
	for _, r := range results {
		if r.err != nil {
			fmt.Fprintf(stdout, "%-16s ERROR %v\n", r.target, r.err)
			failed++
			continue
		}
		if r.status != http.StatusAccepted {
			failed++
		}
		fmt.Fprintf(stdout, "%-16s %d %s\n", r.target, r.status, strings.TrimSpace(r.body))
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// parseTargets accepts only private IP addresses.
func parseTargets(raw string) ([]netip.Addr, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("-targets is required, e.g. -targets 10.0.1.12,10.0.2.34")
	}
	var out []netip.Addr
	for part := range strings.SplitSeq(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ip, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("target %q is not an IP address: %w", part, err)
		}
		if !ip.IsPrivate() {
			return nil, fmt.Errorf("target %s is not a private address; stress must never go through the public load balancer", ip)
		}
		out = append(out, ip)
	}
	if len(out) == 0 {
		return nil, errors.New("-targets is empty")
	}
	return out, nil
}

func trigger(ctx context.Context, client *http.Client, ip netip.Addr, port int, d time.Duration) result {
	target := ip.String()
	u := url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort(target, strconv.Itoa(port)),
		Path:     "/admin/stress",
		RawQuery: url.Values{"duration": {d.String()}}.Encode(),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return result{target: target, err: err}
	}
	resp, err := client.Do(req)
	if err != nil {
		return result{target: target, err: err}
	}
	defer func() { _ = resp.Body.Close() }() // read-only response; close error is not actionable
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return result{target: target, err: fmt.Errorf("read response: %w", err)}
	}
	return result{target: target, status: resp.StatusCode, body: string(body)}
}
