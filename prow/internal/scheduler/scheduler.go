package scheduler

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/persys-dev/prow/internal/models"
	"go.etcd.io/etcd/client/v3"
)

// Constants
const (
	etcdTimeout   = 5 * time.Second
	maxRetries    = 5
	retryWaitTime = 2 * time.Second
	keyDir        = "~/.prow"
	privateKeyFile = "scheduler.key"
	publicKeyFile  = "scheduler.pub"
)

// Scheduler holds the state and configuration for the cluster scheduler.
type Scheduler struct {
	privateKey    *rsa.PrivateKey
	sharedSecret  string
	etcdClient    *clientv3.Client
	httpClient    *http.Client
	domain        string
	publicKeyPEM  string
}

// NewScheduler initializes the scheduler with an etcd client and configuration.
func NewScheduler() (*Scheduler, error) {
	// Resolve home directory for key storage
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	keyDirPath := strings.Replace(keyDir, "~", homeDir, 1)

	// Load or generate key pair
	privateKey, publicKeyPEM, err := loadOrGenerateKeys(keyDirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load/generate keys: %w", err)
	}

	// Get etcd endpoints from environment variable, with fallback
	etcdEndpoints := os.Getenv("ETCD_ENDPOINTS")
	if etcdEndpoints == "" {
		etcdEndpoints = "localhost:2379"
	}

	// Get domain from environment variable, with fallback
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "cluster.local.dev"
	}

	// Get shared secret from environment
	sharedSecret := os.Getenv("AGENT_SECRET")
	if sharedSecret == "" {
		fmt.Print("Warning: AGENT_SECRET not set; TOFU mode will be used unless shared secret is provided")
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   strings.Split(etcdEndpoints, ","),
		DialTimeout: etcdTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %v", err)
	}

	// Verify etcd connection
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	if _, err := cli.Get(ctx, "/health"); err != nil {
		cli.Close()
		return nil, fmt.Errorf("failed to connect to etcd: %v", err)
	}

	return &Scheduler{
		etcdClient:    cli,
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		domain:        domain,
		privateKey:    privateKey,
		publicKeyPEM:  publicKeyPEM,
		sharedSecret:  sharedSecret,
	}, nil
}

// loadOrGenerateKeys loads existing keys or generates new ones
func loadOrGenerateKeys(keyDirPath string) (*rsa.PrivateKey, string, error) {
    if err := os.MkdirAll(keyDirPath, 0700); err != nil {
        return nil, "", fmt.Errorf("failed to create key directory %s: %w", keyDirPath, err)
    }

    privateKeyPath := filepath.Join(keyDirPath, privateKeyFile)
    publicKeyPath := filepath.Join(keyDirPath, publicKeyFile)

    // Try to load existing private key
    if _, err := os.Stat(privateKeyPath); err == nil {
        privateKeyBytes, err := ioutil.ReadFile(privateKeyPath)
        if err != nil {
            return nil, "", fmt.Errorf("failed to read private key %s: %w", privateKeyPath, err)
        }
        privateBlock, _ := pem.Decode(privateKeyBytes)
        if privateBlock == nil || privateBlock.Type != "RSA PRIVATE KEY" {
            return nil, "", fmt.Errorf("invalid private key format in %s", privateKeyPath)
        }
        privateKey, err := x509.ParsePKCS1PrivateKey(privateBlock.Bytes)
        if err != nil {
            return nil, "", fmt.Errorf("failed to parse private key: %w", err)
        }

        // Load public key
        publicKeyBytes, err := ioutil.ReadFile(publicKeyPath)
        if err != nil {
            return nil, "", fmt.Errorf("failed to read public key %s: %w", publicKeyPath, err)
        }
        log.Printf("Loaded public key from %s: %s...", publicKeyPath, string(publicKeyBytes)[:50])
        return privateKey, string(publicKeyBytes), nil
    }

    // Generate new key pair
    privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
    if err != nil {
        return nil, "", fmt.Errorf("failed to generate private key: %w", err)
    }

    // Encode private key
    privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
    privateBlock := &pem.Block{
        Type:  "RSA PRIVATE KEY",
        Bytes: privateKeyBytes,
    }
    privatePem := new(bytes.Buffer)
    if err := pem.Encode(privatePem, privateBlock); err != nil {
        return nil, "", fmt.Errorf("failed to encode private key: %w", err)
    }

    // Encode public key
    publicKeyPEM, err := encodePublicKey(&privateKey.PublicKey)
    if err != nil {
        return nil, "", fmt.Errorf("failed to encode public key: %w", err)
    }

    // Save keys
    if err := ioutil.WriteFile(privateKeyPath, privatePem.Bytes(), 0600); err != nil {
        return nil, "", fmt.Errorf("failed to write private key %s: %w", privateKeyPath, err)
    }
    if err := ioutil.WriteFile(publicKeyPath, []byte(publicKeyPEM), 0644); err != nil {
        return nil, "", fmt.Errorf("failed to write public key %s: %w", publicKeyPath, err)
    }

    log.Printf("Generated and saved new key pair: private=%s, public=%s", privateKeyPath, publicKeyPath)
    return privateKey, publicKeyPEM, nil
}

