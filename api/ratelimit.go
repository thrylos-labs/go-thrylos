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
	mu       sync.RWMutex

	// Configuration
	strictLimit     rate.Limit
	standardLimit   rate.Limit
	permissiveLimit rate.Limit
	faucetLimit     rate.Limit // [FIX M-04] New tier for faucet

	// Burst allowances
	strictBurst     int
	standardBurst   int
	permissiveBurst int
	faucetBurst     int // [FIX M-04] New burst for faucet

	cleanupInterval time.Duration
	maxIdleTime     time.Duration
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

	StrictBurst     int
	StandardBurst   int
	PermissiveBurst int
	FaucetBurst     int // [FIX M-04]

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

		StrictBurst:     3,
		StandardBurst:   20,
		PermissiveBurst: 200,
		FaucetBurst:     1, // [FIX M-04] No burst allowed for faucet

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
		limiters: make(map[string]*IPRateLimiter),

		strictLimit:     rate.Limit(config.StrictRPS),
		standardLimit:   rate.Limit(config.StandardRPS),
		permissiveLimit: rate.Limit(config.PermissiveRPS),
		faucetLimit:     rate.Limit(config.FaucetRPS), // [FIX M-04]

		strictBurst:     config.StrictBurst,
		standardBurst:   config.StandardBurst,
		permissiveBurst: config.PermissiveBurst,
		faucetBurst:     config.FaucetBurst, // [FIX M-04]

		cleanupInterval: config.CleanupInterval,
		maxIdleTime:     config.MaxIdleTime,
	}

	// Initialize limiters for each tier
	rl.limiters["strict"] = newIPRateLimiter(rl.strictLimit, rl.strictBurst)
	rl.limiters["standard"] = newIPRateLimiter(rl.standardLimit, rl.standardBurst)
	rl.limiters["permissive"] = newIPRateLimiter(rl.permissiveLimit, rl.permissiveBurst)
	rl.limiters["faucet"] = newIPRateLimiter(rl.faucetLimit, rl.faucetBurst) // [FIX M-04]

	go rl.cleanupRoutine()

	return rl
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

// Allow checks if a request from the given IP is allowed for the tier
func (rl *RateLimiter) Allow(ip string, tier string) bool {
	limiter := rl.getLimiter(ip, tier)
	return limiter.Allow()
}

// cleanupRoutine periodically removes idle IP limiters
func (rl *RateLimiter) cleanupRoutine() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		rl.cleanup()
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

// RateLimitMiddleware returns middleware that applies rate limiting
func (s *Server) RateLimitMiddleware(tier string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip rate limiting if disabled
			if s.rateLimiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			ip := getClientIP(r)

			if !s.rateLimiter.Allow(ip, tier) {
				// Rate limit exceeded
				s.writeRateLimitError(w, tier)
				return
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
	case "strict":
		message = "Rate limit exceeded for sensitive endpoint. Please try again later."
		retryAfter = 60 // 1 minute
	case "standard":
		message = "Rate limit exceeded. Please slow down your requests."
		retryAfter = 10 // 10 seconds
	case "permissive":
		message = "Rate limit exceeded. Please reduce request frequency."
		retryAfter = 1 // 1 second
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
// Attackers can spoof headers to bypass rate limits.
// In a real production env behind a Load Balancer, you would enable a specific "TrustProxy" flag.
// For this secure default, we rely on RemoteAddr.
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

	return stats
}
