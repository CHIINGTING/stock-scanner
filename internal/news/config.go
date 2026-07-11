package news

// NewsConfig is the centralized news-module configuration, nested under the scanner
// config as `news:`. All lifecycle numbers live here (never hard-coded in logic).
//
// Two-layer gating lives on scanner.Config (enable_news / show_news); this struct
// holds the source + lifecycle settings shared by both layers.
type NewsConfig struct {
	// ShadowMode is always true in Phase 1 (news never affects BUY/WATCH/SELL). It is
	// surfaced in config as an explicit, auditable switch and reserved for Phase 2.
	ShadowMode bool `yaml:"shadow_mode"`

	// MaxAgeDays: signals older than this are EXPIRED (kept in snapshot, de-emphasized).
	MaxAgeDays int `yaml:"max_age_days"`

	// BannerWindows are the day-windows shown side-by-side per sector in the market
	// banner (fast-rotation view). Default [1,3,7]; the widest window also gates which
	// sectors appear as rows.
	BannerWindows []int `yaml:"banner_windows"`

	// Age holds the FRESH/ACTIVE/DECAYING day boundaries (centralized lifecycle).
	Age AgeConfig `yaml:"age"`

	// Providers is the per-source enable + endpoint settings.
	Providers ProvidersConfig `yaml:"providers"`

	// AliasesFile points at the explicit alias/theme dictionary (configs/news_aliases.yaml).
	AliasesFile string `yaml:"aliases_file"`

	// SnapshotDir is where news_YYYYMMDD.json is written. Empty → reuse the report OutputDir.
	SnapshotDir string `yaml:"snapshot_dir"`

	// UserAgent is the browser-like UA used by best-effort HTTP providers (e.g. TWETQ,
	// which serves 403 to non-browser agents). Empty → provider default.
	UserAgent string `yaml:"user_agent"`

	// TimeoutSeconds bounds each provider fetch. Zero → provider default.
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

// AgeConfig holds the inclusive upper-day bound of each lifecycle bucket. Defaults
// (applied by AgeConfig.withDefaults) are FRESH≤2, ACTIVE≤5, DECAYING≤10.
type AgeConfig struct {
	FreshDays    int `yaml:"fresh_days"`
	ActiveDays   int `yaml:"active_days"`
	DecayingDays int `yaml:"decaying_days"`
}

// ProvidersConfig gates each concrete source.
type ProvidersConfig struct {
	SocialWorkerDaily ProviderConfig `yaml:"socialworkerdaily"`
	TWETQ             ProviderConfig `yaml:"twetq"`
}

// ProviderConfig is per-provider settings. BaseURL/APIBase are optional overrides so
// tests and future host changes never require code edits.
type ProviderConfig struct {
	Enabled      bool   `yaml:"enabled"`
	BaseURL      string `yaml:"base_url"`
	APIBase      string `yaml:"api_base"`
	CategorySlug string `yaml:"category_slug"`
	MaxItems     int    `yaml:"max_items"`
}

// Defaulted returns a copy of the config with zero-valued knobs filled in, so callers
// can rely on sane lifecycle boundaries even from a minimal YAML block.
func (c NewsConfig) Defaulted() NewsConfig {
	out := c
	if out.MaxAgeDays <= 0 {
		out.MaxAgeDays = 10
	}
	out.Age = out.Age.withDefaults()
	if out.TimeoutSeconds <= 0 {
		out.TimeoutSeconds = 15
	}
	if len(out.BannerWindows) == 0 {
		out.BannerWindows = []int{1, 3, 7}
	}
	return out
}

func (a AgeConfig) withDefaults() AgeConfig {
	if a.FreshDays <= 0 {
		a.FreshDays = 2
	}
	if a.ActiveDays <= 0 {
		a.ActiveDays = 5
	}
	if a.DecayingDays <= 0 {
		a.DecayingDays = 10
	}
	return a
}
