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

// Innosilicon implements the Miner interface for Innosilicon ASIC miners.
//
// Supported models: T3, T3+, T2T, A10 Pro (ETH — legacy), A11 Pro
//
// Innosilicon T3 uses a mix of cgminer RPC (port 4028) for status
// and an HTTP web panel (port 80) for configuration.
//
// API notes:
//   - RPC summary/stats/pools use standard cgminer protocol
//   - Config push: POST /api/v1/updatePools  (JSON body)
//   - Mode change: POST /api/v1/setMinerMode
//   - Fan control: POST /api/v1/setFanSpeed
//   - Firmware:    POST /api/v1/upgradeUnit  (multipart)
type Innosilicon struct {
	ip     string
	rpcCli *rpc.Client
	client *http.Client
}

func NewInnosilicon(ip string) (*Innosilicon, error) {
	cli, err := rpc.New(ip, 4028, 10)
	if err != nil {
		return nil, err
	}
	return &Innosilicon{
		ip:     ip,
		rpcCli: cli,
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: insecureTLS()},
		},
	}, nil
}

func (n *Innosilicon) IP() string    { return n.ip }
func (n *Innosilicon) Brand() string { return "Innosilicon" }

func (n *Innosilicon) GetData(ctx context.Context) (*MinerData, error) {
	d := &MinerData{
		IP:        n.ip,
		DateTime:  time.Now(),
		Make:      "Innosilicon",
		Algorithm: "SHA-256d",
		Cooling:   "Air",
	}

	sum, err := n.rpcCli.Send(ctx, "summary", nil)
	if err != nil {
		return nil, fmt.Errorf("innosilicon %s summary: %w", n.ip, err)
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

	if stats, err := n.rpcCli.Send(ctx, "stats", nil); err == nil {
		for _, si := range rpc.GetArray(stats, "STATS") {
			stat, ok := si.(map[string]interface{})
			if !ok {
				continue
			}
			if t := rpc.GetString(stat, "Type"); t != "" {
				d.Model = t
			}
			var temps []float64
			for i := 1; i <= 4; i++ {
				for _, key := range []string{fmt.Sprintf("temp%d", i), fmt.Sprintf("temp2_%d", i)} {
					if v, ok := rpc.GetFloat(stat, key); ok && v > 0 {
						temps = append(temps, v)
						break
					}
				}
			}
			if len(temps) > 0 {
				d.Temperature = temps
			}
			var fans []int
			for i := 1; i <= 4; i++ {
				if v, ok := rpc.GetFloat(stat, fmt.Sprintf("fan%d", i)); ok && v > 0 {
					fans = append(fans, int(v))
				}
			}
			if len(fans) > 0 {
				d.FanSpeeds = fans
			}
		}
	}

	if pools, err := n.rpcCli.Send(ctx, "pools", nil); err == nil {
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

func (n *Innosilicon) GetConfig(ctx context.Context) (*MinerConfig, error) {
	pools, err := n.rpcCli.Send(ctx, "pools", nil)
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

func (n *Innosilicon) SendConfig(ctx context.Context, cfg MinerConfig) error {
	type poolEntry struct {
		URL      string `json:"pool"`
		User     string `json:"worker"`
		Password string `json:"passwd"`
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
	return n.apiPost(ctx, "api/v1/updatePools", payload)
}

func (n *Innosilicon) Reboot(ctx context.Context) error {
	if _, err := n.rpcCli.Send(ctx, "restart", nil); err == nil {
		return nil
	}
	return n.apiPost(ctx, "api/v1/reboot", map[string]string{})
}

func (n *Innosilicon) FaultLightOn(ctx context.Context) error {
	return n.apiPost(ctx, "api/v1/setLed", map[string]bool{"led": true})
}
func (n *Innosilicon) FaultLightOff(ctx context.Context) error {
	return n.apiPost(ctx, "api/v1/setLed", map[string]bool{"led": false})
}

func (n *Innosilicon) StopMining(ctx context.Context) error {
	return n.apiPost(ctx, "api/v1/setMinerMode", map[string]string{"mode": "sleep"})
}
func (n *Innosilicon) ResumeMining(ctx context.Context) error {
	return n.apiPost(ctx, "api/v1/setMinerMode", map[string]string{"mode": "normal"})
}
func (n *Innosilicon) IsMining(ctx context.Context) (bool, error) {
	sum, err := n.rpcCli.Send(ctx, "summary", nil)
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

func (n *Innosilicon) SetMode(ctx context.Context, mode MiningMode) error {
	if mode == ModeSleep {
		return n.StopMining(ctx)
	}
	modeMap := map[MiningMode]string{
		ModeNormal:   "normal",
		ModeLowPower: "eco",
		ModeHighPerf: "boost",
	}
	m, ok := modeMap[mode]
	if !ok {
		return fmt.Errorf("innosilicon: unsupported mode %q", mode)
	}
	return n.apiPost(ctx, "api/v1/setMinerMode", map[string]string{"mode": m})
}

func (n *Innosilicon) SetFanSpeed(ctx context.Context, pct int) error {
	if pct != -1 && (pct < 0 || pct > 100) {
		return fmt.Errorf("innosilicon: fan speed %d out of range", pct)
	}
	return n.apiPost(ctx, "api/v1/setFanSpeed",
		map[string]interface{}{"fan_pct": pct, "auto": pct == -1})
}

func (n *Innosilicon) UpdateFirmware(ctx context.Context, fw FirmwareInfo) error {
	if fw.LocalPath == "" && fw.URL == "" {
		return fmt.Errorf("innosilicon: UpdateFirmware requires LocalPath or URL")
	}
	filePath := fw.LocalPath
	if filePath == "" {
		tmp, err := downloadToTemp(ctx, fw.URL)
		if err != nil {
			return fmt.Errorf("innosilicon: firmware download: %w", err)
		}
		defer removeTemp(tmp)
		filePath = tmp
	}
	body, contentType, err := buildMultipartFile("firmware", filePath)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/api/v1/upgradeUnit", n.ip)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("innosilicon firmware upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("innosilicon firmware upload: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (n *Innosilicon) apiPost(ctx context.Context, path string, payload interface{}) error {
	url := fmt.Sprintf("http://%s/%s", n.ip, strings.TrimPrefix(path, "/"))
	data, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("POST %s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}
