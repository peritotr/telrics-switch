// SPDX-License-Identifier: AGPL-3.0-only
package rater

import "softswitch/core/pkg/metrics"

var (
	mStored       = metrics.NewCounter("softswitch_rater_records_total", "Call records taken from the queue, by outcome.", "result=stored")
	mDropped      = metrics.NewCounter("softswitch_rater_records_total", "Call records taken from the queue, by outcome.", "result=dropped")
	mRetried      = metrics.NewCounter("softswitch_rater_records_total", "Call records taken from the queue, by outcome.", "result=retried")
	mFetchErrors  = metrics.NewCounter("softswitch_rater_fetch_errors_total", "Failed reads from the queue.")
	mSettleErrors = metrics.NewCounter("softswitch_rater_settle_errors_total", "Failed reports of charged calls to the engine; the money stays held longer.")
	mExported     = metrics.NewCounter("softswitch_export_records_total", "Call records copied to ClickHouse.")
	mExportErrors = metrics.NewCounter("softswitch_export_errors_total", "Failed batches to ClickHouse; each is retried.")

	mArchived       = metrics.NewCounter("softswitch_archive_records_total", "Old call records removed from PostgreSQL after ClickHouse confirmed them.")
	mArchiveMissing = metrics.NewCounter("softswitch_archive_missing_total", "Old call records ClickHouse did not have; kept and queued for export again.")
	mArchiveErrors  = metrics.NewCounter("softswitch_archive_errors_total", "Failed archive passes; each is tried again.")
)
