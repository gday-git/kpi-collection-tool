package prometheus

import (
	"context"
	"database/sql"
	"math"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/database"
	"github.com/redhat-best-practices-for-k8s/kpi-collection-tool/internal/output"
)

// mockPromAPI is a simple mock that implements only the Query method we actually use.
// All other methods return empty/nil values to satisfy the v1.API interface.
type mockPromAPI struct {
	queryFunc      func(ctx context.Context, query string, ts time.Time) (model.Value, v1.Warnings, error)
	queryRangeFunc func(ctx context.Context, query string, r v1.Range) (model.Value, v1.Warnings, error)
}

func (m *mockPromAPI) Query(ctx context.Context, query string, ts time.Time, opts ...v1.Option) (model.Value, v1.Warnings, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, query, ts)
	}
	return nil, nil, nil
}

// All methods below are unused but required to implement v1.API interface
func (m *mockPromAPI) QueryRange(ctx context.Context, query string, r v1.Range, opts ...v1.Option) (model.Value, v1.Warnings, error) {
	if m.queryRangeFunc != nil {
		return m.queryRangeFunc(ctx, query, r)
	}
	return nil, nil, nil
}
func (m *mockPromAPI) LabelNames(ctx context.Context, matches []string, startTime, endTime time.Time, opts ...v1.Option) ([]string, v1.Warnings, error) {
	return nil, nil, nil
}
func (m *mockPromAPI) LabelValues(ctx context.Context, label string, matches []string, startTime, endTime time.Time, opts ...v1.Option) (model.LabelValues, v1.Warnings, error) {
	return nil, nil, nil
}
func (m *mockPromAPI) Series(ctx context.Context, matches []string, startTime, endTime time.Time, opts ...v1.Option) ([]model.LabelSet, v1.Warnings, error) {
	return nil, nil, nil
}
func (m *mockPromAPI) Alerts(ctx context.Context) (v1.AlertsResult, error) {
	return v1.AlertsResult{}, nil
}
func (m *mockPromAPI) AlertManagers(ctx context.Context) (v1.AlertManagersResult, error) {
	return v1.AlertManagersResult{}, nil
}
func (m *mockPromAPI) CleanTombstones(ctx context.Context) error { return nil }
func (m *mockPromAPI) Config(ctx context.Context) (v1.ConfigResult, error) {
	return v1.ConfigResult{}, nil
}
func (m *mockPromAPI) DeleteSeries(ctx context.Context, matches []string, startTime, endTime time.Time) error {
	return nil
}
func (m *mockPromAPI) Flags(ctx context.Context) (v1.FlagsResult, error) {
	return v1.FlagsResult{}, nil
}
func (m *mockPromAPI) Snapshot(ctx context.Context, skipHead bool) (v1.SnapshotResult, error) {
	return v1.SnapshotResult{}, nil
}
func (m *mockPromAPI) Rules(ctx context.Context) (v1.RulesResult, error) {
	return v1.RulesResult{}, nil
}
func (m *mockPromAPI) Targets(ctx context.Context) (v1.TargetsResult, error) {
	return v1.TargetsResult{}, nil
}
func (m *mockPromAPI) TargetsMetadata(ctx context.Context, matchTarget, metric, limit string) ([]v1.MetricMetadata, error) {
	return nil, nil
}
func (m *mockPromAPI) Metadata(ctx context.Context, metric, limit string) (map[string][]v1.Metadata, error) {
	return nil, nil
}
func (m *mockPromAPI) TSDB(ctx context.Context, opts ...v1.Option) (v1.TSDBResult, error) {
	return v1.TSDBResult{}, nil
}
func (m *mockPromAPI) WalReplay(ctx context.Context) (v1.WalReplayStatus, error) {
	return v1.WalReplayStatus{}, nil
}
func (m *mockPromAPI) Runtimeinfo(ctx context.Context) (v1.RuntimeinfoResult, error) {
	return v1.RuntimeinfoResult{}, nil
}
func (m *mockPromAPI) Buildinfo(ctx context.Context) (v1.BuildinfoResult, error) {
	return v1.BuildinfoResult{}, nil
}
func (m *mockPromAPI) QueryExemplars(ctx context.Context, query string, startTime, endTime time.Time) ([]v1.ExemplarQueryResult, error) {
	return nil, nil
}

