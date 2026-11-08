package server

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/vulnintel"
)

const (
	settingVulnIntelOn           = "vulnintel_enabled"
	settingVulnIntelNVDKey       = "vulnintel_nvd_api_key"
	settingVulnIntelGitHubToken  = "vulnintel_github_token"
	settingVulnIntelProxy        = "vulnintel_proxy"
	settingVulnIntelPoc          = "vulnintel_poc_enabled"
	settingVulnIntelBackfillDays = "vulnintel_backfill_days"
	settingVulnIntelRuntime      = "vulnintel_max_runtime_minutes"
)

type vulnIntelService struct {
	pg        *db.DB
	store     *vulnintel.Store
	nucleiDir string

	mu        sync.Mutex
	running   bool
	startedAt time.Time
	phase     string
	cancel    context.CancelFunc
}

func newVulnIntelService(pg *db.DB, dataDir string) *vulnIntelService {
	return &vulnIntelService{
		pg:        pg,
		store:     vulnintel.New(pg),
		nucleiDir: filepath.Join(dataDir, "nuclei-templates"),
	}
}

func (v *vulnIntelService) wireAgentTools() {
	if v == nil {
		return
	}
	agent.SetVulnIntelStore(v.store)
	agent.SetNucleiTemplatesDir(v.nucleiDir)
}

func (v *vulnIntelService) Run(ctx context.Context) {
	if v == nil || v.pg == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(90 * time.Second):
	}
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		if v.pg.GetBool(settingVulnIntelOn, true) {
			_ = v.runPass(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (v *vulnIntelService) SyncNow() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	if v.running {
		v.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	v.running = true
	v.startedAt = time.Now()
	v.cancel = cancel
	v.phase = "同步排队"
	v.mu.Unlock()
	go func() {
		defer cancel()
		_ = v.runPass(ctx)
		v.mu.Lock()
		v.running = false
		v.cancel = nil
		v.phase = ""
		v.mu.Unlock()
	}()
	return true
}

func (v *vulnIntelService) Cancel() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.running || v.cancel == nil {
		return false
	}
	v.cancel()
	v.phase = "正在中断"
	return true
}

func (v *vulnIntelService) State() (bool, time.Time, string) {
	if v == nil {
		return false, time.Time{}, ""
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.running, v.startedAt, v.phase
}

func (v *vulnIntelService) setPhase(phase string) {
	v.mu.Lock()
	v.phase = phase
	v.mu.Unlock()
}

func (v *vulnIntelService) runPass(parent context.Context) error {
	if v == nil || v.store == nil {
		return errors.New("漏洞情报库未初始化")
	}
	v.mu.Lock()
	if v.running && v.cancel == nil {
		v.mu.Unlock()
		return errors.New("漏洞情报同步已在运行")
	}
	if !v.running {
		v.running = true
		v.startedAt = time.Now()
	}
	ctx, cancel := context.WithTimeout(parent, v.maxRuntime())
	v.cancel = cancel
	v.mu.Unlock()
	defer func() {
		cancel()
		v.mu.Lock()
		v.running = false
		v.cancel = nil
		v.phase = ""
		v.mu.Unlock()
	}()

	v.setPhase("同步 CVE / KEV / PoC")
	cfg := vulnintel.Config{
		NvdAPIKey:    v.setting(settingVulnIntelNVDKey),
		GitHubToken:  v.setting(settingVulnIntelGitHubToken),
		ProxyURL:     v.proxy(),
		PocEnabled:   v.pg.GetBool(settingVulnIntelPoc, false),
		BackfillDays: v.intSetting(settingVulnIntelBackfillDays, 0),
		MaxRuntime:   v.maxRuntime(),
	}
	if err := vulnintel.Run(ctx, v.store, cfg); err != nil && ctx.Err() == nil {
		log.Printf("[vulnintel] sync: %v", err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	v.setPhase("同步 GHSA 版本区间")
	if client, err := vulnintel.NewGhsaClient(cfg.GitHubToken, cfg.ProxyURL); err == nil {
		since := time.Now().Add(-7 * 24 * time.Hour)
		if state, err := v.store.GetSyncState(ctx, "ghsa"); err == nil && state.Cursor != "" {
			if t, err := time.Parse(time.RFC3339, state.Cursor); err == nil {
				since = t
			}
		}
		if _, err := v.store.SyncGhsa(ctx, client, since, 3); err != nil {
			log.Printf("[vulnintel] ghsa: %v", err)
		}
	}

	v.setPhase("更新 nuclei 模板")
	tplCtx, tplCancel := context.WithTimeout(context.Background(), 15*time.Minute)
	if err := vulnintel.EnsureTemplates(tplCtx, v.nucleiDir, cfg.ProxyURL); err != nil {
		log.Printf("[vulnintel] nuclei templates: %v", err)
	}
	tplCancel()
	return nil
}

func (v *vulnIntelService) setting(key string) string {
	if v == nil || v.pg == nil {
		return ""
	}
	value, _, _ := v.pg.GetSetting(key)
	return strings.TrimSpace(value)
}

func (v *vulnIntelService) intSetting(key string, fallback int) int {
	value := v.setting(key)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func (v *vulnIntelService) maxRuntime() time.Duration {
	minutes := v.intSetting(settingVulnIntelRuntime, 30)
	if minutes < 5 {
		minutes = 5
	}
	return time.Duration(minutes) * time.Minute
}

func (v *vulnIntelService) proxy() string {
	if value := v.setting(settingVulnIntelProxy); value != "" {
		return value
	}
	return v.setting(settingGlobalProxy)
}

func nucleiTemplateStats(dir string) map[string]any {
	out := map[string]any{"dir": dir, "exists": false, "templates": 0}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		out["hint"] = "模板树尚未拉取，点击立即同步"
		return out
	}
	out["exists"] = true
	count := 0
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".yaml") {
			count++
		}
		return nil
	})
	out["templates"] = count
	if count == 0 {
		out["hint"] = "目录存在但没有模板，请重新同步"
	} else {
		out["hint"] = "模板树可用"
	}
	return out
}
