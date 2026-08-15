package service

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// TLSFingerprintProfileRepository 定义 TLS 指纹模板的数据访问接口
type TLSFingerprintProfileRepository interface {
	List(ctx context.Context) ([]*model.TLSFingerprintProfile, error)
	GetByID(ctx context.Context, id int64) (*model.TLSFingerprintProfile, error)
	Create(ctx context.Context, profile *model.TLSFingerprintProfile) (*model.TLSFingerprintProfile, error)
	Update(ctx context.Context, profile *model.TLSFingerprintProfile) (*model.TLSFingerprintProfile, error)
	Delete(ctx context.Context, id int64) error
}

// TLSFingerprintProfileCache 定义 TLS 指纹模板的缓存接口
type TLSFingerprintProfileCache interface {
	Get(ctx context.Context) ([]*model.TLSFingerprintProfile, bool)
	Set(ctx context.Context, profiles []*model.TLSFingerprintProfile) error
	Invalidate(ctx context.Context) error
	NotifyUpdate(ctx context.Context) error
	SubscribeUpdates(ctx context.Context, handler func())
}

// TLSFingerprintProfileService TLS 指纹模板管理服务
type TLSFingerprintProfileService struct {
	repo  TLSFingerprintProfileRepository
	cache TLSFingerprintProfileCache

	// 本地 ID→Profile 映射缓存，用于 DoWithTLS 热路径快速查找
	localCache map[int64]*model.TLSFingerprintProfile
	localMu    sync.RWMutex
}

// GenerateTLSFingerprintProfilesResult describes an idempotent installation
// of the recommended built-in TLS profiles.
type GenerateTLSFingerprintProfilesResult struct {
	Profiles []*model.TLSFingerprintProfile `json:"profiles"`
	Created  int                            `json:"created"`
}

// NewTLSFingerprintProfileService 创建 TLS 指纹模板服务
func NewTLSFingerprintProfileService(
	repo TLSFingerprintProfileRepository,
	cache TLSFingerprintProfileCache,
) *TLSFingerprintProfileService {
	svc := &TLSFingerprintProfileService{
		repo:       repo,
		cache:      cache,
		localCache: make(map[int64]*model.TLSFingerprintProfile),
	}

	ctx := context.Background()
	if err := svc.reloadFromDB(ctx); err != nil {
		logger.LegacyPrintf("service.tls_fp_profile", "[TLSFPProfileService] Failed to load profiles from DB on startup: %v", err)
		if fallbackErr := svc.refreshLocalCache(ctx); fallbackErr != nil {
			logger.LegacyPrintf("service.tls_fp_profile", "[TLSFPProfileService] Failed to load profiles from cache fallback on startup: %v", fallbackErr)
		}
	}

	if cache != nil {
		cache.SubscribeUpdates(ctx, func() {
			if err := svc.refreshLocalCache(context.Background()); err != nil {
				logger.LegacyPrintf("service.tls_fp_profile", "[TLSFPProfileService] Failed to refresh cache on notification: %v", err)
			}
		})
	}

	return svc
}

// --- CRUD ---

// List 获取所有模板
func (s *TLSFingerprintProfileService) List(ctx context.Context) ([]*model.TLSFingerprintProfile, error) {
	return s.repo.List(ctx)
}

// GetByID 根据 ID 获取模板
func (s *TLSFingerprintProfileService) GetByID(ctx context.Context, id int64) (*model.TLSFingerprintProfile, error) {
	return s.repo.GetByID(ctx, id)
}

// Create 创建模板
func (s *TLSFingerprintProfileService) Create(ctx context.Context, profile *model.TLSFingerprintProfile) (*model.TLSFingerprintProfile, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}

	created, err := s.repo.Create(ctx, profile)
	if err != nil {
		return nil, err
	}

	refreshCtx, cancel := s.newCacheRefreshContext()
	defer cancel()
	s.invalidateAndNotify(refreshCtx)

	return created, nil
}

// Update 更新模板
func (s *TLSFingerprintProfileService) Update(ctx context.Context, profile *model.TLSFingerprintProfile) (*model.TLSFingerprintProfile, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}

	updated, err := s.repo.Update(ctx, profile)
	if err != nil {
		return nil, err
	}

	refreshCtx, cancel := s.newCacheRefreshContext()
	defer cancel()
	s.invalidateAndNotify(refreshCtx)

	return updated, nil
}

// Delete 删除模板
func (s *TLSFingerprintProfileService) Delete(ctx context.Context, id int64) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	refreshCtx, cancel := s.newCacheRefreshContext()
	defer cancel()
	s.invalidateAndNotify(refreshCtx)

	return nil
}

