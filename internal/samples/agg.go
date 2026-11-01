// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package samples

import (
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strconv"
	"time"
)

const (
	binsPerOctave = 16
	// maxBin is the last bin: 2^54 us, above the longest time.Duration, so
	// that every duration has a bin of finite width.
	maxBin = 1 + binsPerOctave*54
)

// binOf returns the histogram bin of a duration in microseconds.
func binOf(us int64) uint16 {
	if us < 1 {
		return 0
	}
	i := 1 + int(math.Floor(binsPerOctave*math.Log2(float64(us))))
	return uint16(min(i, maxBin)) // #nosec G115 -- 1 <= i <= maxBin
}

// binMiddle returns the geometric middle of a bin, in microseconds.
func binMiddle(bin uint16) float64 {
	if bin == 0 {
		return 0
	}
	return math.Exp2((float64(bin) - 0.5) / binsPerOctave)
}

// hbin is a used bin of a histogram.
type hbin struct {
	I uint16
	C uint64
}

// agg is the aggregate of the samples of one bucket. Durations are in
// microseconds.
type agg struct {
	T   int64 // start of the bucket, unix seconds
	N   uint64
	F   uint64
	Min int64
	Max int64
	Sum int64
	H   []hbin // sorted by I
}

func (a *agg) reset(t int64) {
	*a = agg{T: t, H: a.H[:0]}
}

// add counts one sample.
func (a *agg) add(us int64, failed bool) {
	if us < 0 {
		us = 0
	}
	if a.N == 0 || us < a.Min {
		a.Min = us
	}
	if a.N == 0 || us > a.Max {
		a.Max = us
	}
	a.N++
	if failed {
		a.F++
	}
	a.Sum += us
	a.addBin(binOf(us), 1)
}

func (a *agg) addBin(bin uint16, c uint64) {
	i := sort.Search(len(a.H), func(i int) bool { return a.H[i].I >= bin })
	if i < len(a.H) && a.H[i].I == bin {
		a.H[i].C += c
		return
	}
	a.H = slices.Insert(a.H, i, hbin{bin, c})
}

// merge adds the aggregate b to a. It keeps the time of a.
func (a *agg) merge(b *agg) {
	if b.N == 0 {
		return
	}
	if a.N == 0 || b.Min < a.Min {
		a.Min = b.Min
	}
	if a.N == 0 || b.Max > a.Max {
		a.Max = b.Max
	}
	a.N += b.N
	a.F += b.F
	a.Sum += b.Sum
	for _, h := range b.H {
		a.addBin(h.I, h.C)
	}
}

func (a *agg) clone() agg {
	c := *a
	c.H = slices.Clone(a.H)
	return c
}

// quantile returns the duration of the nearest rank of p, in microseconds.
func (a *agg) quantile(p float64) int64 {
	if a.N == 0 {
		return 0
	}
	rank := uint64(math.Ceil(p * float64(a.N)))
	rank = max(rank, 1)
	var cum uint64
	for _, h := range a.H {
		cum += h.C
		if cum >= rank {
			v := int64(math.Round(binMiddle(h.I)))
			return min(max(v, a.Min), a.Max)
		}
	}
	return a.Max
}

// appendJSON appends the line of the aggregate, with the end of line.
func (a *agg) appendJSON(b []byte) []byte {
	b = append(b, `{"t":`...)
	b = strconv.AppendInt(b, a.T, 10)
	b = append(b, `,"n":`...)
	b = strconv.AppendUint(b, a.N, 10)
	b = append(b, `,"f":`...)
	b = strconv.AppendUint(b, a.F, 10)
	b = append(b, `,"lo":`...)
	b = strconv.AppendInt(b, a.Min, 10)
	b = append(b, `,"hi":`...)
	b = strconv.AppendInt(b, a.Max, 10)
	b = append(b, `,"s":`...)
	b = strconv.AppendInt(b, a.Sum, 10)
	b = append(b, `,"h":[`...)
	for i, h := range a.H {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendUint(b, uint64(h.I), 10)
		b = append(b, ',')
		b = strconv.AppendUint(b, h.C, 10)
	}
	return append(b, "]}\n"...)
}

type aggJSON struct {
	T   int64    `json:"t"`
	N   uint64   `json:"n"`
	F   uint64   `json:"f"`
	Min int64    `json:"lo"`
	Max int64    `json:"hi"`
	Sum int64    `json:"s"`
	H   []uint64 `json:"h"`
}

// parseAgg decodes a line. It reports false for a line that is not valid (for
// example, a line that a crash cut).
func parseAgg(line []byte) (agg, bool) {
	var j aggJSON
	if err := json.Unmarshal(line, &j); err != nil || j.N == 0 || len(j.H)%2 != 0 {
		return agg{}, false
	}
	a := agg{T: j.T, N: j.N, F: j.F, Min: j.Min, Max: j.Max, Sum: j.Sum}
	if len(j.H) > 0 {
		a.H = make([]hbin, 0, len(j.H)/2)
		for i := 0; i < len(j.H); i += 2 {
			if j.H[i] > maxBin {
				return agg{}, false
			}
			a.H = append(a.H, hbin{uint16(j.H[i]), j.H[i+1]})
		}
		if !slices.IsSortedFunc(a.H, func(x, y hbin) int { return int(x.I) - int(y.I) }) {
			return agg{}, false
		}
	}
	return a, true
}

// bucket converts the aggregate to the bucket of the API.
func (a *agg) bucket() Bucket {
	us := func(v int64) time.Duration { return time.Duration(v) * time.Microsecond }
	return Bucket{
		Time:     time.Unix(a.T, 0).UTC(),
		Count:    a.N,
		Failures: a.F,
		Min:      us(a.Min),
		Max:      us(a.Max),
		P50:      us(a.quantile(0.50)),
		P95:      us(a.quantile(0.95)),
	}
}
