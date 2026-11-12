// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package samples records the executions of a continuous step (a step that
// repeats without a limit) or the runs of a service DAG as samples, rolls them
// up in tiers, and answers the queries of the API. See
// specs/083-continuous-runs.md and specs/097-service-run-history.md.
//
// # Layout
//
// The store of one step is a directory (see Dir) with these files:
//
//	raw/YYYYMMDDHH.jsonl     one line for each sample, one file for each hour
//	minute/YYYYMMDD.jsonl    one aggregate for each minute, one file for each day
//	hour/YYYYMM.jsonl        one aggregate for each hour, one file for each month
//	day/YYYY.jsonl           one aggregate for each day, one file for each year
//	events.jsonl             the events; no expiry, unless Options.MaxEventBytes is set
//
// All times are UTC. A file holds only whole buckets of the next tier, so a
// tier can drop a whole file when the next tier holds the aggregates of it.
//
// # Format
//
// The files are append-only text lines (JSON or JSON-like). A text format is
// easy to repair by hand and to read with a tool. The raw line is the one
// place where size matters, so it is a compact array:
//
//	[offsetMs,durationUs,exitCode]            a sample
//	[offsetMs,durationUs,exitCode,"message"]  a failed sample whose message differs
//	                                          from the message of the previous failed
//	                                          sample of the same file
//
// offsetMs is the time of the sample, in milliseconds after the start of the
// file. A raw line has about 17 bytes, so one day of 1 sample for each second
// is about 1.5 MB, which is less than the 3 MiB of the spec.
//
// An aggregate line is a JSON object with the start of the bucket in seconds
// (t), the count (n), the count of failed samples (f), the least, the greatest
// and the sum of the durations in microseconds (lo, hi, s), and the histogram
// (h) as a flat list of index and count pairs, for the used bins only.
//
// An event line is a JSON object with the time in milliseconds (t) and the
// kind (k). The other keys are optional: the exit code (c), the message (m),
// the count of failed samples (n), the outage in milliseconds (o), the run ID
// (r) and the gap in milliseconds (g).
//
// # Histogram
//
// The histogram has 16 bins for each octave of the duration in microseconds.
// Bin 0 holds the durations below 1 us. Bin i (i >= 1) holds the durations d
// with (i-1)/16 <= log2(d/1us) < i/16. The last bin starts at 2^54 us, above
// the longest time.Duration, so every duration has a bin of finite width. Two
// histograms add bin by bin, so the aggregate of a bucket is the sum of the
// aggregates of its parts.
//
// A percentile is the bin that holds the sample of the nearest rank. The value
// is the geometric middle of the bin, limited to the least and the greatest of
// the aggregate. A bin is 2^(1/16) wide (4.4%), so the value is within 2.2% of
// the true value; a bucket of one sample, or of equal samples, is exact.
//
// # Roll-up and repair
//
// The Recorder holds the samples of one minute at most. At the end of a minute
// it appends the raw lines and the aggregate of the minute. At the end of an
// hour it builds the aggregate of the hour from the minute file, and at the
// end of a day the aggregate of the day from the hour file. A roll-up builds
// the aggregates of a whole file and replaces a record only when it differs, so
// a second run gives the same files. OpenRecorder runs the same roll-up for all
// files, and removes the temporary files that a crash left, before it accepts
// a sample. A file is removed when its whole span is older than the retention
// of its tier, after its roll-up ran.
//
// Close also writes the aggregates of the open buckets, so that a stopped
// run shows all its samples in every tier. The next roll-up replaces them.
//
// # Run history
//
// A run history reuses the store with these options. The zero value of each
// option keeps the behavior of a continuous step.
//
// NoLifecycleEvents writes no started event and no stopped event. The outage
// state still survives a restart: OpenRecorder reads the last failed event and
// counts the failed samples of the raw tier after it.
//
// WriteEach writes the raw line of a sample before Record returns. A Reader
// sees the sample at once, because it builds the open minute from the raw tier.
// The fsync points do not change.
//
// Sample.RunID goes into the failed and recovered events that the sample causes.
// The raw line does not keep it.
//
// Sample.GapAfter above zero records a gap event when no outage is open and the
// sample starts more than GapAfter after the end of the previous sample. The
// event time is that end. The event has the time with no sample and the run ID
// of the new sample. OpenRecorder restores the end of the previous sample from
// the newest raw line, or from the newest aggregate when the raw tier is empty.
//
// MaxEventBytes above zero bounds the events file. When an append makes the file
// larger, the recorder replaces it with a file that keeps the newest events, at
// most half of the bound. The write goes to a temporary file, then a rename.
// The first line of the new file is a header, for example {"b":12345}. It gives
// the count of bytes that the file dropped before its first event.
//
// An events cursor is the offset of an event since the first event ever
// written, so it stays exact after a compaction. A cursor that points to a
// dropped event, or not to the start of an event, gives ErrInvalidCursor. The
// outage state stays correct, because the compaction keeps the newest events.
//
// Uptime records the time in which a run was active, with the interval of
// each sample (the start and the duration). A run can last hours, so the
// recorder splits its interval over the buckets that it covers, for the minute,
// the hour and the day tier, and writes it to the directory "up" of the store
// (see uptime.go). A bucket of the "up" store holds its whole up time, so a
// write replaces its line, and a tier is never a roll-up of another tier. The
// part of an interval before the end of the newest interval is not counted
// again, so two runs that overlap count their union. A store that has no "up"
// directory has no known uptime: Uptime.Ratio reports false, and a window that
// starts before the first interval counts from it. The minute tier loses the
// up time of the open minute in a crash. The hour and the day tier are written
// each time the minute moves.
//
// Sample.ActiveGap above zero counts the gap before a sample as active time,
// when the gap is not longer than ActiveGap and an interval is already
// recorded. The gap is from the end of the newest interval to the start of the
// sample. The stored buckets hold the result, so a reader needs no tolerance.
//
// NewestEnd gives the end of the newest sample of a store. It reads the raw
// tier. When the raw tier holds no sample, it reads the finest aggregate tier
// that has a bucket.
//
// # Reader
//
// The Reader opens only the files of the window. The newest bucket of a tier
// has no aggregate yet while it is open, so the Reader builds it from the finer
// tier. Samples that the Recorder still holds in memory (less than one minute)
// are not visible.
package samples
