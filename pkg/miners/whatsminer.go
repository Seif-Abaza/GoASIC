package miners

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goasic/goasic/pkg/rpc"
)

// ── Whatsminer driver ─────────────────────────────────────────────────────────
//
// Legacy firmware (M30S, M31S, M50):  plain cgminer RPC — updatePools
// BTMiner token-auth (M50S++, M60, M61+):
//   1. RPC get_token → { "token": "...", "salt": "...", "time": "..." }
//   2. enc = MD5(salt + MD5(password))
//   3. RPC update_pools with token field = enc

type whatsminerFW int

const (
	wmLegacy    whatsminerFW = iota
	wmTokenAuth              // BTMiner 2023+
)

// Whatsminer implements the Miner interface for all MicroBT WhatsMiner hardware.
type Whatsminer struct {
	ip     string
	rpcCli *rpc.Client
}

// NewWhatsminer creates a Whatsminer driver.
func NewWhatsminer(ip string) (*Whatsminer, error) {
	cli, err := rpc.New(ip, 4028, 10)
	if err != nil {
		return nil, err
	}
	return &Whatsminer{ip: ip, rpcCli: cli}, nil
}

func (w *Whatsminer) IP() string    { return w.ip }
func (w *Whatsminer) Brand() string { return "Whatsminer" }

// ── GetData ───────────────────────────────────────────────────────────────────

func (w *Whatsminer) GetData(ctx context.Context) (*MinerData, error) {
	d := &MinerData{
		IP:        w.ip,
		DateTime:  time.Now(),
		Make:      "Whatsminer",
		Algorithm: "SHA-256d",
		Cooling:   "Air",
	}

	// Summary
	sum, err := w.rpcCli.Send(ctx, "summary", nil)
	if err != nil {
		return nil, fmt.Errorf("whatsminer %s summary: %w", w.ip, err)
	}
	if arr := rpc.GetArray(sum, "SUMMARY"); len(arr) > 0 {
		if s, ok := arr[0].(map[string]interface{}); ok {
			// BTMiner: "HS 5s" (raw H/s); legacy: "GHS 5s"
			if v, ok := rpc.GetFloat(s, "HS 5s"); ok {
				r := v / 1_000_000_000_000
				d.Hashrate = &r
			} else if v, ok := rpc.GetFloat(s, "GHS 5s"); ok {
				r := v / 1_000
				d.Hashrate = &r
			}
			if v, ok := rpc.GetFloat(s, "Elapsed"); ok {
				u := uint64(v)
				d.Uptime = &u
			}
			if d.Hashrate != nil {
				d.IsMining = *d.Hashrate > 0
			}
		}
	}

	// Devs → temps, fans
	devs, err := w.rpcCli.Send(ctx, "devs", nil)
	if err == nil {
		for _, di := range rpc.GetArray(devs, "DEVS") {
			dev, ok := di.(map[string]interface{})
			if !ok {
				continue
			}
			for _, key := range []string{"Temperature", "Chip Temp Max", "Chip Temp Avg"} {
				if v, ok := rpc.GetFloat(dev, key); ok && v > 0 {
					d.Temperature = append(d.Temperature, v)
					break
				}
			}
			for _, key := range []string{"Fan Speed In", "Fan Speed Out"} {
				if v, ok := rpc.GetFloat(dev, key); ok && v > 0 {
					d.FanSpeeds = append(d.FanSpeeds, int(v))
				}
			}
		}
	}

	// Model from get_version
	if ver, err := w.rpcCli.Send(ctx, "get_version", nil); err == nil {
		// BTMiner wraps in "Msg", legacy in "VERSION"
		for _, key := range []string{"Msg", "VERSION"} {
			if arr := rpc.GetArray(ver, key); len(arr) > 0 {
				if v, ok := arr[0].(map[string]interface{}); ok {
					for _, mk := range []string{"Type", "model"} {
						if m := rpc.GetString(v, mk); m != "" {
							d.Model = m
							break
						}
					}
				}
				break
			}
		}
	}

	// Pools
	if pools, err := w.rpcCli.Send(ctx, "pools", nil); err == nil {
		if arr := rpc.GetArray(pools, "POOLS"); len(arr) > 0 {
			if p, ok := arr[0].(map[string]interface{}); ok {
				d.Pool1URL = rpc.GetString(p, "URL")
				d.Pool1User = rpc.GetString(p, "User")
			}
		}
	}

	d.EnrichFromDB()
	return d, nil
}

