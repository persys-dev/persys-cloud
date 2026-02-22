package services

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var (
	ErrNoHealthySchedulers = errors.New("no healthy schedulers")
	ErrUnknownCluster      = errors.New("unknown cluster")
)

const discoveredDefaultClusterID = "cluster-discovered"

type RoutingStrategy string

const (
	StrategyLeaderOnly RoutingStrategy = "leader-only"
	StrategySticky     RoutingStrategy = "sticky"
	StrategyShard      RoutingStrategy = "shard"
)

type SchedulerInstance struct {
	ID       string
	Address  string
	IsLeader bool
	Healthy  bool
	LastSeen time.Time
}

type Cluster struct {
	ID              string
	Name            string
	Schedulers      []SchedulerInstance
	RoutingStrategy RoutingStrategy
}

type SchedulerPoolManager struct {
	cfg               *config.Config
	tlsClient         *tls.Config
	healthPath        string
	healthInterval    time.Duration
	discoveryInterval time.Duration
	logger            *logrus.Entry
	mu                sync.RWMutex
	clusters          map[string]Cluster
}

func NewSchedulerPoolManager(cfg *config.Config, tlsClient *tls.Config) (*SchedulerPoolManager, error) {
	healthInterval, err := time.ParseDuration(cfg.Scheduler.HealthCheckInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid scheduler.health_check_interval: %w", err)
	}
	discoveryInterval, err := time.ParseDuration(cfg.Scheduler.DiscoveryInterval)
	if err != nil {
		return nil, fmt.Errorf("invalid scheduler.discovery_interval: %w", err)
	}
	baseLogger := logrus.New()
	baseLogger.SetFormatter(&logrus.TextFormatter{
		ForceColors:   true,
		FullTimestamp: true,
	})

	m := &SchedulerPoolManager{
		cfg:               cfg,
		tlsClient:         tlsClient,
		healthPath:        cfg.Scheduler.HealthPath,
		healthInterval:    healthInterval,
		discoveryInterval: discoveryInterval,
		logger:            logrus.NewEntry(baseLogger).WithField("component", "scheduler-pool"),
		clusters:          make(map[string]Cluster, len(cfg.Scheduler.Clusters)),
	}

	for _, cc := range cfg.Scheduler.Clusters {
		strategy := RoutingStrategy(strings.TrimSpace(cc.RoutingStrategy))
		if strategy == "" {
			strategy = StrategyLeaderOnly
		}
		schedulers := make([]SchedulerInstance, 0, len(cc.Schedulers))
		for _, sc := range cc.Schedulers {
			schedulers = append(schedulers, SchedulerInstance{ID: sc.ID, Address: sc.Address, IsLeader: sc.IsLeader})
		}
		m.clusters[cc.ID] = Cluster{ID: cc.ID, Name: cc.Name, Schedulers: schedulers, RoutingStrategy: strategy}
	}

	return m, nil
}

func (m *SchedulerPoolManager) Start(ctx context.Context) {
	m.logger.WithFields(logrus.Fields{
		"default_cluster_id": m.cfg.Scheduler.DefaultClusterID,
		"health_interval":    m.healthInterval.String(),
		"discovery_interval": m.discoveryInterval.String(),
		"clusters":           len(m.clusters),
	}).Info("starting scheduler pool manager")

	m.discoverAndMerge(ctx)
	m.refreshHealth(ctx)

	healthTicker := time.NewTicker(m.healthInterval)
	discoveryTicker := time.NewTicker(m.discoveryInterval)
	go func() {
		defer healthTicker.Stop()
		defer discoveryTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				m.logger.Info("scheduler pool manager stopped")
				return
			case <-healthTicker.C:
				m.refreshHealth(ctx)
			case <-discoveryTicker.C:
				m.discoverAndMerge(ctx)
			}
		}
	}()
}

func (m *SchedulerPoolManager) ForceDiscover(ctx context.Context) {
	m.discoverAndMerge(ctx)
}

