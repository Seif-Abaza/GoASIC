package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

// ── cleanJSON ─────────────────────────────────────────────────────────────────

func TestCleanJSON_NullBytes(t *testing.T) {
	input := "{\"key\": \"val\x00ue\"}"
	got := cleanJSON(input)
	want := "{\"key\": \"value\"}"
	if got != want {
		t.Errorf("cleanJSON null bytes: got %q, want %q", got, want)
	}
}

func TestCleanJSON_TrailingCommaInObject(t *testing.T) {
	input := `{"a":1,"b":2,}`
	got := cleanJSON(input)
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Errorf("cleanJSON trailing comma in object: result not valid JSON: %v | got: %q", err, got)
	}
}

func TestCleanJSON_TrailingCommaInArray(t *testing.T) {
	input := `{"arr":[1,2,3,]}`
	got := cleanJSON(input)
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Errorf("cleanJSON trailing comma in array: result not valid JSON: %v | got: %q", err, got)
	}
}

func TestCleanJSON_NestedTrailingCommas(t *testing.T) {
	input := `{"SUMMARY":[{"GHS 5s":100.0,"Status":"Alive",}],"id":1,}`
	got := cleanJSON(input)
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Errorf("cleanJSON nested trailing commas: result not valid JSON: %v | got: %q", err, got)
	}
}

func TestCleanJSON_AlreadyValidJSON(t *testing.T) {
	input := `{"SUMMARY":[{"GHS 5s":270000.5,"Elapsed":3600}]}`
	got := cleanJSON(input)
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Errorf("cleanJSON on valid JSON produced invalid output: %v | got: %q", err, got)
	}
}

func TestCleanJSON_Empty(t *testing.T) {
	got := cleanJSON("")
	if got != "" {
		t.Errorf("cleanJSON empty: got %q, want empty", got)
	}
}

// ── validCommand ──────────────────────────────────────────────────────────────

func TestValidCommand(t *testing.T) {
	valid := []string{"summary", "stats", "pools", "get_version", "updatePools", "update_pools",
		"get_token", "devs", "restart", "reboot", "ledOn", "ledOff", "power_off", "power_on"}
	for _, cmd := range valid {
		if !validCommand(cmd) {
			t.Errorf("validCommand(%q) = false, want true", cmd)
		}
	}

	invalid := []string{"", "rm -rf /", "summary;drop", "cmd|pipe", "has space", "../etc/passwd",
		"cmd\x00inject", "pools&extra", "test>file"}
	for _, cmd := range invalid {
		if validCommand(cmd) {
			t.Errorf("validCommand(%q) = true, want false", cmd)
		}
	}
}

// ── ValidateIP ────────────────────────────────────────────────────────────────

func TestValidateIP_Valid(t *testing.T) {
	valid := []string{
		"192.168.1.50",
		"10.0.0.1",
		"172.16.0.100",
		"192.168.100.200",
	}
	for _, ip := range valid {
		if err := ValidateIP(ip); err != nil {
			t.Errorf("ValidateIP(%q) returned unexpected error: %v", ip, err)
		}
	}
}

func TestValidateIP_Loopback(t *testing.T) {
	loopbacks := []string{"127.0.0.1", "127.0.0.2", "::1"}
	for _, ip := range loopbacks {
		if err := ValidateIP(ip); err == nil {
			t.Errorf("ValidateIP(%q) should have returned error for loopback", ip)
		}
	}
}

func TestValidateIP_Multicast(t *testing.T) {
	if err := ValidateIP("224.0.0.1"); err == nil {
		t.Error("ValidateIP(multicast) should have returned error")
	}
}

func TestValidateIP_Unspecified(t *testing.T) {
	if err := ValidateIP("0.0.0.0"); err == nil {
		t.Error("ValidateIP(0.0.0.0) should have returned error")
	}
}

func TestValidateIP_Hostname(t *testing.T) {
	if err := ValidateIP("miner.local"); err == nil {
		t.Error("ValidateIP(hostname) should have returned error")
	}
}

func TestValidateIP_Empty(t *testing.T) {
	if err := ValidateIP(""); err == nil {
		t.Error("ValidateIP('') should have returned error")
	}
}

// ── JSON navigation helpers ───────────────────────────────────────────────────

func TestGetString(t *testing.T) {
	m := map[string]interface{}{
		"STATUS": []interface{}{
			map[string]interface{}{"Description": "BMMiner 2.0"},
		},
	}
	got := GetString(m, "STATUS", "0", "Description")
	if got != "BMMiner 2.0" {
		t.Errorf("GetString = %q, want 'BMMiner 2.0'", got)
	}
}

func TestGetString_Missing(t *testing.T) {
	m := map[string]interface{}{"key": "val"}
	got := GetString(m, "missing")
	if got != "" {
		t.Errorf("GetString on missing key = %q, want empty string", got)
	}
}

func TestGetFloat(t *testing.T) {
	m := map[string]interface{}{
		"SUMMARY": []interface{}{
			map[string]interface{}{"GHS 5s": 270000.5},
		},
	}
	v, ok := GetFloat(m, "SUMMARY", "0", "GHS 5s")
	if !ok {
		t.Error("GetFloat returned ok=false")
	}
	if v != 270000.5 {
		t.Errorf("GetFloat = %v, want 270000.5", v)
	}
}

func TestGetFloat_Missing(t *testing.T) {
	m := map[string]interface{}{}
	_, ok := GetFloat(m, "SUMMARY", "0", "GHS 5s")
	if ok {
		t.Error("GetFloat on missing path should return ok=false")
	}
}

