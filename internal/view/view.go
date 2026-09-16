// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package view defines shared saved view configurations.
package view

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Render types.
const (
	// TypeKanban renders the view as the Overview Kanban board.
	TypeKanban = "kanban"
	// TypeWorkflow filters and sorts the Workflows page.
	TypeWorkflow = "workflow"
	// TypeRun filters and scopes the Executions page.
	TypeRun = "run"
)

// Workflow workspace scopes.
const (
	WorkspaceScopeAll       = "all"
	WorkspaceScopeDefault   = "default"
	WorkspaceScopeWorkspace = "workspace"
)

// Workflow sort fields and orders.
const (
	WorkflowSortName    = "name"
	WorkflowSortNextRun = "nextRun"
	SortOrderAscending  = "asc"
	SortOrderDescending = "desc"
)

// Kanban columns.
const (
	ColumnQueued  = "queued"
	ColumnRunning = "running"
	ColumnReview  = "review"
	ColumnDone    = "done"
	ColumnFailed  = "failed"
)

// Run filter values. RunStatusAll matches every run status; date-related
// constants mirror the Executions page filter controls.
const (
	RunStatusAll         = "all"
	DateModePreset       = "preset"
	DateModeSpecific     = "specific"
	DateModeCustom       = "custom"
	DatePresetToday      = "today"
	DatePresetYesterday  = "yesterday"
	DatePresetLast7Days  = "last7days"
	DatePresetLast30Days = "last30days"
	DatePresetThisWeek   = "thisWeek"
	DatePresetThisMonth  = "thisMonth"
	SpecificPeriodDate   = "date"
	SpecificPeriodMonth  = "month"
	SpecificPeriodYear   = "year"
)

var defaultColumns = []string{
	ColumnQueued,
	ColumnRunning,
	ColumnReview,
	ColumnDone,
	ColumnFailed,
}

// Field bounds.
const (
	MaxNameLength          = 100
	MaxDAGNameLength       = 255
	MaxLabels              = 50
	MaxLabelLength         = 128
	MinIntervalDays        = 1
	MaxIntervalDays        = 30
	MaxDAGRunIDLength      = 64
	MaxRunStatusLength     = 16
	MaxSpecificValueLength = 16
	MaxDateLength          = 32
)

// Run status bounds mirror the execution lifecycle statuses: NotStarted (0)
// through Rejected (8).
const (
	MinRunStatusCode = 0
	MaxRunStatusCode = 8
)

// Sentinel errors returned by views and their stores.
var (
	ErrInvalidViewID         = errors.New("view: invalid id")
	ErrViewNotFound          = errors.New("view: not found")
	ErrViewExists            = errors.New("view: already exists")
	ErrInvalidName           = errors.New("view: name is required")
	ErrNameTooLong           = errors.New("view: name too long")
	ErrDAGNameTooLong        = errors.New("view: dagName too long")
	ErrInvalidInterval       = errors.New("view: intervalDays out of range")
	ErrTooManyLabels         = errors.New("view: too many labels")
	ErrInvalidType           = errors.New("view: unknown type")
	ErrInvalidColumns        = errors.New("view: invalid columns")
	ErrInvalidWorkspaceScope = errors.New("view: invalid workspace scope")
	ErrInvalidSortField      = errors.New("view: invalid sort field")
	ErrInvalidSortOrder      = errors.New("view: invalid sort order")
	ErrDAGRunIDTooLong       = errors.New("view: dagRunId too long")
	ErrInvalidRunStatus      = errors.New("view: invalid run status")
	ErrRunStatusTooLong      = errors.New("view: runStatus too long")
	ErrInvalidSpecificValue  = errors.New("view: invalid specific value")
	ErrInvalidDateMode       = errors.New("view: invalid date mode")
	ErrInvalidDatePreset     = errors.New("view: invalid date preset")
	ErrInvalidSpecificPeriod = errors.New("view: invalid specific period")
	ErrSpecificValueTooLong  = errors.New("view: specific value too long")
	ErrInvalidDate           = errors.New("view: invalid date")
	ErrDateTooLong           = errors.New("view: date too long")
	ErrViewChanged           = errors.New("view: changed")
)

