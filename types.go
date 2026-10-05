package aviato

import (
	pluginv1 "github.com/getaviato/aviato-go/pluginv1"
)

// Record is a record as exchanged with the agent: column names to JSON values.
type Record = map[string]any

// Caller describes who is calling the plugin and on what. It is sent with every plugin call.
type Caller struct {
	UserID string
	Name   string
	// ActorType is human, agent or api_key.
	ActorType string
	// Role is admin, editor, viewer or a custom role name.
	Role           string
	OrganizationID string
	ProjectID      string
	Environment    string
	RequestID      string
	// InvocationToken authenticates DataService calls made while handling this call.
	InvocationToken string
	// DataURL is the base URL of the agent's DataService.
	DataURL string
}

func callerFromProto(caller *pluginv1.Caller) Caller {
	return Caller{
		UserID:          caller.GetUserId(),
		Name:            caller.GetName(),
		ActorType:       caller.GetActorType(),
		Role:            caller.GetRole(),
		OrganizationID:  caller.GetOrganizationId(),
		ProjectID:       caller.GetProjectId(),
		Environment:     caller.GetEnvironment(),
		RequestID:       caller.GetRequestId(),
		InvocationToken: caller.GetInvocationToken(),
		DataURL:         caller.GetDataUrl(),
	}
}

// Scope is what an action runs on.
type Scope int

const (
	// ScopeSingle actions run on one record.
	ScopeSingle Scope = iota + 1
	// ScopeBulk actions run on a selection of records.
	ScopeBulk
	// ScopeGlobal actions run on the collection, with the current filters, segment and search.
	ScopeGlobal
)

func (s Scope) proto() pluginv1.ActionScope {
	switch s {
	case ScopeSingle:
		return pluginv1.ActionScope_ACTION_SCOPE_SINGLE
	case ScopeBulk:
		return pluginv1.ActionScope_ACTION_SCOPE_BULK
	case ScopeGlobal:
		return pluginv1.ActionScope_ACTION_SCOPE_GLOBAL
	default:
		return pluginv1.ActionScope_ACTION_SCOPE_UNSPECIFIED
	}
}

// HookTiming says whether a hook runs before or after the operation.
type HookTiming int

const (
	// Before hooks may reject the operation or replace the values being written.
	Before HookTiming = iota + 1
	// After hooks run once the operation succeeded.
	After
)

func (t HookTiming) proto() pluginv1.HookTiming {
	switch t {
	case Before:
		return pluginv1.HookTiming_HOOK_TIMING_BEFORE
	case After:
		return pluginv1.HookTiming_HOOK_TIMING_AFTER
	default:
		return pluginv1.HookTiming_HOOK_TIMING_UNSPECIFIED
	}
}

// HookOperation is the operation a hook is attached to.
type HookOperation int

// Hook operations.
const (
	HookCreate HookOperation = iota + 1
	HookUpdate
	HookDelete
	HookList
	HookAction
)

func (o HookOperation) proto() pluginv1.HookOperation {
	switch o {
	case HookCreate:
		return pluginv1.HookOperation_HOOK_OPERATION_CREATE
	case HookUpdate:
		return pluginv1.HookOperation_HOOK_OPERATION_UPDATE
	case HookDelete:
		return pluginv1.HookOperation_HOOK_OPERATION_DELETE
	case HookList:
		return pluginv1.HookOperation_HOOK_OPERATION_LIST
	case HookAction:
		return pluginv1.HookOperation_HOOK_OPERATION_ACTION
	default:
		return pluginv1.HookOperation_HOOK_OPERATION_UNSPECIFIED
	}
}

// RelationKind is the kind of a relation hint.
type RelationKind string

// Relation kinds.
const (
	BelongsTo RelationKind = "belongsTo"
	HasMany   RelationKind = "hasMany"
	HasOne    RelationKind = "hasOne"
)

// RelationHint declares a relation the agent cannot introspect from the database (for example
// when the schema has no foreign key constraints). ForeignKey always holds the referencing
// columns and TargetKey the referenced ones, whichever side the relation is declared on: on
// BelongsTo the foreign key is local, otherwise it is on the target.
type RelationHint struct {
	Collection string
	Name       string
	Kind       RelationKind
	ForeignKey []string
	Target     string
	TargetKey  []string
}

// Form is an action form: a JSON Schema describing the data and an optional JSON Forms UI
// schema laying it out.
type Form struct {
	Schema   map[string]any
	UISchema map[string]any
}