// ── GetConfig ─────────────────────────────────────────────────────────────────

func (w *Whatsminer) GetConfig(ctx context.Context) (*MinerConfig, error) {
	pools, err := w.rpcCli.Send(ctx, "pools", nil)
	if err != nil {
		return nil, err
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

func (w *Whatsminer) SendConfig(ctx context.Context, cfg MinerConfig) error {
	fw := w.detectFirmware(ctx)
	params := map[string]interface{}{}
	for i, p := range cfg.Pools {
		n := i + 1
		pw := p.Password
		if pw == "" {
			pw = "x"
		}
		params[fmt.Sprintf("pool%d_url", n)]  = p.URL
		params[fmt.Sprintf("pool%d_user", n)] = p.User
		params[fmt.Sprintf("pool%d_pwd", n)]  = pw
	}

	switch fw {
	case wmTokenAuth:
		// Fetch token+salt, compute enc, inject into params
		tokenResp, err := w.rpcCli.Send(ctx, "get_token", nil)
		if err != nil {
			return fmt.Errorf("whatsminer get_token: %w", err)
		}
		salt := w.extractTokenField(tokenResp, "salt")
		password := "admin" // default; caller should supply via pool password field
		if len(cfg.Pools) > 0 && cfg.Pools[0].Password != "" {
			password = cfg.Pools[0].Password
		}
		enc := computeWhatsminerToken(salt, password)
		params["token"] = enc
		_, err = w.rpcCli.Send(ctx, "update_pools", params)
		return err
	default:
		_, err := w.rpcCli.Send(ctx, "updatePools", params)
		return err
	}
}

func (w *Whatsminer) extractTokenField(resp map[string]interface{}, field string) string {
	// BTMiner wraps fields in "Msg" object
	if msg, ok := resp["Msg"].(map[string]interface{}); ok {
		if v, ok := msg[field].(string); ok {
			return v
		}
	}
	if v, ok := resp[field].(string); ok {
		return v
	}
	return ""
}

// computeWhatsminerToken computes MD5(salt + MD5(password)).
func computeWhatsminerToken(salt, password string) string {
	pwHash := fmt.Sprintf("%x", md5.Sum([]byte(password)))
	combined := salt + pwHash
	return fmt.Sprintf("%x", md5.Sum([]byte(combined)))
}

func (w *Whatsminer) detectFirmware(ctx context.Context) whatsminerFW {
	ver, err := w.rpcCli.Send(ctx, "get_version", nil)
	if err != nil {
		return wmLegacy
	}
	model := ""
	for _, key := range []string{"Msg", "VERSION"} {
		if arr := rpc.GetArray(ver, key); len(arr) > 0 {
			if v, ok := arr[0].(map[string]interface{}); ok {
				for _, mk := range []string{"Type", "model"} {
					if m := rpc.GetString(v, mk); m != "" {
						model = m
					}
				}
			}
		}
	}
	m := strings.ToLower(model)
	if (strings.Contains(m, "m50s") && strings.Contains(m, "++")) ||
		strings.Contains(m, "m60") || strings.Contains(m, "m61") ||
		strings.Contains(m, "m63") || strings.Contains(m, "m66") {
		return wmTokenAuth
	}
	return wmLegacy
}

// ── Control ops ──────────────────────────────────────────────────────────────

func (w *Whatsminer) Reboot(ctx context.Context) error {
	_, err := w.rpcCli.Send(ctx, "reboot", nil)
	return err
}

func (w *Whatsminer) FaultLightOn(ctx context.Context) error {
	if _, err := w.rpcCli.Send(ctx, "ledOn", nil); err == nil {
		return nil
	}
	_, err := w.rpcCli.Send(ctx, "led_on", nil)
	return err
}

func (w *Whatsminer) FaultLightOff(ctx context.Context) error {
	if _, err := w.rpcCli.Send(ctx, "ledOff", nil); err == nil {
		return nil
	}
	_, err := w.rpcCli.Send(ctx, "led_off", nil)
	return err
}

func (w *Whatsminer) StopMining(ctx context.Context) error {
	_, err := w.rpcCli.Send(ctx, "power_off", map[string]string{"respbefore": "false"})
	return err
}

func (w *Whatsminer) ResumeMining(ctx context.Context) error {
	_, err := w.rpcCli.Send(ctx, "power_on", nil)
	return err
}

func (w *Whatsminer) IsMining(ctx context.Context) (bool, error) {
	sum, err := w.rpcCli.Send(ctx, "summary", nil)
	if err != nil {
		return false, err
	}
	arr := rpc.GetArray(sum, "SUMMARY")
	if len(arr) == 0 {
		return false, nil
	}
	s, _ := arr[0].(map[string]interface{})
	for _, key := range []string{"HS 5s", "GHS 5s"} {
		if v, ok := rpc.GetFloat(s, key); ok {
			return v > 0, nil
		}
	}
	return false, nil
}

// ── SetMode ───────────────────────────────────────────────────────────────────
//
// Whatsminer operating modes:
//   Normal     → power_mode normal
//   LowPower   → power_mode low  (LPM)
//   HighPerf   → power_mode high (some models only)
//   Sleep      → power_off

func (w *Whatsminer) SetMode(ctx context.Context, mode MiningMode) error {
	if mode == ModeSleep {
		return w.StopMining(ctx)
	}
	modeMap := map[MiningMode]string{
		ModeNormal:   "normal",
		ModeLowPower: "low",
		ModeHighPerf: "high",
	}
	m, ok := modeMap[mode]
	if !ok {
		return fmt.Errorf("whatsminer: unsupported mode %q", mode)
	}
	_, err := w.rpcCli.Send(ctx, "set_power_pct", map[string]string{"percent": m})
	if err != nil {
		// Fallback: older firmware uses power_mode command
		_, err = w.rpcCli.Send(ctx, "power_mode", map[string]string{"mode": m})
	}
	return err
}

// ── SetFanSpeed ───────────────────────────────────────────────────────────────
//
// Whatsminer fan control via set_fan_pwm RPC command.
// pct=-1 restores automatic control.

func (w *Whatsminer) SetFanSpeed(ctx context.Context, pct int) error {
	if pct != -1 && (pct < 0 || pct > 100) {
		return fmt.Errorf("whatsminer: fan speed %d out of range", pct)
	}
	auto := "true"
	fanPct := 100
	if pct != -1 {
		auto = "false"
		fanPct = pct
	}
	_, err := w.rpcCli.Send(ctx, "set_fan_pwm",
		map[string]interface{}{"fan_pwm": fanPct, "auto": auto})
	return err
}

// ── UpdateFirmware ────────────────────────────────────────────────────────────
//
// Whatsminer firmware update via HTTP POST to /cgi-bin/luci/admin/upgrade.
// Token-auth firmware (M50S++/M60+) requires an encrypted token in the header.

func (w *Whatsminer) UpdateFirmware(ctx context.Context, fw FirmwareInfo) error {
	if fw.LocalPath == "" && fw.URL == "" {
		return fmt.Errorf("whatsminer: UpdateFirmware requires LocalPath or URL")
	}
	filePath := fw.LocalPath
	if filePath == "" {
		tmp, err := downloadToTemp(ctx, fw.URL)
		if err != nil {
			return fmt.Errorf("whatsminer: firmware download: %w", err)
		}
		defer removeTemp(tmp)
		filePath = tmp
	}
	body, contentType, err := buildMultipartFile("firmware", filePath)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/cgi-bin/luci/admin/upgrade", w.ip)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("whatsminer firmware upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("whatsminer firmware upload: HTTP %d", resp.StatusCode)
	}
	return nil
}
