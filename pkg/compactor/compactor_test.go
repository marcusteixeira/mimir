// SPDX-License-Identifier: AGPL-3.0-only
// Provenance-includes-location: https://github.com/cortexproject/cortex/blob/master/pkg/compactor/compactor_test.go
// Provenance-includes-license: Apache-2.0
// Provenance-includes-copyright: The Cortex Authors.

package compactor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/grafana/dskit/concurrency"
	"github.com/grafana/dskit/flagext"
	"github.com/grafana/dskit/kv/consul"
	"github.com/grafana/dskit/ring"
	"github.com/grafana/dskit/services"
	"github.com/grafana/dskit/test"
	"github.com/grafana/regexp"
	"github.com/oklog/ulid/v2"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	prom_testutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/chunks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/thanos-io/objstore"
	"gopkg.in/yaml.v3"

	"github.com/grafana/mimir/pkg/storage/bucket"
	"github.com/grafana/mimir/pkg/storage/bucket/filesystem"
	mimir_tsdb "github.com/grafana/mimir/pkg/storage/tsdb"
	"github.com/grafana/mimir/pkg/storage/tsdb/block"
	testutil "github.com/grafana/mimir/pkg/util/test"
	"github.com/grafana/mimir/pkg/util/validation"
)

func TestConfig_ShouldSupportYamlConfig(t *testing.T) {
	yamlCfg := `
block_ranges: [2h, 48h]
block_sync_concurrency: 123
data_dir: /tmp
compaction_interval: 15m
compaction_retries: 123
`

	cfg := Config{}
	flagext.DefaultValues(&cfg)
	assert.NoError(t, yaml.Unmarshal([]byte(yamlCfg), &cfg))
	assert.Equal(t, mimir_tsdb.DurationList{2 * time.Hour, 48 * time.Hour}, cfg.BlockRanges)
	assert.Equal(t, 123, cfg.BlockSyncConcurrency)
	assert.Equal(t, "/tmp", cfg.DataDir)
	assert.Equal(t, 15*time.Minute, cfg.CompactionInterval)
	assert.Equal(t, 123, cfg.CompactionRetries)
}

func TestConfig_ShouldSupportCliFlags(t *testing.T) {
	fs := flag.NewFlagSet("", flag.PanicOnError)
	cfg := Config{}
	cfg.RegisterFlags(fs, log.NewNopLogger())
	require.NoError(t, fs.Parse([]string{
		"-compactor.block-ranges=2h,48h",
		"-compactor.block-sync-concurrency=123",
		"-compactor.data-dir=/tmp",
		"-compactor.compaction-interval=15m",
		"-compactor.compaction-retries=123",
	}))

	assert.Equal(t, mimir_tsdb.DurationList{2 * time.Hour, 48 * time.Hour}, cfg.BlockRanges)
	assert.Equal(t, 123, cfg.BlockSyncConcurrency)
	assert.Equal(t, "/tmp", cfg.DataDir)
	assert.Equal(t, 15*time.Minute, cfg.CompactionInterval)
	assert.Equal(t, 123, cfg.CompactionRetries)
}

func TestConfig_Validate(t *testing.T) {
	tests := map[string]struct {
		setup    func(cfg *Config)
		expected string
	}{
		"should pass with the default config": {
			setup:    func(*Config) {},
			expected: "",
		},
		"should pass with only 1 block range period": {
			setup: func(cfg *Config) {
				cfg.BlockRanges = mimir_tsdb.DurationList{time.Hour}
			},
			expected: "",
		},
		"should fail with non divisible block range periods": {
			setup: func(cfg *Config) {
				cfg.BlockRanges = mimir_tsdb.DurationList{2 * time.Hour, 12 * time.Hour, 24 * time.Hour, 30 * time.Hour}
			},
			expected: errors.Errorf(errInvalidBlockRanges, 30*time.Hour, 24*time.Hour).Error(),
		},
		"should fail on unknown compaction jobs order": {
			setup: func(cfg *Config) {
				cfg.CompactionJobsOrder = "everything-is-important"
			},
			expected: errInvalidCompactionOrder.Error(),
		},
		"should fail on invalid value of max-opening-blocks-concurrency": {
			setup:    func(cfg *Config) { cfg.MaxOpeningBlocksConcurrency = 0 },
			expected: errInvalidMaxOpeningBlocksConcurrency.Error(),
		},
		"should fail on invalid value of max-closing-blocks-concurrency": {
			setup:    func(cfg *Config) { cfg.MaxClosingBlocksConcurrency = 0 },
			expected: errInvalidMaxClosingBlocksConcurrency.Error(),
		},
		"should fail on invalid value of symbols-flushers-concurrency": {
			setup:    func(cfg *Config) { cfg.SymbolsFlushersConcurrency = 0 },
			expected: errInvalidSymbolFlushersConcurrency.Error(),
		},
	}

	for testName, testData := range tests {
		t.Run(testName, func(t *testing.T) {
			logger := testutil.NewTestingLogger(t)
			cfg := &Config{}
			flagext.DefaultValues(cfg)
			testData.setup(cfg)

			if actualErr := cfg.Validate(logger); testData.expected != "" {
				assert.EqualError(t, actualErr, testData.expected)
			} else {
				assert.NoError(t, actualErr)
			}
		})
	}
}

func TestMultitenantCompactor_ShouldDoNothingOnNoUserBlocks(t *testing.T) {
	t.Parallel()

	// No user blocks stored in the bucket.
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", []string{}, nil)
	cfg := prepareConfig(t)
	c, _, _, logs, registry := prepare(t, cfg, bucketClient)
	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Compactor doesn't wait for blocks cleaner to finish, but our test checks for cleaner metrics.
	require.NoError(t, c.blocksCleaner.AwaitRunning(context.Background()))

	// Wait until a run has completed.
	test.Poll(t, time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsCompleted)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	assert.Equal(t, prom_testutil.ToFloat64(c.compactionRunInterval), cfg.CompactionInterval.Seconds())

	assert.Equal(t, []string{
		`level=info component=compactor msg="waiting until compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="discovering users from bucket"`,
		`level=info component=compactor msg="discovered users from bucket" users=0`,
	}, removeIgnoredLogs(strings.Split(strings.TrimSpace(logs.String()), "\n")))

	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# TYPE cortex_compactor_runs_started_total counter
		# HELP cortex_compactor_runs_started_total Total number of compaction runs started.
		cortex_compactor_runs_started_total 1

		# TYPE cortex_compactor_runs_completed_total counter
		# HELP cortex_compactor_runs_completed_total Total number of compaction runs successfully completed.
		cortex_compactor_runs_completed_total 1

		# TYPE cortex_compactor_runs_failed_total counter
		# HELP cortex_compactor_runs_failed_total Total number of compaction runs failed.
		cortex_compactor_runs_failed_total{reason="error"} 0
		cortex_compactor_runs_failed_total{reason="shutdown"} 0

		# HELP cortex_compactor_garbage_collection_duration_seconds Time it took to perform garbage collection iteration.
		# TYPE cortex_compactor_garbage_collection_duration_seconds histogram
		cortex_compactor_garbage_collection_duration_seconds_bucket{le="+Inf"} 0
		cortex_compactor_garbage_collection_duration_seconds_sum 0
		cortex_compactor_garbage_collection_duration_seconds_count 0

		# HELP cortex_compactor_garbage_collection_failures_total Total number of failed garbage collection operations.
		# TYPE cortex_compactor_garbage_collection_failures_total counter
		cortex_compactor_garbage_collection_failures_total 0

		# HELP cortex_compactor_garbage_collection_total Total number of garbage collection operations.
		# TYPE cortex_compactor_garbage_collection_total counter
		cortex_compactor_garbage_collection_total 0

		# HELP cortex_compactor_meta_sync_duration_seconds Duration of the blocks metadata synchronization in seconds.
		# TYPE cortex_compactor_meta_sync_duration_seconds histogram
		cortex_compactor_meta_sync_duration_seconds_bucket{le="+Inf"} 0
		cortex_compactor_meta_sync_duration_seconds_sum 0
		cortex_compactor_meta_sync_duration_seconds_count 0

		# HELP cortex_compactor_meta_sync_failures_total Total blocks metadata synchronization failures.
		# TYPE cortex_compactor_meta_sync_failures_total counter
		cortex_compactor_meta_sync_failures_total 0

		# HELP cortex_compactor_meta_syncs_total Total blocks metadata synchronization attempts.
		# TYPE cortex_compactor_meta_syncs_total counter
		cortex_compactor_meta_syncs_total 0

		# HELP cortex_compactor_group_compaction_runs_completed_total Total number of group completed compaction runs. This also includes compactor group runs that resulted with no compaction.
		# TYPE cortex_compactor_group_compaction_runs_completed_total counter
		cortex_compactor_group_compaction_runs_completed_total 0

		# HELP cortex_compactor_group_compaction_runs_started_total Total number of group compaction attempts.
		# TYPE cortex_compactor_group_compaction_runs_started_total counter
		cortex_compactor_group_compaction_runs_started_total 0

		# HELP cortex_compactor_group_compactions_failures_total Total number of failed group compactions.
		# TYPE cortex_compactor_group_compactions_failures_total counter
		cortex_compactor_group_compactions_failures_total 0

		# HELP cortex_compactor_group_compactions_total Total number of group compaction attempts that resulted in new block(s).
		# TYPE cortex_compactor_group_compactions_total counter
		cortex_compactor_group_compactions_total 0

		# TYPE cortex_compactor_block_cleanup_failures_total counter
		# HELP cortex_compactor_block_cleanup_failures_total Total number of blocks failed to be deleted.
		cortex_compactor_block_cleanup_failures_total 0

		# HELP cortex_compactor_blocks_cleaned_total Total number of blocks deleted.
		# TYPE cortex_compactor_blocks_cleaned_total counter
		cortex_compactor_blocks_cleaned_total 0

		# HELP cortex_compactor_blocks_marked_for_deletion_total Total number of blocks marked for deletion in compactor.
		# TYPE cortex_compactor_blocks_marked_for_deletion_total counter
		cortex_compactor_blocks_marked_for_deletion_total{reason="compaction"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="partial"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="retention"} 0

		# TYPE cortex_compactor_block_cleanup_started_total counter
		# HELP cortex_compactor_block_cleanup_started_total Total number of blocks cleanup runs started.
		cortex_compactor_block_cleanup_started_total 1

		# TYPE cortex_compactor_block_cleanup_completed_total counter
		# HELP cortex_compactor_block_cleanup_completed_total Total number of blocks cleanup runs successfully completed.
		cortex_compactor_block_cleanup_completed_total 1

		# TYPE cortex_compactor_block_cleanup_failed_total counter
		# HELP cortex_compactor_block_cleanup_failed_total Total number of blocks cleanup runs failed.
		cortex_compactor_block_cleanup_failed_total 0
	`),
		"cortex_compactor_runs_started_total",
		"cortex_compactor_runs_completed_total",
		"cortex_compactor_runs_failed_total",
		"cortex_compactor_garbage_collection_duration_seconds",
		"cortex_compactor_garbage_collection_failures_total",
		"cortex_compactor_garbage_collection_total",
		"cortex_compactor_meta_sync_duration_seconds",
		"cortex_compactor_meta_sync_failures_total",
		"cortex_compactor_meta_syncs_total",
		"cortex_compactor_group_compaction_runs_completed_total",
		"cortex_compactor_group_compaction_runs_started_total",
		"cortex_compactor_group_compactions_failures_total",
		"cortex_compactor_group_compactions_total",
		"cortex_compactor_block_cleanup_failures_total",
		"cortex_compactor_blocks_cleaned_total",
		"cortex_compactor_blocks_marked_for_deletion_total",
		"cortex_compactor_block_cleanup_started_total",
		"cortex_compactor_block_cleanup_completed_total",
		"cortex_compactor_block_cleanup_failed_total",
	))
}

func TestMultitenantCompactor_ShouldRetryCompactionOnFailureWhileDiscoveringUsersFromBucket(t *testing.T) {
	t.Parallel()

	// Fail to iterate over the bucket while discovering users.
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", nil, errors.New("failed to iterate the bucket"))

	c, _, _, logs, registry := prepare(t, prepareConfig(t), bucketClient)
	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Compactor doesn't wait for blocks cleaner to finish, but our test checks for cleaner metrics.
	require.NoError(t, c.blocksCleaner.AwaitRunning(context.Background()))
	t.Cleanup(func() {
		t.Log(logs.String())
	})

	// Wait until all retry attempts have completed.
	test.Poll(t, time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsErred)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	// Ensure the bucket iteration has been retried the configured number of times.
	// 1 initial Iter to expose metrics before the cleanup run
	// 1 adittional Iter on cleanup run
	// 3 additional Iters on compaction run
	bucketClient.AssertNumberOfCalls(t, "Iter", 1+1+3)

	assert.Equal(t, []string{
		`level=info component=compactor msg="waiting until compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="discovering users from bucket"`,
		`level=error component=compactor msg="failed to discover users from bucket" err="failed to iterate the bucket"`,
	}, removeIgnoredLogs(strings.Split(strings.TrimSpace(logs.String()), "\n")))

	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# TYPE cortex_compactor_runs_started_total counter
		# HELP cortex_compactor_runs_started_total Total number of compaction runs started.
		cortex_compactor_runs_started_total 1

		# TYPE cortex_compactor_runs_completed_total counter
		# HELP cortex_compactor_runs_completed_total Total number of compaction runs successfully completed.
		cortex_compactor_runs_completed_total 0

		# TYPE cortex_compactor_runs_failed_total counter
		# HELP cortex_compactor_runs_failed_total Total number of compaction runs failed.
		cortex_compactor_runs_failed_total{reason="error"} 1
		cortex_compactor_runs_failed_total{reason="shutdown"} 0

		# HELP cortex_compactor_garbage_collection_duration_seconds Time it took to perform garbage collection iteration.
		# TYPE cortex_compactor_garbage_collection_duration_seconds histogram
		cortex_compactor_garbage_collection_duration_seconds_bucket{le="+Inf"} 0
		cortex_compactor_garbage_collection_duration_seconds_sum 0
		cortex_compactor_garbage_collection_duration_seconds_count 0

		# HELP cortex_compactor_garbage_collection_failures_total Total number of failed garbage collection operations.
		# TYPE cortex_compactor_garbage_collection_failures_total counter
		cortex_compactor_garbage_collection_failures_total 0

		# HELP cortex_compactor_garbage_collection_total Total number of garbage collection operations.
		# TYPE cortex_compactor_garbage_collection_total counter
		cortex_compactor_garbage_collection_total 0

		# HELP cortex_compactor_meta_sync_duration_seconds Duration of the blocks metadata synchronization in seconds.
		# TYPE cortex_compactor_meta_sync_duration_seconds histogram
		cortex_compactor_meta_sync_duration_seconds_bucket{le="+Inf"} 0
		cortex_compactor_meta_sync_duration_seconds_sum 0
		cortex_compactor_meta_sync_duration_seconds_count 0

		# HELP cortex_compactor_meta_sync_failures_total Total blocks metadata synchronization failures.
		# TYPE cortex_compactor_meta_sync_failures_total counter
		cortex_compactor_meta_sync_failures_total 0

		# HELP cortex_compactor_meta_syncs_total Total blocks metadata synchronization attempts.
		# TYPE cortex_compactor_meta_syncs_total counter
		cortex_compactor_meta_syncs_total 0

		# HELP cortex_compactor_group_compaction_runs_completed_total Total number of group completed compaction runs. This also includes compactor group runs that resulted with no compaction.
		# TYPE cortex_compactor_group_compaction_runs_completed_total counter
		cortex_compactor_group_compaction_runs_completed_total 0

		# HELP cortex_compactor_group_compaction_runs_started_total Total number of group compaction attempts.
		# TYPE cortex_compactor_group_compaction_runs_started_total counter
		cortex_compactor_group_compaction_runs_started_total 0

		# HELP cortex_compactor_group_compactions_failures_total Total number of failed group compactions.
		# TYPE cortex_compactor_group_compactions_failures_total counter
		cortex_compactor_group_compactions_failures_total 0

		# HELP cortex_compactor_group_compactions_total Total number of group compaction attempts that resulted in new block(s).
		# TYPE cortex_compactor_group_compactions_total counter
		cortex_compactor_group_compactions_total 0

		# TYPE cortex_compactor_block_cleanup_failures_total counter
		# HELP cortex_compactor_block_cleanup_failures_total Total number of blocks failed to be deleted.
		cortex_compactor_block_cleanup_failures_total 0

		# HELP cortex_compactor_blocks_cleaned_total Total number of blocks deleted.
		# TYPE cortex_compactor_blocks_cleaned_total counter
		cortex_compactor_blocks_cleaned_total 0

		# HELP cortex_compactor_blocks_marked_for_deletion_total Total number of blocks marked for deletion in compactor.
		# TYPE cortex_compactor_blocks_marked_for_deletion_total counter
		cortex_compactor_blocks_marked_for_deletion_total{reason="compaction"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="partial"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="retention"} 0

		# TYPE cortex_compactor_block_cleanup_started_total counter
		# HELP cortex_compactor_block_cleanup_started_total Total number of blocks cleanup runs started.
		cortex_compactor_block_cleanup_started_total 1

		# TYPE cortex_compactor_block_cleanup_completed_total counter
		# HELP cortex_compactor_block_cleanup_completed_total Total number of blocks cleanup runs successfully completed.
		cortex_compactor_block_cleanup_completed_total 0

		# TYPE cortex_compactor_block_cleanup_failed_total counter
		# HELP cortex_compactor_block_cleanup_failed_total Total number of blocks cleanup runs failed.
		cortex_compactor_block_cleanup_failed_total 1
	`),
		"cortex_compactor_runs_started_total",
		"cortex_compactor_runs_completed_total",
		"cortex_compactor_runs_failed_total",
		"cortex_compactor_garbage_collection_duration_seconds",
		"cortex_compactor_garbage_collection_failures_total",
		"cortex_compactor_garbage_collection_total",
		"cortex_compactor_meta_sync_duration_seconds",
		"cortex_compactor_meta_sync_failures_total",
		"cortex_compactor_meta_syncs_total",
		"cortex_compactor_group_compaction_runs_completed_total",
		"cortex_compactor_group_compaction_runs_started_total",
		"cortex_compactor_group_compactions_failures_total",
		"cortex_compactor_group_compactions_total",
		"cortex_compactor_block_cleanup_failures_total",
		"cortex_compactor_blocks_cleaned_total",
		"cortex_compactor_blocks_marked_for_deletion_total",
		"cortex_compactor_block_cleanup_started_total",
		"cortex_compactor_block_cleanup_completed_total",
		"cortex_compactor_block_cleanup_failed_total",
	))
}

func TestMultitenantCompactor_ShouldIncrementCompactionErrorIfFailedToCompactASingleTenant(t *testing.T) {
	t.Parallel()

	userID := "test-user"
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", []string{userID}, nil)
	bucketClient.MockIter(userID+"/", []string{userID + "/01DTVP434PA9VFXSW2JKB3392D", userID + "/01DTW0ZCPDDNV4BV83Q2SV4QAZ"}, nil)
	bucketClient.MockIter(userID+"/markers/", nil, nil)
	bucketClient.MockExists(path.Join(userID, mimir_tsdb.TenantDeletionMarkPath), false, nil)
	bucketClient.MockGet(userID+"/01DTVP434PA9VFXSW2JKB3392D/meta.json", mockBlockMetaJSON("01DTVP434PA9VFXSW2JKB3392D"), nil)
	bucketClient.MockGet(userID+"/01DTVP434PA9VFXSW2JKB3392D/deletion-mark.json", "", nil)
	bucketClient.MockGet(userID+"/01DTVP434PA9VFXSW2JKB3392D/no-compact-mark.json", "", nil)
	bucketClient.MockGet(userID+"/01DTW0ZCPDDNV4BV83Q2SV4QAZ/meta.json", mockBlockMetaJSON("01DTW0ZCPDDNV4BV83Q2SV4QAZ"), nil)
	bucketClient.MockGet(userID+"/01DTW0ZCPDDNV4BV83Q2SV4QAZ/deletion-mark.json", "", nil)
	bucketClient.MockGet(userID+"/01DTW0ZCPDDNV4BV83Q2SV4QAZ/no-compact-mark.json", "", nil)
	bucketClient.MockGet(userID+"/bucket-index.json.gz", "", nil)
	bucketClient.MockUpload(userID+"/bucket-index.json.gz", nil)

	c, _, tsdbPlannerMock, _, registry := prepare(t, prepareConfig(t), bucketClient)
	tsdbPlannerMock.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{}, errors.New("Failed to plan"))
	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Wait until all retry attempts have completed.
	test.Poll(t, time.Minute, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsErred)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# TYPE cortex_compactor_runs_started_total counter
		# HELP cortex_compactor_runs_started_total Total number of compaction runs started.
		cortex_compactor_runs_started_total 1

		# TYPE cortex_compactor_runs_completed_total counter
		# HELP cortex_compactor_runs_completed_total Total number of compaction runs successfully completed.
		cortex_compactor_runs_completed_total 0

		# TYPE cortex_compactor_runs_failed_total counter
		# HELP cortex_compactor_runs_failed_total Total number of compaction runs failed.
		cortex_compactor_runs_failed_total{reason="error"} 1
		cortex_compactor_runs_failed_total{reason="shutdown"} 0
	`),
		"cortex_compactor_runs_started_total",
		"cortex_compactor_runs_completed_total",
		"cortex_compactor_runs_failed_total",
	))
}

