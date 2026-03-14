// Package rpc implements the cgminer JSON-RPC client used by Antminer,
// Whatsminer, Avalonminer, and U3 miners (all use TCP port 4028).
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"time"
)

const maxResponseBytes = 1 * 1024 * 1024 // 1 MiB hard cap

// Client is a cgminer JSON-RPC client.
type Client struct {
	addr    string // "ip:port"
	timeout time.Duration
}

// New creates a new RPC client.
// ip must be a valid IP address string (no hostnames).
func New(ip string, port int, timeoutSecs int) (*Client, error) {
	if err := ValidateIP(ip); err != nil {
		return nil, err
	}
	return &Client{
		addr:    fmt.Sprintf("%s:%d", ip, port),
		timeout: time.Duration(timeoutSecs) * time.Second,
	}, nil
}

// Send issues a cgminer RPC command and returns the parsed response.
// params may be nil or a map/struct that will be marshalled into "parameter".
func (c *Client) Send(ctx context.Context, command string, params interface{}) (map[string]interface{}, error) {
	if !validCommand(command) {
		return nil, fmt.Errorf("rpc: invalid command %q (alphanumeric + underscore only)", command)
	}

	payload := map[string]interface{}{"command": command}
	if params != nil {
		payload["parameter"] = params
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("rpc: marshal error: %w", err)
	}

	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, fmt.Errorf("rpc: connect to %s failed: %w", c.addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(c.timeout))

	if _, err = conn.Write(body); err != nil {
		return nil, fmt.Errorf("rpc: write to %s failed: %w", c.addr, err)
	}
	// Signal end-of-write on some firmware
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.CloseWrite()
	}

	buf, err := io.ReadAll(io.LimitReader(conn, int64(maxResponseBytes)))
	if err != nil {
		return nil, fmt.Errorf("rpc: read from %s failed: %w", c.addr, err)
	}
	if len(buf) == maxResponseBytes {
		return nil, fmt.Errorf("rpc: response exceeded %d bytes — possible rogue device", maxResponseBytes)
	}

	cleaned := cleanJSON(string(buf))
	var result map[string]interface{}
	if err = json.Unmarshal([]byte(cleaned), &result); err != nil {
		preview := cleaned
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return nil, fmt.Errorf("rpc: parse error from %s: %w | raw: %s", c.addr, err, preview)
	}
	return result, nil
}

// Addr returns the address string "ip:port".
func (c *Client) Addr() string { return c.addr }

// IP returns just the IP portion of the address.
func (c *Client) IP() string {
	host, _, _ := net.SplitHostPort(c.addr)
	return host
}

// cleanJSON removes null bytes and trailing commas before } or ].
func cleanJSON(s string) string {
	// Remove null bytes
	s = strings.ReplaceAll(s, "\x00", "")
	// Remove trailing commas before closing braces/brackets
	re := regexp.MustCompile(`,\s*([}\]])`)
	return re.ReplaceAllString(s, "$1")
}

var validCmdRe = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

func validCommand(cmd string) bool {
	return validCmdRe.MatchString(cmd)
}

// ValidateIP checks that the string is a routable IP (not hostname, loopback, multicast).
func ValidateIP(ip string) error {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("rpc: %q is not a valid IP address (hostnames not accepted)", ip)
	}
	if parsed.IsLoopback() {
		return fmt.Errorf("rpc: loopback address %q is not a valid miner IP", ip)
	}
	if parsed.IsUnspecified() {
		return fmt.Errorf("rpc: unspecified address %q is not a valid miner IP", ip)
	}
	if parsed.IsMulticast() {
		return fmt.Errorf("rpc: multicast address %q is not a valid miner IP", ip)
	}
	return nil
}

// PortOpen returns true if the TCP port at ip:port accepts connections within timeout.
func PortOpen(ctx context.Context, ip string, port int, timeout time.Duration) bool {
	addr := fmt.Sprintf("%s:%d", ip, port)
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ── JSON helpers ──────────────────────────────────────────────────────────────

// GetString navigates a JSON map path and returns the string value.
// path is a sequence of keys, e.g. GetString(m, "SUMMARY", "0", "GHS 5s").
func GetString(m map[string]interface{}, keys ...string) string {
	v := navigate(m, keys...)
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// GetFloat navigates a JSON map and returns a float64.
func GetFloat(m map[string]interface{}, keys ...string) (float64, bool) {
	v := navigate(m, keys...)
	if v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// GetInt navigates and returns an int.
func GetInt(m map[string]interface{}, keys ...string) (int, bool) {
	f, ok := GetFloat(m, keys...)
	return int(f), ok
}

// GetArray navigates and returns a slice.
func GetArray(m map[string]interface{}, keys ...string) []interface{} {
	v := navigate(m, keys...)
	if v == nil {
		return nil
	}
	a, _ := v.([]interface{})
	return a
}

// GetMap navigates and returns a sub-map.
func GetMap(m map[string]interface{}, keys ...string) map[string]interface{} {
	v := navigate(m, keys...)
	if v == nil {
		return nil
	}
	sub, _ := v.(map[string]interface{})
	return sub
}

// GetArrayItem returns item i from a JSON array field.
func GetArrayItem(m map[string]interface{}, key string, i int) map[string]interface{} {
	arr := GetArray(m, key)
	if i >= len(arr) {
		return nil
	}
	item, _ := arr[i].(map[string]interface{})
	return item
}

func navigate(m map[string]interface{}, keys ...string) interface{} {
	var cur interface{} = m
	for _, k := range keys {
		switch c := cur.(type) {
		case map[string]interface{}:
			cur = c[k]
		case []interface{}:
			idx := 0
			fmt.Sscanf(k, "%d", &idx)
			if idx < len(c) {
				cur = c[idx]
			} else {
				return nil
			}
		default:
			return nil
		}
	}
	return cur
}