var _ = Describe("filterNaNValues", func() {
	Describe("Vector input", func() {
		It("should keep all samples when none are NaN or Inf", func() {
			input := model.Vector{
				&model.Sample{Metric: model.Metric{"__name__": "cpu"}, Value: 0.5, Timestamp: model.Now()},
				&model.Sample{Metric: model.Metric{"__name__": "cpu"}, Value: 1.0, Timestamp: model.Now()},
			}

			result, skipped := filterNaNValues(input)

			vec := result.(model.Vector)
			Expect(vec).To(HaveLen(2))
			Expect(skipped).To(Equal(0))
		})

		It("should remove NaN samples from vector", func() {
			input := model.Vector{
				&model.Sample{Metric: model.Metric{"job": "a"}, Value: 0.5},
				&model.Sample{Metric: model.Metric{"job": "b"}, Value: model.SampleValue(math.NaN())},
				&model.Sample{Metric: model.Metric{"job": "c"}, Value: 0.9},
			}

			result, skipped := filterNaNValues(input)

			vec := result.(model.Vector)
			Expect(vec).To(HaveLen(2))
			Expect(skipped).To(Equal(1))
			Expect(float64(vec[0].Value)).To(Equal(0.5))
			Expect(float64(vec[1].Value)).To(Equal(0.9))
		})

		It("should remove +Inf and -Inf samples from vector", func() {
			input := model.Vector{
				&model.Sample{Metric: model.Metric{"job": "a"}, Value: model.SampleValue(math.Inf(1))},
				&model.Sample{Metric: model.Metric{"job": "b"}, Value: 42},
				&model.Sample{Metric: model.Metric{"job": "c"}, Value: model.SampleValue(math.Inf(-1))},
			}

			result, skipped := filterNaNValues(input)

			vec := result.(model.Vector)
			Expect(vec).To(HaveLen(1))
			Expect(skipped).To(Equal(2))
			Expect(float64(vec[0].Value)).To(Equal(42.0))
		})

		It("should return empty vector and correct count when all values are NaN", func() {
			input := model.Vector{
				&model.Sample{Metric: model.Metric{"job": "a"}, Value: model.SampleValue(math.NaN())},
				&model.Sample{Metric: model.Metric{"job": "b"}, Value: model.SampleValue(math.NaN())},
			}

			result, skipped := filterNaNValues(input)

			vec := result.(model.Vector)
			Expect(vec).To(BeEmpty())
			Expect(skipped).To(Equal(2))
		})

		It("should handle an empty vector", func() {
			result, skipped := filterNaNValues(model.Vector{})

			vec := result.(model.Vector)
			Expect(vec).To(BeEmpty())
			Expect(skipped).To(Equal(0))
		})
	})

	Describe("Matrix input", func() {
		It("should keep all sample pairs when none are NaN or Inf", func() {
			input := model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{"__name__": "cpu", "instance": "node1"},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1000), Value: 0.1},
						{Timestamp: model.TimeFromUnix(1030), Value: 0.2},
					},
				},
			}

			result, skipped := filterNaNValues(input)

			mat := result.(model.Matrix)
			Expect(mat).To(HaveLen(1))
			Expect(mat[0].Values).To(HaveLen(2))
			Expect(skipped).To(Equal(0))
		})

		It("should remove NaN sample pairs and keep the stream", func() {
			input := model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{"cpu": "0"},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1000), Value: 0.5},
						{Timestamp: model.TimeFromUnix(1030), Value: model.SampleValue(math.NaN())},
						{Timestamp: model.TimeFromUnix(1060), Value: 0.7},
					},
				},
			}

			result, skipped := filterNaNValues(input)

			mat := result.(model.Matrix)
			Expect(mat).To(HaveLen(1))
			Expect(mat[0].Values).To(HaveLen(2))
			Expect(skipped).To(Equal(1))
		})

		It("should drop an entire stream when all its values are NaN/Inf", func() {
			input := model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{"cpu": "0"},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1000), Value: model.SampleValue(math.NaN())},
						{Timestamp: model.TimeFromUnix(1030), Value: model.SampleValue(math.Inf(1))},
					},
				},
				&model.SampleStream{
					Metric: model.Metric{"cpu": "1"},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1000), Value: 0.3},
					},
				},
			}

			result, skipped := filterNaNValues(input)

			mat := result.(model.Matrix)
			Expect(mat).To(HaveLen(1))
			Expect(string(mat[0].Metric["cpu"])).To(Equal("1"))
			Expect(skipped).To(Equal(2))
		})

		It("should return empty matrix when all streams are fully NaN", func() {
			input := model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{"cpu": "0"},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1000), Value: model.SampleValue(math.NaN())},
					},
				},
			}

			result, skipped := filterNaNValues(input)

			mat := result.(model.Matrix)
			Expect(mat).To(BeEmpty())
			Expect(skipped).To(Equal(1))
		})

		It("should handle an empty matrix", func() {
			result, skipped := filterNaNValues(model.Matrix{})

			mat := result.(model.Matrix)
			Expect(mat).To(BeEmpty())
			Expect(skipped).To(Equal(0))
		})
	})

})