func TestMultitenantCompactor_ShouldIncrementCompactionShutdownIfTheContextIsCancelled(t *testing.T) {
	t.Parallel()

	userID := "test-user"
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", []string{userID}, nil)
	bucketClient.MockIter(userID+"/", []string{userID + "/01DTVP434PA9VFXSW2JKB3392D", userID + "/01DTW0ZCPDDNV4BV83Q2SV4QAZ"}, nil)
	bucketClient.MockIter(userID+"/markers/", nil, nil)
	bucketClient.MockExists(path.Join(userID, mimir_tsdb.TenantDeletionMarkPath), false, nil)
	bucketClient.MockGet(userID+"/01DTVP434PA9VFXSW2JKB3392D/meta.json", mockBlockMetaJSON("01DTVP434PA9VFXSW2JKB3392D"), nil)
	bucketClient.MockGet(userID+"/01DTVP434PA9VFXSW2JKB3392D/deletion-mark.json", "", nil)
	bucketClient.MockGet(userID+"/01DTVP434PA9VFXSW2JKB3392D/no-compact-mark.json", "", nil)
	bucketClient.MockGet(userID+"/01DTW0ZCPDDNV4BV83Q2SV4QAZ/meta.json", mockBlockMetaJSON("01DTW0ZCPDDNV4BV83Q2SV4QAZ"), nil)
	bucketClient.MockGet(userID+"/01DTW0ZCPDDNV4BV83Q2SV4QAZ/deletion-mark.json", "", nil)
	bucketClient.MockGet(userID+"/01DTW0ZCPDDNV4BV83Q2SV4QAZ/no-compact-mark.json", "", nil)
	bucketClient.MockGet(userID+"/bucket-index.json.gz", "", nil)
	bucketClient.MockUpload(userID+"/bucket-index.json.gz", nil)

	c, _, tsdbPlannerMock, logs, registry := prepare(t, prepareConfig(t), bucketClient)
	t.Cleanup(func() {
		t.Log(logs.String())
	})
	// Mock the planner as if a shutdown was triggered and the service was terminated.
	tsdbPlannerMock.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{}, context.Canceled)
	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Wait until the error is recorded.
	test.Poll(t, time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsShutdown)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# TYPE cortex_compactor_runs_started_total counter
		# HELP cortex_compactor_runs_started_total Total number of compaction runs started.
		cortex_compactor_runs_started_total 1

		# TYPE cortex_compactor_runs_completed_total counter
		# HELP cortex_compactor_runs_completed_total Total number of compaction runs successfully completed.
		cortex_compactor_runs_completed_total 0

		# TYPE cortex_compactor_runs_failed_total counter
		# HELP cortex_compactor_runs_failed_total Total number of compaction runs failed.
		cortex_compactor_runs_failed_total{reason="error"} 0
		cortex_compactor_runs_failed_total{reason="shutdown"} 1
	`),
		"cortex_compactor_runs_started_total",
		"cortex_compactor_runs_completed_total",
		"cortex_compactor_runs_failed_total",
	))
}

func TestMultitenantCompactor_ShouldIterateOverUsersAndRunCompaction(t *testing.T) {
	t.Parallel()

	// Mock the bucket to contain two users, each one with two blocks (to make sure that grouper doesn't skip them).
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", []string{"user-1", "user-2"}, nil)
	bucketClient.MockExists(path.Join("user-1", mimir_tsdb.TenantDeletionMarkPath), false, nil)
	bucketClient.MockExists(path.Join("user-2", mimir_tsdb.TenantDeletionMarkPath), false, nil)
	bucketClient.MockIter("user-1/", []string{"user-1/01DTVP434PA9VFXSW2JKB3392D", "user-1/01FS51A7GQ1RQWV35DBVYQM4KF"}, nil)
	bucketClient.MockIter("user-2/", []string{"user-2/01DTW0ZCPDDNV4BV83Q2SV4QAZ", "user-2/01FRSF035J26D6CGX7STCSD1KG"}, nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/meta.json", mockBlockMetaJSON("01DTVP434PA9VFXSW2JKB3392D"), nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-1/01FS51A7GQ1RQWV35DBVYQM4KF/meta.json", mockBlockMetaJSON("01FS51A7GQ1RQWV35DBVYQM4KF"), nil)
	bucketClient.MockGet("user-1/01FS51A7GQ1RQWV35DBVYQM4KF/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-1/01FS51A7GQ1RQWV35DBVYQM4KF/no-compact-mark.json", "", nil)

	bucketClient.MockGet("user-2/01DTW0ZCPDDNV4BV83Q2SV4QAZ/meta.json", mockBlockMetaJSON("01DTW0ZCPDDNV4BV83Q2SV4QAZ"), nil)
	bucketClient.MockGet("user-2/01DTW0ZCPDDNV4BV83Q2SV4QAZ/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-2/01DTW0ZCPDDNV4BV83Q2SV4QAZ/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-2/01FRSF035J26D6CGX7STCSD1KG/meta.json", mockBlockMetaJSON("01FRSF035J26D6CGX7STCSD1KG"), nil)
	bucketClient.MockGet("user-2/01FRSF035J26D6CGX7STCSD1KG/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-2/01FRSF035J26D6CGX7STCSD1KG/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-1/bucket-index.json.gz", "", nil)
	bucketClient.MockGet("user-2/bucket-index.json.gz", "", nil)
	bucketClient.MockIter("user-1/markers/", nil, nil)
	bucketClient.MockIter("user-2/markers/", nil, nil)
	bucketClient.MockUpload("user-1/bucket-index.json.gz", nil)
	bucketClient.MockUpload("user-2/bucket-index.json.gz", nil)

	c, _, tsdbPlanner, logs, registry := prepare(t, prepareConfig(t), bucketClient)

	// Mock the planner as if there's no compaction to do,
	// in order to simplify tests (all in all, we just want to
	// test our logic and not TSDB compactor which we expect to
	// be already tested).
	tsdbPlanner.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{}, nil)

	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Compactor doesn't wait for blocks cleaner to finish, but our test checks for cleaner metrics.
	require.NoError(t, c.blocksCleaner.AwaitRunning(context.Background()))

	// Wait until a run has completed.
	test.Poll(t, time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsCompleted)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	// Ensure a plan has been executed for the blocks of each user.
	tsdbPlanner.AssertNumberOfCalls(t, "Plan", 2)

	assert.ElementsMatch(t, []string{
		`level=info component=compactor msg="waiting until compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="discovering users from bucket"`,
		`level=info component=compactor msg="discovered users from bucket" users=2`,
		`level=info component=compactor msg="starting compaction of user blocks" user=user-1`,
		`level=info component=compactor user=user-1 msg="start sync of metas"`,
		`level=info component=compactor user=user-1 msg="start of GC"`,
		`level=debug component=compactor user=user-1 msg="grouper found a compactable blocks group" groupKey=0@17241709254077376921-merge--1574776800000-1574784000000 job="stage: merge, range start: 1574776800000, range end: 1574784000000, shard: , blocks: 01DTVP434PA9VFXSW2JKB3392D (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC),01FS51A7GQ1RQWV35DBVYQM4KF (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC)"`,
		`level=info component=compactor user=user-1 msg="start of compactions"`,
		`level=info component=compactor user=user-1 groupKey=0@17241709254077376921-merge--1574776800000-1574784000000 job_type=merge msg="compaction job succeeded" block_count=2`,
		`level=info component=compactor user=user-1 msg="compaction iterations done"`,
		`level=info component=compactor msg="successfully compacted user blocks" user=user-1`,
		`level=info component=compactor msg="starting compaction of user blocks" user=user-2`,
		`level=info component=compactor user=user-2 msg="start sync of metas"`,
		`level=info component=compactor user=user-2 msg="start of GC"`,
		`level=debug component=compactor user=user-2 msg="grouper found a compactable blocks group" groupKey=0@17241709254077376921-merge--1574776800000-1574784000000 job="stage: merge, range start: 1574776800000, range end: 1574784000000, shard: , blocks: 01DTW0ZCPDDNV4BV83Q2SV4QAZ (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC),01FRSF035J26D6CGX7STCSD1KG (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC)"`,
		`level=info component=compactor user=user-2 msg="start of compactions"`,
		`level=info component=compactor user=user-2 groupKey=0@17241709254077376921-merge--1574776800000-1574784000000 job_type=merge msg="compaction job succeeded" block_count=2`,
		`level=info component=compactor user=user-2 msg="compaction iterations done"`,
		`level=info component=compactor msg="successfully compacted user blocks" user=user-2`,
	}, removeIgnoredLogs(strings.Split(strings.TrimSpace(logs.String()), "\n")))

	// Instead of testing for shipper metrics, we only check our metrics here.
	// Real shipper metrics are too variable to embed into a test.
	testedMetrics := []string{
		"cortex_compactor_runs_started_total", "cortex_compactor_runs_completed_total", "cortex_compactor_runs_failed_total",
		"cortex_compactor_blocks_cleaned_total", "cortex_compactor_block_cleanup_failures_total", "cortex_compactor_blocks_marked_for_deletion_total",
		"cortex_compactor_block_cleanup_started_total", "cortex_compactor_block_cleanup_completed_total", "cortex_compactor_block_cleanup_failed_total",
		"cortex_compactor_group_compaction_runs_completed_total", "cortex_compactor_group_compaction_runs_started_total",
		"cortex_compactor_group_compactions_failures_total", "cortex_compactor_group_compactions_total"}

	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# TYPE cortex_compactor_runs_started_total counter
		# HELP cortex_compactor_runs_started_total Total number of compaction runs started.
		cortex_compactor_runs_started_total 1

		# TYPE cortex_compactor_runs_completed_total counter
		# HELP cortex_compactor_runs_completed_total Total number of compaction runs successfully completed.
		cortex_compactor_runs_completed_total 1

		# TYPE cortex_compactor_runs_failed_total counter
		# HELP cortex_compactor_runs_failed_total Total number of compaction runs failed.
		cortex_compactor_runs_failed_total{reason="error"} 0
		cortex_compactor_runs_failed_total{reason="shutdown"} 0

		# HELP cortex_compactor_group_compaction_runs_completed_total Total number of group completed compaction runs. This also includes compactor group runs that resulted with no compaction.
		# TYPE cortex_compactor_group_compaction_runs_completed_total counter
		cortex_compactor_group_compaction_runs_completed_total 2

		# HELP cortex_compactor_group_compaction_runs_started_total Total number of group compaction attempts.
		# TYPE cortex_compactor_group_compaction_runs_started_total counter
		cortex_compactor_group_compaction_runs_started_total 2

		# HELP cortex_compactor_group_compactions_failures_total Total number of failed group compactions.
		# TYPE cortex_compactor_group_compactions_failures_total counter
		cortex_compactor_group_compactions_failures_total 0

		# HELP cortex_compactor_group_compactions_total Total number of group compaction attempts that resulted in new block(s).
		# TYPE cortex_compactor_group_compactions_total counter
		cortex_compactor_group_compactions_total 0

		# TYPE cortex_compactor_block_cleanup_failures_total counter
		# HELP cortex_compactor_block_cleanup_failures_total Total number of blocks failed to be deleted.
		cortex_compactor_block_cleanup_failures_total 0

		# HELP cortex_compactor_blocks_cleaned_total Total number of blocks deleted.
		# TYPE cortex_compactor_blocks_cleaned_total counter
		cortex_compactor_blocks_cleaned_total 0

		# HELP cortex_compactor_blocks_marked_for_deletion_total Total number of blocks marked for deletion in compactor.
		# TYPE cortex_compactor_blocks_marked_for_deletion_total counter
		cortex_compactor_blocks_marked_for_deletion_total{reason="compaction"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="partial"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="retention"} 0

		# TYPE cortex_compactor_block_cleanup_started_total counter
		# HELP cortex_compactor_block_cleanup_started_total Total number of blocks cleanup runs started.
		cortex_compactor_block_cleanup_started_total 1

		# TYPE cortex_compactor_block_cleanup_completed_total counter
		# HELP cortex_compactor_block_cleanup_completed_total Total number of blocks cleanup runs successfully completed.
		cortex_compactor_block_cleanup_completed_total 1

		# TYPE cortex_compactor_block_cleanup_failed_total counter
		# HELP cortex_compactor_block_cleanup_failed_total Total number of blocks cleanup runs failed.
		cortex_compactor_block_cleanup_failed_total 0
	`), testedMetrics...))
}

func TestMultitenantCompactor_ShouldStopCompactingTenantOnReachingMaxCompactionTime(t *testing.T) {
	t.Parallel()

	// By using blocks with different labels, we get two compaction jobs. Only one of these jobs will be started,
	// and since its planning will take longer than maxCompactionTime, we stop compactions early.
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", []string{"user-1"}, nil)
	bucketClient.MockExists(path.Join("user-1", mimir_tsdb.TenantDeletionMarkPath), false, nil)
	bucketClient.MockIter("user-1/", []string{"user-1/01DTVP434PA9VFXSW2JKB3392D", "user-1/01FN3VCQV5X342W2ZKMQQXAZRX", "user-1/01FS51A7GQ1RQWV35DBVYQM4KF", "user-1/01FRQGQB7RWQ2TS0VWA82QTPXE"}, nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/meta.json", mockBlockMetaJSONWithTimeRangeAndLabels("01DTVP434PA9VFXSW2JKB3392D", 1574776800000, 1574784000000, map[string]string{"A": "B"}), nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-1/01FS51A7GQ1RQWV35DBVYQM4KF/meta.json", mockBlockMetaJSONWithTimeRangeAndLabels("01FS51A7GQ1RQWV35DBVYQM4KF", 1574776800000, 1574784000000, map[string]string{"A": "B"}), nil)
	bucketClient.MockGet("user-1/01FS51A7GQ1RQWV35DBVYQM4KF/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-1/01FS51A7GQ1RQWV35DBVYQM4KF/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-1/01FN3VCQV5X342W2ZKMQQXAZRX/meta.json", mockBlockMetaJSONWithTimeRangeAndLabels("01FN3VCQV5X342W2ZKMQQXAZRX", 1574776800000, 1574784000000, map[string]string{"C": "D"}), nil)
	bucketClient.MockGet("user-1/01FN3VCQV5X342W2ZKMQQXAZRX/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-1/01FN3VCQV5X342W2ZKMQQXAZRX/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-1/01FRQGQB7RWQ2TS0VWA82QTPXE/meta.json", mockBlockMetaJSONWithTimeRangeAndLabels("01FRQGQB7RWQ2TS0VWA82QTPXE", 1574776800000, 1574784000000, map[string]string{"C": "D"}), nil)
	bucketClient.MockGet("user-1/01FRQGQB7RWQ2TS0VWA82QTPXE/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-1/01FRQGQB7RWQ2TS0VWA82QTPXE/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-1/bucket-index.json.gz", "", nil)
	bucketClient.MockIter("user-1/markers/", nil, nil)
	bucketClient.MockUpload("user-1/bucket-index.json.gz", nil)

	cfg := prepareConfig(t)
	cfg.MaxCompactionTime = 500 * time.Millisecond // Enough time to start one compaction. We will make it last longer than this.
	cfg.CompactionConcurrency = 1

	c, _, tsdbPlanner, logs, _ := prepare(t, cfg, bucketClient)

	// Planner is called at the beginning of each job. We make it return no work, but only after delay.
	plannerDelay := 2 * cfg.MaxCompactionTime
	tsdbPlanner.On("Plan", mock.Anything, mock.Anything).After(plannerDelay).Return([]*block.Meta{}, nil)

	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Compactor doesn't wait for blocks cleaner to finish, but our test checks for cleaner metrics.
	require.NoError(t, c.blocksCleaner.AwaitRunning(context.Background()))

	// Wait until a run has completed. Since planner takes "2*cfg.MaxCompactionTime", we wait for twice as long.
	test.Poll(t, 2*plannerDelay, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsCompleted)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	// Ensure a plan has been called only once.
	tsdbPlanner.AssertNumberOfCalls(t, "Plan", 1)

	assert.Equal(t, []string{
		`level=info component=compactor msg="waiting until compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="discovering users from bucket"`,
		`level=info component=compactor msg="discovered users from bucket" users=1`,
		`level=info component=compactor msg="starting compaction of user blocks" user=user-1`,
		`level=info component=compactor user=user-1 msg="start sync of metas"`,
		`level=info component=compactor user=user-1 msg="start of GC"`,
		`level=debug component=compactor user=user-1 msg="grouper found a compactable blocks group" groupKey=0@12695595599644216241-merge--1574776800000-1574784000000 job="stage: merge, range start: 1574776800000, range end: 1574784000000, shard: , blocks: 01FN3VCQV5X342W2ZKMQQXAZRX (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC),01FRQGQB7RWQ2TS0VWA82QTPXE (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC)"`,
		`level=debug component=compactor user=user-1 msg="grouper found a compactable blocks group" groupKey=0@414047632870839233-merge--1574776800000-1574784000000 job="stage: merge, range start: 1574776800000, range end: 1574784000000, shard: , blocks: 01DTVP434PA9VFXSW2JKB3392D (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC),01FS51A7GQ1RQWV35DBVYQM4KF (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC)"`,
		`level=info component=compactor user=user-1 msg="start of compactions"`,
		`level=info component=compactor user=user-1 msg="max compaction time reached, no more compactions will be started"`,
		`level=info component=compactor user=user-1 groupKey=0@12695595599644216241-merge--1574776800000-1574784000000 job_type=merge msg="compaction job succeeded" block_count=2`,
		`level=info component=compactor user=user-1 msg="compaction iterations done"`,
		`level=info component=compactor msg="successfully compacted user blocks" user=user-1`,
	}, removeIgnoredLogs(strings.Split(strings.TrimSpace(logs.String()), "\n")))
}