func encodePublicKey(pubKey *rsa.PublicKey) (string, error) {
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		return "", fmt.Errorf("failed to marshal public key: %w", err)
	}
	pemBlock := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	}
	pemBuffer := new(bytes.Buffer)
	if err := pem.Encode(pemBuffer, pemBlock); err != nil {
		return "", fmt.Errorf("failed to encode PEM: %w", err)
	}
	pemHex := hex.EncodeToString(pemBuffer.Bytes())
	log.Printf("Encoded public key (hex, first 50 chars): %s...", pemHex[:50])
	return pemHex, nil
}

// Close shuts down the scheduler gracefully.
func (s *Scheduler) Close() error {
	if s.etcdClient != nil {
		return s.etcdClient.Close()
	}
	return nil
}

func (s *Scheduler) RegisterNode(node models.Node) error {
	if node.NodeID == "" || node.IPAddress == "" || node.AgentPort == 0 {
		return fmt.Errorf("nodeID, IPAddress, and AgentPort are required")
	}
	if node.TotalCPU <= 0 || node.TotalMemory <= 0 {
		return fmt.Errorf("totalCPU and totalMemory must be positive")
	}

	node.LastHeartbeat = time.Now()
	node.DomainName = node.NodeID + "." + s.domain
	if node.Status == "" {
		node.Status = "Active"
	}
	if node.AvailableCPU == 0 {
		node.AvailableCPU = node.TotalCPU
	}
	if node.AvailableMemory == 0 {
		node.AvailableMemory = node.TotalMemory
	}
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	if node.AuthConfig.PublicKey == "" || node.AuthConfig.PublicKey != s.publicKeyPEM {
		node.AuthConfig.PublicKey = s.publicKeyPEM
	}
	if node.AuthConfig.SharedSecret == "" && s.sharedSecret != "" {
		node.AuthConfig.SharedSecret = s.sharedSecret
	}

	log.Printf("Registering node: %+v", node)

	// Check for stale node data
	existingNode, err := s.GetNodeByID(node.NodeID)
	if err == nil && (existingNode.AuthConfig.PublicKey != s.publicKeyPEM || existingNode.Status == "HandshakeFailed") {
		log.Printf("Node %s has mismatched public key or failed handshake, deleting and re-registering", node.NodeID)
		if err := s.DeleteNode(node.NodeID); err != nil {
			log.Printf("Failed to delete node %s: %v", node.NodeID, err)
		}
	}

	// Initiate handshake in a Go routine
    go func(node models.Node) {
        if err := s.initiateHandshake(node); err != nil {
            log.Printf("Failed to initiate handshake with node %s: %v", node.NodeID, err)
            // Optionally update node status in etcd
            node.Status = "HandshakeFailed"
            nodeJSON, _ := json.Marshal(node)
            s.RetryableEtcdPut("/nodes/"+node.NodeID, string(nodeJSON))
        } else {
            log.Printf("Handshake successful with node %s", node.NodeID)
        }
    }(node)

	nodeJSON, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("failed to marshal node: %v", err)
	}

	if err := s.RetryableEtcdPut("/nodes/"+node.NodeID, string(nodeJSON)); err != nil {
		return fmt.Errorf("failed to register node %s: %v", node.NodeID, err)
	}

	if err := s.UpdateCoreDNS(node); err != nil {
		log.Printf("Failed to update CoreDNS for node %s: %v", node.NodeID, err)
	}

	log.Printf("Registered node: %s", node.NodeID)

	return nil
}

