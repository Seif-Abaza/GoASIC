package miners

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goasic/goasic/pkg/rpc"
)

// ── Antminer driver ───────────────────────────────────────────────────────────
//
// Supports ALL Antminer firmware classes:
//
//   Standard Air  — S9, S19, S21, T19, S21a, S21b, S21 XP (air-cooled)
//                   CGI: /cgi-bin/set_miner_conf.cgi  blink.cgi  switchmode.cgi
//
//   Alt-Algo Air  — L7 (Scrypt MHS), L9 (Scrypt GHS), D7 (X11 GHS),
//                   K7/KS5 (kHeavyHash GHS), Z15 Pro (Equihash KSols/s)
//                   Same CGI endpoints, different RPC hashrate fields.
//
//   Hydro         — S19e XP Hyd, S19 XP+ Hyd, S21e Hyd, S21+ Hyd, S21 XP Hyd
//                   Token-auth: GET /cgi-bin/get_token.cgi → token
//                   Write via X-Token header to /cgi-bin/miner_conf.cgi
//
//   Immersion     — S21 Immersion, S21 XP Imm
//                   Same token-auth as Hydro; fan endpoints absent.

// Antminer implements the Miner interface for all Bitmain Antminer hardware.
type Antminer struct {
	ip       string
	rpcCli   *rpc.Client
	httpCli  *http.Client
	firmware string // "standard" | "altair" | "hydro" | "immersion"
}

// NewAntminer creates an Antminer driver for the given IP.
func NewAntminer(ip string) (*Antminer, error) {
	rpcCli, err := rpc.New(ip, 4028, 10)
	if err != nil {
		return nil, err
	}
	httpCli := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: insecureTLS(),
		},
	}
	return &Antminer{
		ip:       ip,
		rpcCli:   rpcCli,
		httpCli:  httpCli,
		firmware: "standard",
	}, nil
}

func (a *Antminer) IP() string    { return a.ip }
func (a *Antminer) Brand() string { return "Antminer" }

// ── GetData ───────────────────────────────────────────────────────────────────

func (a *Antminer) GetData(ctx context.Context) (*MinerData, error) {
	d := &MinerData{
		IP:       a.ip,
		DateTime: time.Now(),
		Make:     "Antminer",
	}

	// 1. Summary
	sum, err := a.rpcCli.Send(ctx, "summary", nil)
	if err != nil {
		return nil, fmt.Errorf("antminer %s summary: %w", a.ip, err)
	}
	sumArr := rpc.GetArray(sum, "SUMMARY")
	var sumItem map[string]interface{}
	if len(sumArr) > 0 {
		sumItem, _ = sumArr[0].(map[string]interface{})
	}
	if uptime, ok := rpc.GetFloat(sumItem, "Elapsed"); ok {
		v := uint64(uptime)
		d.Uptime = &v
	}

	// 2. Stats → model, temps, fans, power
	stats, err := a.rpcCli.Send(ctx, "stats", nil)
	model := ""
	if err == nil {
		statsArr := rpc.GetArray(stats, "STATS")
		for _, si := range statsArr {
			stat, ok := si.(map[string]interface{})
			if !ok {
				continue
			}
			if t := rpc.GetString(stat, "Type"); t != "" && t != "0" {
				model = t
				d.Model = t
			}
			// Temps
			var temps []float64
			for i := 1; i <= 9; i++ {
				for _, key := range []string{fmt.Sprintf("temp2_%d", i), fmt.Sprintf("temp_%d", i), fmt.Sprintf("temp%d", i)} {
					if v, ok := rpc.GetFloat(stat, key); ok && v > 0 {
						temps = append(temps, v)
						break
					}
				}
			}
			// Hydro coolant temps
			for _, key := range []string{"inlet_temp", "outlet_temp", "liquid_temp"} {
				if v, ok := rpc.GetFloat(stat, key); ok && v > 0 {
					temps = append(temps, v)
				}
			}
			if len(temps) > 0 {
				d.Temperature = temps
			}
			// Fans
			var fans []int
			for i := 1; i <= 8; i++ {
				if v, ok := rpc.GetFloat(stat, fmt.Sprintf("fan%d", i)); ok && v > 0 {
					fans = append(fans, int(v))
				}
			}
			if len(fans) > 0 {
				d.FanSpeeds = fans
			}
			// Power
			if w, ok := rpc.GetFloat(stat, "power"); ok && w > 0 {
				wi := int(w)
				d.Wattage = &wi
			}
		}
	}

	// Detect firmware class from model name
	a.firmware = DetectFirmwareClass(model)
	switch a.firmware {
	case "hydro":
		d.Cooling = "Hydro"
		d.Algorithm = "SHA-256d"
	case "immersion":
		d.Cooling = "Immersion"
		d.Algorithm = "SHA-256d"
	default:
		d.Cooling = "Air"
	}

	// 3. Hashrate — depends on algorithm
	if sumItem != nil {
		d.Hashrate = a.parseHashrate(sumItem, model)
		d.IsMining = d.Hashrate != nil && *d.Hashrate > 0
	}

	// 4. Pools
	pools, err := a.rpcCli.Send(ctx, "pools", nil)
	if err == nil {
		poolArr := rpc.GetArray(pools, "POOLS")
		// prefer the active stratum pool
		for _, pi := range poolArr {
			p, ok := pi.(map[string]interface{})
			if !ok {
				continue
			}
			active, _ := p["Stratum Active"].(bool)
			if active || d.Pool1URL == "" {
				if u := rpc.GetString(p, "URL"); u != "" {
					d.Pool1URL = u
				}
				if u := rpc.GetString(p, "User"); u != "" {
					d.Pool1User = u
				}
				if active {
					break
				}
			}
		}
	}

	d.EnrichFromDB()
	return d, nil
}