// GenerateRecommendedProfiles installs the maintained Claude Code profile
// catalog without requiring the administrator to capture ClientHello packets
// from physical devices. Existing profiles with the same name are preserved,
// making the operation safe to run repeatedly.
func (s *TLSFingerprintProfileService) GenerateRecommendedProfiles(ctx context.Context) (*GenerateTLSFingerprintProfilesResult, error) {
	existing, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]*model.TLSFingerprintProfile, len(existing))
	for _, profile := range existing {
		if profile != nil {
			byName[profile.Name] = profile
		}
	}

	builtIn := tlsfingerprint.BuiltInDefaultProfile()
	description := "Maintained Sub2API baseline for Claude Code / Node.js 24.x; reusable across accounts and does not require per-device capture"
	recommended := []*model.TLSFingerprintProfile{
		{
			Name:                builtIn.Name,
			Description:         &description,
			EnableGREASE:        builtIn.EnableGREASE,
			CipherSuites:        builtIn.CipherSuites,
			Curves:              builtIn.Curves,
			PointFormats:        builtIn.PointFormats,
			SignatureAlgorithms: builtIn.SignatureAlgorithms,
			ALPNProtocols:       builtIn.ALPNProtocols,
			SupportedVersions:   builtIn.SupportedVersions,
			KeyShareGroups:      builtIn.KeyShareGroups,
			PSKModes:            builtIn.PSKModes,
			Extensions:          builtIn.Extensions,
		},
	}

	result := &GenerateTLSFingerprintProfilesResult{
		Profiles: make([]*model.TLSFingerprintProfile, 0, len(recommended)),
	}
	for _, profile := range recommended {
		if found := byName[profile.Name]; found != nil {
			result.Profiles = append(result.Profiles, found)
			continue
		}
		if err := profile.Validate(); err != nil {
			return nil, err
		}
		created, createErr := s.repo.Create(ctx, profile)
		if createErr != nil {
			return nil, createErr
		}
		result.Profiles = append(result.Profiles, created)
		result.Created++
	}

	if result.Created > 0 {
		refreshCtx, cancel := s.newCacheRefreshContext()
		defer cancel()
		s.invalidateAndNotify(refreshCtx)
	}

	return result, nil
}

// --- 热路径：运行时 Profile 查找 ---

// GetProfileByID 根据 ID 从本地缓存获取 Profile（用于 DoWithTLS 热路径）
// 返回 nil 表示未找到，调用方应 fallback 到内置默认 Profile
func (s *TLSFingerprintProfileService) GetProfileByID(id int64) *tlsfingerprint.Profile {
	s.localMu.RLock()
	p, ok := s.localCache[id]
	s.localMu.RUnlock()

	if ok && p != nil {
		return p.ToTLSProfile()
	}
	return nil
}

// getRandomProfile 从本地缓存中确定性地选取一个 Profile：按 ID 排序后对账号 ID 取模。
// 同一账号永远得到同一个 Profile（不会在请求间闪烁 JA3），不同账号则分散到不同 Profile。
func (s *TLSFingerprintProfileService) getRandomProfile(accountID int64) *tlsfingerprint.Profile {
	s.localMu.RLock()
	defer s.localMu.RUnlock()

	ids := make([]int64, 0, len(s.localCache))
	for id, p := range s.localCache {
		if p != nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	if accountID < 0 {
		accountID = -accountID
	}
	return s.localCache[ids[accountID%int64(len(ids))]].ToTLSProfile()
}

// ResolveTLSProfile 根据 Account 的配置解析出运行时 TLS Profile
//
// 逻辑：
//  1. 未启用 TLS 指纹 → 返回 nil（不伪装）
//  2. 启用 + 绑定了 profile_id → 从缓存查找对应 profile
//  3. 启用 + 未绑定或找不到 → 返回空 Profile（使用代码内置默认值）
func (s *TLSFingerprintProfileService) ResolveTLSProfile(account *Account) *tlsfingerprint.Profile {
	if account == nil || !account.IsTLSFingerprintEnabled() {
		return nil
	}
	id := account.GetTLSFingerprintProfileID()
	if id > 0 {
		if p := s.GetProfileByID(id); p != nil {
			return p
		}
	}
	if id == -1 {
		// 随机模式：按账号 ID 确定性选取，保证单账号稳定
		if p := s.getRandomProfile(account.ID); p != nil {
			return p
		}
	}
	// TLS 启用但无绑定 profile → 空 Profile → dialer 使用内置默认值
	return &tlsfingerprint.Profile{Name: "Built-in Default (Node.js 24.x)"}
}

// --- 缓存管理 ---

func (s *TLSFingerprintProfileService) refreshLocalCache(ctx context.Context) error {
	if s.cache != nil {
		if profiles, ok := s.cache.Get(ctx); ok {
			s.setLocalCache(profiles)
			return nil
		}
	}
	return s.reloadFromDB(ctx)
}

func (s *TLSFingerprintProfileService) reloadFromDB(ctx context.Context) error {
	profiles, err := s.repo.List(ctx)
	if err != nil {
		return err
	}

	if s.cache != nil {
		if err := s.cache.Set(ctx, profiles); err != nil {
			logger.LegacyPrintf("service.tls_fp_profile", "[TLSFPProfileService] Failed to set cache: %v", err)
		}
	}

	s.setLocalCache(profiles)
	return nil
}

func (s *TLSFingerprintProfileService) setLocalCache(profiles []*model.TLSFingerprintProfile) {
	m := make(map[int64]*model.TLSFingerprintProfile, len(profiles))
	for _, p := range profiles {
		m[p.ID] = p
	}

	s.localMu.Lock()
	s.localCache = m
	s.localMu.Unlock()
}

func (s *TLSFingerprintProfileService) newCacheRefreshContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Second)
}

func (s *TLSFingerprintProfileService) invalidateAndNotify(ctx context.Context) {
	if s.cache != nil {
		if err := s.cache.Invalidate(ctx); err != nil {
			logger.LegacyPrintf("service.tls_fp_profile", "[TLSFPProfileService] Failed to invalidate cache: %v", err)
		}
	}

	if err := s.reloadFromDB(ctx); err != nil {
		logger.LegacyPrintf("service.tls_fp_profile", "[TLSFPProfileService] Failed to refresh local cache: %v", err)
		s.localMu.Lock()
		s.localCache = make(map[int64]*model.TLSFingerprintProfile)
		s.localMu.Unlock()
	}

	if s.cache != nil {
		if err := s.cache.NotifyUpdate(ctx); err != nil {
			logger.LegacyPrintf("service.tls_fp_profile", "[TLSFPProfileService] Failed to notify cache update: %v", err)
		}
	}
}
