// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"encoding/json"
	"image"
)

// elementJSON is the wire form of an Element: bounds as a position and a
// size, optional fields left out, and a path that is never null.
type elementJSON struct {
	Role   string     `json:"role"`
	Name   string     `json:"name"`
	ID     string     `json:"id,omitempty"`
	Value  string     `json:"value,omitempty"`
	Label  string     `json:"label,omitempty"`
	App    string     `json:"app,omitempty"`
	Bounds boundsJSON `json:"bounds"`
	Window string     `json:"window"`
	Path   []PathStep `json:"path"`
}

type boundsJSON struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// MarshalJSON writes the element in its wire form.
func (e Element) MarshalJSON() ([]byte, error) {
	path := e.Path
	if path == nil {
		path = []PathStep{}
	}
	return json.Marshal(elementJSON{
		Role: e.Role, Name: e.Name, ID: e.ID, Value: e.Value, Label: e.Label, App: e.App,
		Bounds: boundsJSON{X: e.Bounds.Min.X, Y: e.Bounds.Min.Y, Width: e.Bounds.Dx(), Height: e.Bounds.Dy()},
		Window: e.Window, Path: path,
	})
}

// UnmarshalJSON reads the wire form.
func (e *Element) UnmarshalJSON(data []byte) error {
	var wire elementJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*e = Element{
		Role: wire.Role, Name: wire.Name, ID: wire.ID, Value: wire.Value, Label: wire.Label, App: wire.App,
		Bounds: image.Rect(wire.Bounds.X, wire.Bounds.Y, wire.Bounds.X+wire.Bounds.Width, wire.Bounds.Y+wire.Bounds.Height),
		Window: wire.Window, Path: wire.Path,
	}
	return nil
}
