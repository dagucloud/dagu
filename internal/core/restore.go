// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "reflect"

// buildOutcomeFields are the JSON-excluded fields that record what happened while
// a DAG was built rather than how it is configured. A DAG rebuilt from its source
// reports the outcome of that rebuild, which says nothing about the run being
// restored, so these are never carried over.
var buildOutcomeFields = map[string]bool{
	"EnvEvaluated":  true,
	"BuildErrors":   true,
	"BuildWarnings": true,
}

// RestoreUnpersistedFrom copies into d every field that JSON serialization omits,
// taking its value from src. Fields recording the outcome of a build rather than
// configuration are left untouched.
//
// A DAG decoded from persisted JSON is missing all of those fields, because they
// hold values deliberately kept off disk. Rebuilding the DAG from its source
// produces them again in src, and this carries them across. Adding a new omitted
// field to DAG therefore extends the restore automatically; a field that must not
// be restored belongs in buildOutcomeFields.
func (d *DAG) RestoreUnpersistedFrom(src *DAG) {
	if d == nil || src == nil {
		return
	}

	dst := reflect.ValueOf(d).Elem()
	from := reflect.ValueOf(src).Elem()
	typ := dst.Type()

	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() || field.Tag.Get("json") != "-" {
			continue
		}
		if buildOutcomeFields[field.Name] {
			continue
		}
		dst.Field(i).Set(from.Field(i))
	}
}