// parseHashrate reads the correct RPC field per algorithm/model family.
func (a *Antminer) parseHashrate(s map[string]interface{}, model string) *float64 {
	m := strings.ToLower(model)

	// Equihash — Z15 Pro, Z9
	if strings.Contains(m, " z15") || strings.Contains(m, " z9") {
		if v, ok := rpc.GetFloat(s, "KSols/s"); ok {
			return PtrFloat64(v)
		}
		if v, ok := rpc.GetFloat(s, "Sol/s"); ok {
			r := v / 1000
			return PtrFloat64(r)
		}
	}

	// Scrypt L7 — reports MHS
	if strings.Contains(m, " l7") {
		if v, ok := rpc.GetFloat(s, "MHS 5s"); ok {
			r := v / 1_000_000
			return PtrFloat64(r)
		}
	}

	// All others (Scrypt L9, X11 D7, kHeavyHash K7/KS5, SHA-256d all): GHS 5s
	if v, ok := rpc.GetFloat(s, "GHS 5s"); ok {
		r := v / 1_000
		return PtrFloat64(r)
	}
	// Fallback: MHS 5s
	if v, ok := rpc.GetFloat(s, "MHS 5s"); ok {
		r := v / 1_000_000
		return PtrFloat64(r)
	}
	return nil
}

// ── GetConfig ─────────────────────────────────────────────────────────────────

func (a *Antminer) GetConfig(ctx context.Context) (*MinerConfig, error) {
	pools, err := a.rpcCli.Send(ctx, "pools", nil)
	if err != nil {
		return nil, fmt.Errorf("antminer %s get_config: %w", a.ip, err)
	}
	cfg := &MinerConfig{}
	for _, pi := range rpc.GetArray(pools, "POOLS") {
		p, ok := pi.(map[string]interface{})
		if !ok {
			continue
		}
		u := rpc.GetString(p, "URL")
		usr := rpc.GetString(p, "User")
		if u != "" {
			cfg.AddPool(u, usr, "x")
		}
	}
	return cfg, nil
}

// ── SendConfig ────────────────────────────────────────────────────────────────

func (a *Antminer) SendConfig(ctx context.Context, cfg MinerConfig) error {
	switch a.firmware {
	case "hydro", "immersion":
		return a.sendConfigHydro(ctx, cfg)
	default:
		return a.sendConfigStandard(ctx, cfg)
	}
}

func (a *Antminer) sendConfigStandard(ctx context.Context, cfg MinerConfig) error {
	payload := map[string]string{
		"_ant_pool1url":  cfg.Pool(0).URL,
		"_ant_pool1user": cfg.Pool(0).User,
		"_ant_pool1pw":   poolPW(cfg.Pool(0)),
		"_ant_pool2url":  cfg.Pool(1).URL,
		"_ant_pool2user": cfg.Pool(1).User,
		"_ant_pool2pw":   poolPW(cfg.Pool(1)),
		"_ant_pool3url":  cfg.Pool(2).URL,
		"_ant_pool3user": cfg.Pool(2).User,
		"_ant_pool3pw":   poolPW(cfg.Pool(2)),
	}
	_, err := a.httpPost(ctx, "cgi-bin/set_miner_conf.cgi", payload, "")
	return err
}