// View is a shared saved view configuration. CreatedBy is recorded for display
// only and confers no ownership.
type View struct {
	ID             string
	Name           string
	Type           string
	Workspace      string
	Labels         []string
	DAGName        string
	IntervalDays   int
	Columns        []string
	Pinned         bool
	WorkspaceScope string
	SortField      string
	SortOrder      string
	ActiveOnly     bool
	// Run filters. Only TypeRun views use these.
	DAGRunID       string
	RunStatus      string
	DateMode       string
	DatePreset     string
	SpecificPeriod string
	SpecificValue  string
	FromDate       string
	ToDate         string
	Default        bool
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Normalize trims string fields, drops empty or oversized labels, and applies
// default values. Call before Validate.
func (v *View) Normalize() {
	v.Name = strings.TrimSpace(v.Name)
	v.Workspace = strings.TrimSpace(v.Workspace)
	v.DAGName = strings.TrimSpace(v.DAGName)
	v.Type = strings.TrimSpace(v.Type)
	if v.Type == "" {
		v.Type = TypeKanban
	}
	switch v.Type {
	case TypeKanban:
		v.WorkspaceScope = ""
		v.SortField = ""
		v.SortOrder = ""
		v.ActiveOnly = false
		v.Default = false
		if len(v.Columns) == 0 {
			v.Columns = DefaultColumns()
		}
	case TypeWorkflow:
		v.WorkspaceScope = strings.TrimSpace(v.WorkspaceScope)
		if v.WorkspaceScope == "" {
			if v.Workspace == "" {
				v.WorkspaceScope = WorkspaceScopeAll
			} else {
				v.WorkspaceScope = WorkspaceScopeWorkspace
			}
		}
		v.SortField = strings.TrimSpace(v.SortField)
		if v.SortField == "" {
			v.SortField = WorkflowSortName
		}
		v.SortOrder = strings.TrimSpace(v.SortOrder)
		if v.SortOrder == "" {
			v.SortOrder = SortOrderAscending
		}
		v.IntervalDays = MinIntervalDays
		v.Columns = nil
	case TypeRun:
		v.WorkspaceScope = strings.TrimSpace(v.WorkspaceScope)
		if v.WorkspaceScope == "" {
			if v.Workspace == "" {
				v.WorkspaceScope = WorkspaceScopeAll
			} else {
				v.WorkspaceScope = WorkspaceScopeWorkspace
			}
		}
		v.DAGRunID = strings.TrimSpace(v.DAGRunID)
		v.RunStatus = strings.TrimSpace(v.RunStatus)
		if v.RunStatus == "" {
			v.RunStatus = RunStatusAll
		}
		v.DateMode = strings.TrimSpace(v.DateMode)
		if v.DateMode == "" {
			v.DateMode = DateModePreset
		}
		v.DatePreset = strings.TrimSpace(v.DatePreset)
		if v.DatePreset == "" {
			v.DatePreset = DatePresetToday
		}
		v.SpecificPeriod = strings.TrimSpace(v.SpecificPeriod)
		if v.SpecificPeriod == "" {
			v.SpecificPeriod = SpecificPeriodDate
		}
		v.SpecificValue = strings.TrimSpace(v.SpecificValue)
		v.FromDate = strings.TrimSpace(v.FromDate)
		v.ToDate = strings.TrimSpace(v.ToDate)
		v.IntervalDays = MinIntervalDays
		v.Columns = nil
	}
	labels := make([]string, 0, len(v.Labels))
	for _, l := range v.Labels {
		l = strings.TrimSpace(l)
		if l != "" && len([]rune(l)) <= MaxLabelLength {
			labels = append(labels, l)
		}
	}
	v.Labels = labels
}

// Validate reports whether the view's fields satisfy their bounds. It assumes
// Normalize has already been applied.
func (v *View) Validate() error {
	switch {
	case v.Name == "":
		return ErrInvalidName
	case len([]rune(v.Name)) > MaxNameLength:
		return ErrNameTooLong
	case len([]rune(v.DAGName)) > MaxDAGNameLength:
		return ErrDAGNameTooLong
	case len(v.Labels) > MaxLabels:
		return ErrTooManyLabels
	case !ValidType(v.Type):
		return ErrInvalidType
	}
	if v.Type == TypeKanban {
		switch {
		case v.IntervalDays < MinIntervalDays || v.IntervalDays > MaxIntervalDays:
			return ErrInvalidInterval
		case !ValidColumns(v.Columns):
			return ErrInvalidColumns
		}
		return nil
	}
	if v.Type == TypeRun {
		switch {
		case !ValidWorkspaceScope(v.WorkspaceScope, v.Workspace):
			return ErrInvalidWorkspaceScope
		case len([]rune(v.DAGRunID)) > MaxDAGRunIDLength:
			return ErrDAGRunIDTooLong
		case len([]rune(v.RunStatus)) > MaxRunStatusLength:
			return ErrRunStatusTooLong
		case !ValidRunStatus(v.RunStatus):
			return ErrInvalidRunStatus
		case !ValidRunDateMode(v.DateMode):
			return ErrInvalidDateMode
		case !ValidRunDatePreset(v.DatePreset):
			return ErrInvalidDatePreset
		case !ValidRunSpecificPeriod(v.SpecificPeriod):
			return ErrInvalidSpecificPeriod
		case len([]rune(v.SpecificValue)) > MaxSpecificValueLength:
			return ErrSpecificValueTooLong
		case v.DateMode == DateModeSpecific && !ValidRunSpecificValue(v.SpecificPeriod, v.SpecificValue):
			return ErrInvalidSpecificValue
		case v.DateMode == DateModeCustom && (!ValidRunDateString(v.FromDate) || !ValidRunDateString(v.ToDate)):
			return ErrInvalidDate
		case len([]rune(v.FromDate)) > MaxDateLength || len([]rune(v.ToDate)) > MaxDateLength:
			return ErrDateTooLong
		}
		return nil
	}
	switch {
	case !ValidWorkspaceScope(v.WorkspaceScope, v.Workspace):
		return ErrInvalidWorkspaceScope
	case !ValidWorkflowSortField(v.SortField):
		return ErrInvalidSortField
	case !ValidSortOrder(v.SortOrder):
		return ErrInvalidSortOrder
	}
	return nil
}

// DefaultColumns returns all Kanban columns in their default display order.
func DefaultColumns() []string {
	return slices.Clone(defaultColumns)
}

// ValidColumns reports whether columns is a non-empty, duplicate-free subset
// of the supported Kanban columns.
func ValidColumns(columns []string) bool {
	if len(columns) == 0 || len(columns) > len(defaultColumns) {
		return false
	}
	seen := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		if !slices.Contains(defaultColumns, column) {
			return false
		}
		if _, exists := seen[column]; exists {
			return false
		}
		seen[column] = struct{}{}
	}
	return true
}