func TestMultitenantCompactor_ShouldNotCompactBlocksMarkedForDeletion(t *testing.T) {
	t.Parallel()

	cfg := prepareConfig(t)
	cfg.DeletionDelay = 10 * time.Minute // Delete block after 10 minutes

	// Mock the bucket to contain two users, each one with one block.
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", []string{"user-1"}, nil)
	bucketClient.MockIter("user-1/", []string{"user-1/01DTVP434PA9VFXSW2JKB3392D", "user-1/01DTW0ZCPDDNV4BV83Q2SV4QAZ"}, nil)
	bucketClient.MockExists(path.Join("user-1", mimir_tsdb.TenantDeletionMarkPath), false, nil)

	// Block that has just been marked for deletion. It will not be deleted just yet, and it also will not be compacted.
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/meta.json", mockBlockMetaJSON("01DTVP434PA9VFXSW2JKB3392D"), nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/deletion-mark.json", mockDeletionMarkJSON("01DTVP434PA9VFXSW2JKB3392D", time.Now()), nil)
	bucketClient.MockGet("user-1/markers/01DTVP434PA9VFXSW2JKB3392D-deletion-mark.json", mockDeletionMarkJSON("01DTVP434PA9VFXSW2JKB3392D", time.Now()), nil)

	// This block will be deleted by cleaner.
	bucketClient.MockGet("user-1/01DTW0ZCPDDNV4BV83Q2SV4QAZ/meta.json", mockBlockMetaJSON("01DTW0ZCPDDNV4BV83Q2SV4QAZ"), nil)
	bucketClient.MockGet("user-1/01DTW0ZCPDDNV4BV83Q2SV4QAZ/deletion-mark.json", mockDeletionMarkJSON("01DTW0ZCPDDNV4BV83Q2SV4QAZ", time.Now().Add(-cfg.DeletionDelay)), nil)
	bucketClient.MockGet("user-1/markers/01DTW0ZCPDDNV4BV83Q2SV4QAZ-deletion-mark.json", mockDeletionMarkJSON("01DTW0ZCPDDNV4BV83Q2SV4QAZ", time.Now().Add(-cfg.DeletionDelay)), nil)

	bucketClient.MockIter("user-1/01DTW0ZCPDDNV4BV83Q2SV4QAZ", []string{
		"user-1/01DTW0ZCPDDNV4BV83Q2SV4QAZ/meta.json",
		"user-1/01DTW0ZCPDDNV4BV83Q2SV4QAZ/deletion-mark.json",
	}, nil)

	bucketClient.MockIter("user-1/markers/", []string{
		"user-1/markers/01DTVP434PA9VFXSW2JKB3392D-deletion-mark.json",
		"user-1/markers/01DTW0ZCPDDNV4BV83Q2SV4QAZ-deletion-mark.json",
	}, nil)

	bucketClient.MockDelete("user-1/01DTW0ZCPDDNV4BV83Q2SV4QAZ/meta.json", nil)
	bucketClient.MockDelete("user-1/01DTW0ZCPDDNV4BV83Q2SV4QAZ/deletion-mark.json", nil)
	bucketClient.MockDelete("user-1/markers/01DTW0ZCPDDNV4BV83Q2SV4QAZ-deletion-mark.json", nil)
	bucketClient.MockDelete("user-1/01DTW0ZCPDDNV4BV83Q2SV4QAZ", nil)
	bucketClient.MockGet("user-1/bucket-index.json.gz", "", nil)
	bucketClient.MockUpload("user-1/bucket-index.json.gz", nil)

	c, _, tsdbPlanner, logs, registry := prepare(t, cfg, bucketClient)

	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Compactor doesn't wait for blocks cleaner to finish, but our test checks for cleaner metrics.
	require.NoError(t, c.blocksCleaner.AwaitRunning(context.Background()))

	// Wait until a run has completed.
	test.Poll(t, time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsCompleted)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	// Since both blocks are marked for deletion, none of them are going to be compacted.
	tsdbPlanner.AssertNumberOfCalls(t, "Plan", 0)

	assert.ElementsMatch(t, []string{
		`level=info component=compactor msg="waiting until compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="discovering users from bucket"`,
		`level=info component=compactor msg="discovered users from bucket" users=1`,
		`level=info component=compactor msg="starting compaction of user blocks" user=user-1`,
		`level=info component=compactor user=user-1 msg="start sync of metas"`,
		`level=info component=compactor user=user-1 msg="start of GC"`,
		`level=info component=compactor user=user-1 msg="start of compactions"`,
		`level=info component=compactor user=user-1 msg="compaction iterations done"`,
		`level=info component=compactor msg="successfully compacted user blocks" user=user-1`,
	}, removeIgnoredLogs(strings.Split(strings.TrimSpace(logs.String()), "\n")))

	// Instead of testing for shipper metrics, we only check our metrics here.
	// Real shipper metrics are too variable to embed into a test.
	testedMetrics := []string{
		"cortex_compactor_runs_started_total", "cortex_compactor_runs_completed_total", "cortex_compactor_runs_failed_total",
		"cortex_compactor_blocks_cleaned_total", "cortex_compactor_block_cleanup_failures_total", "cortex_compactor_blocks_marked_for_deletion_total",
		"cortex_compactor_block_cleanup_started_total", "cortex_compactor_block_cleanup_completed_total", "cortex_compactor_block_cleanup_failed_total",
	}
	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# TYPE cortex_compactor_runs_started_total counter
		# HELP cortex_compactor_runs_started_total Total number of compaction runs started.
		cortex_compactor_runs_started_total 1

		# TYPE cortex_compactor_runs_completed_total counter
		# HELP cortex_compactor_runs_completed_total Total number of compaction runs successfully completed.
		cortex_compactor_runs_completed_total 1

		# TYPE cortex_compactor_runs_failed_total counter
		# HELP cortex_compactor_runs_failed_total Total number of compaction runs failed.
		cortex_compactor_runs_failed_total{reason="error"} 0
		cortex_compactor_runs_failed_total{reason="shutdown"} 0

		# TYPE cortex_compactor_block_cleanup_failures_total counter
		# HELP cortex_compactor_block_cleanup_failures_total Total number of blocks failed to be deleted.
		cortex_compactor_block_cleanup_failures_total 0

		# HELP cortex_compactor_blocks_cleaned_total Total number of blocks deleted.
		# TYPE cortex_compactor_blocks_cleaned_total counter
		cortex_compactor_blocks_cleaned_total 1

		# HELP cortex_compactor_blocks_marked_for_deletion_total Total number of blocks marked for deletion in compactor.
		# TYPE cortex_compactor_blocks_marked_for_deletion_total counter
		cortex_compactor_blocks_marked_for_deletion_total{reason="compaction"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="partial"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="retention"} 0

		# TYPE cortex_compactor_block_cleanup_started_total counter
		# HELP cortex_compactor_block_cleanup_started_total Total number of blocks cleanup runs started.
		cortex_compactor_block_cleanup_started_total 1

		# TYPE cortex_compactor_block_cleanup_completed_total counter
		# HELP cortex_compactor_block_cleanup_completed_total Total number of blocks cleanup runs successfully completed.
		cortex_compactor_block_cleanup_completed_total 1

		# TYPE cortex_compactor_block_cleanup_failed_total counter
		# HELP cortex_compactor_block_cleanup_failed_total Total number of blocks cleanup runs failed.
		cortex_compactor_block_cleanup_failed_total 0
	`), testedMetrics...))
}

func TestMultitenantCompactor_ShouldNotCompactBlocksMarkedForNoCompaction(t *testing.T) {
	t.Parallel()

	cfg := prepareConfig(t)
	cfg.DeletionDelay = 10 * time.Minute // Delete block after 10 minutes

	// Mock the bucket to contain one user with a block marked for no-compaction.
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", []string{"user-1"}, nil)
	bucketClient.MockIter("user-1/", []string{"user-1/01DTVP434PA9VFXSW2JKB3392D"}, nil)
	bucketClient.MockExists(path.Join("user-1", mimir_tsdb.TenantDeletionMarkPath), false, nil)

	// Block that is marked for no compaction. It will be ignored.
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/meta.json", mockBlockMetaJSON("01DTVP434PA9VFXSW2JKB3392D"), nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/no-compact-mark.json", `{"id":"01DTVP434PA9VFXSW2JKB3392D","version":1,"details":"details","no_compact_time":1637757932,"reason":"reason"}`, nil)

	bucketClient.MockIter("user-1/markers/", []string{"user-1/markers/01DTVP434PA9VFXSW2JKB3392D-no-compact-mark.json"}, nil)

	bucketClient.MockGet("user-1/bucket-index.json.gz", "", nil)
	bucketClient.MockUpload("user-1/bucket-index.json.gz", nil)

	c, _, tsdbPlanner, logs, _ := prepare(t, cfg, bucketClient)

	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Compactor doesn't wait for blocks cleaner to finish, but our test checks for cleaner metrics.
	require.NoError(t, c.blocksCleaner.AwaitRunning(context.Background()))

	// Wait until a run has completed.
	test.Poll(t, time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsCompleted)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	// Since block is not compacted, there will be no planning done.
	tsdbPlanner.AssertNumberOfCalls(t, "Plan", 0)

	assert.ElementsMatch(t, []string{
		`level=info component=compactor msg="waiting until compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="discovering users from bucket"`,
		`level=info component=compactor msg="discovered users from bucket" users=1`,
		`level=info component=compactor msg="starting compaction of user blocks" user=user-1`,
		`level=info component=compactor user=user-1 msg="start sync of metas"`,
		`level=info component=compactor user=user-1 msg="start of GC"`,
		`level=info component=compactor user=user-1 msg="start of compactions"`,
		`level=info component=compactor user=user-1 msg="compaction iterations done"`,
		`level=info component=compactor msg="successfully compacted user blocks" user=user-1`,
	}, removeIgnoredLogs(strings.Split(strings.TrimSpace(logs.String()), "\n")))
}

