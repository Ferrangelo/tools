// codex-status reads ChatGPT Codex quotas without starting a model turn.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type window struct {
	UsedPercent float64 `json:"usedPercent"`
	Minutes     *int    `json:"windowDurationMins"`
	ResetsAt    *int64  `json:"resetsAt"`
}

type credits struct {
	HasCredits bool   `json:"hasCredits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance"`
}

type bucket struct {
	Name      string   `json:"limitName"`
	PlanType  string   `json:"planType"`
	Primary   *window  `json:"primary"`
	Secondary *window  `json:"secondary"`
	Credits   *credits `json:"credits"`
}

func readLimits() (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--listen", "stdio://")
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	encoder, decoder := json.NewEncoder(stdin), json.NewDecoder(stdout)
	send := func(id int, method string, params any) error {
		message := map[string]any{"method": method}
		if id != 0 {
			message["id"] = id
		}
		if params != nil {
			message["params"] = params
		}
		return encoder.Encode(message)
	}
	response := func(id int) (json.RawMessage, error) {
		for {
			var message struct {
				ID     *int            `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := decoder.Decode(&message); err != nil {
				if ctx.Err() != nil {
					return nil, errors.New("request timed out after 30 seconds")
				}
				if errors.Is(err, io.EOF) {
					return nil, errors.New("Codex app-server closed the connection")
				}
				return nil, err
			}
			if message.ID == nil || *message.ID != id {
				continue
			}
			if message.Error != nil {
				return nil, errors.New(message.Error.Message)
			}
			if len(message.Result) == 0 {
				return nil, errors.New("Codex returned no result")
			}
			return message.Result, nil
		}
	}
	if err := send(1, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "codex_status", "version": "1.0.0"},
	}); err != nil {
		return nil, err
	}
	if _, err := response(1); err != nil {
		return nil, err
	}
	if err := send(0, "initialized", map[string]any{}); err != nil {
		return nil, err
	}
	if err := send(2, "account/rateLimits/read", nil); err != nil {
		return nil, err
	}
	return response(2)
}

// Output style. Colour is on only for a terminal without NO_COLOR, and the
// bar characters fall back to ASCII when the locale is not UTF-8.
var useColor, useUTF8 bool

func sgr(code string) string {
	if useColor {
		return "\033[" + code + "m"
	}
	return ""
}

func sep() string {
	if useUTF8 {
		return " · "
	}
	return " - "
}

func levelColor(pct float64) string {
	switch {
	case pct >= 80:
		return sgr("31")
	case pct >= 50:
		return sgr("33")
	}
	return sgr("32")
}

func detectStyle() {
	locale := ""
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if locale = os.Getenv(name); locale != "" {
			break
		}
	}
	locale = strings.ToLower(locale)
	useUTF8 = strings.Contains(locale, "utf-8") || strings.Contains(locale, "utf8")
	info, err := os.Stdout.Stat()
	useColor = err == nil && info.Mode()&os.ModeCharDevice != 0 &&
		os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

// usageBar draws a solid bar in eighth-cell steps: full blocks in the usage
// colour over a dark grey track. The partial cell paints its empty part with
// the track colour as background, so the bar has no gap.
func usageBar(pct float64) string {
	const width = 24
	eighths := []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}
	steps := 1
	full, empty := "#", "-"
	if useUTF8 {
		steps, full, empty = 8, "█", "░"
		if useColor {
			empty = full
		}
	}
	filled := 0
	switch {
	case pct >= 100:
		filled = width * steps
	case pct > 0:
		filled = max(int(pct*float64(width*steps)/100+0.5), 1)
	}
	var b strings.Builder
	b.WriteString(levelColor(pct))
	cell := 0
	for ; cell < filled/steps; cell++ {
		b.WriteString(full)
	}
	if filled%steps != 0 {
		b.WriteString(sgr("48;5;238") + eighths[filled%steps])
		cell++
	}
	b.WriteString(sgr("0") + sgr("38;5;238"))
	for ; cell < width; cell++ {
		b.WriteString(empty)
	}
	return b.String() + sgr("0")
}

// resetText returns "resets in 4h 15m · Sat Oct 31 2026, 20:10" in local time.
func resetText(resetsAt *int64) string {
	if resetsAt == nil {
		return sgr("2") + "reset time unknown" + sgr("0")
	}
	at := time.Unix(*resetsAt, 0).Round(time.Minute).Local()
	left := int64(time.Until(at).Seconds())
	if left <= 0 {
		return "resets now"
	}
	d, h, m := left/86400, left%86400/3600, left%3600/60
	var text string
	switch {
	case d > 0:
		text = fmt.Sprintf("resets in %dd %dh", d, h)
	case h > 0:
		text = fmt.Sprintf("resets in %dh %dm", h, m)
	default:
		text = fmt.Sprintf("resets in %dm", max(m, 1))
	}
	return text + sgr("2") + sep() + at.Format("Mon Jan 02 2006, 15:04") + sgr("0")
}

func windowLabel(w *window, fallback string) string {
	if w.Minutes == nil {
		return fallback
	}
	switch minutes := *w.Minutes; {
	case minutes == 300:
		return "Current session"
	case minutes == 7*24*60:
		return "Current week"
	case minutes%(24*60) == 0:
		return fmt.Sprintf("%dd window", minutes/(24*60))
	default:
		return fmt.Sprintf("%gh window", float64(minutes)/60)
	}
}

type row struct {
	heading string // When set, the row is a bucket heading and has no bar.
	label   string
	pct     float64 // Negative: the row has no bar.
	detail  string
}

func display(raw json.RawMessage) error {
	var result struct {
		Limits  *bucket           `json:"rateLimits"`
		Buckets map[string]bucket `json:"rateLimitsByLimitId"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if len(result.Buckets) == 0 && result.Limits != nil {
		result.Buckets = map[string]bucket{"codex": *result.Limits}
	}
	keys := make([]string, 0, len(result.Buckets))
	for key := range result.Buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var rows []row
	plan := ""
	for _, key := range keys {
		b := result.Buckets[key]
		if plan == "" && b.PlanType != "" {
			plan = strings.ToUpper(b.PlanType[:1]) + b.PlanType[1:]
		}
		if len(keys) > 1 {
			name := b.Name
			if name == "" {
				name = key
			}
			rows = append(rows, row{heading: name})
		}
		for i, w := range []*window{b.Primary, b.Secondary} {
			if w != nil {
				label := windowLabel(w, []string{"Primary", "Secondary"}[i])
				rows = append(rows, row{label: label, pct: w.UsedPercent, detail: resetText(w.ResetsAt)})
			}
		}
		if c := b.Credits; c != nil && (c.HasCredits || c.Unlimited) {
			detail := c.Balance + sgr("2") + " left" + sgr("0")
			if c.Unlimited {
				detail = "unlimited"
			}
			rows = append(rows, row{label: "Credits", pct: -1, detail: detail})
		}
	}

	fmt.Print(sgr("1") + "Codex usage" + sgr("0"))
	if plan != "" {
		fmt.Print(sgr("2") + sep() + plan + " plan" + sgr("0"))
	}
	fmt.Print("\n\n")
	if len(rows) == 0 {
		fmt.Println("No ChatGPT usage limits returned. Check: codex login status")
		return nil
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r.label))
	}
	for i, r := range rows {
		if r.heading != "" {
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(sgr("1") + r.heading + sgr("0"))
			continue
		}
		fmt.Printf("  %-*s  ", width, r.label)
		switch {
		case r.pct < 0:
			fmt.Print(strings.Repeat(" ", 24+9))
		case r.pct > 0 && r.pct < 1:
			fmt.Printf("%s  %s <1%%%s   ", usageBar(r.pct), sgr("1")+levelColor(r.pct), sgr("0"))
		default:
			fmt.Printf("%s  %s%3.0f%%%s   ", usageBar(r.pct), sgr("1")+levelColor(r.pct), r.pct, sgr("0"))
		}
		fmt.Println(r.detail)
	}
	return nil
}

func run() error {
	rawOutput := flag.Bool("json", false, "Print the raw quota response")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: codex-status [--json]\n\n"+
			"Prints ChatGPT Codex usage limits.\n")
		flag.PrintDefaults()
		fmt.Fprintln(flag.CommandLine.Output(), "\nSet NO_COLOR=1 to turn off colours.")
	}
	flag.Parse()
	detectStyle()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flag.Args(), " "))
	}
	raw, err := readLimits()
	if err != nil {
		return err
	}
	if *rawOutput {
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, raw, "", "  "); err != nil {
			return err
		}
		fmt.Println(formatted.String())
		return nil
	}
	return display(raw)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "codex-status:", err)
		os.Exit(1)
	}
}