func (s *Scheduler) initiateHandshake(node models.Node) error {
	const maxRetries = 3
	const baseDelay = 10 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		log.Printf("Handshake attempt %d/%d for node %s", attempt, maxRetries, node.NodeID)

		// Prepare handshake payload
		payload := map[string]string{
			"schedulerId": "prow-scheduler",
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		}
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			log.Printf("Failed to marshal handshake payload for node %s: %v", node.NodeID, err)
			continue
		}
		log.Printf("Handshake payload for node %s: %s", node.NodeID, string(payloadBytes))

		// Sign payload
		hash := sha256.Sum256(payloadBytes)
		signature, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, hash[:])
		if err != nil {
			log.Printf("Failed to sign handshake payload for node %s: %v", node.NodeID, err)
			continue
		}
		signatureB64 := base64.StdEncoding.EncodeToString(signature)
		log.Printf("Handshake signatureB64 for node %s: %s...", node.NodeID, signatureB64[:50])

		// Send handshake request
		url := fmt.Sprintf("http://%s:%d/api/v1/handshake", node.IPAddress, node.AgentPort)

		time.Sleep(10 * time.Second)

		req, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
		if err != nil {
			log.Printf("Failed to create handshake request for node %s: %v", node.NodeID, err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Scheduler-Signature", signatureB64)
		req.Header.Set("X-Scheduler-PublicKey", s.publicKeyPEM)
		log.Printf("Handshake public key for node %s: %s...", node.NodeID, s.publicKeyPEM[:50])

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Failed to send handshake request for node %s: %v", node.NodeID, err)
		} else if resp.StatusCode != http.StatusOK {
			log.Printf("Handshake failed for node %s with status: %d", node.NodeID, resp.StatusCode)
			resp.Body.Close()
		} else {
			resp.Body.Close()
			log.Printf("Handshake successful for node %s", node.NodeID)
			return nil
		}

		// Exponential backoff
		if attempt < maxRetries {
			delay := baseDelay * time.Duration(1<<attempt)
			log.Printf("Retrying handshake for node %s after %v", node.NodeID, delay)
			time.Sleep(delay)
		}
	}

	return fmt.Errorf("handshake failed for node %s after %d attempts", node.NodeID, maxRetries)
}

// SendCommandToNode sends a signed request to the node's agent API
func (s *Scheduler) SendCommandToNode(node models.Node, endpoint string, payload interface{}) (string, error) {
    if node.IPAddress == "" || node.AgentPort == 0 {
        return "", fmt.Errorf("node IPAddress and AgentPort are required")
    }

    url := fmt.Sprintf("http://%s:%d%s", node.IPAddress, node.AgentPort, endpoint)
    var req *http.Request
    var err error
    var signatureB64 string

    if payload != nil {
        // Handle POST request
        payloadBytes, err := json.Marshal(payload)
        if err != nil {
            return "", fmt.Errorf("failed to marshal payload: %v", err)
        }
        log.Printf("Sending POST command to %s with payload: %s", url, string(payloadBytes))

        // Sign payload
        hash := sha256.Sum256(payloadBytes)
        signature, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, hash[:])
        if err != nil {
            return "", fmt.Errorf("failed to sign payload: %v", err)
        }
        signatureB64 = base64.StdEncoding.EncodeToString(signature)
        // log.Printf("Signature for node %s: %s...", node.NodeID, signatureB64[:50])

        // Create POST request
        req, err = http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
        if err != nil {
            return "", fmt.Errorf("failed to create POST request: %v", err)
        }
        req.Header.Set("Content-Type", "application/json")
    } else {
        // Handle GET request
        log.Printf("Sending GET command to %s", url)

        // Sign an empty payload or URL for GET request
        hash := sha256.Sum256([]byte("")) // Sign the "" for GET requests
        signature, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, hash[:])
        if err != nil {
            return "", fmt.Errorf("failed to sign URL for GET request: %v", err)
        }
        signatureB64 = base64.StdEncoding.EncodeToString(signature)
        // log.Printf("Signature for node %s: %s...", node.NodeID, signatureB64[:50])

        // Create GET request
        req, err = http.NewRequest("GET", url, nil)
        if err != nil {
            return "", fmt.Errorf("failed to create GET request: %v", err)
        }
    }

    // Set common headers
    req.Header.Set("X-Scheduler-Signature", signatureB64)
    req.Header.Set("X-Scheduler-PublicKey", s.publicKeyPEM)
    log.Printf("Public key sent to node %s: %s...", node.NodeID, s.publicKeyPEM[:50])

    // Send request
    resp, err := s.httpClient.Do(req)
    if err != nil {
        return "", fmt.Errorf("failed to send request to node %s: %v", node.NodeID, err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return "", fmt.Errorf("agent API %s for node %s returned status: %d", endpoint, node.NodeID, resp.StatusCode)
    }

    // Read the response body
    responseBytes, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", fmt.Errorf("failed to read response body from node %s: %v", node.NodeID, err)
    }

    log.Printf("Request to %s sent successfully to node %s", endpoint, node.NodeID)
    log.Printf("Agent Response from node %s: %s", node.NodeID, string(responseBytes))

    return string(responseBytes), nil
}