func TestMultitenantCompactor_ShouldNotCompactBlocksForUsersMarkedForDeletion(t *testing.T) {
	t.Parallel()

	cfg := prepareConfig(t)
	cfg.DeletionDelay = 10 * time.Minute      // Delete block after 10 minutes
	cfg.TenantCleanupDelay = 10 * time.Minute // To make sure it's not 0.

	// Mock the bucket to contain two users, each one with one block.
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", []string{"user-1"}, nil)
	bucketClient.MockIter("user-1/", []string{"user-1/01DTVP434PA9VFXSW2JKB3392D"}, nil)
	bucketClient.MockGet("user-1/bucket-index.json.gz", "", nil)

	bucketClient.MockGet(path.Join("user-1", mimir_tsdb.TenantDeletionMarkPath), `{"deletion_time": 1}`, nil)
	bucketClient.MockUpload(path.Join("user-1", mimir_tsdb.TenantDeletionMarkPath), nil)

	bucketClient.MockIter("user-1/01DTVP434PA9VFXSW2JKB3392D", []string{"user-1/01DTVP434PA9VFXSW2JKB3392D/meta.json", "user-1/01DTVP434PA9VFXSW2JKB3392D/index"}, nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/meta.json", mockBlockMetaJSON("01DTVP434PA9VFXSW2JKB3392D"), nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/index", "some index content", nil)
	bucketClient.MockExists("user-1/01DTVP434PA9VFXSW2JKB3392D/deletion-mark.json", false, nil)
	bucketClient.MockExists("user-1/markers/01DTVP434PA9VFXSW2JKB3392D-deletion-mark.json", false, nil)

	bucketClient.MockDelete("user-1/01DTVP434PA9VFXSW2JKB3392D/meta.json", nil)
	bucketClient.MockDelete("user-1/01DTVP434PA9VFXSW2JKB3392D/index", nil)
	bucketClient.MockDelete("user-1/bucket-index.json.gz", nil)

	c, _, tsdbPlanner, logs, registry := prepare(t, cfg, bucketClient)

	// Mock the planner as if there's no compaction to do,
	// in order to simplify tests (all in all, we just want to
	// test our logic and not TSDB compactor which we expect to
	// be already tested).
	tsdbPlanner.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{}, nil)

	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Compactor doesn't wait for blocks cleaner to finish, but our test checks for cleaner metrics.
	require.NoError(t, c.blocksCleaner.AwaitRunning(context.Background()))

	// Wait until a run has completed.
	test.Poll(t, time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsCompleted)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	// No user is compacted, single user we have is marked for deletion.
	tsdbPlanner.AssertNumberOfCalls(t, "Plan", 0)

	assert.ElementsMatch(t, []string{
		`level=info component=compactor msg="waiting until compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="discovering users from bucket"`,
		`level=info component=compactor msg="discovered users from bucket" users=1`,
		`level=debug component=compactor msg="skipping user because it is marked for deletion" user=user-1`,
	}, removeIgnoredLogs(strings.Split(strings.TrimSpace(logs.String()), "\n")))

	// Instead of testing for shipper metrics, we only check our metrics here.
	// Real shipper metrics are too variable to embed into a test.
	testedMetrics := []string{
		"cortex_compactor_runs_started_total", "cortex_compactor_runs_completed_total", "cortex_compactor_runs_failed_total",
		"cortex_compactor_blocks_cleaned_total", "cortex_compactor_block_cleanup_failures_total", "cortex_compactor_blocks_marked_for_deletion_total",
		"cortex_compactor_block_cleanup_started_total", "cortex_compactor_block_cleanup_completed_total", "cortex_compactor_block_cleanup_failed_total",
		"cortex_bucket_blocks_count", "cortex_bucket_blocks_marked_for_deletion_count", "cortex_bucket_index_last_successful_update_timestamp_seconds",
	}
	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# TYPE cortex_compactor_runs_started_total counter
		# HELP cortex_compactor_runs_started_total Total number of compaction runs started.
		cortex_compactor_runs_started_total 1

		# TYPE cortex_compactor_runs_completed_total counter
		# HELP cortex_compactor_runs_completed_total Total number of compaction runs successfully completed.
		cortex_compactor_runs_completed_total 1

		# TYPE cortex_compactor_runs_failed_total counter
		# HELP cortex_compactor_runs_failed_total Total number of compaction runs failed.
		cortex_compactor_runs_failed_total{reason="error"} 0
		cortex_compactor_runs_failed_total{reason="shutdown"} 0

		# TYPE cortex_compactor_block_cleanup_failures_total counter
		# HELP cortex_compactor_block_cleanup_failures_total Total number of blocks failed to be deleted.
		cortex_compactor_block_cleanup_failures_total 0

		# HELP cortex_compactor_blocks_cleaned_total Total number of blocks deleted.
		# TYPE cortex_compactor_blocks_cleaned_total counter
		cortex_compactor_blocks_cleaned_total 1

		# HELP cortex_compactor_blocks_marked_for_deletion_total Total number of blocks marked for deletion in compactor.
		# TYPE cortex_compactor_blocks_marked_for_deletion_total counter
		cortex_compactor_blocks_marked_for_deletion_total{reason="compaction"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="partial"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="retention"} 0

		# TYPE cortex_compactor_block_cleanup_started_total counter
		# HELP cortex_compactor_block_cleanup_started_total Total number of blocks cleanup runs started.
		cortex_compactor_block_cleanup_started_total 1

		# TYPE cortex_compactor_block_cleanup_completed_total counter
		# HELP cortex_compactor_block_cleanup_completed_total Total number of blocks cleanup runs successfully completed.
		cortex_compactor_block_cleanup_completed_total 1

		# TYPE cortex_compactor_block_cleanup_failed_total counter
		# HELP cortex_compactor_block_cleanup_failed_total Total number of blocks cleanup runs failed.
		cortex_compactor_block_cleanup_failed_total 0
	`), testedMetrics...))
}

func TestMultitenantCompactor_ShouldCompactAllUsersOnShardingEnabledButOnlyOneInstanceRunning(t *testing.T) {
	t.Parallel()

	// Mock the bucket to contain two users, each one with one block.
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", []string{"user-1", "user-2"}, nil)
	bucketClient.MockExists(path.Join("user-1", mimir_tsdb.TenantDeletionMarkPath), false, nil)
	bucketClient.MockExists(path.Join("user-2", mimir_tsdb.TenantDeletionMarkPath), false, nil)
	bucketClient.MockIter("user-1/", []string{"user-1/01DTVP434PA9VFXSW2JKB3392D", "user-1/01FSTQ95C8FS0ZAGTQS2EF1NEG"}, nil)
	bucketClient.MockIter("user-2/", []string{"user-2/01DTW0ZCPDDNV4BV83Q2SV4QAZ", "user-2/01FSV54G6QFQH1G9QE93G3B9TB"}, nil)
	bucketClient.MockIter("user-1/markers/", nil, nil)
	bucketClient.MockIter("user-2/markers/", nil, nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/meta.json", mockBlockMetaJSON("01DTVP434PA9VFXSW2JKB3392D"), nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JKB3392D/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-1/01FSTQ95C8FS0ZAGTQS2EF1NEG/meta.json", mockBlockMetaJSON("01FSTQ95C8FS0ZAGTQS2EF1NEG"), nil)
	bucketClient.MockGet("user-1/01FSTQ95C8FS0ZAGTQS2EF1NEG/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-1/01FSTQ95C8FS0ZAGTQS2EF1NEG/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-2/01DTW0ZCPDDNV4BV83Q2SV4QAZ/meta.json", mockBlockMetaJSON("01DTW0ZCPDDNV4BV83Q2SV4QAZ"), nil)
	bucketClient.MockGet("user-2/01DTW0ZCPDDNV4BV83Q2SV4QAZ/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-2/01DTW0ZCPDDNV4BV83Q2SV4QAZ/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-2/01FSV54G6QFQH1G9QE93G3B9TB/meta.json", mockBlockMetaJSON("01FSV54G6QFQH1G9QE93G3B9TB"), nil)
	bucketClient.MockGet("user-2/01FSV54G6QFQH1G9QE93G3B9TB/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-2/01FSV54G6QFQH1G9QE93G3B9TB/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-1/bucket-index.json.gz", "", nil)
	bucketClient.MockGet("user-2/bucket-index.json.gz", "", nil)
	bucketClient.MockUpload("user-1/bucket-index.json.gz", nil)
	bucketClient.MockUpload("user-2/bucket-index.json.gz", nil)

	ringStore, closer := consul.NewInMemoryClient(ring.GetCodec(), log.NewNopLogger(), nil)
	t.Cleanup(func() { assert.NoError(t, closer.Close()) })

	cfg := prepareConfig(t)
	cfg.ShardingRing.Common.InstanceID = "compactor-1"
	cfg.ShardingRing.Common.InstanceAddr = "1.2.3.4"
	cfg.ShardingRing.Common.KVStore.Mock = ringStore
	c, _, tsdbPlanner, logs, registry := prepare(t, cfg, bucketClient)

	// Mock the planner as if there's no compaction to do,
	// in order to simplify tests (all in all, we just want to
	// test our logic and not TSDB compactor which we expect to
	// be already tested).
	tsdbPlanner.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{}, nil)

	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Compactor doesn't wait for blocks cleaner to finish, but our test checks for cleaner metrics.
	require.NoError(t, c.blocksCleaner.AwaitRunning(context.Background()))

	// Wait until a run has completed.
	test.Poll(t, 5*time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsCompleted)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	// Ensure a plan has been executed for the blocks of each user.
	tsdbPlanner.AssertNumberOfCalls(t, "Plan", 2)

	assert.ElementsMatch(t, []string{
		`level=info component=compactor msg="waiting until compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="discovering users from bucket"`,
		`level=info component=compactor msg="discovered users from bucket" users=2`,
		`level=info component=compactor msg="starting compaction of user blocks" user=user-1`,
		`level=info component=compactor user=user-1 msg="start sync of metas"`,
		`level=info component=compactor user=user-1 msg="start of GC"`,
		`level=debug component=compactor user=user-1 msg="grouper found a compactable blocks group" groupKey=0@17241709254077376921-merge--1574776800000-1574784000000 job="stage: merge, range start: 1574776800000, range end: 1574784000000, shard: , blocks: 01DTVP434PA9VFXSW2JKB3392D (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC),01FSTQ95C8FS0ZAGTQS2EF1NEG (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC)"`,
		`level=info component=compactor user=user-1 msg="start of compactions"`,
		`level=info component=compactor user=user-1 groupKey=0@17241709254077376921-merge--1574776800000-1574784000000 job_type=merge msg="compaction job succeeded" block_count=2`,
		`level=info component=compactor user=user-1 msg="compaction iterations done"`,
		`level=info component=compactor msg="successfully compacted user blocks" user=user-1`,
		`level=info component=compactor msg="starting compaction of user blocks" user=user-2`,
		`level=info component=compactor user=user-2 msg="start sync of metas"`,
		`level=info component=compactor user=user-2 msg="start of GC"`,
		`level=debug component=compactor user=user-2 msg="grouper found a compactable blocks group" groupKey=0@17241709254077376921-merge--1574776800000-1574784000000 job="stage: merge, range start: 1574776800000, range end: 1574784000000, shard: , blocks: 01DTW0ZCPDDNV4BV83Q2SV4QAZ (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC),01FSV54G6QFQH1G9QE93G3B9TB (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC)"`,
		`level=info component=compactor user=user-2 msg="start of compactions"`,
		`level=info component=compactor user=user-2 groupKey=0@17241709254077376921-merge--1574776800000-1574784000000 job_type=merge msg="compaction job succeeded" block_count=2`,
		`level=info component=compactor user=user-2 msg="compaction iterations done"`,
		`level=info component=compactor msg="successfully compacted user blocks" user=user-2`,
	}, removeIgnoredLogs(strings.Split(strings.TrimSpace(logs.String()), "\n")))

	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# TYPE cortex_compactor_runs_started_total counter
		# HELP cortex_compactor_runs_started_total Total number of compaction runs started.
		cortex_compactor_runs_started_total 1

		# TYPE cortex_compactor_runs_completed_total counter
		# HELP cortex_compactor_runs_completed_total Total number of compaction runs successfully completed.
		cortex_compactor_runs_completed_total 1

		# TYPE cortex_compactor_runs_failed_total counter
		# HELP cortex_compactor_runs_failed_total Total number of compaction runs failed.
		cortex_compactor_runs_failed_total{reason="error"} 0
		cortex_compactor_runs_failed_total{reason="shutdown"} 0

		# HELP cortex_compactor_group_compaction_runs_completed_total Total number of group completed compaction runs. This also includes compactor group runs that resulted with no compaction.
		# TYPE cortex_compactor_group_compaction_runs_completed_total counter
		cortex_compactor_group_compaction_runs_completed_total 2

		# HELP cortex_compactor_group_compaction_runs_started_total Total number of group compaction attempts.
		# TYPE cortex_compactor_group_compaction_runs_started_total counter
		cortex_compactor_group_compaction_runs_started_total 2

		# HELP cortex_compactor_group_compactions_failures_total Total number of failed group compactions.
		# TYPE cortex_compactor_group_compactions_failures_total counter
		cortex_compactor_group_compactions_failures_total 0

		# HELP cortex_compactor_group_compactions_total Total number of group compaction attempts that resulted in new block(s).
		# TYPE cortex_compactor_group_compactions_total counter
		cortex_compactor_group_compactions_total 0

		# HELP cortex_compactor_blocks_marked_for_deletion_total Total number of blocks marked for deletion in compactor.
		# TYPE cortex_compactor_blocks_marked_for_deletion_total counter
		cortex_compactor_blocks_marked_for_deletion_total{reason="compaction"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="partial"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="retention"} 0
	`),
		"cortex_compactor_runs_started_total",
		"cortex_compactor_runs_completed_total",
		"cortex_compactor_runs_failed_total",
		"cortex_compactor_group_compaction_runs_completed_total",
		"cortex_compactor_group_compaction_runs_started_total",
		"cortex_compactor_group_compactions_failures_total",
		"cortex_compactor_group_compactions_total",
		"cortex_compactor_blocks_marked_for_deletion_total",
	))
}

func TestMultitenantCompactor_ShouldCompactOnlyUsersOwnedByTheInstanceOnShardingEnabledAndMultipleInstancesRunning(t *testing.T) {
	t.Parallel()

	numUsers := 100

	// Setup user IDs
	userIDs := make([]string, 0, numUsers)
	for i := 1; i <= numUsers; i++ {
		userIDs = append(userIDs, fmt.Sprintf("user-%d", i))
	}

	// Mock the bucket to contain all users, each one with one block.
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", userIDs, nil)
	for _, userID := range userIDs {
		bucketClient.MockIter(userID+"/", []string{userID + "/01DTVP434PA9VFXSW2JKB3392D"}, nil)
		bucketClient.MockIter(userID+"/markers/", nil, nil)
		bucketClient.MockExists(path.Join(userID, mimir_tsdb.TenantDeletionMarkPath), false, nil)
		bucketClient.MockGet(userID+"/01DTVP434PA9VFXSW2JKB3392D/meta.json", mockBlockMetaJSON("01DTVP434PA9VFXSW2JKB3392D"), nil)
		bucketClient.MockGet(userID+"/01DTVP434PA9VFXSW2JKB3392D/deletion-mark.json", "", nil)
		bucketClient.MockGet(userID+"/01DTVP434PA9VFXSW2JKB3392D/no-compact-mark.json", "", nil)
		bucketClient.MockGet(userID+"/bucket-index.json.gz", "", nil)
		bucketClient.MockUpload(userID+"/bucket-index.json.gz", nil)
	}

	// Create a shared KV Store
	kvstore, closer := consul.NewInMemoryClient(ring.GetCodec(), log.NewNopLogger(), nil)
	t.Cleanup(func() { assert.NoError(t, closer.Close()) })

	// Create two compactors
	var compactors []*MultitenantCompactor
	var logs []*concurrency.SyncBuffer

	for i := 1; i <= 2; i++ {
		cfg := prepareConfig(t)
		cfg.ShardingRing.Common.InstanceID = fmt.Sprintf("compactor-%d", i)
		cfg.ShardingRing.Common.InstanceAddr = fmt.Sprintf("127.0.0.%d", i)
		cfg.ShardingRing.WaitStabilityMinDuration = 3 * time.Second
		cfg.ShardingRing.WaitStabilityMaxDuration = 10 * time.Second
		cfg.ShardingRing.Common.KVStore.Mock = kvstore

		var limits validation.Limits
		flagext.DefaultValues(&limits)
		limits.CompactorTenantShardSize = 1
		overrides := validation.NewOverrides(limits, nil)

		c, _, tsdbPlanner, l, _ := prepareWithConfigProvider(t, cfg, bucketClient, overrides)
		defer services.StopAndAwaitTerminated(context.Background(), c) //nolint:errcheck

		compactors = append(compactors, c)
		logs = append(logs, l)

		// Mock the planner as if there's no compaction to do,
		// in order to simplify tests (all in all, we just want to
		// test our logic and not TSDB compactor which we expect to
		// be already tested).
		tsdbPlanner.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{}, nil)
	}

	// Start all compactors
	for _, c := range compactors {
		require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))
	}

	// Wait until a run has been completed on each compactor
	test.Poll(t, 30*time.Second, true, func() interface{} {
		for _, c := range compactors {
			if prom_testutil.ToFloat64(c.compactionRunsCompleted) < 1.0 {
				return false
			}
		}
		return true
	})

	// Ensure that each user has been compacted by the correct instance
	for _, userID := range userIDs {
		_, l, err := findCompactorByUserID(compactors, logs, userID)
		require.NoError(t, err)
		assert.Contains(t, l.String(), fmt.Sprintf(`level=info component=compactor msg="successfully compacted user blocks" user=%s`, userID))
	}
}

func TestMultitenantCompactor_ShouldFailWithInvalidTSDBCompactOutput(t *testing.T) {
	const user = "user-1"

	// Two blocks with overlapping time range
	sourceBlock1Spec := []*block.SeriesSpec{
		{
			Labels: labels.FromStrings("case", "source_spec_1"),
			Chunks: []chunks.Meta{
				must(chunks.ChunkFromSamples([]chunks.Sample{
					testutil.Sample{TS: 1000, Val: 1000},
					testutil.Sample{TS: 2000, Val: 2000}})),
			},
		},
	}

	sourceBlock2Spec := []*block.SeriesSpec{
		{
			Labels: labels.FromStrings("case", "source_spec_2"),
			Chunks: []chunks.Meta{
				must(chunks.ChunkFromSamples([]chunks.Sample{
					testutil.Sample{TS: 1500, Val: 1500},
					testutil.Sample{TS: 2500, Val: 2500}})),
			},
		},
	}

	// Block with sufficient time range so compaction job gets triggered
	sourceBlock3Spec := []*block.SeriesSpec{
		{
			Labels: labels.FromStrings("case", "source_spec_3"),
			Chunks: []chunks.Meta{
				must(chunks.ChunkFromSamples([]chunks.Sample{
					testutil.Sample{TS: 0, Val: 0},
					testutil.Sample{TS: 2*time.Hour.Milliseconds() - 1}})),
			},
		},
	}

	// Compacted block not containing minTime/maxTime from source blocks
	compactedBlockSpec := []*block.SeriesSpec{
		{
			Labels: labels.FromStrings("case", "compacted_spec"),
			Chunks: []chunks.Meta{
				must(chunks.ChunkFromSamples([]chunks.Sample{
					testutil.Sample{TS: 1250, Val: 1250},
					testutil.Sample{TS: 2250, Val: 2250}})),
			},
		},
	}

	storageDir := t.TempDir()

	meta1, err := block.GenerateBlockFromSpec(filepath.Join(storageDir, user), sourceBlock1Spec)
	require.NoError(t, err)
	meta2, err := block.GenerateBlockFromSpec(filepath.Join(storageDir, user), sourceBlock2Spec)
	require.NoError(t, err)
	_, err = block.GenerateBlockFromSpec(filepath.Join(storageDir, user), sourceBlock3Spec)
	require.NoError(t, err)

	bkt, err := filesystem.NewBucketClient(filesystem.Config{Directory: storageDir})
	require.NoError(t, err)

	cfg := prepareConfig(t)
	cfg.CompactionRetries = 1 // No need to retry as we're testing for failure
	c, tsdbCompactor, tsdbPlanner, logs, _ := prepare(t, cfg, bkt)

	tsdbPlanner.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{meta1, meta2}, nil).Once()
	tsdbPlanner.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{}, nil).Once()
	mockCall := tsdbCompactor.On("Compact", mock.Anything, mock.Anything, mock.Anything)
	mockCall.RunFn = func(args mock.Arguments) {
		dir := args.Get(0).(string)

		compactedMeta, err := block.GenerateBlockFromSpec(dir, compactedBlockSpec)
		require.NoError(t, err)
		f, err := os.OpenFile(filepath.Join(dir, compactedMeta.ULID.String(), "tombstones"), os.O_RDONLY|os.O_CREATE, 0666)
		require.NoError(t, err)
		defer f.Close()

		mockCall.ReturnArguments = mock.Arguments{[]ulid.ULID{compactedMeta.ULID}, nil}
	}

	// Start the compactor
	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Compaction block verification should fail due to invalid output block
	test.Poll(t, 5*time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.bucketCompactorMetrics.compactionBlocksVerificationFailed)
	})

	// Stop the compactor.
	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	// Check logs for compacted block verification failure
	assert.Contains(t, logs.String(), "compacted block(s) do not contain minTime 1000 and maxTime 2501 from the source blocks")
}

func TestMultitenantCompactor_ShouldSkipCompactionForJobsNoMoreOwnedAfterPlanning(t *testing.T) {
	t.Parallel()

	// Mock the bucket to contain one user with two non-overlapping blocks (we expect two compaction jobs to be scheduled
	// for the splitting stage).
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", []string{"user-1"}, nil)
	bucketClient.MockExists(path.Join("user-1", mimir_tsdb.TenantDeletionMarkPath), false, nil)
	bucketClient.MockIter("user-1/", []string{"user-1/01DTVP434PA9VFXSW2JK000001", "user-1/01DTVP434PA9VFXSW2JK000002"}, nil)
	bucketClient.MockIter("user-1/markers/", nil, nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JK000001/meta.json", mockBlockMetaJSONWithTimeRange("01DTVP434PA9VFXSW2JK000001", 1574776800000, 1574784000000), nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JK000001/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JK000001/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JK000002/meta.json", mockBlockMetaJSONWithTimeRange("01DTVP434PA9VFXSW2JK000002", 1574863200000, 1574870400000), nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JK000002/deletion-mark.json", "", nil)
	bucketClient.MockGet("user-1/01DTVP434PA9VFXSW2JK000002/no-compact-mark.json", "", nil)
	bucketClient.MockGet("user-1/bucket-index.json.gz", "", nil)
	bucketClient.MockUpload("user-1/bucket-index.json.gz", nil)

	ringStore, closer := consul.NewInMemoryClient(ring.GetCodec(), log.NewNopLogger(), nil)
	t.Cleanup(func() { assert.NoError(t, closer.Close()) })

	cfg := prepareConfig(t)
	cfg.CompactionConcurrency = 1
	cfg.ShardingRing.Common.InstanceID = "compactor-1"
	cfg.ShardingRing.Common.InstanceAddr = "1.2.3.4"
	cfg.ShardingRing.Common.KVStore.Mock = ringStore

	limits := newMockConfigProvider()
	limits.splitAndMergeShards = map[string]int{"user-1": 4}
	limits.splitGroups = map[string]int{"user-1": 4}

	c, _, tsdbPlanner, logs, registry := prepareWithConfigProvider(t, cfg, bucketClient, limits)

	// Mock the planner as if there's no compaction to do, in order to simplify tests.
	tsdbPlanner.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{}, nil).Run(func(mock.Arguments) {
		// As soon as the first Plan() is called by the compactor, we do switch
		// the instance to LEAVING state. This way,  after this call, we expect the compactor
		// to skip next compaction job because not owned anymore by this instance.
		require.NoError(t, c.ringLifecycler.ChangeState(context.Background(), ring.LEAVING))

		// Wait until the compactor ring client has updated.
		test.Poll(t, time.Second, 0, func() interface{} {
			set, _ := c.ring.GetAllHealthy(RingOp)
			return len(set.Instances)
		})
	})

	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Compactor doesn't wait for blocks cleaner to finish, but our test checks for cleaner metrics.
	require.NoError(t, c.blocksCleaner.AwaitRunning(context.Background()))

	// Wait until a run has completed.
	test.Poll(t, 5*time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsCompleted)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	// We expect only 1 compaction job has been expected, while the 2nd has been skipped.
	tsdbPlanner.AssertNumberOfCalls(t, "Plan", 1)

	assert.ElementsMatch(t, []string{
		`level=info component=compactor msg="waiting until compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="compactor is ACTIVE in the ring"`,
		`level=info component=compactor msg="discovering users from bucket"`,
		`level=info component=compactor msg="discovered users from bucket" users=1`,
		`level=info component=compactor msg="starting compaction of user blocks" user=user-1`,
		`level=info component=compactor user=user-1 msg="start sync of metas"`,
		`level=info component=compactor user=user-1 msg="start of GC"`,
		`level=info component=compactor user=user-1 msg="start of compactions"`,
		`level=debug component=compactor user=user-1 msg="grouper found a compactable blocks group" groupKey=0@17241709254077376921-split-4_of_4-1574776800000-1574784000000 job="stage: split, range start: 1574776800000, range end: 1574784000000, shard: 4_of_4, blocks: 01DTVP434PA9VFXSW2JK000001 (min time: 2019-11-26 14:00:00 +0000 UTC, max time: 2019-11-26 16:00:00 +0000 UTC)"`,
		`level=debug component=compactor user=user-1 msg="grouper found a compactable blocks group" groupKey=0@17241709254077376921-split-1_of_4-1574863200000-1574870400000 job="stage: split, range start: 1574863200000, range end: 1574870400000, shard: 1_of_4, blocks: 01DTVP434PA9VFXSW2JK000002 (min time: 2019-11-27 14:00:00 +0000 UTC, max time: 2019-11-27 16:00:00 +0000 UTC)"`,
		// The ownership check is failing because, to keep this test simple, we've just switched
		// the instance state to LEAVING and there are no other instances in the ring.
		`level=info component=compactor user=user-1 groupKey=0@17241709254077376921-split-4_of_4-1574776800000-1574784000000 job_type=split msg="compaction job succeeded" block_count=1`,
		`level=info component=compactor user=user-1 msg="skipped compaction because unable to check whether the job is owned by the compactor instance" groupKey=0@17241709254077376921-split-1_of_4-1574863200000-1574870400000 err="at least 1 live replicas required, could only find 0 - unhealthy instances: 1.2.3.4:0"`,
		`level=info component=compactor user=user-1 msg="compaction iterations done"`,
		`level=info component=compactor msg="successfully compacted user blocks" user=user-1`,
	}, removeIgnoredLogs(strings.Split(strings.TrimSpace(logs.String()), "\n")))

	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# TYPE cortex_compactor_runs_started_total counter
		# HELP cortex_compactor_runs_started_total Total number of compaction runs started.
		cortex_compactor_runs_started_total 1

		# TYPE cortex_compactor_runs_completed_total counter
		# HELP cortex_compactor_runs_completed_total Total number of compaction runs successfully completed.
		cortex_compactor_runs_completed_total 1

		# TYPE cortex_compactor_runs_failed_total counter
		# HELP cortex_compactor_runs_failed_total Total number of compaction runs failed.
		cortex_compactor_runs_failed_total{reason="error"} 0
		cortex_compactor_runs_failed_total{reason="shutdown"} 0

		# HELP cortex_compactor_group_compaction_runs_completed_total Total number of group completed compaction runs. This also includes compactor group runs that resulted with no compaction.
		# TYPE cortex_compactor_group_compaction_runs_completed_total counter
		cortex_compactor_group_compaction_runs_completed_total 1

		# HELP cortex_compactor_group_compaction_runs_started_total Total number of group compaction attempts.
		# TYPE cortex_compactor_group_compaction_runs_started_total counter
		cortex_compactor_group_compaction_runs_started_total 1

		# HELP cortex_compactor_group_compactions_failures_total Total number of failed group compactions.
		# TYPE cortex_compactor_group_compactions_failures_total counter
		cortex_compactor_group_compactions_failures_total 0

		# HELP cortex_compactor_group_compactions_total Total number of group compaction attempts that resulted in new block(s).
		# TYPE cortex_compactor_group_compactions_total counter
		cortex_compactor_group_compactions_total 0

		# HELP cortex_compactor_blocks_marked_for_deletion_total Total number of blocks marked for deletion in compactor.
		# TYPE cortex_compactor_blocks_marked_for_deletion_total counter
		cortex_compactor_blocks_marked_for_deletion_total{reason="compaction"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="partial"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="retention"} 0
	`),
		"cortex_compactor_runs_started_total",
		"cortex_compactor_runs_completed_total",
		"cortex_compactor_runs_failed_total",
		"cortex_compactor_group_compaction_runs_completed_total",
		"cortex_compactor_group_compaction_runs_started_total",
		"cortex_compactor_group_compactions_failures_total",
		"cortex_compactor_group_compactions_total",
		"cortex_compactor_blocks_marked_for_deletion_total",
	))
}

func TestMultitenantCompactor_ShouldSkipCompactionForJobsWithFirstLevelCompactionBlocksAndWaitPeriodNotElapsed(t *testing.T) {
	t.Parallel()

	storageDir := t.TempDir()
	bucketClient, err := filesystem.NewBucketClient(filesystem.Config{Directory: storageDir})
	require.NoError(t, err)

	// Mock two tenants, each with 2 overlapping blocks.
	spec := []*block.SeriesSpec{{
		Labels: labels.FromStrings(labels.MetricName, "series_1"),
		Chunks: []chunks.Meta{must(chunks.ChunkFromSamples([]chunks.Sample{
			testutil.Sample{TS: 1574776800000, Val: 0},
			testutil.Sample{TS: 1574783999999, Val: 0},
		}))},
	}}

	user1Meta1, err := block.GenerateBlockFromSpec(filepath.Join(storageDir, "user-1"), spec)
	require.NoError(t, err)
	user1Meta2, err := block.GenerateBlockFromSpec(filepath.Join(storageDir, "user-1"), spec)
	require.NoError(t, err)
	user2Meta1, err := block.GenerateBlockFromSpec(filepath.Join(storageDir, "user-2"), spec)
	require.NoError(t, err)
	user2Meta2, err := block.GenerateBlockFromSpec(filepath.Join(storageDir, "user-2"), spec)
	require.NoError(t, err)

	// Mock the last modified timestamp returned for each of the block's meta.json.
	const waitPeriod = 10 * time.Minute
	bucketClient = &bucketWithMockedAttributes{
		Bucket: bucketClient,
		customAttributes: map[string]objstore.ObjectAttributes{
			path.Join("user-1", user1Meta1.ULID.String(), block.MetaFilename): {LastModified: time.Now().Add(-20 * time.Minute)},
			path.Join("user-1", user1Meta2.ULID.String(), block.MetaFilename): {LastModified: time.Now().Add(-20 * time.Minute)},
			path.Join("user-2", user2Meta1.ULID.String(), block.MetaFilename): {LastModified: time.Now().Add(-20 * time.Minute)},
			path.Join("user-2", user2Meta2.ULID.String(), block.MetaFilename): {LastModified: time.Now().Add(-5 * time.Minute)},
		},
	}

	cfg := prepareConfig(t)
	cfg.CompactionWaitPeriod = waitPeriod
	c, _, tsdbPlanner, logs, registry := prepare(t, cfg, bucketClient)

	// Mock the planner as if there's no compaction to do, in order to simplify tests.
	tsdbPlanner.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{}, nil)

	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Compactor doesn't wait for blocks cleaner to finish, but our test checks for cleaner metrics.
	require.NoError(t, c.blocksCleaner.AwaitRunning(context.Background()))

	// Wait until a run has completed.
	test.Poll(t, 5*time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsCompleted)
	})

	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	// We expect only 1 compaction job has been expected, while the 2nd has been skipped.
	tsdbPlanner.AssertNumberOfCalls(t, "Plan", 1)

	// Ensure the skipped compaction job is the expected one.
	assert.Contains(t, strings.Split(strings.TrimSpace(logs.String()), "\n"),
		fmt.Sprintf(`level=info component=compactor user=user-2 msg="skipping compaction job because blocks in this job were uploaded too recently (within wait period)" groupKey=0@17241709254077376921-merge--1574776800000-1574784000000 waitPeriodNotElapsedFor="%s (min time: 1574776800000, max time: 1574784000000)"`, user2Meta2.ULID.String()))

	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# TYPE cortex_compactor_runs_started_total counter
		# HELP cortex_compactor_runs_started_total Total number of compaction runs started.
		cortex_compactor_runs_started_total 1

		# TYPE cortex_compactor_runs_completed_total counter
		# HELP cortex_compactor_runs_completed_total Total number of compaction runs successfully completed.
		cortex_compactor_runs_completed_total 1

		# TYPE cortex_compactor_runs_failed_total counter
		# HELP cortex_compactor_runs_failed_total Total number of compaction runs failed.
		cortex_compactor_runs_failed_total{reason="error"} 0
		cortex_compactor_runs_failed_total{reason="shutdown"} 0

		# HELP cortex_compactor_group_compaction_runs_completed_total Total number of group completed compaction runs. This also includes compactor group runs that resulted with no compaction.
		# TYPE cortex_compactor_group_compaction_runs_completed_total counter
		cortex_compactor_group_compaction_runs_completed_total 1

		# HELP cortex_compactor_group_compaction_runs_started_total Total number of group compaction attempts.
		# TYPE cortex_compactor_group_compaction_runs_started_total counter
		cortex_compactor_group_compaction_runs_started_total 1

		# HELP cortex_compactor_group_compactions_failures_total Total number of failed group compactions.
		# TYPE cortex_compactor_group_compactions_failures_total counter
		cortex_compactor_group_compactions_failures_total 0

		# HELP cortex_compactor_group_compactions_total Total number of group compaction attempts that resulted in new block(s).
		# TYPE cortex_compactor_group_compactions_total counter
		cortex_compactor_group_compactions_total 0

		# HELP cortex_compactor_blocks_marked_for_deletion_total Total number of blocks marked for deletion in compactor.
		# TYPE cortex_compactor_blocks_marked_for_deletion_total counter
		cortex_compactor_blocks_marked_for_deletion_total{reason="compaction"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="partial"} 0
		cortex_compactor_blocks_marked_for_deletion_total{reason="retention"} 0
	`),
		"cortex_compactor_runs_started_total",
		"cortex_compactor_runs_completed_total",
		"cortex_compactor_runs_failed_total",
		"cortex_compactor_group_compaction_runs_completed_total",
		"cortex_compactor_group_compaction_runs_started_total",
		"cortex_compactor_group_compactions_failures_total",
		"cortex_compactor_group_compactions_total",
		"cortex_compactor_blocks_marked_for_deletion_total",
	))
}

