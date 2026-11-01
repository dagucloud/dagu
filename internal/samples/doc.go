// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package samples records the executions of a continuous step (a step that
// repeats without a limit) as samples, rolls them up in tiers, and answers the
// queries of the API. See specs/083-continuous-runs.md.
//
// # Layout
//
// The store of one step is a directory (see Dir) with these files:
//
//	raw/YYYYMMDDHH.jsonl     one line for each sample, one file for each hour
//	minute/YYYYMMDD.jsonl    one aggregate for each minute, one file for each day
//	hour/YYYYMM.jsonl        one aggregate for each hour, one file for each month
//	day/YYYY.jsonl           one aggregate for each day, one file for each year
//	events.jsonl             the events; no expiry
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
// # Reader
//
// The Reader opens only the files of the window. The newest bucket of a tier
// has no aggregate yet while it is open, so the Reader builds it from the finer
// tier. Samples that the Recorder still holds in memory (less than one minute)
// are not visible.
package samples
