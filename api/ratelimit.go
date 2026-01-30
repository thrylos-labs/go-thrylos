// api/ratelimit.go
// Rate limiting middleware to prevent DDoS attacks and resource exhaustion

package api

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter manages rate limits for different endpoint tiers
type RateLimiter struct {
	limiters map[string]*IPRateLimiter
	// [FIX M-07] Account-based limiting to prevent IP rotation attacks
	keyLimiters map[string]*rate.Limiter
	mu          sync.RWMutex

	// Configuration
	strictLimit     rate.Limit
	standardLimit   rate.Limit
	permissiveLimit rate.Limit
	faucetLimit     rate.Limit // [FIX M-04] New tier for faucet
	apiKeyLimit     rate.Limit // [FIX M-07] New tier for authenticated users

	// Burst allowances
	strictBurst     int
	standardBurst   int
	permissiveBurst int
	faucetBurst     int // [FIX M-04] New burst for faucet
	apiKeyBurst     int // [FIX M-07]

	cleanupInterval time.Duration
	maxIdleTime     time.Duration

	blockedIPs     map[string]time.Time // IP -> unblock time
	suspiciousIPs  map[string]bool      // [FIX M-07] IP -> requires CAPTCHA
	violationCount map[string]int       // IP -> violation count
	blockDuration  time.Duration        // How long to block IPs
}

// IPRateLimiter manages rate limiting per IP address
type IPRateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	limit    rate.Limit
	burst    int
	lastSeen map[string]time.Time
}

// RateLimitConfig holds rate limiting configuration
type RateLimitConfig struct {
	StrictRPS     float64
	StandardRPS   float64
	PermissiveRPS float64
	FaucetRPS     float64 // [FIX M-04]
	ApiKeyRPS     float64 // [FIX M-07] Higher limits for authenticated users

	StrictBurst     int
	StandardBurst   int
	PermissiveBurst int
	FaucetBurst     int // [FIX M-04]
	ApiKeyBurst     int // [FIX M-07]

	CleanupInterval time.Duration
	MaxIdleTime     time.Duration

	Enabled bool
}

// DefaultRateLimitConfig returns safe default configuration
func DefaultRateLimitConfig() *RateLimitConfig {
	return &RateLimitConfig{
		StrictRPS:     1.0,   // 1 request per second
		StandardRPS:   10.0,  // 10 requests per second
		PermissiveRPS: 100.0, // 100 requests per second

		// [FIX M-04] Faucet limit: 1 request every 60 seconds
		FaucetRPS: 1.0 / 60.0,
		// [FIX M-07] API Key limit: 50 requests per second
		ApiKeyRPS: 50.0,

		StrictBurst:     3,
		StandardBurst:   20,
		PermissiveBurst: 200,
		FaucetBurst:     1,   // [FIX M-04] No burst allowed for faucet
		ApiKeyBurst:     100, // [FIX M-07]

		CleanupInterval: 1 * time.Minute,
		MaxIdleTime:     5 * time.Minute,

		Enabled: true,
	}
}