func (a *Antminer) sendConfigHydro(ctx context.Context, cfg MinerConfig) error {
	token, err := a.getHydroToken(ctx)
	if err != nil {
		return fmt.Errorf("antminer hydro token: %w", err)
	}
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
	payload := map[string]interface{}{"pools": pools}
	_, err = a.httpPost(ctx, "cgi-bin/miner_conf.cgi", payload, token)
	return err
}

// getHydroToken fetches a session token from /cgi-bin/get_token.cgi.
func (a *Antminer) getHydroToken(ctx context.Context) (string, error) {
	resp, err := a.httpGet(ctx, "cgi-bin/get_token.cgi")
	if err != nil {
		return "", err
	}
	var result map[string]interface{}
	if err = json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("hydro token parse: %w", err)
	}
	token, ok := result["token"].(string)
	if !ok || token == "" {
		return "", fmt.Errorf("hydro token: 'token' field missing from response")
	}
	return token, nil
}

// ── Reboot ────────────────────────────────────────────────────────────────────

func (a *Antminer) Reboot(ctx context.Context) error {
	if _, err := a.rpcCli.Send(ctx, "restart", nil); err == nil {
		return nil
	}
	// Hydro firmware
	token, err := a.getHydroToken(ctx)
	if err != nil {
		return fmt.Errorf("antminer %s reboot: %w", a.ip, err)
	}
	_, err = a.httpPost(ctx, "cgi-bin/reboot.cgi", map[string]string{}, token)
	return err
}

// ── FaultLight ────────────────────────────────────────────────────────────────

func (a *Antminer) FaultLightOn(ctx context.Context) error {
	return a.setFaultLight(ctx, true)
}
func (a *Antminer) FaultLightOff(ctx context.Context) error {
	return a.setFaultLight(ctx, false)
}
func (a *Antminer) setFaultLight(ctx context.Context, on bool) error {
	payload := map[string]bool{"blink": on}
	switch a.firmware {
	case "hydro", "immersion":
		token, err := a.getHydroToken(ctx)
		if err != nil {
			return err
		}
		_, err = a.httpPost(ctx, "cgi-bin/blink.cgi", payload, token)
		return err
	default:
		_, err := a.httpPost(ctx, "cgi-bin/blink.cgi", payload, "")
		return err
	}
}

// ── StopMining / ResumeMining ─────────────────────────────────────────────────

func (a *Antminer) StopMining(ctx context.Context) error {
	return a.switchMode(ctx, "sleep")
}
func (a *Antminer) ResumeMining(ctx context.Context) error {
	return a.switchMode(ctx, "mining")
}
func (a *Antminer) switchMode(ctx context.Context, mode string) error {
	payload := map[string]string{"switchmode": mode}
	switch a.firmware {
	case "hydro", "immersion":
		token, err := a.getHydroToken(ctx)
		if err != nil {
			return err
		}
		_, err = a.httpPost(ctx, "cgi-bin/switchmode.cgi", payload, token)
		return err
	default:
		_, err := a.httpPost(ctx, "cgi-bin/switchmode.cgi", payload, "")
		return err
	}
}

// ── IsMining ─────────────────────────────────────────────────────────────────