// ValidType reports whether t is a known render type.
func ValidType(t string) bool {
	switch t {
	case TypeKanban, TypeWorkflow, TypeRun:
		return true
	default:
		return false
	}
}

// ValidRunStatus reports whether status is a supported run status: the
// wildcard "all" or a numeric status code within the execution lifecycle
// range [MinRunStatusCode, MaxRunStatusCode].
func ValidRunStatus(status string) bool {
	if status == RunStatusAll {
		return true
	}
	n, err := strconv.Atoi(status)
	if err != nil {
		return false
	}
	return n >= MinRunStatusCode && n <= MaxRunStatusCode
}

// ValidRunSpecificValue reports whether value matches the granularity of
// period: a date (YYYY-MM-DD), month (YYYY-MM), or year (YYYY). Periods the
// caller does not recognize are not the concern of this checker, so they
// count as valid to avoid masking an invalid-period error.
func ValidRunSpecificValue(period string, value string) bool {
	var layout string
	switch period {
	case SpecificPeriodDate:
		layout = "2006-01-02"
	case SpecificPeriodMonth:
		layout = "2006-01"
	case SpecificPeriodYear:
		layout = "2006"
	default:
		return true
	}
	_, err := time.Parse(layout, value)
	return err == nil
}

// ValidRunDateString reports whether value is empty or a datetime-local
// string (YYYY-MM-DDTHH:mm, optionally with seconds).
func ValidRunDateString(value string) bool {
	if value == "" {
		return true
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if _, err := time.Parse(layout, value); err == nil {
			return true
		}
	}
	return false
}

// ValidRunDateMode reports whether mode is a supported Executions page date
// range mode.
func ValidRunDateMode(mode string) bool {
	switch mode {
	case DateModePreset, DateModeSpecific, DateModeCustom:
		return true
	default:
		return false
	}
}

// ValidRunDatePreset reports whether preset is a supported relative run date
// preset.
func ValidRunDatePreset(preset string) bool {
	switch preset {
	case DatePresetToday, DatePresetYesterday, DatePresetLast7Days,
		DatePresetLast30Days, DatePresetThisWeek, DatePresetThisMonth:
		return true
	default:
		return false
	}
}

// ValidRunSpecificPeriod reports whether period is a supported run period
// granularity.
func ValidRunSpecificPeriod(period string) bool {
	switch period {
	case SpecificPeriodDate, SpecificPeriodMonth, SpecificPeriodYear:
		return true
	default:
		return false
	}
}

// ValidWorkspaceScope reports whether scope and workspace identify a workflow scope.
func ValidWorkspaceScope(scope string, workspace string) bool {
	switch scope {
	case WorkspaceScopeAll, WorkspaceScopeDefault:
		return workspace == ""
	case WorkspaceScopeWorkspace:
		return workspace != ""
	default:
		return false
	}
}