func (m *SchedulerPoolManager) discoverAndMerge(ctx context.Context) {
	discovered, source, err := m.discoverSchedulers(ctx)
	if err != nil {
		m.logger.WithError(err).Warn("scheduler discovery failed")
		return
	}
	if len(discovered) == 0 {
		m.logger.WithField("source", "config").Info("scheduler discovery returned no addresses; using static config")
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	defaultClusterID := m.defaultClusterIDLocked()
	if defaultClusterID == "" {
		defaultClusterID = discoveredDefaultClusterID
	}
	cluster, ok := m.clusters[defaultClusterID]
	if !ok {
		cluster = Cluster{
			ID:              defaultClusterID,
			Name:            "Discovered Cluster",
			RoutingStrategy: StrategyLeaderOnly,
			Schedulers:      make([]SchedulerInstance, 0, len(discovered)),
		}
		m.clusters[defaultClusterID] = cluster
		m.logger.WithFields(logrus.Fields{
			"cluster_id": defaultClusterID,
			"source":     source,
		}).Info("bootstrapped scheduler cluster from discovery")
	}

	initialCount := len(cluster.Schedulers)
	resolver := m.newResolver()

	existingByAddr := make(map[string]int, len(cluster.Schedulers))
	existingByCanonical := make(map[string]int, len(cluster.Schedulers))
	for i, s := range cluster.Schedulers {
		existingByAddr[s.Address] = i
		for _, key := range canonicalAddressKeys(ctx, resolver, s.Address) {
			if _, exists := existingByCanonical[key]; !exists {
				existingByCanonical[key] = i
			}
		}
	}

	discoveredSet := make(map[string]struct{}, len(discovered))
	for _, addr := range discovered {
		discoveredSet[addr] = struct{}{}
		if idx, ok := existingByAddr[addr]; ok {
			if cluster.Schedulers[idx].ID == "" {
				cluster.Schedulers[idx].ID = discoveredID(addr)
			}
			continue
		}
		duplicate := false
		for _, key := range canonicalAddressKeys(ctx, resolver, addr) {
			if _, ok := existingByCanonical[key]; ok {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		cluster.Schedulers = append(cluster.Schedulers, SchedulerInstance{
			ID:      discoveredID(addr),
			Address: addr,
		})
		idx := len(cluster.Schedulers) - 1
		existingByAddr[addr] = idx
		for _, key := range canonicalAddressKeys(ctx, resolver, addr) {
			if _, exists := existingByCanonical[key]; !exists {
				existingByCanonical[key] = idx
			}
		}
	}

	// Drop stale discovered entries while keeping statically configured ones.
	filtered := make([]SchedulerInstance, 0, len(cluster.Schedulers))
	staticSet := make(map[string]struct{}, 8)
	for _, c := range m.cfg.Scheduler.Clusters {
		if c.ID != defaultClusterID {
			continue
		}
		for _, s := range c.Schedulers {
			staticSet[s.Address] = struct{}{}
		}
	}

	for _, s := range cluster.Schedulers {
		if _, isStatic := staticSet[s.Address]; isStatic {
			filtered = append(filtered, s)
			continue
		}
		if strings.HasPrefix(s.ID, "discovered-") {
			if _, stillPresent := discoveredSet[s.Address]; stillPresent {
				filtered = append(filtered, s)
			}
			continue
		}
		filtered = append(filtered, s)
	}

	cluster.Schedulers = filtered
	m.clusters[defaultClusterID] = cluster
	m.logger.WithFields(logrus.Fields{
		"cluster_id":       defaultClusterID,
		"source":           source,
		"discovered_addrs": discovered,
		"before_count":     initialCount,
		"after_count":      len(cluster.Schedulers),
	}).Info("scheduler discovery merged into pool")
}

func (m *SchedulerPoolManager) discoverSchedulers(ctx context.Context) ([]string, string, error) {
	service := strings.TrimSpace(m.cfg.Prow.DiscoverySvc)
	domain := strings.TrimSpace(m.cfg.Prow.DiscoveryDomain)
	if service == "" || domain == "" {
		return nil, "config", fmt.Errorf("discovery service/domain not configured")
	}
	resolver := m.newResolver()

	serviceLabel := strings.TrimPrefix(service, "_")
	if serviceLabel == "" {
		return nil, "config", fmt.Errorf("invalid discovery service %q", service)
	}

	var srvRecords []*net.SRV
	source := "coredns-srv"
	_, records, err := resolver.LookupSRV(ctx, serviceLabel, "tcp", domain)
	if err == nil {
		srvRecords = records
	} else {
		// Also support callers that provide the full SRV label (e.g. "_svc._tcp.domain").
		fullName := service
		if !strings.Contains(fullName, "._tcp.") {
			fullName = "_" + serviceLabel + "._tcp." + domain
		}
		_, records, err = resolver.LookupSRV(ctx, "", "", fullName)
		if err == nil {
			srvRecords = records
		} else {
			// Support CoreDNS zones that publish SRV directly on the domain.
			_, records, err = resolver.LookupSRV(ctx, "", "", domain)
			if err == nil {
				srvRecords = records
			} else {
				source = "coredns-host-fallback"
				m.logger.WithFields(logrus.Fields{
					"service": serviceLabel,
					"domain":  domain,
					"full":    fullName,
				}).WithError(err).Debug("SRV lookup failed; falling back to host lookups")
			}
		}
	}

	addrs := make([]string, 0, len(srvRecords)+2)
	for _, srv := range srvRecords {
		if srv.Port == 0 {
			continue
		}
		ips, ipErr := resolveSRVTarget(ctx, resolver, srv.Target)
		if ipErr != nil || len(ips) == 0 {
			continue
		}
		addrs = append(addrs, net.JoinHostPort(ips[0], strconv.Itoa(int(srv.Port))))
	}

	if len(addrs) == 0 {
		source = "coredns-host-fallback"
		hosts := []string{
			serviceLabel + "." + domain,
			"persys-scheduler." + domain,
			serviceLabel,
			"persys-scheduler",
			"persys-scheduler.local",
		}
		for _, host := range hosts {
			ips, ipErr := resolver.LookupHost(ctx, host)
			if ipErr != nil {
				continue
			}
			for _, ip := range ips {
				addrs = append(addrs, net.JoinHostPort(ip, "8085"))
			}
			if len(addrs) > 0 {
				break
			}
		}
	}

	deduped := dedupe(addrs)
	m.logger.WithFields(logrus.Fields{
		"source":         source,
		"service":        serviceLabel,
		"domain":         domain,
		"resolved_addrs": deduped,
	}).Debug("scheduler discovery resolution completed")
	return deduped, source, nil
}

func (m *SchedulerPoolManager) newResolver() *net.Resolver {
	coreDNSAddr := strings.TrimSpace(m.cfg.CoreDNS.Addr)
	if coreDNSAddr == "" {
		return net.DefaultResolver
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", coreDNSAddr)
		},
	}
}

func (m *SchedulerPoolManager) refreshHealth(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, cluster := range m.clusters {
		for i := range cluster.Schedulers {
			inst := &cluster.Schedulers[i]
			previous := inst.Healthy
			if m.checkInstanceHealth(ctx, inst.Address) {
				inst.Healthy = true
				inst.LastSeen = time.Now().UTC()
			} else {
				inst.Healthy = false
			}
			if previous != inst.Healthy {
				m.logger.WithFields(logrus.Fields{
					"cluster_id": id,
					"scheduler":  inst.Address,
					"healthy":    inst.Healthy,
				}).Info("scheduler health state changed")
			}
		}
		m.clusters[id] = cluster
	}
}

func (m *SchedulerPoolManager) checkInstanceHealth(ctx context.Context, address string) bool {
	healthCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		healthCtx,
		address,
		grpc.WithTransportCredentials(credentials.NewTLS(m.tlsClient)),
		grpc.WithBlock(),
	)
	if err != nil {
		m.logger.WithFields(logrus.Fields{
			"scheduler": address,
		}).WithError(err).Debug("scheduler gRPC health probe failed")
		return false
	}
	_ = conn.Close()
	return true
}

func (m *SchedulerPoolManager) ResolveClusterForRepository(repo string) string {
	clusterID := strings.TrimSpace(m.cfg.Scheduler.RepositoryClusterMap[repo])
	if clusterID != "" {
		return clusterID
	}
	return m.DefaultClusterID()
}

func (m *SchedulerPoolManager) MarkUnhealthy(clusterID, address string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cluster, ok := m.clusters[clusterID]
	if !ok {
		return
	}
	for i := range cluster.Schedulers {
		if cluster.Schedulers[i].Address == address {
			if cluster.Schedulers[i].Healthy {
				m.logger.WithFields(logrus.Fields{
					"cluster_id": clusterID,
					"scheduler":  address,
				}).Warn("marking scheduler unhealthy due to request failure")
			}
			cluster.Schedulers[i].Healthy = false
		}
	}
	m.clusters[clusterID] = cluster
}

func (m *SchedulerPoolManager) OrderedSchedulers(clusterID, sessionKey, workloadKey string) ([]SchedulerInstance, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if strings.TrimSpace(clusterID) == "" {
		clusterID = m.defaultClusterIDLocked()
	}

	cluster, ok := m.clusters[clusterID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCluster, clusterID)
	}

	healthy := make([]SchedulerInstance, 0, len(cluster.Schedulers))
	for _, s := range cluster.Schedulers {
		if s.Healthy {
			healthy = append(healthy, s)
		}
	}
	if len(healthy) == 0 {
		return nil, ErrNoHealthySchedulers
	}

	sort.SliceStable(healthy, func(i, j int) bool { return healthy[i].ID < healthy[j].ID })

	switch cluster.RoutingStrategy {
	case StrategyLeaderOnly:
		for i, s := range healthy {
			if s.IsLeader {
				if i == 0 {
					return healthy, nil
				}
				ordered := []SchedulerInstance{s}
				ordered = append(ordered, healthy[:i]...)
				ordered = append(ordered, healthy[i+1:]...)
				return ordered, nil
			}
		}
		m.logger.WithFields(logrus.Fields{
			"cluster_id": clusterID,
			"strategy":   cluster.RoutingStrategy,
			"candidates": schedulerAddresses(healthy),
		}).Debug("scheduler candidates selected")
		return healthy, nil
	case StrategySticky:
		ordered := rotateByIndex(healthy, indexByKey(sessionKey, len(healthy)))
		m.logger.WithFields(logrus.Fields{
			"cluster_id": clusterID,
			"strategy":   cluster.RoutingStrategy,
			"session":    sessionKey,
			"candidates": schedulerAddresses(ordered),
		}).Debug("scheduler candidates selected")
		return ordered, nil
	case StrategyShard:
		ordered := rotateByIndex(healthy, indexByKey(workloadKey, len(healthy)))
		m.logger.WithFields(logrus.Fields{
			"cluster_id": clusterID,
			"strategy":   cluster.RoutingStrategy,
			"workload":   workloadKey,
			"candidates": schedulerAddresses(ordered),
		}).Debug("scheduler candidates selected")
		return ordered, nil
	default:
		return nil, fmt.Errorf("unsupported routing strategy %q", cluster.RoutingStrategy)
	}
}

