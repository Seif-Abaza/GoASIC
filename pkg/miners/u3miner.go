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

	"github.com/goasic/goasic/pkg/rpc"
)

// U3Miner implements the Miner interface for U3 high-density hydro miners.
//
// U3 (e.g. S21 XP Hyd 860T) uses Bitmain ASIC chips with custom firmware:
//   - cgminer RPC on port 4028  (same as Antminer — used for data/status)
//   - Custom REST API on port 8888  (hydro controls, config push)
type U3Miner struct {
	ip     string
	rpcCli *rpc.Client
	client *http.Client
}

func NewU3Miner(ip string) (*U3Miner, error) {
	cli, err := rpc.New(ip, 4028, 10)
	if err != nil {
		return nil, err
	}
	return &U3Miner{
		ip:     ip,
		rpcCli: cli,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: insecureTLS(),
			},
		},
	}, nil
}

func (u *U3Miner) IP() string    { return u.ip }
func (u *U3Miner) Brand() string { return "U3" }

func (u *U3Miner) GetData(ctx context.Context) (*MinerData, error) {
	d := &MinerData{
		IP:        u.ip,
		DateTime:  time.Now(),
		Make:      "U3",
		Algorithm: "SHA-256d",
		Cooling:   "Hydro",
	}

	// RPC summary
	sum, err := u.rpcCli.Send(ctx, "summary", nil)
	if err != nil {
		return nil, fmt.Errorf("u3 %s summary: %w", u.ip, err)
	}
	if arr := rpc.GetArray(sum, "SUMMARY"); len(arr) > 0 {
		if s, ok := arr[0].(map[string]interface{}); ok {
			if v, ok := rpc.GetFloat(s, "GHS 5s"); ok {
				r := v / 1_000
				d.Hashrate = &r
				d.IsMining = r > 0
			}
			if v, ok := rpc.GetFloat(s, "Elapsed"); ok {
				u2 := uint64(v)
				d.Uptime = &u2
			}
		}
	}

	// RPC stats
	if stats, err := u.rpcCli.Send(ctx, "stats", nil); err == nil {
		for _, si := range rpc.GetArray(stats, "STATS") {
			stat, ok := si.(map[string]interface{})
			if !ok {
				continue
			}
			if t := rpc.GetString(stat, "Type"); t != "" {
				d.Model = t
			}
			var temps []float64
			for i := 1; i <= 9; i++ {
				for _, key := range []string{fmt.Sprintf("temp2_%d", i), fmt.Sprintf("temp_%d", i)} {
					if v, ok := rpc.GetFloat(stat, key); ok && v > 0 {
						temps = append(temps, v)
						break
					}
				}
			}
			if len(temps) > 0 {
				d.Temperature = temps
			}
		}
	}

	// RPC pools
	if pools, err := u.rpcCli.Send(ctx, "pools", nil); err == nil {
		if arr := rpc.GetArray(pools, "POOLS"); len(arr) > 0 {
			if p, ok := arr[0].(map[string]interface{}); ok {
				d.Pool1URL = rpc.GetString(p, "URL")
				d.Pool1User = rpc.GetString(p, "User")
			}
		}
	}

	// REST hydro extras (coolant inlet/outlet)
	if hydro, err := u.restGet(ctx, "api/hydro/status"); err == nil {
		if t, ok := hydro["inlet_temp"].(float64); ok && t > 0 {
			d.Temperature = append(d.Temperature, t)
		}
		if t, ok := hydro["outlet_temp"].(float64); ok && t > 0 {
			d.Temperature = append(d.Temperature, t)
		}
		if w, ok := hydro["power"].(float64); ok && w > 0 {
			wi := int(w)
			d.Wattage = &wi
		}
	}

	d.EnrichFromDB()
	return d, nil
}

func (u *U3Miner) GetConfig(ctx context.Context) (*MinerConfig, error) {
	pools, err := u.rpcCli.Send(ctx, "pools", nil)
	if err != nil {
		return nil, err
	}
	cfg := &MinerConfig{}
	for _, pi := range rpc.GetArray(pools, "POOLS") {
		p, ok := pi.(map[string]interface{})
		if !ok {
			continue
		}
		url := rpc.GetString(p, "URL")
		usr := rpc.GetString(p, "User")
		if url != "" {
			cfg.AddPool(url, usr, "x")
		}
	}
	return cfg, nil
}

