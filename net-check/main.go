// net-check reports independent network diagnostics.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type options struct {
	timeout          time.Duration
	target, host, ip string
}
type route struct {
	Type    string `json:"type"`
	Dev     string `json:"dev"`
	Src     string `json:"prefsrc"`
	Gateway string `json:"gateway"`
}
type dependencies struct {
	command func(context.Context, string, ...string) ([]byte, error)
	iface   func(string) (*net.Interface, error)
	lookup  func(context.Context, string) ([]net.IPAddr, error)
	dial    func(context.Context, string, string) (net.Conn, error)
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("net-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.DurationVar(&o.timeout, "timeout", 3*time.Second, "timeout for each diagnostic operation")
	fs.StringVar(&o.target, "target", "1.1.1.1:443", "numeric IP and TCP port")
	fs.StringVar(&o.host, "dns-host", "example.com", "hostname for system name resolution")
	fs.Usage = func() { fmt.Fprintln(stderr, "Usage: net-check [options]"); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() != 0 {
		return o, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if o.timeout <= 0 {
		return o, errors.New("timeout must be positive")
	}
	host, port, err := net.SplitHostPort(o.target)
	if err != nil || net.ParseIP(host) == nil {
		return o, errors.New("target must contain a numeric IP and port")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return o, errors.New("target port must be between 1 and 65535")
	}
	if o.host == "" {
		return o, errors.New("dns-host must not be empty")
	}
	o.ip = host
	return o, nil
}
func parseRoute(data []byte) (route, error) {
	var routes []route
	if err := json.Unmarshal(data, &routes); err != nil {
		return route{}, fmt.Errorf("invalid route output: %w", err)
	}
	for _, r := range routes {
		if r.Type != "" && r.Type != "unicast" && r.Type != "local" {
			continue
		}
		if r.Dev == "" || (r.Src != "" && net.ParseIP(r.Src) == nil) || (r.Gateway != "" && net.ParseIP(r.Gateway) == nil) {
			continue
		}
		return r, nil
	}
	return route{}, errors.New("no usable route in ip output")
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if left := 8192 - b.Len(); left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
func command(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, stderr limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return nil, fmt.Errorf("%s: %w: %s", name, err, s)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out.Bytes(), nil
}
func defaults() dependencies {
	return dependencies{command, net.InterfaceByName, net.DefaultResolver.LookupIPAddr, (&net.Dialer{}).DialContext}
}

// boundedLookup returns at the deadline even if a platform resolver ignores cancellation.
func boundedLookup(ctx context.Context, lookup func(context.Context, string) ([]net.IPAddr, error), host string) ([]net.IPAddr, error) {
	type result struct {
		ips []net.IPAddr
		err error
	}
	ch := make(chan result, 1)
	go func() { ips, err := lookup(ctx, host); ch <- result{ips, err} }()
	select {
	case r := <-ch:
		return r.ips, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func diagnose(o options, d dependencies, out io.Writer) int {
	failed := false
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	data, err := d.command(ctx, "ip", "-j", "route", "get", o.ip)
	cancel()
	var r route
	if err == nil {
		r, err = parseRoute(data)
	}
	if err == nil {
		_, err = d.iface(r.Dev)
		if err != nil {
			err = fmt.Errorf("interface %s: %w", r.Dev, err)
		}
	}
	if err != nil {
		fmt.Fprintf(out, "FAIL local: %v\n", err)
		failed = true
	} else {
		fmt.Fprintf(out, "OK local: interface %s", r.Dev)
		if r.Src != "" {
			fmt.Fprintf(out, ", source %s", r.Src)
		}
		fmt.Fprintln(out)
	}
	if err != nil {
		fmt.Fprintln(out, "SKIP gateway: route unavailable")
	} else if r.Gateway == "" {
		fmt.Fprintln(out, "SKIP gateway: direct route or tunnel")
	} else {
		family := "-4"
		gateway := net.ParseIP(r.Gateway)
		if gateway.To4() == nil {
			family = "-6"
		}
		args := []string{family, "-n", "-c", "1"}
		if gateway.To4() == nil && gateway.IsLinkLocalUnicast() {
			args = append(args, "-I", r.Dev)
		}
		args = append(args, r.Gateway)
		ctx, cancel = context.WithTimeout(context.Background(), o.timeout)
		_, e := d.command(ctx, "ping", args...)
		cancel()
		if e != nil {
			fmt.Fprintf(out, "WARN gateway: %s: %v (ICMP may be filtered)\n", r.Gateway, e)
		} else {
			fmt.Fprintf(out, "OK gateway: %s responded\n", r.Gateway)
		}
	}
	ctx, cancel = context.WithTimeout(context.Background(), o.timeout)
	ips, e := boundedLookup(ctx, d.lookup, o.host)
	cancel()
	if e != nil {
		fmt.Fprintf(out, "FAIL DNS: system name resolution for %s: %v\n", o.host, e)
		failed = true
	} else if len(ips) == 0 {
		fmt.Fprintf(out, "FAIL DNS: system name resolution for %s returned no addresses\n", o.host)
		failed = true
	} else {
		addresses := make([]string, len(ips))
		for i, ip := range ips {
			addresses[i] = ip.String()
		}
		fmt.Fprintf(out, "OK DNS: system name resolution for %s -> %s\n", o.host, strings.Join(addresses, ", "))
	}
	ctx, cancel = context.WithTimeout(context.Background(), o.timeout)
	start := time.Now()
	conn, e := d.dial(ctx, "tcp", o.target)
	elapsed := time.Since(start)
	cancel()
	if e != nil {
		fmt.Fprintf(out, "FAIL internet: TCP %s: %v\n", o.target, e)
		failed = true
	} else {
		_ = conn.Close()
		fmt.Fprintf(out, "OK internet: TCP %s connected in %s (endpoint only)\n", o.target, elapsed.Round(time.Millisecond))
	}
	if failed {
		return 1
	}
	return 0
}
func run(args []string, out, stderr io.Writer, d dependencies) int {
	o, err := parseOptions(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "net-check: %v\n", err)
		return 2
	}
	return diagnose(o, d, out)
}
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, defaults())) }