var _ = Describe("Client", func() {

	// Test the setupPromClient function
	Describe("setupPromClient", func() {
		// Test successful client creation with valid parameters
		It("should create a Prometheus client with valid parameters", func() {
			// Valid Prometheus/Thanos URL and bearer token
			thanosURL := "thanos.example.com"
			bearerToken := "valid-token"
			insecureTLS := true

			// We setup the Prometheus client
			promClient, err := setupPromClient(thanosURL, bearerToken, "", insecureTLS)

			// The client should be created successfully
			Expect(err).NotTo(HaveOccurred())
			// The client should not be nil
			Expect(promClient).NotTo(BeNil())

		})

		// Test with empty bearer token (should still create client)
		It("should create client even with empty bearer token", func() {
			// Empty bearer token
			thanosURL := "thanos.example.com"
			bearerToken := ""

			// We create the client
			promClient, err := setupPromClient(thanosURL, bearerToken, "", true)

			// It should still succeed (auth might fail later, but client creation works)
			Expect(err).NotTo(HaveOccurred())
			Expect(promClient).NotTo(BeNil())
		})

		It("should accept a valid socks5 proxy URL", func() {
			promClient, err := setupPromClient("thanos.example.com", "token", "socks5://127.0.0.1:1337", true)
			Expect(err).NotTo(HaveOccurred())
			Expect(promClient).NotTo(BeNil())
		})

		It("should reject an invalid proxy URL", func() {
			promClient, err := setupPromClient("thanos.example.com", "token", "://bad", true)
			Expect(err).To(HaveOccurred())
			Expect(promClient).To(BeNil())
		})

		// Test with insecure TLS disabled
		It("should create client with insecure TLS disabled", func() {
			// insecureTLS set to false
			thanosURL := "thanos.example.com"
			bearerToken := "token"
			insecureTLS := false

			// We create the client
			promClient, err := setupPromClient(thanosURL, bearerToken, "", insecureTLS)

			// It should succeed
			Expect(err).NotTo(HaveOccurred())
			Expect(promClient).NotTo(BeNil())
		})
	})

	// Test executeQuery with mock Prometheus client
	Describe("executeQuery", func() {
		var (
			testDB    *sql.DB
			sqliteDB  database.Database
			clusterID int64
			tmpDir    string
			originCwd string
		)

		BeforeEach(func() {
			var err error
			tmpDir, err = os.MkdirTemp("", "prom-test-*")
			Expect(err).NotTo(HaveOccurred())

			database.OutputDir = database.DefaultOutputDir

			// Change to temp directory so InitDB creates the artifact dir there
			originCwd, err = os.Getwd()
			Expect(err).NotTo(HaveOccurred())
			err = os.Chdir(tmpDir)
			Expect(err).NotTo(HaveOccurred())

			sqliteDB = database.NewSQLiteDB()
			testDB, err = sqliteDB.InitDB()
			Expect(err).NotTo(HaveOccurred())

			clusterID, err = sqliteDB.GetOrCreateCluster(testDB, "test-cluster", "")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			if testDB != nil {
				err := testDB.Close()
				Expect(err).NotTo(HaveOccurred())
			}
			database.OutputDir = database.DefaultOutputDir
			if originCwd != "" {
				err := os.Chdir(originCwd)
				Expect(err).NotTo(HaveOccurred())
			}
			if tmpDir != "" {
				err := os.RemoveAll(tmpDir)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		// Test executeQuery with successful response
		It("should successfully execute query and store results with vector response", func() {
			// A mock client that returns a vector result (no actual server)
			mock := &mockPromAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time) (model.Value, v1.Warnings, error) {
					return model.Vector{
						&model.Sample{
							Metric:    model.Metric{"__name__": "up", "job": "prometheus"},
							Value:     1,
							Timestamp: model.Now(),
						},
					}, nil, nil
				},
			}

			// We execute a query with the mock client
			ctx := context.Background()
			info := output.QueryInfo{QueryID: "test-query-1", PromQuery: "up", Frequency: 5 * time.Second, SampleNumber: 1, TotalSamples: 4}
			executeQuery(ctx, mock, testDB, sqliteDB, clusterID, info)

			// Results should be stored in the database
			var count int
			err := testDB.QueryRow("SELECT COUNT(*) FROM query_results WHERE kpi_id = ?", "test-query-1").Scan(&count)
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(BeNumerically(">", 0))
		})

		// Test executeQuery with multiple metrics in response
		It("should successfully execute query and store results with multiple metrics", func() {
			// A mock client that returns multiple metrics (no actual server)
			mock := &mockPromAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time) (model.Value, v1.Warnings, error) {
					return model.Vector{
						&model.Sample{Metric: model.Metric{"__name__": "cpu_usage", "pod": "test-pod-1"}, Value: 0.5},
						&model.Sample{Metric: model.Metric{"__name__": "cpu_usage", "pod": "test-pod-2"}, Value: 0.7},
						&model.Sample{Metric: model.Metric{"__name__": "cpu_usage", "pod": "test-pod-3"}, Value: 0.9},
					}, nil, nil
				},
			}

			ctx := context.Background()
			info := output.QueryInfo{QueryID: "test-query-2", PromQuery: "cpu_usage", Frequency: 5 * time.Second, SampleNumber: 1, TotalSamples: 4}
			executeQuery(ctx, mock, testDB, sqliteDB, clusterID, info)

			// Multiple results should be stored
			var count int
			err := testDB.QueryRow("SELECT COUNT(*) FROM query_results WHERE kpi_id = ?", "test-query-2").Scan(&count)
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(3))
		})

		// Test executeQuery with query error
		It("should increment error count when query fails", func() {
			// A mock client that returns an error (no actual server)
			mock := &mockPromAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time) (model.Value, v1.Warnings, error) {
					return nil, nil, &v1.Error{Type: v1.ErrBadData, Msg: "invalid query syntax"}
				},
			}

			ctx := context.Background()
			queryID := "test-query-error"
			info := output.QueryInfo{QueryID: queryID, PromQuery: "invalid{query", Frequency: 5 * time.Second, SampleNumber: 1, TotalSamples: 4}
			executeQuery(ctx, mock, testDB, sqliteDB, clusterID, info)

			// Error count should be incremented in database
			var errorCount int
			err := testDB.QueryRow("SELECT errors FROM query_errors WHERE kpi_id = ?", queryID).Scan(&errorCount)
			Expect(err).NotTo(HaveOccurred())
			Expect(errorCount).To(Equal(1))
		})

		// Test executeQuery with warnings
		It("should handle Prometheus warnings", func() {
			// A mock client that returns warnings (no actual server)
			mock := &mockPromAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time) (model.Value, v1.Warnings, error) {
					return model.Vector{&model.Sample{Metric: model.Metric{"__name__": "test"}, Value: 42}},
						v1.Warnings{"query took longer", "metrics dropped"},
						nil
				},
			}

			ctx := context.Background()
			info := output.QueryInfo{QueryID: "test-query-warnings", PromQuery: "test", Frequency: 5 * time.Second, SampleNumber: 1, TotalSamples: 4}
			executeQuery(ctx, mock, testDB, sqliteDB, clusterID, info)
		})

		// Test executeQuery with empty results
		It("should handle empty query results", func() {
			// A mock client that returns empty results (no actual server)
			mock := &mockPromAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time) (model.Value, v1.Warnings, error) {
					return model.Vector{}, nil, nil
				},
			}

			ctx := context.Background()
			info := output.QueryInfo{QueryID: "test-query-empty", PromQuery: "nonexistent_metric", Frequency: 5 * time.Second, SampleNumber: 1, TotalSamples: 4}
			executeQuery(ctx, mock, testDB, sqliteDB, clusterID, info)

			// No results should be stored
			var count int
			err := testDB.QueryRow("SELECT COUNT(*) FROM query_results WHERE kpi_id = ?", "test-query-empty").Scan(&count)
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(0))
		})

		// Test executeQuery with context timeout
		It("should respect context timeout", func() {
			// A mock client that simulates slow query (no actual server)
			mock := &mockPromAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time) (model.Value, v1.Warnings, error) {
					select {
					case <-ctx.Done():
						return nil, nil, ctx.Err()
					case <-time.After(2 * time.Second):
						return model.Vector{}, nil, nil
					}
				},
			}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			info := output.QueryInfo{QueryID: "test-query-timeout", PromQuery: "slow_query", Frequency: 5 * time.Second, SampleNumber: 1, TotalSamples: 4}
			executeQuery(ctx, mock, testDB, sqliteDB, clusterID, info)
		})

		It("should execute range queries and flatten matrix results", func() {
			now := time.Now()
			mock := &mockPromAPI{
				queryRangeFunc: func(ctx context.Context, query string, r v1.Range) (model.Value, v1.Warnings, error) {
					Expect(query).To(Equal("rate(node_cpu_seconds_total[5m])"))
					Expect(r.Step).To(Equal(30 * time.Second))
					Expect(r.End.Sub(r.Start)).To(Equal(time.Hour))
					Expect(r.End).To(BeTemporally("~", now, 2*time.Second))

					return model.Matrix{
						&model.SampleStream{
							Metric: model.Metric{"__name__": "node_cpu_seconds_total", "cpu": "0"},
							Values: []model.SamplePair{
								{Timestamp: model.TimeFromUnix(1700000000), Value: model.SampleValue(0.15)},
								{Timestamp: model.TimeFromUnix(1700000030), Value: model.SampleValue(0.21)},
							},
						},
						&model.SampleStream{
							Metric: model.Metric{"__name__": "node_cpu_seconds_total", "cpu": "1"},
							Values: []model.SamplePair{
								{Timestamp: model.TimeFromUnix(1700000000), Value: model.SampleValue(0.12)},
							},
						},
					}, nil, nil
				},
			}

			info := output.QueryInfo{
				QueryID:      "test-query-range",
				PromQuery:    "rate(node_cpu_seconds_total[5m])",
				Frequency:    5 * time.Second,
				SampleNumber: 1,
				TotalSamples: 4,
				QueryType:    "range",
				Step:         30 * time.Second,
				Since:        now.Add(-time.Hour),
				Until:        now,
			}
			executeQuery(context.Background(), mock, testDB, sqliteDB, clusterID, info)

			var count int
			err := testDB.QueryRow("SELECT COUNT(*) FROM query_results WHERE kpi_id = ?", "test-query-range").Scan(&count)
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(3))
		})

		It("should deduplicate overlapping range query results", func() {
			matrixData := model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{"__name__": "cpu", "instance": "node1"},
					Values: []model.SamplePair{
						{Timestamp: model.TimeFromUnix(1700000000), Value: model.SampleValue(0.15)},
						{Timestamp: model.TimeFromUnix(1700000030), Value: model.SampleValue(0.21)},
					},
				},
			}

			mock := &mockPromAPI{
				queryRangeFunc: func(ctx context.Context, query string, r v1.Range) (model.Value, v1.Warnings, error) {
					return matrixData, nil, nil
				},
			}

			now := time.Now()
			info := output.QueryInfo{
				QueryID:      "test-query-dedup",
				PromQuery:    "rate(cpu[5m])",
				Frequency:    5 * time.Second,
				SampleNumber: 1,
				TotalSamples: 4,
				QueryType:    "range",
				Step:         30 * time.Second,
				Since:        now.Add(-time.Hour),
				Until:        now,
			}

			executeQuery(context.Background(), mock, testDB, sqliteDB, clusterID, info)
			executeQuery(context.Background(), mock, testDB, sqliteDB, clusterID, info)

			var count int
			err := testDB.QueryRow("SELECT COUNT(*) FROM query_results WHERE kpi_id = ?", "test-query-dedup").Scan(&count)
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(2), "duplicate data points should be silently skipped")
		})

		It("should increment errors when range query fails", func() {
			mock := &mockPromAPI{
				queryRangeFunc: func(ctx context.Context, query string, r v1.Range) (model.Value, v1.Warnings, error) {
					return nil, nil, &v1.Error{Type: v1.ErrBadData, Msg: "range query error"}
				},
			}

			now := time.Now()
			queryID := "test-query-range-error"
			info := output.QueryInfo{
				QueryID:      queryID,
				PromQuery:    "rate(node_cpu_seconds_total[5m])",
				Frequency:    5 * time.Second,
				SampleNumber: 1,
				TotalSamples: 4,
				QueryType:    "range",
				Step:         30 * time.Second,
				Since:        now.Add(-time.Hour),
				Until:        now,
			}
			executeQuery(context.Background(), mock, testDB, sqliteDB, clusterID, info)

			var errorCount int
			err := testDB.QueryRow("SELECT errors FROM query_errors WHERE kpi_id = ?", queryID).Scan(&errorCount)
			Expect(err).NotTo(HaveOccurred())
			Expect(errorCount).To(Equal(1))
		})

		It("should execute range queries with absolute since/until timestamps", func() {
			since := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
			until := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)

			mock := &mockPromAPI{
				queryRangeFunc: func(ctx context.Context, query string, r v1.Range) (model.Value, v1.Warnings, error) {
					Expect(r.Start).To(BeTemporally("~", since, time.Second))
					Expect(r.End).To(BeTemporally("~", until, time.Second))
					Expect(r.Step).To(Equal(time.Minute))

					return model.Matrix{
						&model.SampleStream{
							Metric: model.Metric{"__name__": "cpu"},
							Values: []model.SamplePair{
								{Timestamp: model.TimeFromUnix(since.Unix()), Value: model.SampleValue(0.5)},
							},
						},
					}, nil, nil
				},
			}

			info := output.QueryInfo{
				QueryID:      "test-query-since-until",
				PromQuery:    "rate(node_cpu_seconds_total[5m])",
				Frequency:    5 * time.Second,
				SampleNumber: 1,
				TotalSamples: 4,
				QueryType:    "range",
				Step:         time.Minute,
				Since:        since,
				Until:        until,
			}
			executeQuery(context.Background(), mock, testDB, sqliteDB, clusterID, info)

			var count int
			err := testDB.QueryRow("SELECT COUNT(*) FROM query_results WHERE kpi_id = ?", "test-query-since-until").Scan(&count)
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(1))
		})

		It("should default to instant query when query type is not provided", func() {
			queryCalled := false
			queryRangeCalled := false

			mock := &mockPromAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time) (model.Value, v1.Warnings, error) {
					queryCalled = true
					return model.Vector{
						&model.Sample{
							Metric:    model.Metric{"__name__": "up", "job": "prometheus"},
							Value:     1,
							Timestamp: model.Now(),
						},
					}, nil, nil
				},
				queryRangeFunc: func(ctx context.Context, query string, r v1.Range) (model.Value, v1.Warnings, error) {
					queryRangeCalled = true
					return model.Matrix{}, nil, nil
				},
			}

			info := output.QueryInfo{
				QueryID:      "test-query-default-instant",
				PromQuery:    "up",
				Frequency:    5 * time.Second,
				SampleNumber: 1,
				TotalSamples: 4,
			}
			executeQuery(context.Background(), mock, testDB, sqliteDB, clusterID, info)

			Expect(queryCalled).To(BeTrue())
			Expect(queryRangeCalled).To(BeFalse())
		})
	})

})
