package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"easy_proxies/internal/boxmgr"
	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
)

// Logger defines logging interface.
type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// Option configures the Manager.
type Option func(*Manager)

// WithLogger sets a custom logger.
func WithLogger(l Logger) Option {
	return func(m *Manager) { m.logger = l }
}

// Manager handles periodic subscription refresh.
type Manager struct {
	mu sync.RWMutex

	baseCfg    *config.Config
	boxMgr     *boxmgr.Manager
	logger     Logger
	httpClient *http.Client // Custom HTTP client with connection pooling

	status        monitor.SubscriptionStatus
	ctx           context.Context
	cancel        context.CancelFunc
	refreshMu     sync.Mutex // prevents concurrent refreshes
	manualRefresh chan struct{}

	// Track nodes.txt content hash to detect modifications
	lastSubHash      string    // Hash of nodes.txt content after last subscription refresh
	lastNodesModTime time.Time // Last known modification time of nodes.txt
}

// New creates a SubscriptionManager.
func New(cfg *config.Config, boxMgr *boxmgr.Manager, opts ...Option) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	// Create optimized HTTP client with connection pooling
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second, // Overall timeout
	}

	m := &Manager{
		baseCfg:       cfg,
		boxMgr:        boxMgr,
		ctx:           ctx,
		cancel:        cancel,
		manualRefresh: make(chan struct{}, 1),
		httpClient:    httpClient,
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.logger == nil {
		m.logger = defaultLogger{}
	}
	return m
}

// Start begins the periodic refresh loop.
func (m *Manager) Start() {
	if !m.baseCfg.SubscriptionRefresh.Enabled {
		m.logger.Infof("subscription refresh disabled")
		return
	}
	if len(m.baseCfg.Subscriptions) == 0 {
		m.logger.Infof("no subscriptions configured, refresh disabled")
		return
	}

	interval := m.baseCfg.SubscriptionRefresh.Interval
	m.logger.Infof("starting subscription refresh, interval: %s", interval)

	go m.refreshLoop(interval)
}

// Stop stops the periodic refresh.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	// Close idle connections
	if m.httpClient != nil {
		m.httpClient.CloseIdleConnections()
	}
}

// RefreshNow triggers an immediate refresh.
func (m *Manager) RefreshNow() error {
	select {
	case m.manualRefresh <- struct{}{}:
	default:
		// Already a refresh pending
	}

	// Wait for refresh to complete or timeout
	timeout := m.baseCfg.SubscriptionRefresh.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(m.ctx, timeout+m.baseCfg.SubscriptionRefresh.HealthCheckTimeout)
	defer cancel()

	// Poll status until refresh completes
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	startCount := m.Status().RefreshCount
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("refresh timeout")
		case <-ticker.C:
			status := m.Status()
			if status.RefreshCount > startCount {
				if status.LastError != "" {
					return fmt.Errorf("refresh failed: %s", status.LastError)
				}
				return nil
			}
		}
	}
}

// Status returns the current refresh status.
func (m *Manager) Status() monitor.SubscriptionStatus {
	m.mu.RLock()
	status := m.status
	m.mu.RUnlock()

	// Check if nodes have been modified since last refresh
	status.NodesModified = m.CheckNodesModified()
	return status
}

// refreshLoop runs the periodic refresh.
func (m *Manager) refreshLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Update next refresh time
	m.mu.Lock()
	m.status.NextRefresh = time.Now().Add(interval)
	m.mu.Unlock()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.doRefresh()
			m.mu.Lock()
			m.status.NextRefresh = time.Now().Add(interval)
			m.mu.Unlock()
		case <-m.manualRefresh:
			m.doRefresh()
			// Reset ticker after manual refresh
			ticker.Reset(interval)
			m.mu.Lock()
			m.status.NextRefresh = time.Now().Add(interval)
			m.mu.Unlock()
		}
	}
}

