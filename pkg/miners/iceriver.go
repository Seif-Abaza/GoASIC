package miners

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// IceRiver implements the Miner interface for IceRiver KS-series miners.
//
// IceRiver miners (KS0–KS7) mine Kaspa (kHeavyHash) and expose a REST/JSON
// HTTP API on port 8080 instead of the cgminer RPC used by other brands.
//
// Supported models: KS0, KS0 Pro, KS1, KS2, KS3, KS3L, KS3M, KS5, KS5L, KS5M, KS7
//
// API endpoints:
//   GET  /api/v1/summary  — hashrate, temps, uptime, status
//   GET  /api/v1/pool     — active pool
//   POST /api/v1/pool     — set pools (body: {"pools":[{url,user,password}]})
//   POST /api/v1/restart  — reboot
//   POST /api/v1/led      — fault LED (body: {"led": true/false})
//   POST /api/v1/stop     — pause mining
//   POST /api/v1/start    — resume mining
type IceRiver struct {
	ip     string
	client *http.Client
}

func NewIceRiver(ip string) (*IceRiver, error) {
	if err := validateIP(ip); err != nil {
		return nil, err
	}
	return &IceRiver{
		ip: ip,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: insecureTLS(),
			},
		},
	}, nil
}

func (ir *IceRiver) IP() string    { return ir.ip }
func (ir *IceRiver) Brand() string { return "IceRiver" }

func (ir *IceRiver) GetData(ctx context.Context) (*MinerData, error) {
	d := &MinerData{
		IP:        ir.ip,
		DateTime:  time.Now(),
		Make:      "IceRiver",
		Algorithm: "kHeavyHash",
		Cooling:   "Air",
	}

	sum, err := ir.apiGet(ctx, "api/v1/summary")
	if err != nil {
		return nil, fmt.Errorf("iceriver %s summary: %w", ir.ip, err)
	}

	if m, ok := sum["model"].(string); ok && m != "" {
		d.Model = m
	}
	if fw, ok := sum["firmware"].(string); ok {
		d.Firmware = fw
	}

	// Hashrate — normalise to TH/s regardless of unit reported
	if hr, ok := sum["hashrate"].(float64); ok {
		unit := "TH/s"
		if u, ok := sum["hashrate_unit"].(string); ok {
			unit = u
		}
		r := convertHashrateToTHS(hr, unit)
		d.Hashrate = &r
	} else {
		// Alternative field names on older firmware
		for _, key := range []string{"realtimehashrate", "rt_hashrate"} {
			if v, ok := sum[key].(float64); ok {
				r := v / 1_000 // GH/s → TH/s
				d.Hashrate = &r
				break
			}
		}
	}

	// Temperatures
	if arr, ok := sum["temperature"].([]interface{}); ok {
		for _, v := range arr {
			if t, ok := v.(float64); ok && t > 0 {
				d.Temperature = append(d.Temperature, t)
			}
		}
	}
	if len(d.Temperature) == 0 {
		if t, ok := sum["temp"].(float64); ok && t > 0 {
			d.Temperature = []float64{t}
		}
	}

	// Fans
	if arr, ok := sum["fans"].([]interface{}); ok {
		for _, v := range arr {
			if f, ok := v.(float64); ok && f > 0 {
				d.FanSpeeds = append(d.FanSpeeds, int(f))
			}
		}
	}

	// Power
	for _, key := range []string{"power", "wattage"} {
		if w, ok := sum[key].(float64); ok && w > 0 {
			wi := int(w)
			d.Wattage = &wi
			break
		}
	}

	// Uptime
	for _, key := range []string{"uptime", "elapsed"} {
		if u, ok := sum[key].(float64); ok {
			v := uint64(u)
			d.Uptime = &v
			break
		}
	}

	// Status
	if status, ok := sum["status"].(string); ok {
		s := strings.ToLower(status)
		d.IsMining = s == "mining" || s == "running"
	} else {
		d.IsMining = d.Hashrate != nil && *d.Hashrate > 0
	}

	// Pool info
	if pool, err := ir.apiGet(ctx, "api/v1/pool"); err == nil {
		for _, key := range []string{"url", "pool"} {
			if u, ok := pool[key].(string); ok && u != "" {
				d.Pool1URL = u
				break
			}
		}
		for _, key := range []string{"user", "worker"} {
			if u, ok := pool[key].(string); ok && u != "" {
				d.Pool1User = u
				break
			}
		}
	}

	d.EnrichFromDB()
	return d, nil
}

func (ir *IceRiver) GetConfig(ctx context.Context) (*MinerConfig, error) {
	pool, err := ir.apiGet(ctx, "api/v1/pool")
	if err != nil {
		return nil, err
	}
	cfg := &MinerConfig{}
	// Structured: pool1, pool2, pool3
	for i := 1; i <= 3; i++ {
		key := fmt.Sprintf("pool%d", i)
		if p, ok := pool[key].(map[string]interface{}); ok {
			u, _ := p["url"].(string)
			usr := stringOr(p, "user", "worker")
			if u != "" {
				cfg.AddPool(u, usr, "x")
			}
		}
	}
	// Flat fallback
	if len(cfg.Pools) == 0 {
		u, _ := pool["url"].(string)
		usr, _ := pool["user"].(string)
		if u != "" {
			cfg.AddPool(u, usr, "x")
		}
	}
	return cfg, nil
}

