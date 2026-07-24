package services

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/persys-dev/persys-cloud/persys-gateway/config"
	"os"
)

type ClusterControlService struct {
	config        *config.Config
	clientTLS     *tls.Config
	serverTLS     *tls.Config
	schedulerPool *SchedulerPoolManager
}

func NewClusterControlService(cfg *config.Config) *ClusterControlService {
	service := &ClusterControlService{config: cfg}

	if err := service.loadTLSConfigs(); err != nil {
		panic(fmt.Sprintf("failed to load TLS configs: %v", err))
	}

	pool, err := NewSchedulerPoolManager(cfg, service.clientTLS)
	if err != nil {
		panic(fmt.Sprintf("failed to initialize scheduler pool manager: %v", err))
	}
	service.schedulerPool = pool

	return service
}

func (s *ClusterControlService) Start(ctx context.Context) {
	s.schedulerPool.Start(ctx)
}

func (s *ClusterControlService) loadTLSConfigs() error {
	cert, err := tls.LoadX509KeyPair(s.config.TLS.CertPath, s.config.TLS.KeyPath)
	if err != nil {
		return fmt.Errorf("failed to load client certificate: %w", err)
	}

	caCert, err := os.ReadFile(s.config.TLS.CAPath)
	if err != nil {
		return fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return fmt.Errorf("failed to append CA certificate")
	}

	s.clientTLS = &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: caCertPool}
	s.serverTLS = &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: caCertPool, ClientAuth: tls.RequireAndVerifyClientCert}
	return nil
}

func (s *ClusterControlService) DiscoverAndPrintSchedulers() {
	s.schedulerPool.ForceDiscover(context.Background())
}

func (s *ClusterControlService) DiscoverSchedulers(_ string) error {
	s.schedulerPool.ForceDiscover(context.Background())
	return nil
}

func (s *ClusterControlService) GetSchedulerAddress() string {
	inst, err := s.schedulerPool.OrderedSchedulers(s.schedulerPool.DefaultClusterID(), "", "")
	if err != nil || len(inst) == 0 {
		return s.config.LegacyScheduler.FallbackAddr
	}
	return inst[0].Address
}

func (s *ClusterControlService) GetSchedulerAddresses() []string {
	clusterID := s.schedulerPool.DefaultClusterID()
	addrs := make([]string, 0)
	for _, c := range s.schedulerPool.Snapshot() {
		if c.ID != clusterID {
			continue
		}
		for _, sch := range c.Schedulers {
			addrs = append(addrs, sch.Address)
		}
	}
	return addrs
}

func (s *ClusterControlService) IsProxyEnabled() bool {
	return s.config.LegacyScheduler.ProxyEnabled
}

func IsSchedulerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrNoHealthySchedulers)
}

func IsUnknownCluster(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrUnknownCluster)
}

func (s *ClusterControlService) SnapshotClusters() []Cluster {
	return s.schedulerPool.Snapshot()
}

func (s *ClusterControlService) DefaultClusterID() string {
	return s.schedulerPool.DefaultClusterID()
}