func rotateByIndex(in []SchedulerInstance, idx int) []SchedulerInstance {
	if len(in) <= 1 || idx <= 0 {
		return in
	}
	out := make([]SchedulerInstance, 0, len(in))
	out = append(out, in[idx:]...)
	out = append(out, in[:idx]...)
	return out
}

func (m *SchedulerPoolManager) Snapshot() []Cluster {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Cluster, 0, len(m.clusters))
	for _, c := range m.clusters {
		copySchedulers := make([]SchedulerInstance, len(c.Schedulers))
		copy(copySchedulers, c.Schedulers)
		out = append(out, Cluster{ID: c.ID, Name: c.Name, Schedulers: copySchedulers, RoutingStrategy: c.RoutingStrategy})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *SchedulerPoolManager) DefaultClusterID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultClusterIDLocked()
}

func (m *SchedulerPoolManager) defaultClusterIDLocked() string {
	if cfgDefault := strings.TrimSpace(m.cfg.Scheduler.DefaultClusterID); cfgDefault != "" {
		if _, ok := m.clusters[cfgDefault]; ok {
			return cfgDefault
		}
	}
	if len(m.clusters) == 0 {
		return ""
	}
	ids := make([]string, 0, len(m.clusters))
	for id := range m.clusters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids[0]
}