func (u *U3Miner) SendConfig(ctx context.Context, cfg MinerConfig) error {
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
	_, err := u.restPost(ctx, "cgi-bin/set_miner_conf.cgi", payload)
	return err
}

func (u *U3Miner) Reboot(ctx context.Context) error {
	if _, err := u.rpcCli.Send(ctx, "restart", nil); err == nil {
		return nil
	}
	_, err := u.restPost(ctx, "api/system/reboot", map[string]string{})
	return err
}

func (u *U3Miner) FaultLightOn(ctx context.Context) error {
	_, err := u.restPost(ctx, "api/led", map[string]bool{"blink": true})
	return err
}

func (u *U3Miner) FaultLightOff(ctx context.Context) error {
	_, err := u.restPost(ctx, "api/led", map[string]bool{"blink": false})
	return err
}

func (u *U3Miner) StopMining(ctx context.Context) error {
	_, err := u.restPost(ctx, "api/mining/stop", map[string]string{})
	return err
}

func (u *U3Miner) ResumeMining(ctx context.Context) error {
	_, err := u.restPost(ctx, "api/mining/start", map[string]string{})
	return err
}

func (u *U3Miner) IsMining(ctx context.Context) (bool, error) {
	sum, err := u.rpcCli.Send(ctx, "summary", nil)
	if err != nil {
		return false, err
	}
	arr := rpc.GetArray(sum, "SUMMARY")
	if len(arr) == 0 {
		return false, nil
	}
	s, _ := arr[0].(map[string]interface{})
	v, ok := rpc.GetFloat(s, "GHS 5s")
	return ok && v > 0, nil
}

// ── REST helpers ──────────────────────────────────────────────────────────────

func (u *U3Miner) restGet(ctx context.Context, path string) (map[string]interface{}, error) {
	url := fmt.Sprintf("http://%s:8888/%s", u.ip, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
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
		return nil, fmt.Errorf("parse %s: %w", url, err)
	}
	return result, nil
}

func (u *U3Miner) restPost(ctx context.Context, path string, payload interface{}) (map[string]interface{}, error) {
	url := fmt.Sprintf("http://%s:8888/%s", u.ip, strings.TrimPrefix(path, "/"))
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if len(strings.TrimSpace(string(respBody))) == 0 {
		return map[string]interface{}{"ok": true}, nil
	}
	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	return result, nil
}

// ── SetMode ───────────────────────────────────────────────────────────────────

func (u *U3Miner) SetMode(ctx context.Context, mode MiningMode) error {
	if mode == ModeSleep {
		return u.StopMining(ctx)
	}
	modeMap := map[MiningMode]string{
		ModeNormal:   "0",
		ModeLowPower: "1",
		ModeHighPerf: "2",
	}
	m, ok := modeMap[mode]
	if !ok {
		return fmt.Errorf("u3miner: unsupported mode %q", mode)
	}
	_, err := u.restPost(ctx, "api/mining/mode", map[string]string{"mode": m})
	return err
}

// ── SetFanSpeed ───────────────────────────────────────────────────────────────

func (u *U3Miner) SetFanSpeed(ctx context.Context, pct int) error {
	// U3 is hydro-cooled — no fans. Return descriptive error.
	return fmt.Errorf("u3miner: fan speed control not applicable (hydro-cooled unit)")
}

// ── UpdateFirmware ────────────────────────────────────────────────────────────

func (u *U3Miner) UpdateFirmware(ctx context.Context, fw FirmwareInfo) error {
	if fw.LocalPath == "" && fw.URL == "" {
		return fmt.Errorf("u3miner: UpdateFirmware requires LocalPath or URL")
	}
	filePath := fw.LocalPath
	if filePath == "" {
		tmp, err := downloadToTemp(ctx, fw.URL)
		if err != nil {
			return fmt.Errorf("u3miner: firmware download: %w", err)
		}
		defer removeTemp(tmp)
		filePath = tmp
	}
	body, contentType, err := buildMultipartFile("firmware", filePath)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s:8888/api/firmware/update", u.ip)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("u3miner firmware upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("u3miner firmware upload: HTTP %d", resp.StatusCode)
	}
	return nil
}
