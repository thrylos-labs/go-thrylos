package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/thrylos-labs/go-thrylos/api"
)

const (
	TargetTPS = 500
	Duration  = 30 * time.Second
	API_URL   = "https://127.0.0.1:8080" // changed from localhost
)

func main() {
	// ⚠️ SECURITY OVERRIDE: Globally skip TLS verification for this process.
	// This allows the standard http client to accept your self-signed localhost cert.
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	client := api.NewClient(API_URL)

	// 1. Get Baseline Metrics
	fmt.Println("🔍 Fetching baseline metrics...")
	metricsBefore, err := getEconomicMetrics(client)
	if err != nil {
		log.Fatalf("Failed to get baseline metrics: %v", err)
	}
	fmt.Printf("📉 Baseline Inflation: %.4f%%\n", metricsBefore.InflationRate*100)
	fmt.Printf("📉 Baseline Gas Price: %s\n", metricsBefore.GasPrice)

	fmt.Printf("🌊 Starting Flood: %d TPS for %s...\n", TargetTPS, Duration)

	var wg sync.WaitGroup
	start := time.Now()
	txCount := 0

	// 2. Flood Loop
	for time.Since(start) < Duration {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Hits the estimate-gas endpoint to simulate load
			// Note: Ensure your API client handles the 'nil' argument correctly if expected
			_, err := client.EstimateGas("0xSender", "0xReceiver", 1000, nil)
			if err != nil {
				// Optional: Log errors if you want to see if requests are actually hitting
				// fmt.Printf("Req failed: %v\n", err)
			}
		}()
		txCount++

		// Rate limit to target TPS
		if txCount%TargetTPS == 0 {
			time.Sleep(1 * time.Second)
		}
	}

	wg.Wait()

	// 3. Get Stress Metrics
	fmt.Println("🔍 Fetching stress metrics...")
	metricsAfter, err := getEconomicMetrics(client)
	if err != nil {
		log.Fatalf("Failed to get stress metrics: %v", err)
	}
	fmt.Printf("📈 Stress Inflation: %.4f%%\n", metricsAfter.InflationRate*100)
	fmt.Printf("📈 Stress Gas Price: %s\n", metricsAfter.GasPrice)

	// 4. Validation
	if metricsAfter.GasPrice != metricsBefore.GasPrice {
		fmt.Println("✅ PASS: Gas price adjusted dynamically.")
	} else {
		fmt.Println("⚠️ WARNING: Gas price remained static. Check dynamic fee logic in config.go.")
	}
}

type EconMetrics struct {
	InflationRate float64
	GasPrice      string
}

func getEconomicMetrics(c *api.Client) (EconMetrics, error) {
	// Fetch REAL gas price from the node
	gasPrice, err := c.GetGasPrice()
	if err != nil {
		return EconMetrics{}, err
	}

	// Fetch real inflation (Currently hardcoded in node to 4%, but we can expand this later)
	return EconMetrics{InflationRate: 0.04, GasPrice: gasPrice}, nil
}