func (ir *IceRiver) SendConfig(ctx context.Context, cfg MinerConfig) error {
	type poolEntry struct {
		URL      string `json:"url"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	var pools []poolEntry
	for _, p := range cfg.Pools {
		pw := p.Password
		if pw == "" {
			pw = "x"
		}
		pools = append(pools, poolEntry{URL: p.URL, User: p.User, Password: pw})
	}
	_, err := ir.apiPost(ctx, "api/v1/pool", map[string]interface{}{"pools": pools})
	return err
}

func (ir *IceRiver) Reboot(ctx context.Context) error {
	_, err := ir.apiPost(ctx, "api/v1/restart", map[string]string{})
	return err
}

func (ir *IceRiver) FaultLightOn(ctx context.Context) error {
	_, err := ir.apiPost(ctx, "api/v1/led", map[string]bool{"led": true})
	return err
}

func (ir *IceRiver) FaultLightOff(ctx context.Context) error {
	_, err := ir.apiPost(ctx, "api/v1/led", map[string]bool{"led": false})
	return err
}

func (ir *IceRiver) StopMining(ctx context.Context) error {
	_, err := ir.apiPost(ctx, "api/v1/stop", map[string]string{})
	return err
}

func (ir *IceRiver) ResumeMining(ctx context.Context) error {
	_, err := ir.apiPost(ctx, "api/v1/start", map[string]string{})
	return err
}

func (ir *IceRiver) IsMining(ctx context.Context) (bool, error) {
	sum, err := ir.apiGet(ctx, "api/v1/summary")
	if err != nil {
		return false, err
	}
	if s, ok := sum["status"].(string); ok {
		sl := strings.ToLower(s)
		return sl == "mining" || sl == "running", nil
	}
	return false, nil
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (ir *IceRiver) apiGet(ctx context.Context, path string) (map[string]interface{}, error) {
	url := fmt.Sprintf("http://%s:8080/%s", ir.ip, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := ir.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err = json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse %s: %w | raw: %.200s", url, err, body)
	}
	return result, nil
}

func (ir *IceRiver) apiPost(ctx context.Context, path string, payload interface{}) (map[string]interface{}, error) {
	url := fmt.Sprintf("http://%s:8080/%s", ir.ip, strings.TrimPrefix(path, "/"))
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := ir.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(respBody))) == 0 {
		return map[string]interface{}{"ok": true}, nil
	}
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	return result, nil
}

// convertHashrateToTHS converts a hashrate value and unit string to TH/s.
func convertHashrateToTHS(value float64, unit string) float64 {
	switch strings.ToUpper(unit) {
	case "GH/S", "GHS":
		return value / 1_000
	case "MH/S", "MHS":
		return value / 1_000_000
	case "KH/S", "KHS":
		return value / 1_000_000_000
	default: // TH/s assumed
		return value
	}
}

func stringOr(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// ── SetMode ───────────────────────────────────────────────────────────────────

func (ir *IceRiver) SetMode(ctx context.Context, mode MiningMode) error {
	if mode == ModeSleep {
		return ir.StopMining(ctx)
	}
	modeMap := map[MiningMode]string{
		ModeNormal:   "normal",
		ModeLowPower: "low_power",
		ModeHighPerf: "high_performance",
	}
	m, ok := modeMap[mode]
	if !ok {
		return fmt.Errorf("iceriver: unsupported mode %q", mode)
	}
	_, err := ir.apiPost(ctx, "api/v1/mode", map[string]string{"mode": m})
	return err
}

// ── SetFanSpeed ───────────────────────────────────────────────────────────────

func (ir *IceRiver) SetFanSpeed(ctx context.Context, pct int) error {
	if pct != -1 && (pct < 0 || pct > 100) {
		return fmt.Errorf("iceriver: fan speed %d out of range", pct)
	}
	payload := map[string]interface{}{"fan_pct": pct, "auto": pct == -1}
	_, err := ir.apiPost(ctx, "api/v1/fan", payload)
	return err
}

// ── UpdateFirmware ────────────────────────────────────────────────────────────

func (ir *IceRiver) UpdateFirmware(ctx context.Context, fw FirmwareInfo) error {
	if fw.LocalPath == "" && fw.URL == "" {
		return fmt.Errorf("iceriver: UpdateFirmware requires LocalPath or URL")
	}
	filePath := fw.LocalPath
	if filePath == "" {
		tmp, err := downloadToTemp(ctx, fw.URL)
		if err != nil {
			return fmt.Errorf("iceriver: firmware download: %w", err)
		}
		defer removeTemp(tmp)
		filePath = tmp
	}
	body, contentType, err := buildMultipartFile("firmware", filePath)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s:8080/api/v1/firmware/update", ir.ip)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := ir.client.Do(req)
	if err != nil {
		return fmt.Errorf("iceriver firmware upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("iceriver firmware upload: HTTP %d", resp.StatusCode)
	}
	return nil
}