func (a *Antminer) IsMining(ctx context.Context) (bool, error) {
	sum, err := a.rpcCli.Send(ctx, "summary", nil)
	if err != nil {
		return false, err
	}
	arr := rpc.GetArray(sum, "SUMMARY")
	if len(arr) == 0 {
		return false, nil
	}
	s, _ := arr[0].(map[string]interface{})
	// Try multiple hashrate fields
	for _, key := range []string{"GHS 5s", "MHS 5s", "KSols/s"} {
		if v, ok := rpc.GetFloat(s, key); ok {
			return v > 0, nil
		}
	}
	return false, nil
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (a *Antminer) httpGet(ctx context.Context, path string) ([]byte, error) {
	url := fmt.Sprintf("http://%s/%s", a.ip, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.httpCli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
}

func (a *Antminer) httpPost(ctx context.Context, path string, payload interface{}, token string) ([]byte, error) {
	url := fmt.Sprintf("http://%s/%s", a.ip, strings.TrimPrefix(path, "/"))
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Token", token)
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.httpCli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
}

func poolPW(p PoolConfig) string {
	if p.Password == "" {
		return "x"
	}
	return p.Password
}

// ── SetMode ───────────────────────────────────────────────────────────────────
//
// Antminer operating modes map to bitmain-work-mode CGI parameter:
//   0 = Normal, 1 = Low Power, 2 = High Performance (not all models)
// Hydro/Immersion use the same token-auth endpoint.

func (a *Antminer) SetMode(ctx context.Context, mode MiningMode) error {
	modeCode := map[MiningMode]string{
		ModeNormal:   "0",
		ModeLowPower: "1",
		ModeHighPerf: "2",
		ModeSleep:    "sleep",
	}
	code, ok := modeCode[mode]
	if !ok {
		return fmt.Errorf("antminer: unsupported mode %q", mode)
	}
	if mode == ModeSleep {
		return a.StopMining(ctx)
	}
	payload := map[string]string{"bitmain-work-mode": code}
	switch a.firmware {
	case "hydro", "immersion":
		token, err := a.getHydroToken(ctx)
		if err != nil {
			return err
		}
		_, err = a.httpPost(ctx, "cgi-bin/set_miner_conf.cgi", payload, token)
		return err
	default:
		_, err := a.httpPost(ctx, "cgi-bin/set_miner_conf.cgi", payload, "")
		return err
	}
}

// ── SetFanSpeed ───────────────────────────────────────────────────────────────
//
// Antminer fan speed is set via bitmain-fan-pwm (0–100) and bitmain-fan-ctrl.
// Pass pct=-1 to restore automatic control.

func (a *Antminer) SetFanSpeed(ctx context.Context, pct int) error {
	if pct != -1 && (pct < 0 || pct > 100) {
		return fmt.Errorf("antminer: fan speed %d out of range (0–100, or -1 for auto)", pct)
	}
	var payload map[string]interface{}
	if pct == -1 {
		payload = map[string]interface{}{"bitmain-fan-ctrl": true, "bitmain-fan-pwm": "100"}
	} else {
		payload = map[string]interface{}{"bitmain-fan-ctrl": false, "bitmain-fan-pwm": fmt.Sprintf("%d", pct)}
	}
	switch a.firmware {
	case "hydro", "immersion":
		token, err := a.getHydroToken(ctx)
		if err != nil {
			return err
		}
		_, err = a.httpPost(ctx, "cgi-bin/set_miner_conf.cgi", payload, token)
		return err
	default:
		_, err := a.httpPost(ctx, "cgi-bin/set_miner_conf.cgi", payload, "")
		return err
	}
}

// ── UpdateFirmware ────────────────────────────────────────────────────────────
//
// Antminer firmware update:
//   - Standard/AltAlgo: POST multipart form to /cgi-bin/upgrade.cgi
//   - Hydro/Immersion:  Token-auth POST to /cgi-bin/firmware_update.cgi
//
// If fw.URL is set and fw.LocalPath is empty, the firmware file is fetched
// from the URL first, then streamed to the miner.

func (a *Antminer) UpdateFirmware(ctx context.Context, fw FirmwareInfo) error {
	if fw.LocalPath == "" && fw.URL == "" {
		return fmt.Errorf("antminer: UpdateFirmware requires LocalPath or URL")
	}

	// Fetch remote file if only URL provided
	filePath := fw.LocalPath
	if filePath == "" {
		tmp, err := downloadToTemp(ctx, fw.URL)
		if err != nil {
			return fmt.Errorf("antminer: firmware download failed: %w", err)
		}
		defer removeTemp(tmp)
		filePath = tmp
	}

	switch a.firmware {
	case "hydro", "immersion":
		token, err := a.getHydroToken(ctx)
		if err != nil {
			return fmt.Errorf("antminer: hydro firmware token: %w", err)
		}
		return a.uploadFirmwareMultipart(ctx, "cgi-bin/firmware_update.cgi", filePath, token)
	default:
		return a.uploadFirmwareMultipart(ctx, "cgi-bin/upgrade.cgi", filePath, "")
	}
}

func (a *Antminer) uploadFirmwareMultipart(ctx context.Context, path, filePath, token string) error {
	body, contentType, err := buildMultipartFile("firmware", filePath)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/%s", a.ip, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("X-Token", token)
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.httpCli.Do(req)
	if err != nil {
		return fmt.Errorf("antminer firmware upload to %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("antminer firmware upload: HTTP %d", resp.StatusCode)
	}
	return nil
}
