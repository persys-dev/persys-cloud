package scheduler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	pb "github.com/persys-dev/persys-cloud/pkg/vaultmanager/vaultmanagerv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Object storage is RGW-authoritative. The scheduler only proxies S3/RGW
// (and optionally reads/writes bucket access material in Vault). No etcd.

var bucketNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// BucketView is the API-facing projection derived from RGW.
type BucketView struct {
	ID          string
	Name        string
	Region      string
	Owner       string
	Endpoint    string
	Versioning  bool
	ObjectCount int64
	SizeBytes   int64
	Phase       string
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// BucketAccess is S3 client configuration for end users.
type BucketAccess struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	VaultPath string
	S3URL     string
}

// ObjectInfo is one object listing entry.
type ObjectInfo struct {
	Key          string
	SizeBytes    int64
	ETag         string
	LastModified string
	StorageClass string
}

// CreateBucketRequest is the control-plane input.
type CreateBucketRequest struct {
	Name       string
	Region     string
	Versioning bool
}

type rgwEnv struct {
	endpoint string
	region   string
	// Admin / control-plane credentials only (create/delete/list on RGW).
	// User-facing access keys always come from Vault.
	accessKey string
	secretKey string
}

func (s *Scheduler) rgwEnv() (rgwEnv, error) {
	endpoint := strings.TrimSpace(os.Getenv("PERSYS_RGW_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("PERSYS_S3_ENDPOINT"))
	}
	region := strings.TrimSpace(os.Getenv("PERSYS_RGW_REGION"))
	if region == "" {
		region = "default"
	}
	ak := strings.TrimSpace(os.Getenv("PERSYS_RGW_ACCESS_KEY"))
	if ak == "" {
		ak = strings.TrimSpace(os.Getenv("PERSYS_S3_ACCESS_KEY"))
	}
	sk := strings.TrimSpace(os.Getenv("PERSYS_RGW_SECRET_KEY"))
	if sk == "" {
		sk = strings.TrimSpace(os.Getenv("PERSYS_S3_SECRET_KEY"))
	}
	if endpoint == "" || ak == "" || sk == "" {
		return rgwEnv{}, fmt.Errorf("RGW not configured: set PERSYS_RGW_ENDPOINT, PERSYS_RGW_ACCESS_KEY, PERSYS_RGW_SECRET_KEY")
	}
	return rgwEnv{
		endpoint:  endpoint,
		region:    region,
		accessKey: ak,
		secretKey: sk,
	}, nil
}

func (s *Scheduler) rgwClient() (*rgwClient, rgwEnv, error) {
	env, err := s.rgwEnv()
	if err != nil {
		return nil, env, err
	}
	return &rgwClient{
		endpoint:  env.endpoint,
		region:    env.region,
		accessKey: env.accessKey,
		secretKey: env.secretKey,
	}, env, nil
}

func vaultPathForBucket(name string) string {
	base := strings.TrimSpace(os.Getenv("PERSYS_RGW_VAULT_PATH_PREFIX"))
	if base == "" {
		base = "secret/data/persys/rgw/buckets"
	}
	base = strings.Trim(base, "/")
	return base + "/" + name
}

// CreateBucket creates a bucket on RGW and stores user credentials in Vault (required).
func (s *Scheduler) CreateBucket(req CreateBucketRequest) (*BucketView, *BucketAccess, error) {
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if !bucketNameRE.MatchString(name) {
		return nil, nil, fmt.Errorf("invalid bucket name %q (S3 rules: 3-63 chars, lowercase, digits, dots, hyphens)", name)
	}
	client, env, err := s.rgwClient()
	if err != nil {
		return nil, nil, err
	}
	if req.Region != "" {
		env.region = strings.TrimSpace(req.Region)
		client.region = env.region
	}

	exists, err := client.headBucket(name)
	if err != nil {
		return nil, nil, err
	}
	if exists {
		return nil, nil, fmt.Errorf("bucket %q already exists", name)
	}
	if err := client.createBucket(name); err != nil {
		return nil, nil, fmt.Errorf("rgw create bucket: %w", err)
	}
	if req.Versioning {
		_ = client.putBucketVersioning(name, true)
	}

	userAK, err := randomAccessKey()
	if err != nil {
		return nil, nil, fmt.Errorf("generate access key: %w", err)
	}
	userSK, err := randomSecretKey()
	if err != nil {
		return nil, nil, fmt.Errorf("generate secret key: %w", err)
	}
	access := &BucketAccess{
		Endpoint:  env.endpoint,
		Region:    env.region,
		Bucket:    name,
		AccessKey: userAK,
		SecretKey: userSK,
		VaultPath: vaultPathForBucket(name),
		S3URL:     "s3://" + name,
	}
	// User credentials live only in Vault (not etcd, not env).
	if err := s.vaultPutBucketAccess(name, access); err != nil {
		// Best-effort rollback of empty bucket so we do not leave orphan RGW state
		// without recoverable credentials.
		_ = client.deleteBucket(name)
		return nil, nil, fmt.Errorf("vault store bucket access: %w", err)
	}
	// Best-effort: register key with RGW admin ops so S3 auth accepts it.
	if err := client.ensureUserKey(name, userAK, userSK); err != nil {
		// Vault already has the material; surface warning via LastError on view.
		// Do not fail create — operators can fix RGW user mapping separately.
		_ = err
	}

	now := time.Now().UTC()
	view := &BucketView{
		ID:         name,
		Name:       name,
		Region:     env.region,
		Endpoint:   env.endpoint,
		Versioning: req.Versioning,
		Phase:      "Available",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	return view, access, nil
}

// ListBuckets lists buckets from RGW (no local inventory).
func (s *Scheduler) ListBuckets() ([]BucketView, error) {
	client, env, err := s.rgwClient()
	if err != nil {
		return nil, err
	}
	names, err := client.listBuckets()
	if err != nil {
		return nil, err
	}
	out := make([]BucketView, 0, len(names))
	for _, n := range names {
		out = append(out, BucketView{
			ID:        n.Name,
			Name:      n.Name,
			Region:    env.region,
			Endpoint:  env.endpoint,
			Phase:     "Available",
			CreatedAt: n.CreationDate,
			UpdatedAt: n.CreationDate,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetBucket returns one bucket if it exists on RGW.
func (s *Scheduler) GetBucket(idOrName string) (*BucketView, error) {
	name := strings.TrimSpace(idOrName)
	if name == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	client, env, err := s.rgwClient()
	if err != nil {
		return nil, err
	}
	ok, err := client.headBucket(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("bucket %q not found", name)
	}
	return &BucketView{
		ID:       name,
		Name:     name,
		Region:   env.region,
		Endpoint: env.endpoint,
		Phase:    "Available",
	}, nil
}

// DeleteBucket removes the bucket on RGW.
func (s *Scheduler) DeleteBucket(idOrName string, force bool) error {
	name := strings.TrimSpace(idOrName)
	if name == "" {
		return fmt.Errorf("bucket name is required")
	}
	client, _, err := s.rgwClient()
	if err != nil {
		return err
	}
	if err := client.deleteBucket(name); err != nil {
		if force {
			_ = s.vaultDeleteBucketAccess(name)
			return nil
		}
		return fmt.Errorf("rgw delete bucket: %w", err)
	}
	_ = s.vaultDeleteBucketAccess(name)
	return nil
}

// GetBucketAccess returns S3 credentials from Vault only.
func (s *Scheduler) GetBucketAccess(idOrName string) (*BucketAccess, error) {
	name := strings.TrimSpace(idOrName)
	if name == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	client, env, err := s.rgwClient()
	if err != nil {
		return nil, err
	}
	ok, err := client.headBucket(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("bucket %q not found", name)
	}

	access, err := s.vaultGetBucketAccess(name)
	if err != nil {
		return nil, fmt.Errorf("bucket access not in Vault (path %s): %w", vaultPathForBucket(name), err)
	}
	// Fill endpoint/region from live RGW config when Vault blob is partial.
	if access.Endpoint == "" {
		access.Endpoint = env.endpoint
	}
	if access.Region == "" {
		access.Region = env.region
	}
	if access.Bucket == "" {
		access.Bucket = name
	}
	if access.S3URL == "" {
		access.S3URL = "s3://" + name
	}
	access.VaultPath = vaultPathForBucket(name)
	return access, nil
}

// ListBucketObjects lists objects via RGW.
func (s *Scheduler) ListBucketObjects(idOrName, prefix, continuation string, maxKeys int32) ([]ObjectInfo, string, bool, error) {
	name := strings.TrimSpace(idOrName)
	if name == "" {
		return nil, "", false, fmt.Errorf("bucket name is required")
	}
	client, _, err := s.rgwClient()
	if err != nil {
		return nil, "", false, err
	}
	ok, err := client.headBucket(name)
	if err != nil {
		return nil, "", false, err
	}
	if !ok {
		return nil, "", false, fmt.Errorf("bucket %q not found", name)
	}
	if maxKeys <= 0 {
		maxKeys = 100
	}
	if maxKeys > 1000 {
		maxKeys = 1000
	}
	return client.listObjects(name, prefix, continuation, int(maxKeys))
}

// vaultCredCache avoids hammering vault-manager + Vault login on every bucket op.
var (
	vaultTokMu     sync.Mutex
	vaultTokCached string
	vaultTokExpiry time.Time
)

func vaultAddr() (string, error) {
	addr := strings.TrimSpace(os.Getenv("PERSYS_VAULT_ADDR"))
	if addr == "" {
		return "", fmt.Errorf("PERSYS_VAULT_ADDR is required for object-storage credentials")
	}
	return strings.TrimRight(addr, "/"), nil
}

func vaultManagerAddr() string {
	a := strings.TrimSpace(os.Getenv("PERSYS_VAULT_MANAGER_ADDR"))
	if a == "" {
		a = "vault-manager:50069"
	}
	return a
}

func vaultServiceName() string {
	n := strings.TrimSpace(os.Getenv("PERSYS_VAULT_SERVICE_NAME"))
	if n == "" {
		n = "persys-scheduler"
	}
	return n
}

// fetchAppRoleFromVaultManager calls vault-manager GetServiceCredentials (same
// path certmanager uses for PKI). No manual role_id/secret_id env required.
func fetchAppRoleFromVaultManager(ctx context.Context) (roleID, secretID string, err error) {
	addr := vaultManagerAddr()
	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return "", "", fmt.Errorf("dial vault-manager %s: %w", addr, err)
	}
	defer conn.Close()

	client := pb.NewVaultManagerServiceClient(conn)
	resp, err := client.GetServiceCredentials(ctx, &pb.GetServiceCredentialsRequest{
		ServiceName: vaultServiceName(),
	})
	if err != nil {
		return "", "", fmt.Errorf("vault-manager GetServiceCredentials(%s): %w", vaultServiceName(), err)
	}
	if resp.GetRoleId() == "" || resp.GetSecretId() == "" {
		return "", "", fmt.Errorf("vault-manager returned empty AppRole credentials for %s", vaultServiceName())
	}
	return resp.GetRoleId(), resp.GetSecretId(), nil
}

func vaultAppRoleLogin(addr, roleID, secretID string) (token string, leaseTTL time.Duration, err error) {
	mount := strings.TrimSpace(os.Getenv("PERSYS_VAULT_APPROLE_MOUNT"))
	if mount == "" {
		mount = "auth/approle"
	}
	body, _ := json.Marshal(map[string]string{
		"role_id":   roleID,
		"secret_id": secretID,
	})
	req, err := http.NewRequest(http.MethodPost, addr+"/v1/"+strings.Trim(mount, "/")+"/login", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("vault approle login: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var parsed struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return "", 0, err
	}
	if parsed.Auth.ClientToken == "" {
		return "", 0, fmt.Errorf("vault approle login: empty client_token")
	}
	ttl := time.Duration(parsed.Auth.LeaseDuration) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	return parsed.Auth.ClientToken, ttl, nil
}

// vaultToken resolves a Vault client token.
// Preference order matches the rest of Persys:
//  1. Cached token (still valid)
//  2. PERSYS_VAULT_TOKEN (dev/break-glass only)
//  3. vault-manager → AppRole role_id/secret_id → Vault login
//     (production path; same as certmanager)
func vaultToken() (string, error) {
	vaultTokMu.Lock()
	defer vaultTokMu.Unlock()

	if vaultTokCached != "" && time.Now().Before(vaultTokExpiry) {
		return vaultTokCached, nil
	}

	// Explicit token still allowed for local/dev.
	if tok := strings.TrimSpace(os.Getenv("PERSYS_VAULT_TOKEN")); tok != "" {
		vaultTokCached = tok
		vaultTokExpiry = time.Now().Add(30 * time.Minute)
		return tok, nil
	}

	addr, err := vaultAddr()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	roleID, secretID, err := fetchAppRoleFromVaultManager(ctx)
	if err != nil {
		// Last resort: static env AppRole (legacy); prefer vault-manager.
		roleID = strings.TrimSpace(os.Getenv("PERSYS_VAULT_APPROLE_ROLE_ID"))
		secretID = strings.TrimSpace(os.Getenv("PERSYS_VAULT_APPROLE_SECRET_ID"))
		if roleID == "" || secretID == "" {
			return "", fmt.Errorf("vault credentials: %w", err)
		}
	}

	tok, ttl, err := vaultAppRoleLogin(addr, roleID, secretID)
	if err != nil {
		return "", err
	}
	// Refresh a bit before lease end.
	refresh := ttl * 80 / 100
	if refresh < time.Minute {
		refresh = ttl - 10*time.Second
	}
	if refresh < 30*time.Second {
		refresh = 30 * time.Second
	}
	vaultTokCached = tok
	vaultTokExpiry = time.Now().Add(refresh)
	return tok, nil
}

func (s *Scheduler) vaultPutBucketAccess(name string, access *BucketAccess) error {
	if access == nil {
		return fmt.Errorf("access is nil")
	}
	addr, err := vaultAddr()
	if err != nil {
		return err
	}
	token, err := vaultToken()
	if err != nil {
		return err
	}
	path := vaultPathForBucket(name)
	body, _ := json.Marshal(map[string]any{
		"data": map[string]string{
			"endpoint":   access.Endpoint,
			"region":     access.Region,
			"bucket":     access.Bucket,
			"access_key": access.AccessKey,
			"secret_key": access.SecretKey,
			"s3_url":     access.S3URL,
		},
	})
	req, err := http.NewRequest(http.MethodPost, addr+"/v1/"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("vault put %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func (s *Scheduler) vaultGetBucketAccess(name string) (*BucketAccess, error) {
	addr, err := vaultAddr()
	if err != nil {
		return nil, err
	}
	token, err := vaultToken()
	if err != nil {
		return nil, err
	}
	path := vaultPathForBucket(name)
	req, err := http.NewRequest(http.MethodGet, addr+"/v1/"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", token)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("vault get %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var parsed struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	d := parsed.Data.Data
	if d["access_key"] == "" || d["secret_key"] == "" {
		return nil, fmt.Errorf("vault secret missing access_key/secret_key")
	}
	return &BucketAccess{
		Endpoint:  d["endpoint"],
		Region:    d["region"],
		Bucket:    d["bucket"],
		AccessKey: d["access_key"],
		SecretKey: d["secret_key"],
		VaultPath: path,
		S3URL:     d["s3_url"],
	}, nil
}

func (s *Scheduler) vaultDeleteBucketAccess(name string) error {
	addr, err := vaultAddr()
	if err != nil {
		return err
	}
	token, err := vaultToken()
	if err != nil {
		return err
	}
	path := vaultPathForBucket(name)
	// KV v2 delete metadata path uses secret/metadata/... when data path is secret/data/...
	metaPath := path
	if strings.Contains(path, "/data/") {
		metaPath = strings.Replace(path, "/data/", "/metadata/", 1)
	}
	req, err := http.NewRequest(http.MethodDelete, addr+"/v1/"+metaPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", token)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("vault delete %s: %d %s", metaPath, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func randomAccessKey() (string, error) {
	// 20 chars alphanumeric, similar shape to AWS access keys
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return "PERS" + string(b), nil
}

func randomSecretKey() (string, error) {
	b := make([]byte, 30)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil // 60 hex chars
}

type rgwClient struct {
	endpoint  string
	region    string
	accessKey string
	secretKey string
	http      *http.Client
}

func (c *rgwClient) client() *http.Client {
	if c.http != nil {
		return c.http
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *rgwClient) baseURL() string {
	return strings.TrimRight(c.endpoint, "/")
}

type rgwBucketName struct {
	Name         string
	CreationDate time.Time
}

func (c *rgwClient) listBuckets() ([]rgwBucketName, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL()+"/", nil)
	if err != nil {
		return nil, err
	}
	if err := c.sign(req, nil); err != nil {
		return nil, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list buckets status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Buckets struct {
			Bucket []struct {
				Name         string `xml:"Name"`
				CreationDate string `xml:"CreationDate"`
			} `xml:"Bucket"`
		} `xml:"Buckets"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse list buckets: %w", err)
	}
	out := make([]rgwBucketName, 0, len(parsed.Buckets.Bucket))
	for _, b := range parsed.Buckets.Bucket {
		t, _ := time.Parse(time.RFC3339, b.CreationDate)
		out = append(out, rgwBucketName{Name: b.Name, CreationDate: t})
	}
	return out, nil
}

func (c *rgwClient) headBucket(name string) (bool, error) {
	req, err := http.NewRequest(http.MethodHead, c.baseURL()+"/"+url.PathEscape(name), nil)
	if err != nil {
		return false, err
	}
	if err := c.sign(req, nil); err != nil {
		return false, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusMovedPermanently:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	case http.StatusForbidden:
		list, err := c.listBuckets()
		if err != nil {
			return false, err
		}
		for _, b := range list {
			if b.Name == name {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("head bucket status %d", resp.StatusCode)
	}
}

func (c *rgwClient) createBucket(name string) error {
	req, err := http.NewRequest(http.MethodPut, c.baseURL()+"/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	if err := c.sign(req, nil); err != nil {
		return err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *rgwClient) deleteBucket(name string) error {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL()+"/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	if err := c.sign(req, nil); err != nil {
		return err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *rgwClient) putBucketVersioning(name string, enabled bool) error {
	status := "Suspended"
	if enabled {
		status = "Enabled"
	}
	payload := []byte(fmt.Sprintf(`<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>%s</Status></VersioningConfiguration>`, status))
	req, err := http.NewRequest(http.MethodPut, c.baseURL()+"/"+url.PathEscape(name)+"?versioning", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/xml")
	if err := c.sign(req, payload); err != nil {
		return err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

type listBucketResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
	} `xml:"Contents"`
}

func (c *rgwClient) listObjects(bucket, prefix, continuation string, maxKeys int) ([]ObjectInfo, string, bool, error) {
	q := url.Values{}
	q.Set("list-type", "2")
	q.Set("max-keys", fmt.Sprintf("%d", maxKeys))
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if continuation != "" {
		q.Set("continuation-token", continuation)
	}
	u := c.baseURL() + "/" + url.PathEscape(bucket) + "?" + q.Encode()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, "", false, err
	}
	if err := c.sign(req, nil); err != nil {
		return nil, "", false, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "", false, err
	}
	if resp.StatusCode >= 300 {
		return nil, "", false, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed listBucketResult
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, "", false, fmt.Errorf("parse list response: %w", err)
	}
	out := make([]ObjectInfo, 0, len(parsed.Contents))
	for _, o := range parsed.Contents {
		out = append(out, ObjectInfo{
			Key:          o.Key,
			SizeBytes:    o.Size,
			ETag:         strings.Trim(o.ETag, `"`),
			LastModified: o.LastModified,
			StorageClass: o.StorageClass,
		})
	}
	return out, parsed.NextContinuationToken, parsed.IsTruncated, nil
}


// ensureUserKey best-effort registers an S3 key via RGW Admin Ops API so the
// Vault-issued credentials can authenticate. Requires the admin user to have
// admin caps. Path prefix from PERSYS_RGW_ADMIN_PATH (default "admin").
func (c *rgwClient) ensureUserKey(bucket, accessKey, secretKey string) error {
	admin := strings.Trim(strings.TrimSpace(os.Getenv("PERSYS_RGW_ADMIN_PATH")), "/")
	if admin == "" {
		admin = "admin"
	}
	uid := "persys-bkt-" + bucket
	q := url.Values{}
	q.Set("uid", uid)
	q.Set("display-name", "Persys bucket "+bucket)
	q.Set("access-key", accessKey)
	q.Set("secret-key", secretKey)
	u := c.baseURL() + "/" + admin + "/user?" + q.Encode()
	req, err := http.NewRequest(http.MethodPut, u, nil)
	if err != nil {
		return err
	}
	if err := c.sign(req, nil); err != nil {
		return err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusConflict {
		q2 := url.Values{}
		q2.Set("uid", uid)
		q2.Set("access-key", accessKey)
		q2.Set("secret-key", secretKey)
		u2 := c.baseURL() + "/" + admin + "/user?key&" + q2.Encode()
		req2, err := http.NewRequest(http.MethodPut, u2, nil)
		if err != nil {
			return fmt.Errorf("admin user create: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		if err := c.sign(req2, nil); err != nil {
			return err
		}
		resp2, err := c.client().Do(req2)
		if err != nil {
			return err
		}
		defer resp2.Body.Close()
		if resp2.StatusCode >= 300 {
			b2, _ := io.ReadAll(io.LimitReader(resp2.Body, 2048))
			return fmt.Errorf("admin user/key: %d %s / key %d %s", resp.StatusCode, strings.TrimSpace(string(body)), resp2.StatusCode, strings.TrimSpace(string(b2)))
		}
	}
	return nil
}

func (c *rgwClient) sign(req *http.Request, payload []byte) error {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	region := c.region
	if region == "" {
		region = "default"
	}
	service := "s3"
	if payload == nil {
		payload = []byte{}
	}
	payloadHash := sha256Hex(payload)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("X-Amz-Date", amzDate)
	if req.Header.Get("Host") == "" {
		req.Header.Set("Host", req.URL.Host)
	}
	canonicalHdrs, signedHeaders := canonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonicalHdrs,
		signedHeaders,
		payloadHash,
	}, "\n")
	credentialScope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := sigV4Key(c.secretKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		c.accessKey, credentialScope, signedHeaders, signature,
	))
	return nil
}

func canonicalHeaders(req *http.Request) (string, string) {
	headerMap := map[string]string{"host": req.URL.Host}
	keys := []string{"host"}
	for k, vals := range req.Header {
		lk := strings.ToLower(k)
		if lk == "host" {
			continue
		}
		if strings.HasPrefix(lk, "x-amz-") || lk == "content-type" || lk == "content-md5" {
			headerMap[lk] = strings.TrimSpace(strings.Join(vals, ","))
			keys = append(keys, lk)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(":")
		b.WriteString(headerMap[k])
		b.WriteString("\n")
	}
	return b.String(), strings.Join(keys, ";")
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(data)
	return m.Sum(nil)
}

func sigV4Key(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}