// matchesLabels checks if workload labels are a subset of node labels
func matchesLabels(workloadLabels, nodeLabels map[string]string) bool {
	if len(workloadLabels) == 0 {
		return true // No labels to match, accept node
	}
	for k, v := range workloadLabels {
		if nodeVal, ok := nodeLabels[k]; !ok || nodeVal != v {
			return false
		}
	}
	return true
}

// ScheduleWorkload assigns a workload to a suitable node and sends a command via the agent API.
func (s *Scheduler) ScheduleWorkload(workload models.Workload) (string, error) {
	if workload.ID == "" {
        workload.ID = uuid.New().String()
        log.Printf("Generated workload ID: %s", workload.ID)
    }
	log.Printf("Scheduling workload: %+v", workload)

	workload.CreatedAt = time.Now()
	workload.Status = "Pending"

	// Fetch nodes from etcd
	resp, err := s.RetryableEtcdGet("/nodes/", clientv3.WithPrefix())
	if err != nil {
		return "", fmt.Errorf("failed to get nodes for scheduling: %v", err)
	}
	if len(resp.Kvs) == 0 {
		log.Printf("No nodes found in etcd under /nodes/")
		return "", fmt.Errorf("no nodes available")
	}

	var selectedNode models.Node
	for _, kv := range resp.Kvs {
		var node models.Node
		if err := json.Unmarshal(kv.Value, &node); err != nil {
			log.Printf("Failed to unmarshal node data for key %s: %v", kv.Key, err)
			continue
		}
		log.Printf("Evaluating node %s: Status=%s, LastHeartbeat=%v, Labels=%v",
			node.NodeID, node.Status, node.LastHeartbeat, node.Labels)

		// Check node suitability
		if node.Status != "active" {
			log.Printf("Node %s is not Active (Status: %s)", node.NodeID, node.Status)
			continue
		}
		if time.Since(node.LastHeartbeat) > 10*time.Minute {
			log.Printf("Node %s heartbeat too old: %v", node.NodeID, node.LastHeartbeat)
			continue
		}
		if !matchesLabels(workload.Labels, node.Labels) {
			log.Printf("Node %s labels %v do not match workload labels %v", node.NodeID, node.Labels, workload.Labels)
			continue
		}

		selectedNode = node
		log.Printf("Selected node %s for workload %s", node.NodeID, workload.ID)
		break
	}

	if selectedNode.NodeID == "" {
		log.Printf("No suitable node found for workload %s", workload.ID)
		return "", fmt.Errorf("no suitable node available")
	}

	workload.NodeID = selectedNode.NodeID
	workload.Status = "Scheduled"

	// Prepare agent API request
	var endpoint string
	var payload interface{}
	switch workload.Type {
	case "docker-container":
		endpoint = "/docker/run"
		payload = map[string]interface{}{
			"image":         workload.Image,
			"name":          workload.Name,
			"command":       workload.Command,
			"env":           workload.EnvVars,
			"ports":         workload.Ports,
			"volumes":       workload.Volumes,
			"network":       workload.Network,
			"restartPolicy": workload.RestartPolicy,
			"detach":        true,
		}
	case "docker-compose":
		if workload.LocalPath == "" {
			return "", fmt.Errorf("LocalPath required for docker-compose type")
		}
		endpoint = "/compose/run"
		payload = map[string]interface{}{
			"composeDir":   workload.LocalPath,
			"envVariables": workload.EnvVars,
		}
	case "git-compose":
		if workload.GitRepo == "" {
			return "", fmt.Errorf("GitRepo required for git-compose type")
		}
		endpoint = "/compose/clone"
		payload = map[string]interface{}{
			"repoUrl":      workload.GitRepo,
			"branch":       workload.GitBranch,
			"authToken":    workload.GitToken,
			"envVariables": workload.EnvVars,
		}
	default:
		return "", fmt.Errorf("unsupported workload type: %s", workload.Type)
	}

	if response ,err := s.SendCommandToNode(selectedNode, endpoint, payload); err != nil {
		return "", fmt.Errorf("failed to send command to node %s: %v", selectedNode.NodeID, err)
	} else {
		log.Printf("Agent: %s response: %v", selectedNode.NodeID, response)
	}
	

	// Store workload in etcd
	workloadJSON, err := json.Marshal(workload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal workload: %v", err)
	}
	log.Printf("Storing workload %s at /workloads/%s: %s", workload.ID, workload.ID, string(workloadJSON))
	if err := s.RetryableEtcdPut("/workloads/"+workload.ID, string(workloadJSON)); err != nil {
		log.Printf("Failed to store workload %s: %v", workload.ID, err)
		return "", fmt.Errorf("failed to store workload %s: %v", workload.ID, err)
	}

	log.Printf("Workload %s successfully scheduled on node %s", workload.ID, selectedNode.NodeID)
	return selectedNode.NodeID, nil
}

