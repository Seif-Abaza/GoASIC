package miners

// ── Alternative Firmware Drivers ─────────────────────────────────────────────
//
// Third-party firmware replaces the stock Bitmain/Canaan firmware on the
// hardware.  The underlying ASIC chips and connectivity are identical to
// Antminer, but the API endpoints, authentication, and feature sets differ.
//
// | Firmware     | Auth method          | API style         | Special features        |
// |--------------|----------------------|-------------------|-------------------------|
// | Braiins OS+  | HTTP Basic / API key | REST+JSON port 80 | Auto-tuning, profiles   |
// | Vnish        | HTTP Basic auth      | CGI + JSON        | Per-chip tuning         |
// | LuxOS        | Bearer token         | REST port 4028+80 | Power targets, LED      |
// | Hiveon       | HTTP Basic auth      | CGI + JSON        | Auto-fan, overclocking  |
//
// All four extend AltFirmwareMiner which embeds Antminer for shared RPC logic
// and overrides only the endpoints that differ.

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

// ─────────────────────────────────────────────────────────────────────────────
// Braiins OS+ driver
// ─────────────────────────────────────────────────────────────────────────────
//
// Braiins OS+ (BOS+) replaces stock firmware with an open-source alternative
// offering auto-tuning, per-chip voltage control, and custom profiles.
//
// API: REST/JSON on port 80
//   GET  /cgi-bin/luci/admin/miner/api/status  → summary + hashrate
//   POST /cgi-bin/luci/admin/miner/api/pools   → pool config
//   POST /cgi-bin/luci/admin/miner/api/reboot  → reboot
//   POST /cgi-bin/luci/admin/miner/api/profile → set performance profile
//   Auth: HTTP Basic (admin / <password>)

type BraiinsOS struct {
	ip       string
	rpcCli   *rpc.Client
	client   *http.Client
	username string
	password string
}

// NewBraiinsOS creates a BraiinsOS+ driver.
// username/password default to "root"/admin if empty.
func NewBraiinsOS(ip, username, password string) (*BraiinsOS, error) {
	cli, err := rpc.New(ip, 4028, 10)
	if err != nil {
		return nil, err
	}
	if username == "" {
		username = "root"
	}
	if password == "" {
		password = "admin"
	}
	return &BraiinsOS{
		ip:       ip,
		rpcCli:   cli,
		username: username,
		password: password,
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: insecureTLS()},
		},
	}, nil
}

func (b *BraiinsOS) IP() string    { return b.ip }
func (b *BraiinsOS) Brand() string { return "BraiinsOS" }

func (b *BraiinsOS) GetData(ctx context.Context) (*MinerData, error) {
	// BOS+ exposes the standard cgminer RPC — reuse same parsing as Antminer
	ant := &Antminer{ip: b.ip, rpcCli: b.rpcCli, httpCli: b.client}
	d, err := ant.GetData(ctx)
	if err != nil {
		return nil, err
	}
	d.Make = "BraiinsOS"
	d.Firmware = "Braiins OS+"
	return d, nil
}

func (b *BraiinsOS) GetConfig(ctx context.Context) (*MinerConfig, error) {
	ant := &Antminer{ip: b.ip, rpcCli: b.rpcCli}
	return ant.GetConfig(ctx)
}