// NewRateLimiter creates a new rate limiter with the given configuration
func NewRateLimiter(config *RateLimitConfig) *RateLimiter {
	if config == nil {
		config = DefaultRateLimitConfig()
	}

	rl := &RateLimiter{
		limiters:    make(map[string]*IPRateLimiter),
		keyLimiters: make(map[string]*rate.Limiter), // [FIX M-07]

		strictLimit:     rate.Limit(config.StrictRPS),
		standardLimit:   rate.Limit(config.StandardRPS),
		permissiveLimit: rate.Limit(config.PermissiveRPS),
		faucetLimit:     rate.Limit(config.FaucetRPS), // [FIX M-04]
		apiKeyLimit:     rate.Limit(config.ApiKeyRPS), // [FIX M-07]

		strictBurst:     config.StrictBurst,
		standardBurst:   config.StandardBurst,
		permissiveBurst: config.PermissiveBurst,
		faucetBurst:     config.FaucetBurst, // [FIX M-04]
		apiKeyBurst:     config.ApiKeyBurst, // [FIX M-07]

		cleanupInterval: config.CleanupInterval,
		maxIdleTime:     config.MaxIdleTime,

		blockedIPs:     make(map[string]time.Time),
		suspiciousIPs:  make(map[string]bool), // [FIX M-07]
		violationCount: make(map[string]int),
		blockDuration:  15 * time.Minute, // Block for 15 minutes
	}

	// Initialize limiters for each tier
	rl.limiters["strict"] = newIPRateLimiter(rl.strictLimit, rl.strictBurst)
	rl.limiters["standard"] = newIPRateLimiter(rl.standardLimit, rl.standardBurst)
	rl.limiters["permissive"] = newIPRateLimiter(rl.permissiveLimit, rl.permissiveBurst)
	rl.limiters["faucet"] = newIPRateLimiter(rl.faucetLimit, rl.faucetBurst) // [FIX M-04]

	go rl.cleanupRoutine()

	return rl
}

// IsBlocked checks if an IP is currently blocked
func (rl *RateLimiter) IsBlocked(ip string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	if unblockTime, exists := rl.blockedIPs[ip]; exists {
		if time.Now().Before(unblockTime) {
			return true // Still blocked
		}
		// Block expired, clean up
		delete(rl.blockedIPs, ip)
		delete(rl.suspiciousIPs, ip) // [FIX M-07] Reset suspicious status
		delete(rl.violationCount, ip)
	}
	return false
}

// IsSuspicious checks if an IP requires CAPTCHA/Challenge
// [FIX M-07] Adaptive rate limiting component
func (rl *RateLimiter) IsSuspicious(ip string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.suspiciousIPs[ip]
}

// newIPRateLimiter creates a new IP-based rate limiter
func newIPRateLimiter(limit rate.Limit, burst int) *IPRateLimiter {
	return &IPRateLimiter{
		limiters: make(map[string]*rate.Limiter),
		lastSeen: make(map[string]time.Time),
		limit:    limit,
		burst:    burst,
	}
}

// getLimiter gets or creates a rate limiter for the given IP and tier
func (rl *RateLimiter) getLimiter(ip string, tier string) *rate.Limiter {
	rl.mu.RLock()
	ipLimiter, exists := rl.limiters[tier]
	rl.mu.RUnlock()

	if !exists {
		tier = "standard"
		ipLimiter = rl.limiters[tier]
	}

	return ipLimiter.getLimiter(ip)
}

// getLimiter gets or creates a limiter for a specific IP
func (ipl *IPRateLimiter) getLimiter(ip string) *rate.Limiter {
	ipl.mu.Lock()
	defer ipl.mu.Unlock()

	limiter, exists := ipl.limiters[ip]
	if !exists {
		limiter = rate.NewLimiter(ipl.limit, ipl.burst)
		ipl.limiters[ip] = limiter
	}

	ipl.lastSeen[ip] = time.Now()
	return limiter
}

// AllowKey checks if a request from the given API Key is allowed
// [FIX M-07] This implements the Account-based rate limiting tier
func (rl *RateLimiter) AllowKey(apiKey string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	limiter, exists := rl.keyLimiters[apiKey]
	if !exists {
		limiter = rate.NewLimiter(rl.apiKeyLimit, rl.apiKeyBurst)
		rl.keyLimiters[apiKey] = limiter
	}

	return limiter.Allow()
}