func createCustomTSDBBlock(t *testing.T, bkt objstore.Bucket, userID string, externalLabels map[string]string, appendFunc func(*tsdb.DB)) ulid.ULID {
	// Create a temporary dir for TSDB.
	tempDir := t.TempDir()

	// Create a temporary dir for the snapshot.
	snapshotDir := t.TempDir()

	// Create a new TSDB.
	db, err := tsdb.Open(tempDir, nil, nil, &tsdb.Options{
		MinBlockDuration:  int64(2 * 60 * 60 * 1000), // 2h period
		MaxBlockDuration:  int64(2 * 60 * 60 * 1000), // 2h period
		RetentionDuration: int64(15 * 86400 * 1000),  // 15 days
	}, nil)
	require.NoError(t, err)

	db.DisableCompactions()

	appendFunc(db)

	require.NoError(t, db.Compact(context.Background()))
	require.NoError(t, db.Snapshot(snapshotDir, true))

	// Look for the created block (we expect one).
	entries, err := os.ReadDir(snapshotDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.True(t, entries[0].IsDir())

	blockID, err := ulid.Parse(entries[0].Name())
	require.NoError(t, err)

	// Inject Thanos external labels to the block.
	meta := block.ThanosMeta{
		Labels: externalLabels,
		Source: "test",
	}
	_, err = block.InjectThanosMeta(log.NewNopLogger(), filepath.Join(snapshotDir, blockID.String()), meta, nil)
	require.NoError(t, err)

	// Copy the block files to the bucket.
	srcRoot := filepath.Join(snapshotDir, blockID.String())
	require.NoError(t, filepath.Walk(srcRoot, func(file string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Read the file content in memory.
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}

		// Upload it to the bucket.
		relPath, err := filepath.Rel(srcRoot, file)
		if err != nil {
			return err
		}

		return bkt.Upload(context.Background(), path.Join(userID, blockID.String(), relPath), bytes.NewReader(content))
	}))

	return blockID
}

