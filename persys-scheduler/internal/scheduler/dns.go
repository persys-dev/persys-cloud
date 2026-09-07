package scheduler

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/persys-dev/persys-cloud/persys-scheduler/internal/models"
)

func (s *Scheduler) UpdateCoreDNS(node models.Node) error {
	if node.NodeID == "" || node.IPAddress == "" {
		return fmt.Errorf("invalid node data: NodeID and IPAddress are required")
	}
	shard := strings.TrimSpace(s.schedulerShard)
	if shard == "" {
		shard = "genesis"
	}
	// Agents are discoverable at <nodeID>.<shard>.agents.persys.cloud
	key := fmt.Sprintf("/skydns/%s/%s/%s", reverseDomain(s.agentsDomain), shard, node.NodeID)
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

// RegisterSchedulerInCoreDNS registers this scheduler replica in CoreDNS for
// service discovery by persys-gateway.
//
// Each replica writes to its own instance-keyed child (mirroring the same
// pattern UpdateCoreDNS already uses for agent nodes above), rather than a
// single shared key. Previously every replica wrote to exactly one fixed
// key (/skydns/.../persys-scheduler), so with more than one scheduler
// running, whichever replica registered last silently clobbered the
// others — the gateway would only ever resolve one instance, and not
// necessarily a healthy one, no matter how many replicas were actually
// running. CoreDNS's etcd backend treats sibling keys under a shared
// prefix as multiple records for that name, so persys-gateway resolving
// persys-scheduler.<domain> now gets one answer per registered (i.e.
// running) replica.
func (s *Scheduler) RegisterSchedulerInCoreDNS(ipAddress string, port int) error {
	if ipAddress == "" || port == 0 {
		return fmt.Errorf("invalid scheduler data: IPAddress and Port are required")
	}

	instanceKey := s.instanceID
	if strings.TrimSpace(instanceKey) == "" {
		instanceKey = ipAddress
	}

	// Register SRV record for _persys-scheduler.<domain>
	srvKey := fmt.Sprintf("/skydns/%s/_persys-scheduler/%s", reverseDomain(s.domain), instanceKey)
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
	aKey := fmt.Sprintf("/skydns/%s/persys-scheduler/%s", reverseDomain(s.domain), instanceKey)
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

// DeregisterSchedulerSelfFromCoreDNS removes this replica's own CoreDNS
// records. Called on clean shutdown so a stopped replica doesn't linger as
// a dead A/SRV record for persys-gateway to route to until the 300s TTL
// happens to expire on its own. Best-effort: errors are logged, not
// returned, since a failed deregistration during shutdown shouldn't block
// the rest of the shutdown sequence, and a stale record self-heals via TTL
// either way.
func (s *Scheduler) DeregisterSchedulerSelfFromCoreDNS() {
	instanceKey := s.instanceID
	if strings.TrimSpace(instanceKey) == "" {
		return
	}
	aKey := fmt.Sprintf("/skydns/%s/persys-scheduler/%s", reverseDomain(s.domain), instanceKey)
	srvKey := fmt.Sprintf("/skydns/%s/_persys-scheduler/%s", reverseDomain(s.domain), instanceKey)
	if err := s.RetryableEtcdDelete(aKey); err != nil {
		schedulerLogger.WithError(err).Debug("failed to deregister scheduler A record on shutdown")
	}
	if err := s.RetryableEtcdDelete(srvKey); err != nil {
		schedulerLogger.WithError(err).Debug("failed to deregister scheduler SRV record on shutdown")
	}
}

// RegisterSchedulerSelfInCoreDNS registers this scheduler instance into CoreDNS.
// It resolves advertise IP/port from env with sane defaults.
func (s *Scheduler) RegisterSchedulerSelfInCoreDNS(defaultPort int) error {
	ipAddress := strings.TrimSpace(s.cfg.SchedulerAdvertiseIP)
	if ipAddress == "" {
		resolved, err := firstNonLoopbackIPv4()
		if err != nil {
			return fmt.Errorf("resolve scheduler advertise IP: %w", err)
		}
		ipAddress = resolved
	}

	port := s.cfg.SchedulerAdvertisePort
	if port <= 0 {
		port = defaultPort
	}

	if err := s.RegisterSchedulerInCoreDNS(ipAddress, port); err != nil {
		return err
	}
	schedulerLogger.WithFields(map[string]interface{}{
		"ip":             ipAddress,
		"port":           port,
		"scheduler_name": "persys-scheduler",
		"domain":         s.domain,
	}).Info("registered scheduler in CoreDNS")
	return nil
}

func firstNonLoopbackIPv4() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			ip = ip.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("no non-loopback IPv4 address found")
}
