// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	noLimit = math.MaxInt64
	minInt  = math.MinInt64 / 2
)

// rollRaw builds the minute aggregates of a raw file. It builds a minute only
// when the minute ended before limit (unix seconds).
func (s *store) rollRaw(f fileInfo, limit int64) error {
	byMinute := make(map[int64]*agg)
	err := scanRaw(f, func(ms, us int64, exit int) bool {
		m := floorTo(ms/1000, 60)
		a := byMinute[m]
		if a == nil {
			a = &agg{T: m}
			byMinute[m] = a
		}
		a.add(us, exit != 0)
		return true
	})
	if err != nil {
		return err
	}
	return s.putBuilt(TierMinute, byMinute, limit)
}

// rollTier builds the aggregates of the next tier from the aggregates of a file.
func (s *store) rollTier(src Tier, f fileInfo, lo, hi, limit int64) error {
	var aggs []agg
	err := scanAggs(f.path, lo, hi, func(a agg) { aggs = append(aggs, a) })
	if err != nil {
		return err
	}
	dst := src + 1
	w := tierSec(dst)
	byBucket := make(map[int64]*agg)
	for i := range aggs {
		t := floorTo(aggs[i].T, w)
		a := byBucket[t]
		if a == nil {
			a = &agg{T: t}
			byBucket[t] = a
		}
		a.merge(&aggs[i])
	}
	return s.putBuilt(dst, byBucket, limit)
}

func (s *store) putBuilt(t Tier, built map[int64]*agg, limit int64) error {
	w := tierSec(t)
	var list []agg
	for start, a := range built {
		if start+w <= limit {
			list = append(list, *a)
		}
	}
	if len(list) == 0 {
		return nil
	}
	slices.SortFunc(list, func(x, y agg) int { return cmpInt(x.T, y.T) })
	return s.putAggs(t, list)
}

// roll builds the next tier from a file of a tier.
func (s *store) roll(t Tier, f fileInfo, limit int64) error {
	return s.rollRange(t, f, minInt, noLimit, limit)
}

// rollRange is roll for the records of the file with lo <= T < hi.
func (s *store) rollRange(t Tier, f fileInfo, lo, hi, limit int64) error {
	if t == TierRaw {
		return s.rollRaw(f, limit)
	}
	return s.rollTier(t, f, lo, hi, limit)
}

// repair builds the aggregates that are missing, and removes the files that
// are past their retention. now is in unix seconds.
func (s *store) repair(now int64) error {
	for _, t := range allTiers {
		s.removeTemporary(t)
	}
	for _, t := range allTiers[:TierDay] {
		for _, f := range s.list(t) {
			if err := s.roll(t, f, now); err != nil {
				return err
			}
		}
	}
	return s.prune(now)
}

func (s *store) removeTemporary(t Tier) {
	dir := filepath.Join(s.dir, tierDirs[t])
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), tmpSuffix) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// prune removes the files whose whole span is older than the retention of the
// tier. It builds the aggregates of the next tier first.
func (s *store) prune(now int64) error {
	for _, t := range allTiers[:TierDay] {
		for _, f := range s.list(t) {
			if f.end+retentionSec(t) > now {
				continue
			}
			if err := s.roll(t, f, noLimit); err != nil {
				return err
			}
			s.forget(f.path)
			if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}