func createTSDBBlock(t *testing.T, bkt objstore.Bucket, userID string, minT, maxT int64, numSeries int, externalLabels map[string]string) ulid.ULID {
	return createCustomTSDBBlock(t, bkt, userID, externalLabels, func(db *tsdb.DB) {
		appendSample := func(seriesID int, ts int64, value float64) {
			lbls := labels.FromStrings("series_id", strconv.Itoa(seriesID))

			app := db.Appender(context.Background())
			_, err := app.Append(0, lbls, ts, value)
			require.NoError(t, err)

			err = app.Commit()
			require.NoError(t, err)
		}

		seriesID := 0

		// Append a sample for each series, spreading it between minT and maxT-1 (both included).
		// Since we append one more series below, here we create N-1 series.
		if numSeries > 1 {
			for ts := minT; ts < maxT; ts += (maxT - minT) / int64(numSeries-1) {
				appendSample(seriesID, ts, float64(seriesID))
				seriesID++
			}
		}

		// Guarantee a series with a sample at time maxT-1
		appendSample(seriesID, maxT-1, float64(seriesID))
	})
}

func createDeletionMark(t *testing.T, bkt objstore.Bucket, userID string, blockID ulid.ULID, deletionTime time.Time) {
	content := mockDeletionMarkJSON(blockID.String(), deletionTime)
	blockPath := path.Join(userID, blockID.String())
	markPath := path.Join(blockPath, block.DeletionMarkFilename)

	require.NoError(t, bkt.Upload(context.Background(), markPath, strings.NewReader(content)))
}