// GetNodes retrieves all nodes from etcd.
func (s *Scheduler) GetNodes() ([]models.Node, error) {
	resp, err := s.RetryableEtcdGet("/nodes/", clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %v", err)
	}

	nodes := make([]models.Node, 0)
	if resp == nil {
		return nodes, nil
	}

	for _, kv := range resp.Kvs {
		var node models.Node
		if err := json.Unmarshal(kv.Value, &node); err != nil {
			log.Printf("Failed to unmarshal node data for key %s: %v", kv.Key, err)
			continue
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// GetNodeByID retrieves a specific node by ID.
func (s *Scheduler) GetNodeByID(nodeID string) (models.Node, error) {
	resp, err := s.RetryableEtcdGet("/nodes/" + nodeID)
	if err != nil {
		return models.Node{}, fmt.Errorf("failed to get node %s: %v", nodeID, err)
	}

	if len(resp.Kvs) == 0 {
		return models.Node{}, fmt.Errorf("node %s not found", nodeID)
	}

	var node models.Node
	if err := json.Unmarshal(resp.Kvs[0].Value, &node); err != nil {
		return models.Node{}, fmt.Errorf("failed to unmarshal node %s: %v", nodeID, err)
	}

	return node, nil
}

// DeleteNode removes a node from etcd and CoreDNS.
func (s *Scheduler) DeleteNode(nodeID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	_, err := s.etcdClient.Delete(ctx, "/nodes/"+nodeID)
	if err != nil {
		return fmt.Errorf("failed to delete node %s: %v", nodeID, err)
	}

	// Remove from CoreDNS
	key := fmt.Sprintf("/skydns/%s/%s", reverseDomain(s.domain), nodeID)
	_, err = s.etcdClient.Delete(ctx, key)
	if err != nil {
		log.Printf("Failed to remove CoreDNS entry for node %s: %v", nodeID, err)
	}

	log.Printf("Deleted node: %s", nodeID)
	return nil
}

// GetWorkloads retrieves all workloads from etcd.
func (s *Scheduler) GetWorkloads() ([]models.Workload, error) {
	resp, err := s.RetryableEtcdGet("/workloads/", clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to get workloads: %v", err)
	}

	workloads := make([]models.Workload, 0)
	if resp == nil {
		return workloads, nil
	}

	for _, kv := range resp.Kvs {
		var workload models.Workload
		if err := json.Unmarshal(kv.Value, &workload); err != nil {
			log.Printf("Failed to unmarshal workload data for key %s: %v", kv.Key, err)
			continue
		}
		workloads = append(workloads, workload)
	}

	return workloads, nil
}

// GetWorkloadByID retrieves a specific workload by ID.
func (s *Scheduler) GetWorkloadByID(workloadID string) (models.Workload, error) {
	resp, err := s.RetryableEtcdGet("/workloads/" + workloadID)
	if err != nil {
		return models.Workload{}, fmt.Errorf("failed to get workload %s: %v", workloadID, err)
	}

	if len(resp.Kvs) == 0 {
		return models.Workload{}, fmt.Errorf("workload %s not found", workloadID)
	}

	var workload models.Workload
	if err := json.Unmarshal(resp.Kvs[0].Value, &workload); err != nil {
		return models.Workload{}, fmt.Errorf("failed to unmarshal workload %s: %v", workloadID, err)
	}

	return workload, nil
}

// DeleteWorkload removes a workload from etcd.
func (s *Scheduler) DeleteWorkload(workloadID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), etcdTimeout)
	defer cancel()
	_, err := s.etcdClient.Delete(ctx, "/workloads/"+workloadID)
	if err != nil {
		return fmt.Errorf("failed to delete workload %s: %v", workloadID, err)
	}
	log.Printf("Deleted workload: %s", workloadID)
	return nil
}

// UpdateWorkloadStatus updates the status of a workload.
func (s *Scheduler) UpdateWorkloadStatus(workloadID, status string) error {
	workload, err := s.GetWorkloadByID(workloadID)
	if err != nil {
		return err
	}

	workload.Status = status
	workloadJSON, err := json.Marshal(workload)
	if err != nil {
		return fmt.Errorf("failed to marshal workload %s: %v", workloadID, err)
	}

	if err := s.RetryableEtcdPut("/workloads/"+workloadID, string(workloadJSON)); err != nil {
		return fmt.Errorf("failed to update workload %s status: %v", workloadID, err)
	}

	log.Printf("Updated workload %s status to %s", workloadID, status)
	return nil
}

// GetWorkloadsByNode retrieves all workloads assigned to a specific node.
func (s *Scheduler) GetWorkloadsByNode(nodeID string) ([]models.Workload, error) {
	workloads, err := s.GetWorkloads()
	if err != nil {
		return nil, err
	}

	nodeWorkloads := make([]models.Workload, 0)
	for _, workload := range workloads {
		if workload.NodeID == nodeID {
			nodeWorkloads = append(nodeWorkloads, workload)
		}
	}

	return nodeWorkloads, nil
}

// MonitorNodes periodically checks node health and updates status.
func (s *Scheduler) MonitorNodes(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("Stopping node monitoring")
			return
		case <-ticker.C:
			nodes, err := s.GetNodes()
			if err != nil {
				log.Printf("Error monitoring nodes: %v", err)
				continue
			}
			for _, node := range nodes {
				if time.Since(node.LastHeartbeat) > 10*time.Minute {
					log.Printf("Node %s inactive, last heartbeat: %v", node.NodeID, node.LastHeartbeat)
					node.Status = "Inactive"
					nodeJSON, err := json.Marshal(node)
					if err != nil {
						log.Printf("Failed to marshal node %s: %v", node.NodeID, err)
						continue
					}
					if err := s.RetryableEtcdPut("/nodes/"+node.NodeID, string(nodeJSON)); err != nil {
						log.Printf("Failed to update node %s status: %v", node.NodeID, err)
					} else {
						log.Printf("Updated node %s to Inactive", node.NodeID)
					}
				}
			}
		}
	}
}