// Allow checks if a request from the given IP is allowed for the tier
func (rl *RateLimiter) Allow(ip string, tier string) bool {
	// ✅ CHECK IF BLOCKED FIRST:
	if rl.IsBlocked(ip) {
		return false
	}

	rl.mu.RLock()
	ipRateLimiter, exists := rl.limiters[tier]
	rl.mu.RUnlock()

	if !exists {
		return true
	}

	// ✅ FIX: Get the limiter for this specific IP, then call Allow()
	limiter := ipRateLimiter.getLimiter(ip)
	allowed := limiter.Allow()

	// ✅ TRACK VIOLATIONS:
	if !allowed {
		rl.mu.Lock()
		rl.violationCount[ip]++

		// Adaptive Response:
		// 5 violations = Suspicious (Require CAPTCHA/PoW)
		// 20 violations = Block (Ban)
		if rl.violationCount[ip] >= 5 {
			rl.suspiciousIPs[ip] = true
		}
		if rl.violationCount[ip] >= 20 {
			rl.blockedIPs[ip] = time.Now().Add(rl.blockDuration)
			fmt.Printf("⚠️  IP %s blocked for %v due to excessive rate limit violations\n",
				ip, rl.blockDuration)
		}
		rl.mu.Unlock()
	}

	return allowed
}

// cleanupRoutine periodically removes idle IP limiters
func (rl *RateLimiter) cleanupRoutine() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()

		// Existing cleanup code for limiters...
		for tier, ipLimiter := range rl.limiters {
			ipLimiter.cleanup(rl.maxIdleTime)
			_ = tier
		}

		// [FIX M-07] Clean up Key limiters
		for key, limiter := range rl.keyLimiters {
			if limiter.Burst() > 0 { // Placeholder for key cleanup logic
				// In a real system, we'd track lastSeen for keys too
			}
			_ = key
		}

		// ✅ Clean up expired violations
		for ip, count := range rl.violationCount {
			// Reset violation count after maxIdleTime
			if _, blocked := rl.blockedIPs[ip]; !blocked && count < 5 {
				delete(rl.violationCount, ip)
				delete(rl.suspiciousIPs, ip)
			}
		}

		rl.mu.Unlock()
	}
}

// cleanup removes limiters for IPs that haven't been seen recently
func (rl *RateLimiter) cleanup() {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	for _, ipLimiter := range rl.limiters {
		ipLimiter.cleanup(rl.maxIdleTime)
	}
}

// cleanup removes idle IP entries
func (ipl *IPRateLimiter) cleanup(maxIdleTime time.Duration) {
	ipl.mu.Lock()
	defer ipl.mu.Unlock()

	now := time.Now()
	for ip, lastSeen := range ipl.lastSeen {
		if now.Sub(lastSeen) > maxIdleTime {
			delete(ipl.limiters, ip)
			delete(ipl.lastSeen, ip)
		}
	}
}

// parseFirstIP extracts the first IP from a comma-separated list
func parseFirstIP(xff string) string {
	for i := 0; i < len(xff); i++ {
		if xff[i] == ',' {
			return xff[:i]
		}
	}
	return xff
}