func findCompactorByUserID(compactors []*MultitenantCompactor, logs []*concurrency.SyncBuffer, userID string) (*MultitenantCompactor, *concurrency.SyncBuffer, error) {
	var compactor *MultitenantCompactor
	var log *concurrency.SyncBuffer

	for i, c := range compactors {
		owned, err := c.shardingStrategy.compactorOwnsUser(userID)
		if err != nil {
			return nil, nil, err
		}

		// Ensure the user is not owned by multiple compactors
		if owned && compactor != nil {
			return nil, nil, fmt.Errorf("user %s owned by multiple compactors", userID)
		}
		if owned {
			compactor = c
			log = logs[i]
		}
	}

	// Return an error if we've not been able to find it
	if compactor == nil {
		return nil, nil, fmt.Errorf("user %s not owned by any compactor", userID)
	}

	return compactor, log, nil
}

func removeIgnoredLogs(input []string) []string {
	ignoredLogStringsMap := map[string]struct{}{

		// Since we moved to the component logger from the global logger for the ring in dskit these lines are now expected but are just ring setup information.
		`level=info component=compactor msg="ring doesn't exist in KV store yet"`:                                                                                 {},
		`level=info component=compactor msg="not loading tokens from file, tokens file path is empty"`:                                                            {},
		`level=info component=compactor msg="tokens verification succeeded" ring=compactor`:                                                                       {},
		`level=info component=compactor msg="waiting stable tokens" ring=compactor`:                                                                               {},
		`level=info component=compactor msg="instance not found in ring, adding with no tokens" ring=compactor`:                                                   {},
		`level=debug component=compactor msg="JoinAfter expired" ring=compactor`:                                                                                  {},
		`level=info component=compactor msg="auto-joining cluster after timeout" ring=compactor`:                                                                  {},
		`level=info component=compactor msg="lifecycler loop() exited gracefully" ring=compactor`:                                                                 {},
		`level=info component=compactor msg="changing instance state from" old_state=ACTIVE new_state=LEAVING ring=compactor`:                                     {},
		`level=error component=compactor msg="failed to set state to LEAVING" ring=compactor err="Changing instance state from LEAVING -> LEAVING is disallowed"`: {},
		`level=error component=compactor msg="failed to set state to LEAVING" ring=compactor err="Changing instance state from JOINING -> LEAVING is disallowed"`: {},
		`level=info component=compactor msg="unregistering instance from ring" ring=compactor`:                                                                    {},
		`level=info component=compactor msg="instance removed from the ring" ring=compactor`:                                                                      {},
		`level=info component=compactor msg="observing tokens before going ACTIVE" ring=compactor`:                                                                {},
		`level=info component=compactor msg="lifecycler entering final sleep before shutdown" final_sleep=0s`:                                                     {},
		`level=info component=compactor msg="ring lifecycler is shutting down" ring=compactor`:                                                                    {},
	}

	out := make([]string, 0, len(input))

	for i := 0; i < len(input); i++ {
		log := input[i]
		if strings.Contains(log, "block.MetaFetcher") || strings.Contains(log, "instance not found in the ring") {
			continue
		}

		if _, exists := ignoredLogStringsMap[log]; exists {
			continue
		}

		out = append(out, log)
	}

	return out
}

func prepareConfig(t *testing.T) Config {
	compactorCfg := Config{}
	flagext.DefaultValues(&compactorCfg)

	compactorCfg.retryMinBackoff = 0
	compactorCfg.retryMaxBackoff = 0

	// Use settings that ensure things will be done concurrently, verifying ordering assumptions.
	// Helps to expose bugs such as https://github.com/prometheus/prometheus/pull/10108
	compactorCfg.MaxOpeningBlocksConcurrency = 3
	compactorCfg.MaxClosingBlocksConcurrency = 3

	// Do not wait for ring stability by default, in order to speed up tests.
	compactorCfg.ShardingRing.WaitStabilityMinDuration = 0
	compactorCfg.ShardingRing.WaitStabilityMaxDuration = 0

	// Set lower timeout for waiting on compactor to become ACTIVE in the ring for unit tests
	compactorCfg.ShardingRing.WaitActiveInstanceTimeout = 5 * time.Second

	// Inject default KV store. Must be overridden if "real" sharding is required.
	inmem, closer := consul.NewInMemoryClient(ring.GetCodec(), log.NewNopLogger(), nil)
	t.Cleanup(func() { _ = closer.Close() })
	compactorCfg.ShardingRing.Common.KVStore.Mock = inmem
	compactorCfg.ShardingRing.Common.InstanceAddr = "localhost"

	// The new default is 25m, but tests rely on the previous value of 0s
	compactorCfg.CompactionWaitPeriod = 0

	return compactorCfg
}

func prepare(t *testing.T, compactorCfg Config, bucketClient objstore.Bucket) (*MultitenantCompactor, *tsdbCompactorMock, *tsdbPlannerMock, *concurrency.SyncBuffer, prometheus.Gatherer) {
	var limits validation.Limits
	flagext.DefaultValues(&limits)
	overrides := validation.NewOverrides(limits, nil)

	return prepareWithConfigProvider(t, compactorCfg, bucketClient, overrides)
}

func prepareWithConfigProvider(t *testing.T, compactorCfg Config, bucketClient objstore.Bucket, limits ConfigProvider) (*MultitenantCompactor, *tsdbCompactorMock, *tsdbPlannerMock, *concurrency.SyncBuffer, prometheus.Gatherer) {
	storageCfg := mimir_tsdb.BlocksStorageConfig{}
	flagext.DefaultValues(&storageCfg)

	// Create a temporary directory for compactor data.
	dataDir := t.TempDir()

	compactorCfg.DataDir = dataDir

	tsdbCompactor := &tsdbCompactorMock{}
	tsdbPlanner := &tsdbPlannerMock{}
	logs := &concurrency.SyncBuffer{}
	logger := &componentLogger{component: "compactor", log: log.NewLogfmtLogger(logs)}
	registry := prometheus.NewRegistry()

	bucketClientFactory := func(context.Context) (objstore.Bucket, error) {
		return bucketClient, nil
	}

	blocksCompactorFactory := func(context.Context, Config, log.Logger, prometheus.Registerer) (Compactor, Planner, error) {
		return tsdbCompactor, tsdbPlanner, nil
	}

	c, err := newMultitenantCompactor(compactorCfg, storageCfg, limits, logger, registry, bucketClientFactory, splitAndMergeGrouperFactory, blocksCompactorFactory)
	require.NoError(t, err)

	return c, tsdbCompactor, tsdbPlanner, logs, registry
}

type componentLogger struct {
	component string
	log       log.Logger
}

func (c *componentLogger) Log(keyvals ...interface{}) error {
	// Remove duration fields.
	for ix := 0; ix+1 < len(keyvals); {
		k := keyvals[ix]

		ks, ok := k.(string)
		if !ok {
			ix += 2
			continue
		}

		if ks == "duration" || ks == "duration_ms" {
			keyvals = append(keyvals[:ix], keyvals[ix+2:]...)
		} else {
			ix += 2
		}
	}

	for ix := 0; ix+1 < len(keyvals); ix += 2 {
		k := keyvals[ix]
		v := keyvals[ix+1]

		ks, ok := k.(string)
		if !ok {
			continue
		}
		vs, ok := v.(string)
		if !ok {
			continue
		}
		if ks == "component" && vs == c.component {
			return c.log.Log(keyvals...)
		}
	}
	return nil
}

type tsdbCompactorMock struct {
	mock.Mock
}

func (m *tsdbCompactorMock) Plan(dir string) ([]string, error) {
	args := m.Called(dir)
	return args.Get(0).([]string), args.Error(1)
}

func (m *tsdbCompactorMock) Write(dest string, b tsdb.BlockReader, mint, maxt int64, parent *tsdb.BlockMeta) ([]ulid.ULID, error) {
	args := m.Called(dest, b, mint, maxt, parent)
	return args.Get(0).([]ulid.ULID), args.Error(1)
}

func (m *tsdbCompactorMock) Compact(dest string, dirs []string, open []*tsdb.Block) ([]ulid.ULID, error) {
	args := m.Called(dest, dirs, open)
	return args.Get(0).([]ulid.ULID), args.Error(1)
}

func (m *tsdbCompactorMock) CompactWithSplitting(dest string, dirs []string, open []*tsdb.Block, shardCount uint64) (result []ulid.ULID, _ error) {
	args := m.Called(dest, dirs, open, shardCount)
	return args.Get(0).([]ulid.ULID), args.Error(1)
}

type tsdbPlannerMock struct {
	mock.Mock
}

func (m *tsdbPlannerMock) Plan(ctx context.Context, metasByMinTime []*block.Meta) ([]*block.Meta, error) {
	args := m.Called(ctx, metasByMinTime)
	return args.Get(0).([]*block.Meta), args.Error(1)
}

func mockBlockMetaJSON(id string) string {
	return mockBlockMetaJSONWithTimeRange(id, 1574776800000, 1574784000000)
}

func mockBlockMetaJSONWithTimeRange(id string, mint, maxt int64) string {
	return mockBlockMetaJSONWithTimeRangeAndLabels(id, mint, maxt, nil)
}

func mockBlockMetaJSONWithTimeRangeAndLabels(id string, mint, maxt int64, lbls map[string]string) string {
	content, err := json.Marshal(blockMeta(id, mint, maxt, lbls))
	if err != nil {
		panic("failed to marshal mocked block meta")
	}
	return string(content)
}

func blockMeta(id string, mint, maxt int64, lbls map[string]string) *block.Meta {
	return &block.Meta{
		BlockMeta: tsdb.BlockMeta{
			Version: 1,
			ULID:    ulid.MustParse(id),
			MinTime: mint,
			MaxTime: maxt,
			Compaction: tsdb.BlockMetaCompaction{
				Level:   1,
				Sources: []ulid.ULID{ulid.MustParse(id)},
			},
		},
		Thanos: block.ThanosMeta{
			Labels: lbls,
		},
	}
}

func mockDeletionMarkJSON(id string, deletionTime time.Time) string {
	meta := block.DeletionMark{
		Version:      block.DeletionMarkVersion1,
		ID:           ulid.MustParse(id),
		DeletionTime: deletionTime.Unix(),
	}

	content, err := json.Marshal(meta)
	if err != nil {
		panic("failed to marshal mocked block meta")
	}

	return string(content)
}

func TestMultitenantCompactor_DeleteLocalSyncFiles(t *testing.T) {
	numUsers := 10

	// Setup user IDs
	userIDs := make([]string, 0, numUsers)
	for i := 1; i <= numUsers; i++ {
		userIDs = append(userIDs, fmt.Sprintf("user-%d", i))
	}

	inmem := objstore.NewInMemBucket()
	for _, userID := range userIDs {
		id, err := ulid.New(ulid.Now(), rand.Reader)
		require.NoError(t, err)
		require.NoError(t, inmem.Upload(context.Background(), userID+"/"+id.String()+"/meta.json", strings.NewReader(mockBlockMetaJSON(id.String()))))
	}

	// Create a shared KV Store
	kvstore, closer := consul.NewInMemoryClient(ring.GetCodec(), log.NewNopLogger(), nil)
	t.Cleanup(func() { assert.NoError(t, closer.Close()) })

	// Create two compactors
	var compactors []*MultitenantCompactor

	for i := 1; i <= 2; i++ {
		cfg := prepareConfig(t)
		cfg.CompactionInterval = 10 * time.Minute // We will only call compaction manually.

		cfg.ShardingRing.Common.InstanceID = fmt.Sprintf("compactor-%d", i)
		cfg.ShardingRing.Common.InstanceAddr = fmt.Sprintf("127.0.0.%d", i)
		cfg.ShardingRing.WaitStabilityMinDuration = 3 * time.Second
		cfg.ShardingRing.WaitStabilityMaxDuration = 10 * time.Second
		cfg.ShardingRing.Common.KVStore.Mock = kvstore

		// Each compactor will get its own temp dir for storing local files.
		var limits validation.Limits
		flagext.DefaultValues(&limits)
		limits.CompactorTenantShardSize = 1 // Each tenant will belong to single compactor only.
		overrides := validation.NewOverrides(limits, nil)

		c, _, tsdbPlanner, _, _ := prepareWithConfigProvider(t, cfg, inmem, overrides)
		t.Cleanup(func() {
			require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))
		})

		compactors = append(compactors, c)

		// Mock the planner as if there's no compaction to do,
		// in order to simplify tests (all in all, we just want to
		// test our logic and not TSDB compactor which we expect to
		// be already tested).
		tsdbPlanner.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{}, nil)
	}

	require.Equal(t, 2, len(compactors))
	c1 := compactors[0]
	c2 := compactors[1]

	// Start first compactor
	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c1))

	// Wait until a run has been completed on first compactor. This happens as soon as compactor starts.
	test.Poll(t, 10*time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c1.compactionRunsCompleted)
	})

	require.NoError(t, os.Mkdir(c1.metaSyncDirForUser("new-user"), 0600))

	// Verify that first compactor has synced all the users, plus there is one extra we have just created.
	require.Equal(t, numUsers+1, len(c1.listTenantsWithMetaSyncDirectories()))

	// Now start second compactor, and wait until it runs compaction.
	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c2))
	test.Poll(t, 10*time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c2.compactionRunsCompleted)
	})

	// Let's check how many users second compactor has.
	c2Users := len(c2.listTenantsWithMetaSyncDirectories())
	require.NotZero(t, c2Users)

	// Force new compaction cycle on first compactor. It will run the cleanup of un-owned users at the end of compaction cycle.
	c1.compactUsers(context.Background())
	c1Users := len(c1.listTenantsWithMetaSyncDirectories())

	// Now compactor 1 should have cleaned old sync files.
	require.NotEqual(t, numUsers, c1Users)
	require.Equal(t, numUsers, c1Users+c2Users)
}