// ValidWorkflowSortField reports whether field is supported by the Workflows page.
func ValidWorkflowSortField(field string) bool {
	return field == WorkflowSortName || field == WorkflowSortNextRun
}

// ValidSortOrder reports whether order is a supported view sort order.
func ValidSortOrder(order string) bool {
	return order == SortOrderAscending || order == SortOrderDescending
}

// ViewForStorage is the on-disk JSON representation of a View.
type ViewForStorage struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Type           string    `json:"type"`
	Workspace      string    `json:"workspace,omitempty"`
	Labels         []string  `json:"labels,omitempty"`
	DAGName        string    `json:"dag_name,omitempty"`
	IntervalDays   int       `json:"interval_days"`
	Columns        []string  `json:"columns,omitempty"`
	Pinned         bool      `json:"pinned,omitempty"`
	WorkspaceScope string    `json:"workspace_scope,omitempty"`
	SortField      string    `json:"sort_field,omitempty"`
	SortOrder      string    `json:"sort_order,omitempty"`
	ActiveOnly     bool      `json:"active_only,omitempty"`
	DAGRunID       string    `json:"dag_run_id,omitempty"`
	RunStatus      string    `json:"run_status,omitempty"`
	DateMode       string    `json:"date_mode,omitempty"`
	DatePreset     string    `json:"date_preset,omitempty"`
	SpecificPeriod string    `json:"specific_period,omitempty"`
	SpecificValue  string    `json:"specific_value,omitempty"`
	FromDate       string    `json:"from_date,omitempty"`
	ToDate         string    `json:"to_date,omitempty"`
	Default        bool      `json:"default,omitempty"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ToStorage converts a View to its persistence representation.
func (v *View) ToStorage() *ViewForStorage {
	return &ViewForStorage{
		ID:             v.ID,
		Name:           v.Name,
		Type:           v.Type,
		Workspace:      v.Workspace,
		Labels:         slices.Clone(v.Labels),
		DAGName:        v.DAGName,
		IntervalDays:   v.IntervalDays,
		Columns:        slices.Clone(v.Columns),
		Pinned:         v.Pinned,
		WorkspaceScope: v.WorkspaceScope,
		SortField:      v.SortField,
		SortOrder:      v.SortOrder,
		ActiveOnly:     v.ActiveOnly,
		DAGRunID:       v.DAGRunID,
		RunStatus:      v.RunStatus,
		DateMode:       v.DateMode,
		DatePreset:     v.DatePreset,
		SpecificPeriod: v.SpecificPeriod,
		SpecificValue:  v.SpecificValue,
		FromDate:       v.FromDate,
		ToDate:         v.ToDate,
		Default:        v.Default,
		CreatedBy:      v.CreatedBy,
		CreatedAt:      v.CreatedAt,
		UpdatedAt:      v.UpdatedAt,
	}
}

// ToView converts a stored representation back to a View.
func (s *ViewForStorage) ToView() *View {
	columns := slices.Clone(s.Columns)
	if len(columns) == 0 && (s.Type == "" || s.Type == TypeKanban) {
		columns = DefaultColumns()
	}
	return &View{
		ID:             s.ID,
		Name:           s.Name,
		Type:           s.Type,
		Workspace:      s.Workspace,
		Labels:         slices.Clone(s.Labels),
		DAGName:        s.DAGName,
		IntervalDays:   s.IntervalDays,
		Columns:        columns,
		Pinned:         s.Pinned,
		WorkspaceScope: s.WorkspaceScope,
		SortField:      s.SortField,
		SortOrder:      s.SortOrder,
		ActiveOnly:     s.ActiveOnly,
		DAGRunID:       s.DAGRunID,
		RunStatus:      s.RunStatus,
		DateMode:       s.DateMode,
		DatePreset:     s.DatePreset,
		SpecificPeriod: s.SpecificPeriod,
		SpecificValue:  s.SpecificValue,
		FromDate:       s.FromDate,
		ToDate:         s.ToDate,
		Default:        s.Default,
		CreatedBy:      s.CreatedBy,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}

// Store persists view configurations. Implementations are safe for concurrent
// use. List returns views ordered by creation time, oldest first.
type Store interface {
	Create(ctx context.Context, v *View) error
	GetByID(ctx context.Context, id string) (*View, error)
	List(ctx context.Context) ([]*View, error)
	Update(ctx context.Context, v *View, expectedWorkspace string) error
	Delete(ctx context.Context, id string, expectedWorkspace string) error
}