func (b *BraiinsOS) SendConfig(ctx context.Context, cfg MinerConfig) error {
	// BOS+ uses JSON POST to /cgi-bin/luci/admin/miner/api/pools
	type pool struct {
		URL      string `json:"url"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	var pools []pool
	for _, p := range cfg.Pools {
		pw := p.Password
		if pw == "" {
			pw = "x"
		}
		pools = append(pools, pool{URL: p.URL, User: p.User, Password: pw})
	}
	return b.post(ctx, "cgi-bin/luci/admin/miner/api/pools",
		map[string]interface{}{"pools": pools})
}

func (b *BraiinsOS) Reboot(ctx context.Context) error {
	return b.post(ctx, "cgi-bin/luci/admin/miner/api/reboot", map[string]string{})
}
func (b *BraiinsOS) FaultLightOn(ctx context.Context) error {
	return b.post(ctx, "cgi-bin/luci/admin/miner/api/identify",
		map[string]bool{"identify": true})
}
func (b *BraiinsOS) FaultLightOff(ctx context.Context) error {
	return b.post(ctx, "cgi-bin/luci/admin/miner/api/identify",
		map[string]bool{"identify": false})
}
func (b *BraiinsOS) StopMining(ctx context.Context) error {
	return b.post(ctx, "cgi-bin/luci/admin/miner/api/stop", map[string]string{})
}
func (b *BraiinsOS) ResumeMining(ctx context.Context) error {
	return b.post(ctx, "cgi-bin/luci/admin/miner/api/start", map[string]string{})
}
func (b *BraiinsOS) IsMining(ctx context.Context) (bool, error) {
	ant := &Antminer{ip: b.ip, rpcCli: b.rpcCli}
	return ant.IsMining(ctx)
}
func (b *BraiinsOS) SetMode(ctx context.Context, mode MiningMode) error {
	profileMap := map[MiningMode]string{
		ModeNormal:   "power_saving",
		ModeLowPower: "low_power",
		ModeHighPerf: "balanced",
	}
	if mode == ModeSleep {
		return b.StopMining(ctx)
	}
	p, ok := profileMap[mode]
	if !ok {
		return fmt.Errorf("braiins: unsupported mode %q", mode)
	}
	return b.post(ctx, "cgi-bin/luci/admin/miner/api/profile",
		map[string]string{"profile": p})
}
func (b *BraiinsOS) SetFanSpeed(ctx context.Context, pct int) error {
	if pct != -1 && (pct < 0 || pct > 100) {
		return fmt.Errorf("braiins: fan speed %d out of range", pct)
	}
	auto := pct == -1
	return b.post(ctx, "cgi-bin/luci/admin/miner/api/fan",
		map[string]interface{}{"fan_speed": pct, "auto": auto})
}
func (b *BraiinsOS) UpdateFirmware(ctx context.Context, fw FirmwareInfo) error {
	filePath := fw.LocalPath
	if filePath == "" {
		if fw.URL == "" {
			return fmt.Errorf("braiins: UpdateFirmware requires LocalPath or URL")
		}
		tmp, err := downloadToTemp(ctx, fw.URL)
		if err != nil {
			return err
		}
		defer removeTemp(tmp)
		filePath = tmp
	}
	body, ct, err := buildMultipartFile("firmware", filePath)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/cgi-bin/luci/admin/miner/api/firmware", b.ip)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	req.Header.Set("Content-Type", ct)
	req.SetBasicAuth(b.username, b.password)
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("braiins firmware: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (b *BraiinsOS) post(ctx context.Context, path string, payload interface{}) error {
	data, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://%s/%s", b.ip, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(b.username, b.password)
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("braiins POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Vnish driver
// ─────────────────────────────────────────────────────────────────────────────
//
// Vnish is a third-party firmware for Antminer hardware offering per-chip
// frequency tuning, voltage control, and efficiency profiles.
//
// API: Antminer-compatible CGI on port 80 + JSON extensions
//   POST /cgi-bin/set_miner_conf.cgi  → pools + freq
//   POST /cgi-bin/vnish/fan.cgi       → fan control
//   POST /cgi-bin/vnish/mode.cgi      → operating mode
//   POST /cgi-bin/upgrade.cgi         → firmware update
//   Auth: HTTP Basic (root / <password>)

type Vnish struct {
	ip       string
	rpcCli   *rpc.Client
	client   *http.Client
	username string
	password string
}

func NewVnish(ip, username, password string) (*Vnish, error) {
	cli, err := rpc.New(ip, 4028, 10)
	if err != nil {
		return nil, err
	}
	if username == "" {
		username = "root"
	}
	if password == "" {
		password = "admin"
	}
	return &Vnish{
		ip: ip, rpcCli: cli,
		username: username, password: password,
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: insecureTLS()},
		},
	}, nil
}

func (v *Vnish) IP() string    { return v.ip }
func (v *Vnish) Brand() string { return "Vnish" }

func (v *Vnish) GetData(ctx context.Context) (*MinerData, error) {
	ant := &Antminer{ip: v.ip, rpcCli: v.rpcCli, httpCli: v.client}
	d, err := ant.GetData(ctx)
	if err != nil {
		return nil, err
	}
	d.Make = "Vnish"
	d.Firmware = "Vnish"
	return d, nil
}
func (v *Vnish) GetConfig(ctx context.Context) (*MinerConfig, error) {
	return (&Antminer{ip: v.ip, rpcCli: v.rpcCli}).GetConfig(ctx)
}
func (v *Vnish) SendConfig(ctx context.Context, cfg MinerConfig) error {
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
	return v.post(ctx, "cgi-bin/set_miner_conf.cgi", payload)
}
func (v *Vnish) Reboot(ctx context.Context) error {
	v.rpcCli.Send(ctx, "restart", nil)
	return nil
}
func (v *Vnish) FaultLightOn(ctx context.Context) error {
	return v.post(ctx, "cgi-bin/vnish/led.cgi", map[string]bool{"blink": true})
}
func (v *Vnish) FaultLightOff(ctx context.Context) error {
	return v.post(ctx, "cgi-bin/vnish/led.cgi", map[string]bool{"blink": false})
}
func (v *Vnish) StopMining(ctx context.Context) error {
	return v.post(ctx, "cgi-bin/vnish/mode.cgi", map[string]string{"mode": "sleep"})
}
func (v *Vnish) ResumeMining(ctx context.Context) error {
	return v.post(ctx, "cgi-bin/vnish/mode.cgi", map[string]string{"mode": "mining"})
}
func (v *Vnish) IsMining(ctx context.Context) (bool, error) {
	return (&Antminer{ip: v.ip, rpcCli: v.rpcCli}).IsMining(ctx)
}
func (v *Vnish) SetMode(ctx context.Context, mode MiningMode) error {
	if mode == ModeSleep {
		return v.StopMining(ctx)
	}
	modeMap := map[MiningMode]string{
		ModeNormal:   "normal",
		ModeLowPower: "efficient",
		ModeHighPerf: "performance",
	}
	m, ok := modeMap[mode]
	if !ok {
		return fmt.Errorf("vnish: unsupported mode %q", mode)
	}
	return v.post(ctx, "cgi-bin/vnish/mode.cgi", map[string]string{"mode": m})
}
func (v *Vnish) SetFanSpeed(ctx context.Context, pct int) error {
	if pct != -1 && (pct < 0 || pct > 100) {
		return fmt.Errorf("vnish: fan speed %d out of range", pct)
	}
	return v.post(ctx, "cgi-bin/vnish/fan.cgi",
		map[string]interface{}{"fan_pwm": pct, "auto": pct == -1})
}
func (v *Vnish) UpdateFirmware(ctx context.Context, fw FirmwareInfo) error {
	filePath := fw.LocalPath
	if filePath == "" {
		tmp, err := downloadToTemp(ctx, fw.URL)
		if err != nil {
			return err
		}
		defer removeTemp(tmp)
		filePath = tmp
	}
	body, ct, err := buildMultipartFile("firmware", filePath)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/cgi-bin/upgrade.cgi", v.ip)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	req.Header.Set("Content-Type", ct)
	req.SetBasicAuth(v.username, v.password)
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
func (v *Vnish) post(ctx context.Context, path string, payload interface{}) error {
	data, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://%s/%s", v.ip, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(v.username, v.password)
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("vnish POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// LuxOS driver
// ─────────────────────────────────────────────────────────────────────────────
//
// LuxOS (by Luxor Tech) is a cloud-managed firmware offering power target mode,
// per-chip tuning, and remote monitoring. Used on S19/S21 series.
//
// API: Bearer token auth, REST on port 80
//   GET  /api/v1/status         → summary
//   POST /api/v1/updatePools    → pools
//   POST /api/v1/setProfile     → profile / power target
//   POST /api/v1/setFan         → fan control
//   POST /api/v1/reboot         → reboot
//   POST /api/v1/upgradeFirmware → OTA update

type LuxOS struct {
	ip     string
	rpcCli *rpc.Client
	client *http.Client
	token  string // Bearer token
}

func NewLuxOS(ip, bearerToken string) (*LuxOS, error) {
	cli, err := rpc.New(ip, 4028, 10)
	if err != nil {
		return nil, err
	}
	return &LuxOS{
		ip: ip, rpcCli: cli, token: bearerToken,
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: insecureTLS()},
		},
	}, nil
}

func (l *LuxOS) IP() string    { return l.ip }
func (l *LuxOS) Brand() string { return "LuxOS" }

func (l *LuxOS) GetData(ctx context.Context) (*MinerData, error) {
	ant := &Antminer{ip: l.ip, rpcCli: l.rpcCli, httpCli: l.client}
	d, err := ant.GetData(ctx)
	if err != nil {
		return nil, err
	}
	d.Make = "LuxOS"
	d.Firmware = "LuxOS"
	return d, nil
}
func (l *LuxOS) GetConfig(ctx context.Context) (*MinerConfig, error) {
	return (&Antminer{ip: l.ip, rpcCli: l.rpcCli}).GetConfig(ctx)
}
func (l *LuxOS) SendConfig(ctx context.Context, cfg MinerConfig) error {
	type pool struct {
		URL      string `json:"url"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	var pools []pool
	for _, p := range cfg.Pools {
		pw := p.Password
		if pw == "" {
			pw = "x"
		}
		pools = append(pools, pool{URL: p.URL, User: p.User, Password: pw})
	}
	return l.post(ctx, "api/v1/updatePools", map[string]interface{}{"pools": pools})
}
func (l *LuxOS) Reboot(ctx context.Context) error {
	return l.post(ctx, "api/v1/reboot", map[string]string{})
}
func (l *LuxOS) FaultLightOn(ctx context.Context) error {
	return l.post(ctx, "api/v1/led", map[string]bool{"led": true})
}
func (l *LuxOS) FaultLightOff(ctx context.Context) error {
	return l.post(ctx, "api/v1/led", map[string]bool{"led": false})
}
func (l *LuxOS) StopMining(ctx context.Context) error {
	return l.post(ctx, "api/v1/stopMining", map[string]string{})
}
func (l *LuxOS) ResumeMining(ctx context.Context) error {
	return l.post(ctx, "api/v1/startMining", map[string]string{})
}
func (l *LuxOS) IsMining(ctx context.Context) (bool, error) {
	return (&Antminer{ip: l.ip, rpcCli: l.rpcCli}).IsMining(ctx)
}
func (l *LuxOS) SetMode(ctx context.Context, mode MiningMode) error {
	if mode == ModeSleep {
		return l.StopMining(ctx)
	}
	profileMap := map[MiningMode]string{
		ModeNormal:   "balanced",
		ModeLowPower: "efficiency",
		ModeHighPerf: "performance",
	}
	p, ok := profileMap[mode]
	if !ok {
		return fmt.Errorf("luxos: unsupported mode %q", mode)
	}
	return l.post(ctx, "api/v1/setProfile", map[string]string{"profile": p})
}
func (l *LuxOS) SetFanSpeed(ctx context.Context, pct int) error {
	if pct != -1 && (pct < 0 || pct > 100) {
		return fmt.Errorf("luxos: fan speed %d out of range", pct)
	}
	return l.post(ctx, "api/v1/setFan",
		map[string]interface{}{"fan_pct": pct, "auto": pct == -1})
}
func (l *LuxOS) UpdateFirmware(ctx context.Context, fw FirmwareInfo) error {
	if fw.URL != "" {
		// LuxOS supports OTA by URL directly
		return l.post(ctx, "api/v1/upgradeFirmware", map[string]string{"url": fw.URL})
	}
	filePath := fw.LocalPath
	body, ct, err := buildMultipartFile("firmware", filePath)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/api/v1/upgradeFirmware", l.ip)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	req.Header.Set("Content-Type", ct)
	if l.token != "" {
		req.Header.Set("Authorization", "Bearer "+l.token)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
func (l *LuxOS) post(ctx context.Context, path string, payload interface{}) error {
	data, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://%s/%s", l.ip, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if l.token != "" {
		req.Header.Set("Authorization", "Bearer "+l.token)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return fmt.Errorf("luxos POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Hiveon driver
// ─────────────────────────────────────────────────────────────────────────────
//
// Hiveon is a firmware / management platform for Antminer hardware.
// API is Antminer-compatible CGI with JSON extensions on port 80.
//   Auth: HTTP Basic (root / <password>)
//   POST /cgi-bin/set_miner_conf.cgi  → pool config
//   POST /cgi-bin/hiveon/fan.cgi      → fan control
//   POST /cgi-bin/hiveon/mode.cgi     → operating mode
//   POST /cgi-bin/upgrade.cgi         → firmware update

type Hiveon struct {
	ip       string
	rpcCli   *rpc.Client
	client   *http.Client
	username string
	password string
}

func NewHiveon(ip, username, password string) (*Hiveon, error) {
	cli, err := rpc.New(ip, 4028, 10)
	if err != nil {
		return nil, err
	}
	if username == "" {
		username = "root"
	}
	if password == "" {
		password = "admin"
	}
	return &Hiveon{
		ip: ip, rpcCli: cli,
		username: username, password: password,
		client: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: insecureTLS()},
		},
	}, nil
}

func (h *Hiveon) IP() string    { return h.ip }
func (h *Hiveon) Brand() string { return "Hiveon" }

func (h *Hiveon) GetData(ctx context.Context) (*MinerData, error) {
	ant := &Antminer{ip: h.ip, rpcCli: h.rpcCli, httpCli: h.client}
	d, err := ant.GetData(ctx)
	if err != nil {
		return nil, err
	}
	d.Make = "Hiveon"
	d.Firmware = "Hiveon"
	return d, nil
}
func (h *Hiveon) GetConfig(ctx context.Context) (*MinerConfig, error) {
	return (&Antminer{ip: h.ip, rpcCli: h.rpcCli}).GetConfig(ctx)
}
func (h *Hiveon) SendConfig(ctx context.Context, cfg MinerConfig) error {
	payload := map[string]string{
		"_ant_pool1url":  cfg.Pool(0).URL,  "_ant_pool1user": cfg.Pool(0).User,
		"_ant_pool1pw":   poolPW(cfg.Pool(0)),
		"_ant_pool2url":  cfg.Pool(1).URL,  "_ant_pool2user": cfg.Pool(1).User,
		"_ant_pool2pw":   poolPW(cfg.Pool(1)),
		"_ant_pool3url":  cfg.Pool(2).URL,  "_ant_pool3user": cfg.Pool(2).User,
		"_ant_pool3pw":   poolPW(cfg.Pool(2)),
	}
	return h.post(ctx, "cgi-bin/set_miner_conf.cgi", payload)
}
func (h *Hiveon) Reboot(ctx context.Context) error {
	h.rpcCli.Send(ctx, "restart", nil)
	return nil
}
func (h *Hiveon) FaultLightOn(ctx context.Context) error {
	return h.post(ctx, "cgi-bin/blink.cgi", map[string]bool{"blink": true})
}
func (h *Hiveon) FaultLightOff(ctx context.Context) error {
	return h.post(ctx, "cgi-bin/blink.cgi", map[string]bool{"blink": false})
}
func (h *Hiveon) StopMining(ctx context.Context) error {
	return h.post(ctx, "cgi-bin/hiveon/mode.cgi", map[string]string{"mode": "sleep"})
}
func (h *Hiveon) ResumeMining(ctx context.Context) error {
	return h.post(ctx, "cgi-bin/hiveon/mode.cgi", map[string]string{"mode": "mining"})
}
func (h *Hiveon) IsMining(ctx context.Context) (bool, error) {
	return (&Antminer{ip: h.ip, rpcCli: h.rpcCli}).IsMining(ctx)
}
func (h *Hiveon) SetMode(ctx context.Context, mode MiningMode) error {
	if mode == ModeSleep {
		return h.StopMining(ctx)
	}
	modeMap := map[MiningMode]string{
		ModeNormal:   "normal",
		ModeLowPower: "low_power",
		ModeHighPerf: "high_performance",
	}
	m, ok := modeMap[mode]
	if !ok {
		return fmt.Errorf("hiveon: unsupported mode %q", mode)
	}
	return h.post(ctx, "cgi-bin/hiveon/mode.cgi", map[string]string{"mode": m})
}
func (h *Hiveon) SetFanSpeed(ctx context.Context, pct int) error {
	if pct != -1 && (pct < 0 || pct > 100) {
		return fmt.Errorf("hiveon: fan speed %d out of range", pct)
	}
	return h.post(ctx, "cgi-bin/hiveon/fan.cgi",
		map[string]interface{}{"fan_pwm": pct, "auto": pct == -1})
}
func (h *Hiveon) UpdateFirmware(ctx context.Context, fw FirmwareInfo) error {
	filePath := fw.LocalPath
	if filePath == "" {
		tmp, err := downloadToTemp(ctx, fw.URL)
		if err != nil {
			return err
		}
		defer removeTemp(tmp)
		filePath = tmp
	}
	body, ct, err := buildMultipartFile("firmware", filePath)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/cgi-bin/upgrade.cgi", h.ip)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	req.Header.Set("Content-Type", ct)
	req.SetBasicAuth(h.username, h.password)
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
func (h *Hiveon) post(ctx context.Context, path string, payload interface{}) error {
	data, _ := json.Marshal(payload)
	url := fmt.Sprintf("http://%s/%s", h.ip, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(h.username, h.password)
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("hiveon POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Compiler-enforced interface compliance
// Ensures all alternative firmware types satisfy the Miner interface.
// If any method is missing the build fails with a clear error.
// ─────────────────────────────────────────────────────────────────────────────

var (
	_ Miner = (*BraiinsOS)(nil)
	_ Miner = (*Vnish)(nil)
	_ Miner = (*LuxOS)(nil)
	_ Miner = (*Hiveon)(nil)
	_ Miner = (*Innosilicon)(nil)
)
