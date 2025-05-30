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