func indexByKey(key string, size int) int {
	if size <= 1 {
		return 0
	}
	if strings.TrimSpace(key) == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32()) % size
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func discoveredID(address string) string {
	replacer := strings.NewReplacer(":", "-", ".", "-")
	return "discovered-" + replacer.Replace(address)
}

func schedulerAddresses(in []SchedulerInstance) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.Address)
	}
	return out
}

func resolveSRVTarget(ctx context.Context, resolver *net.Resolver, target string) ([]string, error) {
	host := strings.TrimSuffix(strings.TrimSpace(target), ".")
	if host == "" {
		return nil, fmt.Errorf("empty SRV target")
	}
	ips, err := resolver.LookupHost(ctx, host)
	if err == nil && len(ips) > 0 {
		return ips, nil
	}
	if strings.HasPrefix(host, "_") {
		return resolver.LookupHost(ctx, strings.TrimPrefix(host, "_"))
	}
	return nil, err
}

func canonicalAddressKeys(ctx context.Context, resolver *net.Resolver, address string) []string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return nil
	}
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" || port == "" {
		return nil
	}

	// Keep the original endpoint key so exact hostnames still dedupe quickly.
	keys := []string{strings.ToLower(net.JoinHostPort(host, port))}
	if ip := net.ParseIP(host); ip != nil {
		keys = append(keys, net.JoinHostPort(ip.String(), port))
		return dedupe(keys)
	}

	ips, err := resolver.LookupHost(ctx, host)
	if err != nil {
		return dedupe(keys)
	}
	for _, ip := range ips {
		if parsed := net.ParseIP(strings.TrimSpace(ip)); parsed != nil {
			keys = append(keys, net.JoinHostPort(parsed.String(), port))
		}
	}
	return dedupe(keys)
}
