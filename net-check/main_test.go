package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOptions(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{{nil, 0}, {[]string{"-h"}, 0}, {[]string{"-timeout=0"}, 2}, {[]string{"extra"}, 2}, {[]string{"-target=example.com:443"}, 2}, {[]string{"-target=1.1.1.1:0"}, 2}, {[]string{"-target=[::1]:443"}, 0}} {
		o, e := parseOptions(tc.args, &bytes.Buffer{})
		if len(tc.args) > 0 && tc.args[0] == "-h" {
			if e == nil {
				t.Fatal("help")
			}
			continue
		}
		if (e != nil) != (tc.code == 2) {
			t.Fatalf("%v: %v", tc.args, e)
		}
		if e == nil && o.timeout <= 0 {
			t.Fatal(o)
		}
	}
}
func TestRoutes(t *testing.T) {
	for _, tc := range []struct {
		json string
		ok   bool
	}{{`[{"dev":"eth0","gateway":"192.0.2.1","prefsrc":"192.0.2.2"}]`, true}, {`[{"dev":"tun0","gateway":"2001:db8::1","table":100}]`, true}, {`[{"dev":"wg0"}]`, true}, {`[{"type":"unreachable","dev":"x"}]`, false}, {`[{"type":"blackhole"}]`, false}, {`[{"type":"prohibit"}]`, false}, {`[{"dev":"x","gateway":"bad"}]`, false}, {`[]`, false}, {`oops`, false}} {
		_, err := parseRoute([]byte(tc.json))
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.json, err)
		}
	}
}
func fakeDeps() dependencies {
	return dependencies{
		command: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if _, ok := ctx.Deadline(); !ok {
				panic("no deadline")
			}
			if name == "ip" {
				return []byte(`[{"dev":"lo","gateway":"127.0.0.1"}]`), nil
			}
			return nil, nil
		},
		iface: func(string) (*net.Interface, error) { return &net.Interface{}, nil },
		lookup: func(ctx context.Context, s string) ([]net.IPAddr, error) {
			if _, ok := ctx.Deadline(); !ok {
				panic("no deadline")
			}
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		},
		dial: func(ctx context.Context, n, a string) (net.Conn, error) {
			if _, ok := ctx.Deadline(); !ok {
				panic("no deadline")
			}
			a1, a2 := net.Pipe()
			a2.Close()
			return a1, nil
		},
	}
}
func TestDiagnostics(t *testing.T) {
	o, _ := parseOptions(nil, &bytes.Buffer{})
	d := fakeDeps()
	d.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("fe80::1"), Zone: "eth0"}, {IP: net.ParseIP("192.0.2.1")}}, nil
	}
	var out bytes.Buffer
	if code := diagnose(o, d, &out); code != 0 || !strings.Contains(out.String(), "OK gateway") {
		t.Fatal(code, out.String())
	}
	if !strings.Contains(out.String(), "OK DNS: system name resolution for example.com -> fe80::1%eth0, 192.0.2.1\n") {
		t.Fatal(out.String())
	}
	d.command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ip" {
			return []byte(`[{"dev":"lo","gateway":"127.0.0.1"}]`), nil
		}
		return nil, errors.New("filtered")
	}
	out.Reset()
	if diagnose(o, d, &out) != 0 || !strings.Contains(out.String(), "WARN gateway") {
		t.Fatal(out.String())
	}
	d.command = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("missing ip") }
	out.Reset()
	if diagnose(o, d, &out) != 1 || !strings.Contains(out.String(), "OK DNS") || !strings.Contains(out.String(), "OK internet") || !strings.Contains(out.String(), "SKIP gateway") {
		t.Fatal(out.String())
	}
	d = fakeDeps()
	d.lookup = func(context.Context, string) ([]net.IPAddr, error) { return nil, nil }
	d.dial = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("refused") }
	out.Reset()
	if diagnose(o, d, &out) != 1 || !strings.Contains(out.String(), "returned no addresses") || !strings.Contains(out.String(), "refused") {
		t.Fatal(out.String())
	}
}
func TestDeadlineAndLoopback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err := boundedLookup(ctx, func(context.Context, string) ([]net.IPAddr, error) {
		time.Sleep(20 * time.Millisecond)
		return nil, nil
	}, "x")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestProbeVariants(t *testing.T) {
	o, _ := parseOptions([]string{"-timeout=2ms", "-target=[::1]:443"}, &bytes.Buffer{})
	for _, problem := range []string{"missing ping", "filtered", "timeout"} {
		t.Run(problem, func(t *testing.T) {
			d := fakeDeps()
			d.command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name == "ip" {
					if strings.Join(args, " ") != "-j route get ::1" {
						t.Fatal(args)
					}
					return []byte(`[{"dev":"lo","gateway":"::1"}]`), nil
				}
				if strings.Join(args, " ") != "-6 -n -c 1 ::1" {
					t.Fatal(args)
				}
				if problem == "timeout" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return nil, errors.New(problem)
			}
			var out bytes.Buffer
			if diagnose(o, d, &out) != 0 || !strings.Contains(out.String(), "WARN gateway") {
				t.Fatal(out.String())
			}
		})
	}
	d := fakeDeps()
	d.command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`[{"dev":"tun0"}]`), nil
	}
	var out bytes.Buffer
	if diagnose(o, d, &out) != 0 || !strings.Contains(out.String(), "SKIP gateway") {
		t.Fatal(out.String())
	}
	for _, e := range []error{errors.New("resolver error"), context.DeadlineExceeded} {
		d = fakeDeps()
		d.lookup = func(context.Context, string) ([]net.IPAddr, error) { return nil, e }
		out.Reset()
		if diagnose(o, d, &out) != 1 || !strings.Contains(out.String(), e.Error()) {
			t.Fatal(out.String())
		}
	}
	d = fakeDeps()
	d.dial = func(ctx context.Context, n, a string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }
	out.Reset()
	if diagnose(o, d, &out) != 1 || !strings.Contains(out.String(), "deadline exceeded") {
		t.Fatal(out.String())
	}
}
func TestCommandErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := command(ctx, "net-check-command-that-does-not-exist")
	if err == nil {
		t.Fatal("missing command succeeded")
	}
	_, err = command(ctx, "sh", "-c", "echo diagnostic >&2; exit 1")
	if err == nil || !strings.Contains(err.Error(), "diagnostic") {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	_, err = command(ctx2, "ip")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestRunHelpAndInvalid(t *testing.T) {
	var out, stderr bytes.Buffer
	if run([]string{"-h"}, &out, &stderr, fakeDeps()) != 0 || out.Len() != 0 {
		t.Fatal(out.String(), stderr.String())
	}
	stderr.Reset()
	if run([]string{"-timeout=-1s"}, &out, &stderr, fakeDeps()) != 2 || stderr.Len() == 0 {
		t.Fatal(stderr.String())
	}
}

func TestGatewayPingArguments(t *testing.T) {
	for _, tc := range []struct {
		gateway string
		want    []string
	}{
		{"fe80::1", []string{"-6", "-n", "-c", "1", "-I", "eth0", "fe80::1"}},
		{"2001:db8::1", []string{"-6", "-n", "-c", "1", "2001:db8::1"}},
		{"192.0.2.1", []string{"-4", "-n", "-c", "1", "192.0.2.1"}},
	} {
		t.Run(tc.gateway, func(t *testing.T) {
			d := fakeDeps()
			called := false
			d.command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name == "ip" {
					return []byte(`[{"dev":"eth0","gateway":"` + tc.gateway + `"}]`), nil
				}
				called = true
				if name != "ping" || !reflect.DeepEqual(args, tc.want) {
					t.Errorf("command %s %q, want ping %q", name, args, tc.want)
				}
				return nil, nil
			}
			o, _ := parseOptions(nil, &bytes.Buffer{})
			if diagnose(o, d, &bytes.Buffer{}) != 0 || !called {
				t.Fatal("gateway ping did not succeed")
			}
		})
	}
}