func TestGetArray(t *testing.T) {
	m := map[string]interface{}{
		"POOLS": []interface{}{
			map[string]interface{}{"URL": "stratum+tcp://pool:3333"},
		},
	}
	arr := GetArray(m, "POOLS")
	if len(arr) != 1 {
		t.Errorf("GetArray len = %d, want 1", len(arr))
	}
}

func TestGetArrayItem(t *testing.T) {
	m := map[string]interface{}{
		"DEVS": []interface{}{
			map[string]interface{}{"Temperature": 65.0},
			map[string]interface{}{"Temperature": 68.0},
		},
	}
	item := GetArrayItem(m, "DEVS", 1)
	if item == nil {
		t.Fatal("GetArrayItem returned nil")
	}
	temp, ok := GetFloat(item, "Temperature")
	if !ok || temp != 68.0 {
		t.Errorf("GetArrayItem[1].Temperature = %v, want 68.0", temp)
	}
}

func TestGetArrayItem_OutOfBounds(t *testing.T) {
	m := map[string]interface{}{
		"DEVS": []interface{}{
			map[string]interface{}{"T": 65.0},
		},
	}
	item := GetArrayItem(m, "DEVS", 99)
	if item != nil {
		t.Error("GetArrayItem out-of-bounds should return nil")
	}
}

// ── PortOpen ──────────────────────────────────────────────────────────────────

func TestPortOpen_ClosedPort(t *testing.T) {
	ctx := context.Background()
	// Port 19999 is almost certainly not open on localhost in tests
	if PortOpen(ctx, "127.0.0.1", 19999, 100*time.Millisecond) {
		t.Log("Port 19999 was unexpectedly open — skipping assertion")
	}
}

func TestPortOpen_OpenPort(t *testing.T) {
	// Start a temporary TCP listener to simulate an open port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create test listener: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	ctx := context.Background()
	if !PortOpen(ctx, "127.0.0.1", addr.Port, 500*time.Millisecond) {
		t.Errorf("PortOpen returned false for a live listener on port %d", addr.Port)
	}
}

// ── New (constructor) ─────────────────────────────────────────────────────────

func TestNew_ValidIP(t *testing.T) {
	cli, err := New("192.168.1.10", 4028, 5)
	if err != nil {
		t.Fatalf("New with valid IP returned error: %v", err)
	}
	if cli.IP() != "192.168.1.10" {
		t.Errorf("cli.IP() = %q, want '192.168.1.10'", cli.IP())
	}
	if cli.Addr() != "192.168.1.10:4028" {
		t.Errorf("cli.Addr() = %q, want '192.168.1.10:4028'", cli.Addr())
	}
}

func TestNew_LoopbackIP(t *testing.T) {
	_, err := New("127.0.0.1", 4028, 5)
	if err == nil {
		t.Error("New with loopback IP should have returned error")
	}
}

func TestNew_Hostname(t *testing.T) {
	_, err := New("miner.local", 4028, 5)
	if err == nil {
		t.Error("New with hostname should have returned error")
	}
}

// ── Send (live mock server) ───────────────────────────────────────────────────

// mockRPCServer starts a minimal TCP server that responds to one RPC call.
func mockRPCServer(t *testing.T, response string) (port int, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mock server listen: %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		conn.Read(buf)
		fmt.Fprint(conn, response)
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.Port, func() { ln.Close() }
}

func TestSend_ValidResponse(t *testing.T) {
	response := `{"STATUS":[{"Code":14,"Description":"BMMiner 2.0.0"}],"SUMMARY":[{"GHS 5s":270000.0,"Elapsed":3600}],"id":1}`
	port, stop := mockRPCServer(t, response)
	defer stop()

	// Use 127.0.0.1 directly — bypass ValidateIP which rejects loopback
	cli := &Client{
		addr:    fmt.Sprintf("127.0.0.1:%d", port),
		timeout: 2 * time.Second,
	}
	ctx := context.Background()
	result, err := cli.Send(ctx, "summary", nil)
	if err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	arr := GetArray(result, "SUMMARY")
	if len(arr) == 0 {
		t.Fatal("SUMMARY array empty")
	}
	ghs, ok := GetFloat(arr[0].(map[string]interface{}), "GHS 5s")
	if !ok || ghs != 270000.0 {
		t.Errorf("GHS 5s = %v, want 270000.0", ghs)
	}
}

func TestSend_TrailingCommaResponse(t *testing.T) {
	// Many miners emit trailing commas — verify cleanJSON fixes it before parse
	response := `{"SUMMARY":[{"GHS 5s":100.0,"Status":"Alive",}],"id":1,}`
	port, stop := mockRPCServer(t, response)
	defer stop()

	cli := &Client{
		addr:    fmt.Sprintf("127.0.0.1:%d", port),
		timeout: 2 * time.Second,
	}
	ctx := context.Background()
	result, err := cli.Send(ctx, "summary", nil)
	if err != nil {
		t.Fatalf("Send with trailing-comma response failed: %v", err)
	}
	if GetArray(result, "SUMMARY") == nil {
		t.Error("Expected SUMMARY array in result")
	}
}

func TestSend_InvalidCommand(t *testing.T) {
	cli := &Client{addr: "192.168.1.1:4028", timeout: time.Second}
	ctx := context.Background()
	_, err := cli.Send(ctx, "cmd; rm -rf /", nil)
	if err == nil {
		t.Error("Send with invalid command should return error")
	}
}
