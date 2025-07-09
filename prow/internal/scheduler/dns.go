package scheduler

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/persys-dev/prow/internal/models"
)

func (s *Scheduler) UpdateCoreDNS(node models.Node) error {
	if node.NodeID == "" || node.IPAddress == "" {
		return fmt.Errorf("invalid node data: NodeID and IPAddress are required")
	}
	key := fmt.Sprintf("/skydns/%s/%s", reverseDomain(s.domain), node.NodeID)
	record := struct {
		Host string `json:"host"`
		TTL  int    `json:"ttl"`
	}{node.IPAddress, 300}
	jsonValue, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal DNS record: %v", err)
	}
	return s.RetryableEtcdPut(key, string(jsonValue))
}

func reverseDomain(domain string) string {
	parts := strings.Split(domain, ".")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "/")
}

// RegisterSchedulerInCoreDNS registers the scheduler in CoreDNS for service discovery
func (s *Scheduler) RegisterSchedulerInCoreDNS(ipAddress string, port int) error {
	if ipAddress == "" || port == 0 {
		return fmt.Errorf("invalid scheduler data: IPAddress and Port are required")
	}

	// Register SRV record
	srvKey := fmt.Sprintf("/skydns/%s/_prow-scheduler/_tcp", reverseDomain(s.domain))
	srvRecord := struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		TTL  int    `json:"ttl"`
	}{
		Host: ipAddress,
		Port: port,
		TTL:  300,
	}
	srvJSON, err := json.Marshal(srvRecord)
	if err != nil {
		return fmt.Errorf("failed to marshal SRV record: %v", err)
	}
	if err := s.RetryableEtcdPut(srvKey, string(srvJSON)); err != nil {
		return fmt.Errorf("failed to register SRV record: %v", err)
	}

	// Also register A record for direct IP lookup
	aKey := fmt.Sprintf("/skydns/%s/scheduler", reverseDomain(s.domain))
	aRecord := struct {
		Host string `json:"host"`
		TTL  int    `json:"ttl"`
	}{
		Host: ipAddress,
		TTL:  300,
	}
	aJSON, err := json.Marshal(aRecord)
	if err != nil {
		return fmt.Errorf("failed to marshal A record: %v", err)
	}
	if err := s.RetryableEtcdPut(aKey, string(aJSON)); err != nil {
		return fmt.Errorf("failed to register A record: %v", err)
	}

	return nil
}