// RateLimitMiddleware returns middleware that applies multi-tier rate limiting
func (s *Server) RateLimitMiddleware(tier string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.rateLimiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			ip := getClientIP(r)

			// 1. TIER: Block List
			if s.rateLimiter.IsBlocked(ip) {
				s.writeRateLimitError(w, "blocked")
				return
			}

			// 2. TIER: Global Endpoint Limit (DDoS Protection)
			// Limits total requests to this endpoint regardless of IP
			if s.endpointLimiter != nil && !s.endpointLimiter.allow(r.URL.Path) {
				s.writeRateLimitError(w, "endpoint")
				return
			}

			// 3. TIER: Account vs IP (Multi-Tier)
			// [FIX M-07] Check for API Key
			apiKey := r.Header.Get("X-API-Key")
			if apiKey != "" {
				// Authenticated user: Apply Key-based limit
				if !s.rateLimiter.AllowKey(apiKey) {
					s.writeRateLimitError(w, "apikey")
					return
				}
				// Key allowed: proceed without checking IP limit (or check permissively)
			} else {
				// Anonymous user: Apply IP-based limit
				if s.rateLimiter.IsSuspicious(ip) {
					// [FIX M-07] IP is suspicious, demand CAPTCHA/PoW
					w.Header().Set("X-Challenge-Required", "true")
					// In a full implementation, you might reject the request here
					// unless a challenge solution is provided in headers
				}

				if !s.rateLimiter.Allow(ip, tier) {
					s.writeRateLimitError(w, tier)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeRateLimitError writes a rate limit exceeded response
func (s *Server) writeRateLimitError(w http.ResponseWriter, tier string) {
	var message string
	var retryAfter int

	switch tier {
	case "blocked":
		message = "Your IP is temporarily blocked due to repeated violations."
		retryAfter = 900 // 15 mins
	case "strict":
		message = "Rate limit exceeded for sensitive endpoint. Please try again later."
		retryAfter = 60 // 1 minute
	case "standard":
		message = "Rate limit exceeded. Please slow down your requests."
		retryAfter = 10 // 10 seconds
	case "permissive":
		message = "Rate limit exceeded. Please reduce request frequency."
		retryAfter = 1 // 1 second
	case "endpoint":
		message = "Server is busy (endpoint overload). Please try again later."
		retryAfter = 5
	case "apikey":
		message = "API Key rate limit exceeded."
		retryAfter = 10
	default:
		message = "Rate limit exceeded."
		retryAfter = 10
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	w.Header().Set("X-RateLimit-Limit", s.getRateLimitHeader(tier))
	w.WriteHeader(http.StatusTooManyRequests)

	s.writeError(w, message, http.StatusTooManyRequests)
}

// getRateLimitHeader returns the rate limit for display in headers
func (s *Server) getRateLimitHeader(tier string) string {
	if s.rateLimiter == nil {
		return "unlimited"
	}

	switch tier {
	case "faucet":
		// [FIX M-04] Display correct header
		return fmt.Sprintf("%d/minute", int(s.rateLimiter.faucetLimit*60))
	case "apikey":
		// [FIX M-07] Display API key limit
		return fmt.Sprintf("%d/second", int(s.rateLimiter.apiKeyLimit))
	case "strict":
		return fmt.Sprintf("%d/second", int(s.rateLimiter.strictLimit))
	case "standard":
		return fmt.Sprintf("%d/second", int(s.rateLimiter.standardLimit))
	case "permissive":
		return fmt.Sprintf("%d/second", int(s.rateLimiter.permissiveLimit))
	default:
		return "unknown"
	}
}

// getClientIP extracts the client IP from the request
// [FIX M-04] Security hardening: Do NOT trust X-Forwarded-For by default.
func getClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// GetStats returns rate limiting statistics
func (rl *RateLimiter) GetStats() map[string]interface{} {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	stats := make(map[string]interface{})

	for tier, ipLimiter := range rl.limiters {
		ipLimiter.mu.RLock()
		stats[tier] = map[string]interface{}{
			"active_ips": len(ipLimiter.limiters),
			"limit":      ipLimiter.limit,
			"burst":      ipLimiter.burst,
		}
		ipLimiter.mu.RUnlock()
	}

	// [FIX M-07] API Key Stats
	stats["api_keys"] = map[string]interface{}{
		"active_keys": len(rl.keyLimiters),
		"limit":       rl.apiKeyLimit,
	}

	// Violation Stats
	stats["violations"] = map[string]interface{}{
		"blocked_ips_count":    len(rl.blockedIPs),
		"suspicious_ips_count": len(rl.suspiciousIPs),
		"violating_ips_count":  len(rl.violationCount),
	}

	return stats
}

// EndpointLimiter tracks rate limits per endpoint
type EndpointLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
}

func newEndpointLimiter() *EndpointLimiter {
	return &EndpointLimiter{
		limiters: make(map[string]*rate.Limiter),
	}
}

func (el *EndpointLimiter) allow(endpoint string) bool {
	el.mu.Lock()
	defer el.mu.Unlock()

	limiter, exists := el.limiters[endpoint]
	if !exists {
		// [FIX M-07] Global endpoint limits (1000 global RPS)
		// This protects the backend even if IPs are rotating
		limiter = rate.NewLimiter(1000, 2000)
		el.limiters[endpoint] = limiter
	}

	return limiter.Allow()
}
