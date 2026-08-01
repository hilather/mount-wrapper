package metrics

// ArchiveSource lists archives for the collector (injected; typically state.Store).
// Keeps metrics free of a hard dependency on the state package.
type ArchiveSource interface {
	// Get returns one archive by id, or nil if not found.
	Get(archiveID string) (*ArchiveInput, error)
	// List returns archives, optionally filtered by status (nil/empty = all).
	List(statuses []string) ([]ArchiveInput, error)
}

// Collector computes and caches archive metrics (parity MetricsService).
// Full index DB walks are behind ExtractedSizeProvider.
type Collector struct {
	Source    ArchiveSource
	Sizes     SizeProvider
	Extracted ExtractedSizeProvider
	Meta      ConvertMetaProvider
	Cache     *Cache
	Config    CollectorConfig
}

// NewCollector builds a Collector with defaults for nil providers.
// cfg.CacheTTLSeconds of 0 uses DefaultCacheTTLSeconds; negative disables cache
// by setting a zero TTL (Get always misses after Put age check).
func NewCollector(src ArchiveSource, cfg CollectorConfig) *Collector {
	ttl := cfg.CacheTTLSeconds
	if ttl == 0 {
		ttl = DefaultCacheTTLSeconds
	}
	if ttl < 0 {
		ttl = 0
	}
	return &Collector{
		Source:    src,
		Sizes:     FSSizeProvider{},
		Extracted: DefaultExtractedProvider{},
		Meta:      NoConvertMeta{},
		Cache:     NewCache(ttl),
		Config:    CollectorConfig{CacheTTLSeconds: ttl},
	}
}

// MetricsCollector is the interface surface for control/API layers.
type MetricsCollector interface {
	// GetOne returns metrics for one archive, or nil if the archive is unknown.
	GetOne(archiveID string, opts QueryOptions) (*ArchiveMetrics, error)
	// GetAll returns metrics for all (or status-filtered) archives.
	GetAll(opts QueryOptions, statuses []string) ([]ArchiveMetrics, error)
	// Summary aggregates the given slice, or GetAll when items is nil.
	Summary(items []ArchiveMetrics, opts QueryOptions) (Summary, error)
	// Invalidate drops cached metrics for archiveID, or all when empty.
	Invalidate(archiveID string)
}

// Ensure *Collector implements MetricsCollector.
var _ MetricsCollector = (*Collector)(nil)

// GetOne implements MetricsCollector.
func (c *Collector) GetOne(archiveID string, opts QueryOptions) (*ArchiveMetrics, error) {
	opts = NormalizeQueryOptions(opts)
	useCache := opts.UseCache == nil || *opts.UseCache
	if useCache && c.Cache != nil {
		if hit, ok := c.Cache.Get(archiveID, opts.PreferMount); ok {
			m := hit
			return &m, nil
		}
	}
	if c.Source == nil {
		return nil, nil
	}
	in, err := c.Source.Get(archiveID)
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, nil
	}
	m := c.compute(*in, opts)
	if c.Cache != nil {
		// Always store under the preference used for this compute so both
		// index-first and prefer_mount paths keep independent TTL entries.
		// no_cache still refreshes the matching key (parity with warm path).
		c.Cache.Put(m, opts.PreferMount)
	}
	return &m, nil
}

// GetAll implements MetricsCollector.
func (c *Collector) GetAll(opts QueryOptions, statuses []string) ([]ArchiveMetrics, error) {
	opts = NormalizeQueryOptions(opts)
	useCache := opts.UseCache == nil || *opts.UseCache
	if c.Source == nil {
		return nil, nil
	}
	inputs, err := c.Source.List(statuses)
	if err != nil {
		return nil, err
	}
	out := make([]ArchiveMetrics, 0, len(inputs))
	for i := range inputs {
		id := inputs[i].ArchiveID
		if useCache && c.Cache != nil {
			if hit, ok := c.Cache.Get(id, opts.PreferMount); ok {
				out = append(out, hit)
				continue
			}
		}
		m := c.compute(inputs[i], opts)
		if c.Cache != nil {
			c.Cache.Put(m, opts.PreferMount)
		}
		out = append(out, m)
	}
	return out, nil
}

// Summary implements MetricsCollector.
// When items is nil, loads GetAll with opts. When items is non-nil (including
// empty slice), aggregates that slice without reloading.
func (c *Collector) Summary(items []ArchiveMetrics, opts QueryOptions) (Summary, error) {
	if items == nil {
		all, err := c.GetAll(opts, nil)
		if err != nil {
			return Summary{}, err
		}
		items = all
	}
	return Summarize(items), nil
}

// Invalidate implements MetricsCollector.
func (c *Collector) Invalidate(archiveID string) {
	if c.Cache != nil {
		c.Cache.Invalidate(archiveID)
	}
}

func (c *Collector) compute(in ArchiveInput, opts QueryOptions) ArchiveMetrics {
	return ComputeArchiveMetrics(in, c.Sizes, c.Extracted, c.Meta, ComputeOptions{
		PreferMount: opts.PreferMount,
	})
}

// MapArchiveSource is a test ArchiveSource.
type MapArchiveSource struct {
	ByID map[string]ArchiveInput
	// Order optional stable list order for List; if empty, iterates map (unstable).
	Order []string
}

// Get implements ArchiveSource.
func (s *MapArchiveSource) Get(archiveID string) (*ArchiveInput, error) {
	if s == nil || s.ByID == nil {
		return nil, nil
	}
	in, ok := s.ByID[archiveID]
	if !ok {
		return nil, nil
	}
	cp := in
	return &cp, nil
}

// List implements ArchiveSource.
func (s *MapArchiveSource) List(statuses []string) ([]ArchiveInput, error) {
	if s == nil || s.ByID == nil {
		return nil, nil
	}
	statusSet := map[string]struct{}{}
	for _, st := range statuses {
		statusSet[st] = struct{}{}
	}
	filter := len(statusSet) > 0

	var out []ArchiveInput
	if len(s.Order) > 0 {
		for _, id := range s.Order {
			in, ok := s.ByID[id]
			if !ok {
				continue
			}
			if filter {
				if _, ok := statusSet[in.Status]; !ok {
					continue
				}
			}
			out = append(out, in)
		}
		return out, nil
	}
	for _, in := range s.ByID {
		if filter {
			if _, ok := statusSet[in.Status]; !ok {
				continue
			}
		}
		out = append(out, in)
	}
	return out, nil
}
