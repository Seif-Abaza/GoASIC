package miners

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"strconv"
	"time"

	"github.com/goasic/goasic/pkg/rpc"
)

// Avalonminer implements the Miner interface for all Canaan Avalon hardware.
// Models: Nano 3, Nano 3S, Mini 3, A1346, A1366, A15 XP
//
// Avalon encodes sensor data in "MM ID0" string fields:
//   "TA[55 60 58] FAN[1200 1350] TEMP[65]"
type Avalonminer struct {
	ip      string
	rpcCli  *rpc.Client
	httpCli *http.Client
}

func NewAvalonminer(ip string) (*Avalonminer, error) {
	cli, err := rpc.New(ip, 4028, 10)
	if err != nil {
		return nil, err
	}
	return &Avalonminer{ip: ip, rpcCli: cli, httpCli: &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: insecureTLS()},
	}}, nil
}

func (a *Avalonminer) IP() string    { return a.ip }
func (a *Avalonminer) Brand() string { return "Avalonminer" }

func (a *Avalonminer) GetData(ctx context.Context) (*MinerData, error) {
	d := &MinerData{
		IP:        a.ip,
		DateTime:  time.Now(),
		Make:      "Avalonminer",
		Algorithm: "SHA-256d",
		Cooling:   "Air",
	}

	sum, err := a.rpcCli.Send(ctx, "summary", nil)
	if err != nil {
		return nil, fmt.Errorf("avalonminer %s summary: %w", a.ip, err)
	}
	if arr := rpc.GetArray(sum, "SUMMARY"); len(arr) > 0 {
		if s, ok := arr[0].(map[string]interface{}); ok {
			if v, ok := rpc.GetFloat(s, "GHS 5s"); ok {
				r := v / 1_000
				d.Hashrate = &r
				d.IsMining = r > 0
			}
			if v, ok := rpc.GetFloat(s, "Elapsed"); ok {
				u := uint64(v)
				d.Uptime = &u
			}
		}
	}

	if stats, err := a.rpcCli.Send(ctx, "stats", nil); err == nil {
		for _, si := range rpc.GetArray(stats, "STATS") {
			stat, ok := si.(map[string]interface{})
			if !ok {
				continue
			}
			if t := rpc.GetString(stat, "Type"); t != "" {
				d.Model = t
			}
			// Avalon stores sensor data in MM IDx encoded strings
			for _, key := range []string{"MM ID0", "MM ID1", "MM ID2", "MM ID3"} {
				if encoded, ok := stat[key].(string); ok && encoded != "" {
					if temps := parseAvalonField(encoded, "TA"); len(temps) > 0 {
						d.Temperature = append(d.Temperature, temps...)
					}
					if fans := parseAvalonFieldInt(encoded, "FAN"); len(fans) > 0 {
						d.FanSpeeds = append(d.FanSpeeds, fans...)
					}
				}
			}
		}
	}

	if pools, err := a.rpcCli.Send(ctx, "pools", nil); err == nil {
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

func (a *Avalonminer) GetConfig(ctx context.Context) (*MinerConfig, error) {
	pools, err := a.rpcCli.Send(ctx, "pools", nil)
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

func (a *Avalonminer) SendConfig(ctx context.Context, cfg MinerConfig) error {
	param := map[string]string{}
	for i, p := range cfg.Pools {
		pw := p.Password
		if pw == "" {
			pw = "x"
		}
		param[fmt.Sprintf("pool%d", i+1)] = fmt.Sprintf("%s,%s,%s", p.URL, p.User, pw)
	}
	_, err := a.rpcCli.Send(ctx, "updateconfig", param)
	return err
}

func (a *Avalonminer) Reboot(ctx context.Context) error {
	_, err := a.rpcCli.Send(ctx, "reboot", nil)
	return err
}

func (a *Avalonminer) FaultLightOn(ctx context.Context) error {
	_, err := a.rpcCli.Send(ctx, "setled", map[string]int{"led": 1})
	return err
}

func (a *Avalonminer) FaultLightOff(ctx context.Context) error {
	_, err := a.rpcCli.Send(ctx, "setled", map[string]int{"led": 0})
	return err
}

func (a *Avalonminer) StopMining(ctx context.Context) error {
	_, err := a.rpcCli.Send(ctx, "devpow", map[string]string{"action": "off"})
	return err
}

func (a *Avalonminer) ResumeMining(ctx context.Context) error {
	_, err := a.rpcCli.Send(ctx, "devpow", map[string]string{"action": "on"})
	return err
}

func (a *Avalonminer) IsMining(ctx context.Context) (bool, error) {
	sum, err := a.rpcCli.Send(ctx, "summary", nil)
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

// parseAvalonField extracts float values from Avalon's encoded strings.
// e.g. "TA[55 60 58]" with tag "TA" → []float64{55, 60, 58}
func parseAvalonField(s, tag string) []float64 {
	prefix := tag + "["
	idx := strings.Index(s, prefix)
	if idx < 0 {
		return nil
	}
	rest := s[idx+len(prefix):]
	end := strings.Index(rest, "]")
	if end < 0 {
		return nil
	}
	var result []float64
	for _, tok := range strings.Fields(rest[:end]) {
		if v, err := strconv.ParseFloat(tok, 64); err == nil && v > 0 {
			result = append(result, v)
		}
	}
	return result
}

func parseAvalonFieldInt(s, tag string) []int {
	prefix := tag + "["
	idx := strings.Index(s, prefix)
	if idx < 0 {
		return nil
	}
	rest := s[idx+len(prefix):]
	end := strings.Index(rest, "]")
	if end < 0 {
		return nil
	}
	var result []int
	for _, tok := range strings.Fields(rest[:end]) {
		if v, err := strconv.Atoi(tok); err == nil && v > 0 {
			result = append(result, v)
		}
	}
	return result
}

// ── SetMode ───────────────────────────────────────────────────────────────────

func (a *Avalonminer) SetMode(ctx context.Context, mode MiningMode) error {
	if mode == ModeSleep {
		return a.StopMining(ctx)
	}
	modeMap := map[MiningMode]string{
		ModeNormal:   "normal",
		ModeLowPower: "eco",
		ModeHighPerf: "turbo",
	}
	m, ok := modeMap[mode]
	if !ok {
		return fmt.Errorf("avalonminer: unsupported mode %q", mode)
	}
	_, err := a.rpcCli.Send(ctx, "setmode", map[string]string{"mode": m})
	return err
}

// ── SetFanSpeed ───────────────────────────────────────────────────────────────

func (a *Avalonminer) SetFanSpeed(ctx context.Context, pct int) error {
	if pct != -1 && (pct < 0 || pct > 100) {
		return fmt.Errorf("avalonminer: fan speed %d out of range", pct)
	}
	auto := 1
	if pct != -1 {
		auto = 0
	}
	_, err := a.rpcCli.Send(ctx, "setfan",
		map[string]interface{}{"fan_pct": pct, "auto": auto})
	return err
}

// ── UpdateFirmware ────────────────────────────────────────────────────────────

func (a *Avalonminer) UpdateFirmware(ctx context.Context, fw FirmwareInfo) error {
	if fw.LocalPath == "" && fw.URL == "" {
		return fmt.Errorf("avalonminer: UpdateFirmware requires LocalPath or URL")
	}
	filePath := fw.LocalPath
	if filePath == "" {
		tmp, err := downloadToTemp(ctx, fw.URL)
		if err != nil {
			return fmt.Errorf("avalonminer: firmware download: %w", err)
		}
		defer removeTemp(tmp)
		filePath = tmp
	}
	body, contentType, err := buildMultipartFile("firmware", filePath)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/cgi-bin/upgrade.cgi", a.ip)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
		body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := a.httpCli.Do(req)
	if err != nil {
		return fmt.Errorf("avalonminer firmware upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("avalonminer firmware upload: HTTP %d", resp.StatusCode)
	}
	return nil
}
