package miners

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/goasic/goasic/pkg/rpc"
)

// ── Factory ───────────────────────────────────────────────────────────────────

// Factory auto-detects the miner type at the given IP and returns the correct driver.
//
// Detection order:
//  1. Port 4028 (cgminer RPC) — Antminer / Whatsminer / Avalonminer / U3
//  2. Port 8080 (IceRiver REST API)
//  3. Port 80 (HTTP web-UI fingerprint)
func Detect(ctx context.Context, ip string) (Miner, error) {
	if err := validateIP(ip); err != nil {
		return nil, err
	}

	probe := 600 * time.Millisecond

	// ── 1. cgminer RPC port 4028 ─────────────────────────────────────────
	if portOpen(ctx, ip, 4028, probe) {
		cli, err := rpc.New(ip, 4028, 3)
		if err == nil {
			if sum, err := cli.Send(ctx, "summary", nil); err == nil {
				desc := extractDescription(sum)
				d := strings.ToLower(desc)
				switch {
				case strings.Contains(d, "u3") || strings.Contains(d, "ultra"):
					return NewU3Miner(ip)
				case strings.Contains(d, "whatsminer") || strings.Contains(d, "microbt"):
					return NewWhatsminer(ip)
				case strings.Contains(d, "antminer") || strings.Contains(d, "bitmain"):
					return NewAntminer(ip)
				case strings.Contains(d, "avalon") || strings.Contains(d, "canaan"):
					return NewAvalonminer(ip)
				}
			}
		}
		log.Printf("factory: port 4028 open on %s but vendor unknown — defaulting to Antminer", ip)
		return NewAntminer(ip)
	}

	// ── 2. IceRiver REST port 8080 ────────────────────────────────────────
	if portOpen(ctx, ip, 8080, probe) {
		c := &http.Client{Timeout: 2 * time.Second}
		url := fmt.Sprintf("http://%s:8080/api/v1/summary", ip)
		if resp, err := c.Get(url); err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			resp.Body.Close()
			low := strings.ToLower(string(body))
			if strings.Contains(low, "iceriver") || strings.Contains(low, "hashrate") {
				return NewIceRiver(ip)
			}
		}
	}

	// ── 3. HTTP web-UI fingerprint port 80 ────────────────────────────────
	c := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if resp, err := c.Get(fmt.Sprintf("http://%s", ip)); err == nil {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		low := strings.ToLower(string(body))
		switch {
		case strings.Contains(low, "iceriver") || strings.Contains(low, "ks miner"):
			return NewIceRiver(ip)
		case strings.Contains(low, "u3") && (strings.Contains(low, "hyd") || strings.Contains(low, "hydro")):
			return NewU3Miner(ip)
		case strings.Contains(low, "antminer") || strings.Contains(low, "bitmain"):
			return NewAntminer(ip)
		case strings.Contains(low, "whatsminer") || strings.Contains(low, "microbt"):
			return NewWhatsminer(ip)
		case strings.Contains(low, "avalon") || strings.Contains(low, "canaan"):
			return NewAvalonminer(ip)
		}
	}

	return nil, fmt.Errorf("factory: no miner detected at %s", ip)
}

// extractDescription pulls the Description or Type field from a cgminer STATUS/SUMMARY response.
func extractDescription(m map[string]interface{}) string {
	for _, key := range []string{"STATUS", "SUMMARY"} {
		if arr := rpc.GetArray(m, key); len(arr) > 0 {
			if item, ok := arr[0].(map[string]interface{}); ok {
				for _, f := range []string{"Description", "Type"} {
					if s := rpc.GetString(item, f); s != "" {
						return s
					}
				}
			}
		}
	}
	return ""
}

// ── IP validation ─────────────────────────────────────────────────────────────

func validateIP(ip string) error {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("miners: %q is not a valid IP address", ip)
	}
	if parsed.IsLoopback() || parsed.IsUnspecified() || parsed.IsMulticast() {
		return fmt.Errorf("miners: %q is not a routable miner IP", ip)
	}
	return nil
}

// ── Port probe ────────────────────────────────────────────────────────────────

func portOpen(ctx context.Context, ip string, port int, timeout time.Duration) bool {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", ip, port))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ── TLS helper ────────────────────────────────────────────────────────────────

// insecureTLS returns a TLS config that accepts self-signed certs.
// Miner firmware ships with self-signed certificates and we cannot
// provision real CA-signed certs for LAN devices.
func insecureTLS() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec
}