// doRefresh performs a single refresh operation.
func (m *Manager) doRefresh() {
	// Prevent concurrent refreshes
	if !m.refreshMu.TryLock() {
		m.logger.Warnf("refresh already in progress, skipping")
		return
	}
	defer m.refreshMu.Unlock()

	m.mu.Lock()
	m.status.IsRefreshing = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.status.IsRefreshing = false
		m.status.RefreshCount++
		m.mu.Unlock()
	}()

	m.logger.Infof("starting subscription refresh")

	// Fetch nodes from all subscriptions
	nodes, err := m.fetchAllSubscriptions()
	if err != nil {
		m.logger.Errorf("fetch subscriptions failed: %v", err)
		m.mu.Lock()
		m.status.LastError = err.Error()
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	if len(nodes) == 0 {
		m.logger.Warnf("no nodes fetched from subscriptions")
		m.mu.Lock()
		m.status.LastError = "no nodes fetched"
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	m.logger.Infof("fetched %d nodes from subscriptions", len(nodes))

	// Write subscription nodes to nodes.txt
	nodesFilePath := m.getNodesFilePath()
	if err := m.writeNodesToFile(nodesFilePath, nodes); err != nil {
		m.logger.Errorf("failed to write nodes.txt: %v", err)
		m.mu.Lock()
		m.status.LastError = fmt.Sprintf("write nodes.txt: %v", err)
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}
	m.logger.Infof("written %d nodes to %s", len(nodes), nodesFilePath)

	// Update hash and mod time after writing
	newHash := m.computeNodesHash(nodes)
	m.mu.Lock()
	m.lastSubHash = newHash
	if info, err := os.Stat(nodesFilePath); err == nil {
		m.lastNodesModTime = info.ModTime()
	} else {
		m.lastNodesModTime = time.Now()
	}
	m.status.NodesModified = false
	m.mu.Unlock()

	// Get current port mapping to preserve existing node ports
	portMap := m.boxMgr.CurrentPortMap()

	// Create new config with updated nodes
	newCfg := m.createNewConfig(nodes)

	// Trigger BoxManager reload with port preservation
	if err := m.boxMgr.ReloadWithPortMap(newCfg, portMap); err != nil {
		m.logger.Errorf("reload failed: %v", err)
		m.mu.Lock()
		m.status.LastError = err.Error()
		m.status.LastRefresh = time.Now()
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	m.status.LastRefresh = time.Now()
	m.status.NodeCount = len(nodes)
	m.status.LastError = ""
	m.mu.Unlock()

	m.logger.Infof("subscription refresh completed, %d nodes active", len(nodes))
}

// getNodesFilePath returns the path to nodes.txt.
func (m *Manager) getNodesFilePath() string {
	if m.baseCfg.NodesFile != "" {
		return m.baseCfg.NodesFile
	}
	return filepath.Join(filepath.Dir(m.baseCfg.FilePath()), "nodes.txt")
}

// writeNodesToFile writes nodes to a file (one URI per line).
func (m *Manager) writeNodesToFile(path string, nodes []config.NodeConfig) error {
	var lines []string
	for _, node := range nodes {
		lines = append(lines, node.URI)
	}
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// computeNodesHash computes a hash of node URIs for change detection.
func (m *Manager) computeNodesHash(nodes []config.NodeConfig) string {
	var uris []string
	for _, node := range nodes {
		uris = append(uris, node.URI)
	}
	content := strings.Join(uris, "\n")
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// CheckNodesModified checks if nodes.txt has been modified since last refresh.
// Uses file modification time as a fast path to avoid unnecessary file reads.
func (m *Manager) CheckNodesModified() bool {
	m.mu.RLock()
	lastHash := m.lastSubHash
	lastMod := m.lastNodesModTime
	m.mu.RUnlock()

	if lastHash == "" {
		return false // No previous refresh, can't determine modification
	}

	nodesFilePath := m.getNodesFilePath()

	// Fast path: check modification time first
	info, err := os.Stat(nodesFilePath)
	if err != nil {
		return false // File doesn't exist or can't stat
	}
	modTime := info.ModTime()
	if !modTime.After(lastMod) {
		return false // File hasn't been modified
	}

	// Slow path: file was modified, compute hash
	data, err := os.ReadFile(nodesFilePath)
	if err != nil {
		return false // File doesn't exist or can't read
	}

	// Parse nodes from file content
	var nodes []config.NodeConfig
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if isProxyURI(line) {
			nodes = append(nodes, config.NodeConfig{URI: line})
		}
	}

	currentHash := m.computeNodesHash(nodes)
	changed := currentHash != lastHash

	// Update cached mod time
	m.mu.Lock()
	m.lastNodesModTime = modTime
	m.mu.Unlock()

	return changed
}

// MarkNodesModified updates the modification status.
func (m *Manager) MarkNodesModified() {
	m.mu.Lock()
	m.status.NodesModified = true
	m.mu.Unlock()
}

// fetchAllSubscriptions fetches nodes from all configured subscription URLs.
func (m *Manager) fetchAllSubscriptions() ([]config.NodeConfig, error) {
	var allNodes []config.NodeConfig
	var lastErr error

	timeout := m.baseCfg.SubscriptionRefresh.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	for _, sub := range m.baseCfg.Subscriptions {
		nodes, err := m.fetchSubscription(sub.URL, timeout)
		if err != nil {
			m.logger.Warnf("failed to fetch %s: %v", sub.URL, err)
			lastErr = err
			continue
		}
		// Tag nodes with subscription source URL
		for i := range nodes {
			nodes[i].SubscriptionURL = sub.URL
		}
		m.logger.Infof("fetched %d nodes from subscription %q", len(nodes), sub.URL)
		allNodes = append(allNodes, nodes...)
	}

	if len(allNodes) == 0 && lastErr != nil {
		return nil, lastErr
	}

	return allNodes, nil
}

// fetchSubscription fetches and parses a single subscription URL.
func (m *Manager) fetchSubscription(subURL string, timeout time.Duration) ([]config.NodeConfig, error) {
	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", subURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "*/*")

	// Use custom HTTP client with connection pooling
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	// Limit read size to prevent memory exhaustion
	const maxBodySize = 10 * 1024 * 1024 // 10MB
	limitedReader := io.LimitReader(resp.Body, maxBodySize)

	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return parseSubscriptionContent(string(body))
}

// createNewConfig creates a new config with updated nodes while preserving other settings.
func (m *Manager) createNewConfig(nodes []config.NodeConfig) *config.Config {
	// Deep copy base config
	newCfg := *m.baseCfg

	// Assign port numbers to nodes in multi-port mode
	if newCfg.Mode == "multi-port" {
		portCursor := newCfg.MultiPort.BasePort
		for i := range nodes {
			nodes[i].Port = portCursor
			portCursor++
			// Apply default credentials
			if nodes[i].Username == "" {
				nodes[i].Username = newCfg.MultiPort.Username
				nodes[i].Password = newCfg.MultiPort.Password
			}
		}
	}

	// Process node names
	for i := range nodes {
		nodes[i].Name = strings.TrimSpace(nodes[i].Name)
		nodes[i].URI = strings.TrimSpace(nodes[i].URI)

		// Extract name from URI fragment if not provided
		if nodes[i].Name == "" {
			if parsed, err := url.Parse(nodes[i].URI); err == nil && parsed.Fragment != "" {
				if decoded, err := url.QueryUnescape(parsed.Fragment); err == nil {
					nodes[i].Name = decoded
				} else {
					nodes[i].Name = parsed.Fragment
				}
			}
		}
		if nodes[i].Name == "" {
			nodes[i].Name = fmt.Sprintf("node-%d", i)
		}
	}

	newCfg.Nodes = nodes
	return &newCfg
}

// parseSubscriptionContent parses subscription content in various formats.
// This is a simplified version - the full implementation is in config package.
func parseSubscriptionContent(content string) ([]config.NodeConfig, error) {
	content = strings.TrimSpace(content)

	// Check if it's base64 encoded
	if isBase64(content) {
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(content)
			if err != nil {
				return parseNodesFromContent(content)
			}
		}
		content = string(decoded)
	}

	// Parse as plain text (one URI per line)
	return parseNodesFromContent(content)
}

func isBase64(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return false
	}
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	if strings.Contains(s, "://") {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		_, err = base64.RawStdEncoding.DecodeString(s)
	}
	return err == nil
}

func parseNodesFromContent(content string) ([]config.NodeConfig, error) {
	var nodes []config.NodeConfig
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if isProxyURI(line) {
			nodes = append(nodes, config.NodeConfig{URI: line})
		}
	}
	return nodes, nil
}

// SubscriptionInfo represents a subscription source with runtime statistics.
type SubscriptionInfo struct {
	URL        string  `json:"url"`
	Alias      string  `json:"alias"`
	NodeCount  int     `json:"node_count"`
	AliveCount int     `json:"alive_count"`
	AliveRate  float64 `json:"alive_rate"` // 0-100 percentage
}

// ListSubscriptions returns all configured subscription sources.
func (m *Manager) ListSubscriptions() []config.SubscriptionSource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]config.SubscriptionSource, len(m.baseCfg.Subscriptions))
	copy(result, m.baseCfg.Subscriptions)
	return result
}

// AddSubscription adds a new subscription source and persists to config.
func (m *Manager) AddSubscription(sub config.SubscriptionSource) error {
	m.mu.Lock()
	// Check for duplicate URL
	for _, existing := range m.baseCfg.Subscriptions {
		if existing.URL == sub.URL {
			m.mu.Unlock()
			return fmt.Errorf("订阅源已存在: %s", sub.URL)
		}
	}
	m.baseCfg.Subscriptions = append(m.baseCfg.Subscriptions, sub)
	m.mu.Unlock()

	return m.baseCfg.SaveSubscriptions()
}

// UpdateSubscription updates an existing subscription source by URL.
func (m *Manager) UpdateSubscription(oldURL string, sub config.SubscriptionSource) error {
	m.mu.Lock()
	found := false
	for i, existing := range m.baseCfg.Subscriptions {
		if existing.URL == oldURL {
			// If URL changed, check for duplicate
			if sub.URL != oldURL {
				for j, other := range m.baseCfg.Subscriptions {
					if j != i && other.URL == sub.URL {
						m.mu.Unlock()
						return fmt.Errorf("订阅源已存在: %s", sub.URL)
					}
				}
			}
			m.baseCfg.Subscriptions[i] = sub
			found = true
			break
		}
	}
	m.mu.Unlock()

	if !found {
		return fmt.Errorf("订阅源不存在: %s", oldURL)
	}
	return m.baseCfg.SaveSubscriptions()
}

// DeleteSubscription removes a subscription source by URL and cleans up its nodes.
func (m *Manager) DeleteSubscription(subURL string) error {
	m.mu.Lock()
	found := false
	subs := m.baseCfg.Subscriptions
	for i, existing := range subs {
		if existing.URL == subURL {
			m.baseCfg.Subscriptions = append(subs[:i], subs[i+1:]...)
			found = true
			break
		}
	}
	m.mu.Unlock()

	if !found {
		return fmt.Errorf("订阅源不存在: %s", subURL)
	}

	if err := m.baseCfg.SaveSubscriptions(); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	// Clean up nodes belonging to the deleted subscription
	m.removeSubscriptionNodes(subURL)

	return nil
}

// removeSubscriptionNodes removes nodes from a deleted subscription,
// updates nodes.txt, and reloads boxmgr.
func (m *Manager) removeSubscriptionNodes(subURL string) {
	// Prevent conflicts with concurrent refresh operations
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	currentNodes, err := m.boxMgr.ListConfigNodes(context.Background())
	if err != nil {
		m.logger.Warnf("failed to get current nodes for cleanup: %v", err)
		return
	}

	// Filter out nodes belonging to the deleted subscription
	var remaining []config.NodeConfig
	removed := 0
	for _, node := range currentNodes {
		if node.SubscriptionURL == subURL {
			removed++
			continue
		}
		remaining = append(remaining, node)
	}

	if removed == 0 {
		m.logger.Infof("no nodes found for subscription %q, skipping cleanup", subURL)
		return
	}

	m.logger.Infof("removing %d nodes from deleted subscription %q", removed, subURL)

	// Write remaining subscription nodes to nodes.txt
	var subNodes []config.NodeConfig
	for _, node := range remaining {
		if node.SubscriptionURL != "" {
			subNodes = append(subNodes, node)
		}
	}

	nodesFilePath := m.getNodesFilePath()
	if err := m.writeNodesToFile(nodesFilePath, subNodes); err != nil {
		m.logger.Errorf("failed to update nodes.txt after subscription delete: %v", err)
		return
	}

	// Update hash and modification time tracking
	newHash := m.computeNodesHash(subNodes)
	m.mu.Lock()
	m.lastSubHash = newHash
	if info, statErr := os.Stat(nodesFilePath); statErr == nil {
		m.lastNodesModTime = info.ModTime()
	}
	m.status.NodeCount = len(remaining)
	m.status.NodesModified = false
	m.mu.Unlock()

	// Reload boxmgr with remaining nodes (preserving port assignments)
	portMap := m.boxMgr.CurrentPortMap()
	newCfg := m.createNewConfig(remaining)
	if err := m.boxMgr.ReloadWithPortMap(newCfg, portMap); err != nil {
		m.logger.Errorf("failed to reload after subscription delete: %v", err)
		return
	}

	m.logger.Infof("subscription cleanup completed, %d nodes remaining", len(remaining))
}

// RefreshOne fetches nodes from a single subscription URL and returns the count.
func (m *Manager) RefreshOne(subURL string) (int, error) {
	timeout := m.baseCfg.SubscriptionRefresh.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	nodes, err := m.fetchSubscription(subURL, timeout)
	if err != nil {
		return 0, err
	}
	return len(nodes), nil
}

func isProxyURI(s string) bool {
	schemes := []string{"vmess://", "vless://", "trojan://", "ss://", "ssr://", "hysteria://", "hysteria2://", "hy2://"}
	lower := strings.ToLower(s)
	for _, scheme := range schemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

type defaultLogger struct{}

func (defaultLogger) Infof(format string, args ...any) {
	log.Printf("[subscription] "+format, args...)
}

func (defaultLogger) Warnf(format string, args ...any) {
	log.Printf("[subscription] WARN: "+format, args...)
}

func (defaultLogger) Errorf(format string, args ...any) {
	log.Printf("[subscription] ERROR: "+format, args...)
}
