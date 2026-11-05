// Package prometheus provides client functionality for querying Prometheus/Thanos
// metrics endpoints. It handles HTTP client configuration with bearer token
// authentication, TLS settings, and executes PromQL queries with results
// stored via the database package.
package prometheus

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/config"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/database"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/output"

	"github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

const (
	// Instant queries are lightweight — 30 seconds is generous.
	instantQueryTimeout = 30 * time.Second
	// Range queries can scan hours of data; allow up to 10 minutes for the
	// Prometheus/Thanos HTTP call. Database storage runs separately without a timeout.
	rangeQueryTimeout = 10 * time.Minute
)

// setupPromClient creates and configures a Prometheus API client.
// prometheusURL may include a scheme (http:// or https://); if omitted, https:// is assumed.
// proxyURL, when non-empty, routes queries through the given proxy (http/https/socks5).
func setupPromClient(prometheusURL, bearerToken, proxyURL string, insecureTLS bool) (promv1.API, error) {
	address := prometheusURL
	if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
		address = "https://" + address
	}

	// TLSClientConfig is only relevant for HTTPS; for plain HTTP, Go ignores it.
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecureTLS},
	}
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
		}
		transport.Proxy = http.ProxyURL(u)
	}

	client, err := api.NewClient(api.Config{
		Address: address,
		RoundTripper: &tokenRoundTripper{
			Token: bearerToken,
			RT:    transport,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create prometheus client: %v", err)
	}

	return promv1.NewAPI(client), nil
}

// RunQueries executes all Prometheus queries and stores results in the given database.
// The caller is responsible for opening and closing the database connection.
func RunQueries(db *sql.DB, dbImpl database.Database, kpisToRun config.KPIs, flags config.InputFlags,
	sampleNumber int, totalSamples int, frequency time.Duration) error {

	// Get or create cluster in DB
	clusterID, err := dbImpl.GetOrCreateCluster(db, flags.ClusterName, flags.ClusterType)
	if err != nil {
		return fmt.Errorf("failed to get cluster ID: %v", err)
	}

	// Verify that no KPI has changed its category since the last run
	if err := dbImpl.ValidateCategoryConsistency(db, kpisToRun.Queries); err != nil {
		return fmt.Errorf("category consistency check failed: %w", err)
	}

	// Create Prometheus client
	v1api, err := setupPromClient(flags.ThanosURL, flags.BearerToken, flags.ProxyURL, flags.InsecureTLS)
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	var failedCount int
	for _, query := range kpisToRun.Queries {
		queryInfo := output.QueryInfo{
			QueryID:      query.ID,
			PromQuery:    query.PromQuery,
			Category:     query.Category,
			Frequency:    frequency,
			SampleNumber: sampleNumber,
			TotalSamples: totalSamples,
			QueryType:    query.GetEffectiveQueryType(),
		}
		if query.Range != nil {
			now := time.Now()
			if query.Range.Step != nil {
				queryInfo.Step = query.Range.Step.Duration
			}
			if query.Range.Since != nil {
				queryInfo.Since = query.Range.Since.Resolve(now)
			}
			if query.Range.Until != nil {
				queryInfo.Until = query.Range.Until.Resolve(now)
			} else {
				queryInfo.Until = now
			}
		}

		timeout := instantQueryTimeout
		if queryInfo.QueryType == "range" {
			timeout = rangeQueryTimeout
		}
		queryCtx, queryCancel := context.WithTimeout(context.Background(), timeout)

		if !executeQuery(queryCtx, v1api, db, dbImpl, clusterID, queryInfo) {
			failedCount++
		}
		queryCancel()
	}

	if failedCount > 0 {
		return fmt.Errorf("%d of %d queries failed", failedCount, len(kpisToRun.Queries))
	}

	return nil
}

// executeQuery executes a single Prometheus query, handles the result,
// and returns true if the query succeeded.
func executeQuery(ctx context.Context, v1api promv1.API, db *sql.DB, dbImpl database.Database, clusterID int64, info output.QueryInfo) bool {
	queryStart := time.Now()

	var (
		result   model.Value
		warnings promv1.Warnings
		err      error
	)

	log.Printf("[%s] Prometheus query starting", info.QueryID)

	if info.QueryType == "range" {
		queryRange := promv1.Range{
			Start: info.Since,
			End:   info.Until,
			Step:  info.Step,
		}
		result, warnings, err = v1api.QueryRange(ctx, info.PromQuery, queryRange)
	} else {
		result, warnings, err = v1api.Query(ctx, info.PromQuery, queryStart)
	}

	promDuration := time.Since(queryStart)
	log.Printf("[%s] Prometheus query completed in %s", info.QueryID, promDuration.Round(time.Millisecond))

	queryResult := output.QueryResult{
		Warnings: warnings,
	}

	if err != nil {
		queryResult.Success = false
		queryResult.Error = err
		output.PrintQueryResult(info, queryResult)
		if storeErr := dbImpl.IncrementQueryError(db, info.QueryID); storeErr != nil {
			fmt.Fprintf(os.Stderr, "Failed to increment error count: %v\n", storeErr)
		}
		return false
	}

	var nanCount int
	result, nanCount = filterNaNValues(result)

	if isEmptyResult(result) {
		queryResult.Success = true
		output.PrintQueryResult(info, queryResult)
		if nanCount > 0 {
			fmt.Printf("  Warning: all %d sample(s) were NaN — nothing stored\n", nanCount)
			log.Printf("[%s] All %d sample(s) were NaN, nothing stored: %s", info.QueryID, nanCount, info.PromQuery)
		} else {
			fmt.Printf("  Warning: query returned no data (metric may not exist on this cluster)\n")
			log.Printf("[%s] Query returned no data: %s", info.QueryID, info.PromQuery)
		}
		return true
	}

	log.Printf("[%s] Database write starting", info.QueryID)
	storeStart := time.Now()
	err = dbImpl.StoreQueryResults(db, clusterID, info.QueryID, info.Category, result)
	storeDuration := time.Since(storeStart)
	log.Printf("[%s] Database write completed in %s", info.QueryID, storeDuration.Round(time.Millisecond))

	if err != nil {
		queryResult.Success = false
		queryResult.Error = fmt.Errorf("failed to store: %v", err)
		output.PrintQueryResult(info, queryResult)
		return false
	}

	queryResult.Success = true
	output.PrintQueryResult(info, queryResult)

	totalDuration := time.Since(queryStart)
	log.Printf("[%s] Total: %s (query=%s, store=%s)",
		info.QueryID, totalDuration.Round(time.Millisecond),
		promDuration.Round(time.Millisecond), storeDuration.Round(time.Millisecond))

	if nanCount > 0 {
		fmt.Printf("  Note: skipped %d NaN value(s)\n", nanCount)
		log.Printf("[%s] Skipped %d NaN/Inf value(s): %s", info.QueryID, nanCount, info.PromQuery)
	}
	return true
}

// isEmptyResult checks whether a Prometheus query returned no data points.
func isEmptyResult(result model.Value) bool {
	switch v := result.(type) {
	case model.Vector:
		return len(v) == 0
	case model.Matrix:
		return len(v) == 0
	default:
		return false
	}
}

// filterNaNValues removes NaN and Inf samples from a Prometheus result,
// returning the cleaned result and the number of samples removed.
func filterNaNValues(result model.Value) (model.Value, int) {
	switch v := result.(type) {
	case model.Vector:
		filtered := make(model.Vector, 0, len(v))
		for _, sample := range v {
			if math.IsNaN(float64(sample.Value)) || math.IsInf(float64(sample.Value), 0) {
				continue
			}
			filtered = append(filtered, sample)
		}
		return filtered, len(v) - len(filtered)

	case model.Matrix:
		skipped := 0
		filtered := make(model.Matrix, 0, len(v))
		for _, stream := range v {
			cleanPairs := make([]model.SamplePair, 0, len(stream.Values))
			for _, pair := range stream.Values {
				if math.IsNaN(float64(pair.Value)) || math.IsInf(float64(pair.Value), 0) {
					skipped++
					continue
				}
				cleanPairs = append(cleanPairs, pair)
			}
			if len(cleanPairs) > 0 {
				filtered = append(filtered, &model.SampleStream{
					Metric: stream.Metric,
					Values: cleanPairs,
				})
			}
		}
		return filtered, skipped

	default:
		return result, 0
	}
}