func TestMultitenantCompactor_ShouldFailCompactionOnTimeout(t *testing.T) {
	t.Parallel()

	// Mock the bucket
	bucketClient := &bucket.ClientMock{}
	bucketClient.MockIter("", []string{}, nil)

	ringStore, closer := consul.NewInMemoryClient(ring.GetCodec(), log.NewNopLogger(), nil)
	t.Cleanup(func() { assert.NoError(t, closer.Close()) })

	cfg := prepareConfig(t)
	cfg.ShardingRing.Common.InstanceID = "compactor-1"
	cfg.ShardingRing.Common.InstanceAddr = "1.2.3.4"
	cfg.ShardingRing.Common.KVStore.Mock = ringStore

	// Set ObservePeriod to longer than the timeout period to mock a timeout while waiting on ring to become ACTIVE
	cfg.ShardingRing.ObservePeriod = time.Second * 10

	c, _, _, _, _ := prepare(t, cfg, bucketClient)

	// Try to start the compactor with a bad consul kv-store. The
	err := services.StartAndAwaitRunning(context.Background(), c)

	// Assert that the compactor timed out
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

type ownUserReason int

const (
	ownUserReasonBlocksCleaner ownUserReason = iota
	ownUserReasonCompactor
)

func TestOwnUser(t *testing.T) {
	type testCase struct {
		compactors      int
		enabledUsers    []string
		disabledUsers   []string
		compactorShards map[string]int

		check func(t *testing.T, comps []*MultitenantCompactor)
	}

	const user1 = "user1"
	const user2 = "another-user"

	testCases := map[string]testCase{
		"5 compactors, sharding enabled, no compactor shard size": {
			compactors:      5,
			compactorShards: nil, // no limits

			check: func(t *testing.T, comps []*MultitenantCompactor) {
				require.Len(t, owningCompactors(t, comps, user1, ownUserReasonCompactor), 5)
				require.Len(t, owningCompactors(t, comps, user1, ownUserReasonBlocksCleaner), 1)

				require.Len(t, owningCompactors(t, comps, user2, ownUserReasonCompactor), 5)
				require.Len(t, owningCompactors(t, comps, user2, ownUserReasonBlocksCleaner), 1)
			},
		},

		"10 compactors, sharding enabled, with non-zero shard sizes": {
			compactors:      10,
			compactorShards: map[string]int{user1: 2, user2: 3},

			check: func(t *testing.T, comps []*MultitenantCompactor) {
				require.Len(t, owningCompactors(t, comps, user1, ownUserReasonCompactor), 2)
				require.Len(t, owningCompactors(t, comps, user1, ownUserReasonBlocksCleaner), 1)
				// Blocks cleanup is done by one of the compactors that "own" the user.
				require.Subset(t, owningCompactors(t, comps, user1, ownUserReasonCompactor), owningCompactors(t, comps, user1, ownUserReasonBlocksCleaner))

				require.Len(t, owningCompactors(t, comps, user2, ownUserReasonCompactor), 3)
				require.Len(t, owningCompactors(t, comps, user2, ownUserReasonBlocksCleaner), 1)
				// Blocks cleanup is done by one of the compactors that "own" the user.
				require.Subset(t, owningCompactors(t, comps, user2, ownUserReasonCompactor), owningCompactors(t, comps, user2, ownUserReasonBlocksCleaner))
			},
		},

		"10 compactors, sharding enabled, with zero shard size": {
			compactors:      10,
			compactorShards: map[string]int{user2: 0},

			check: func(t *testing.T, comps []*MultitenantCompactor) {
				require.Len(t, owningCompactors(t, comps, user2, ownUserReasonCompactor), 10)
				require.Len(t, owningCompactors(t, comps, user2, ownUserReasonBlocksCleaner), 1)
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kvStore, closer := consul.NewInMemoryClient(ring.GetCodec(), log.NewNopLogger(), nil)
			t.Cleanup(func() { assert.NoError(t, closer.Close()) })

			inmem := objstore.NewInMemBucket()

			compactors := []*MultitenantCompactor(nil)

			for i := 0; i < tc.compactors; i++ {
				cfg := prepareConfig(t)
				cfg.CompactionInterval = 10 * time.Minute // We will only call compaction manually.

				cfg.EnabledTenants = tc.enabledUsers
				cfg.DisabledTenants = tc.disabledUsers

				cfg.ShardingRing.Common.InstanceID = fmt.Sprintf("compactor-%d", i)
				cfg.ShardingRing.Common.InstanceAddr = fmt.Sprintf("127.0.0.%d", i)
				// No need to wait. All compactors are started before we do any tests, and we wait for all of them
				// to appear in all rings.
				cfg.ShardingRing.WaitStabilityMinDuration = 0
				cfg.ShardingRing.WaitStabilityMaxDuration = 0
				cfg.ShardingRing.Common.KVStore.Mock = kvStore

				limits := newMockConfigProvider()
				limits.instancesShardSize = tc.compactorShards

				c, _, _, _, _ := prepareWithConfigProvider(t, cfg, inmem, limits)
				require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))
				t.Cleanup(stopServiceFn(t, c))

				compactors = append(compactors, c)
			}

			// Make sure all compactors see all other compactors in the ring before running tests.
			test.Poll(t, 2*time.Second, true, func() interface{} {
				for _, c := range compactors {
					rs, err := c.ring.GetAllHealthy(RingOp)
					if err != nil {
						return false
					}
					if len(rs.Instances) != len(compactors) {
						return false
					}
				}
				return true
			})

			tc.check(t, compactors)
		})
	}
}

func owningCompactors(t *testing.T, comps []*MultitenantCompactor, user string, reason ownUserReason) []string {
	result := []string(nil)
	for _, c := range comps {
		var f func(string) (bool, error)
		if reason == ownUserReasonCompactor {
			f = c.shardingStrategy.compactorOwnsUser
		} else {
			f = c.shardingStrategy.blocksCleanerOwnsUser
		}
		ok, err := f(user)
		require.NoError(t, err)
		if ok {
			// We set instance ID even when not using sharding. It makes output nicer, since
			// calling method only wants to see some identifier.
			result = append(result, c.compactorCfg.ShardingRing.Common.InstanceID)
		}
	}
	return result
}

func stopServiceFn(t *testing.T, serv services.Service) func() {
	return func() {
		require.NoError(t, services.StopAndAwaitTerminated(context.Background(), serv))
	}
}

func TestMultitenantCompactor_OutOfOrderCompaction(t *testing.T) {
	const user = "user"

	var (
		ctx        = context.Background()
		storageDir = t.TempDir()
		fixtureDir = filepath.Join("fixtures", "test-ooo-compaction")
	)

	// Utility function originally used to generate a block with out of order chunks
	// used by this test. The block has been generated commenting out the checks done
	// by TSDB block Writer to prevent OOO chunks writing.
	_ = func() {
		specs := []*block.SeriesSpec{
			{
				Labels: labels.FromStrings("case", "out_of_order"),
				Chunks: []chunks.Meta{
					must(chunks.ChunkFromSamples([]chunks.Sample{testutil.Sample{TS: 20, Val: 20}, testutil.Sample{TS: 21, Val: 21}})),
					must(chunks.ChunkFromSamples([]chunks.Sample{testutil.Sample{TS: 10, Val: 10}, testutil.Sample{TS: 11, Val: 11}})),
					// Extend block to cover 2h.
					must(chunks.ChunkFromSamples([]chunks.Sample{testutil.Sample{TS: 0, Val: 0}, testutil.Sample{TS: 2*time.Hour.Milliseconds() - 1, Val: 0}})),
				},
			},
		}

		_, err := block.GenerateBlockFromSpec(fixtureDir, specs)
		require.NoError(t, err)

		_, err = block.GenerateBlockFromSpec(fixtureDir, specs)
		require.NoError(t, err)
	}

	bkt, err := filesystem.NewBucketClient(filesystem.Config{Directory: storageDir})
	require.NoError(t, err)
	userBkt := bucket.NewUserBucketClient(user, bkt, nil)

	// Copy blocks from fixtures dir to the test bucket.
	var metas []*block.Meta

	entries, err := os.ReadDir(fixtureDir)
	require.NoError(t, err)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		blockDir := filepath.Join(fixtureDir, entry.Name())

		blockID, err := ulid.Parse(entry.Name())
		require.NoErrorf(t, err, "parsing block ID from directory name %q", entry.Name())

		meta, err := block.ReadMetaFromDir(blockDir)
		require.NoErrorf(t, err, "reading meta from block at &s", blockDir)

		require.NoError(t, block.Upload(ctx, log.NewNopLogger(), userBkt, filepath.Join(fixtureDir, blockID.String()), meta))

		metas = append(metas, meta)
	}

	// We expect 2 blocks have been copied.
	require.Len(t, metas, 2)

	cfg := prepareConfig(t)
	c, _, tsdbPlanner, logs, registry := prepare(t, cfg, bkt)

	tsdbPlanner.On("Plan", mock.Anything, mock.Anything).Return(metas, nil)

	// Start the compactor
	require.NoError(t, services.StartAndAwaitRunning(ctx, c))

	// Wait until a compaction run has been completed.
	test.Poll(t, 10*time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsCompleted)
	})

	// Stop the compactor.
	require.NoError(t, services.StopAndAwaitTerminated(ctx, c))

	// Verify that compactor has found block with out of order chunks, and this block is now marked for no-compaction.
	r := regexp.MustCompile("level=info component=compactor user=user msg=\"block has been marked for no compaction\" block=([0-9A-Z]+)")
	matches := r.FindStringSubmatch(logs.String())
	require.Len(t, matches, 2) // Entire string match + single group match.

	skippedBlock := matches[1]
	require.True(t, skippedBlock == metas[0].ULID.String() || skippedBlock == metas[1].ULID.String())

	m := &block.NoCompactMark{}
	require.NoError(t, block.ReadMarker(ctx, log.NewNopLogger(), objstore.WithNoopInstr(bkt), path.Join(user, skippedBlock), m))
	require.Equal(t, skippedBlock, m.ID.String())
	require.NotZero(t, m.NoCompactTime)
	require.Equal(t, block.NoCompactReason(block.OutOfOrderChunksNoCompactReason), m.Reason)

	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# HELP cortex_compactor_blocks_marked_for_no_compaction_total Total number of blocks that were marked for no-compaction.
		# TYPE cortex_compactor_blocks_marked_for_no_compaction_total counter
		cortex_compactor_blocks_marked_for_no_compaction_total{reason="block-index-out-of-order-chunk"} 1
		cortex_compactor_blocks_marked_for_no_compaction_total{reason="critical"} 0
	`),
		"cortex_compactor_blocks_marked_for_no_compaction_total",
	))
}

func TestMultitenantCompactor_CriticalIssue(t *testing.T) {
	// Generate a single block with a single chunk.
	specs := []*block.SeriesSpec{
		{
			Labels: labels.FromStrings("case", "critical"),
			Chunks: []chunks.Meta{
				must(chunks.ChunkFromSamples([]chunks.Sample{
					testutil.Sample{TS: 0, Val: 0},
					testutil.Sample{TS: 2*time.Hour.Milliseconds() - 1, Val: 1},
				})),
			},
		},
	}

	const user = "user"

	storageDir := t.TempDir()
	// We need two blocks to start compaction.
	meta1, err := block.GenerateBlockFromSpec(filepath.Join(storageDir, user), specs)
	require.NoError(t, err)
	meta2, err := block.GenerateBlockFromSpec(filepath.Join(storageDir, user), specs)
	require.NoError(t, err)

	// Force chunk to fall out of the block time range by modifying MaxTime.
	meta1.MaxTime -= 1000
	meta2.MaxTime -= 1000

	bkt, err := filesystem.NewBucketClient(filesystem.Config{Directory: storageDir})
	require.NoError(t, err)

	cfg := prepareConfig(t)
	c, _, tsdbPlanner, logs, registry := prepare(t, cfg, bkt)

	tsdbPlanner.On("Plan", mock.Anything, mock.Anything).Return([]*block.Meta{meta1, meta2}, nil)

	// Start the compactor
	require.NoError(t, services.StartAndAwaitRunning(context.Background(), c))

	// Wait until a compaction run has been completed.
	test.Poll(t, 10*time.Second, 1.0, func() interface{} {
		return prom_testutil.ToFloat64(c.compactionRunsCompleted)
	})

	// Stop the compactor.
	require.NoError(t, services.StopAndAwaitTerminated(context.Background(), c))

	// Verify that compactor has found the unhealthy block, and this block is now marked for no-compaction.
	r := regexp.MustCompile("level=info component=compactor user=user msg=\"block has been marked for no compaction\" block=([0-9A-Z]+)")
	matches := r.FindStringSubmatch(logs.String())
	require.Len(t, matches, 2) // Entire string match + single group match.

	skippedBlock := matches[1]
	require.True(t, skippedBlock == meta1.ULID.String() || skippedBlock == meta2.ULID.String())

	m := &block.NoCompactMark{}
	require.NoError(t, block.ReadMarker(context.Background(), log.NewNopLogger(), objstore.WithNoopInstr(bkt), path.Join(user, skippedBlock), m))
	require.Equal(t, skippedBlock, m.ID.String())
	require.NotZero(t, m.NoCompactTime)
	require.Equal(t, block.NoCompactReason(block.CriticalNoCompactReason), m.Reason)

	assert.NoError(t, prom_testutil.GatherAndCompare(registry, strings.NewReader(`
		# HELP cortex_compactor_blocks_marked_for_no_compaction_total Total number of blocks that were marked for no-compaction.
		# TYPE cortex_compactor_blocks_marked_for_no_compaction_total counter
		cortex_compactor_blocks_marked_for_no_compaction_total{reason="block-index-out-of-order-chunk"} 0
		cortex_compactor_blocks_marked_for_no_compaction_total{reason="critical"} 1
	`),
		"cortex_compactor_blocks_marked_for_no_compaction_total",
	))
}

type bucketWithMockedAttributes struct {
	objstore.Bucket

	customAttributes map[string]objstore.ObjectAttributes
}

func (b *bucketWithMockedAttributes) Attributes(ctx context.Context, name string) (objstore.ObjectAttributes, error) {
	if attrs, ok := b.customAttributes[name]; ok {
		return attrs, nil
	}

	return b.Bucket.Attributes(ctx, name)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
