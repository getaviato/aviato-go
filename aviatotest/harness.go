// Package aviatotest calls a plugin's registered actions, computed fields, hooks, segments,
// searches, write overrides, charts and custom datasources in unit tests, with a fake caller and
// without an agent.
//
//	h := aviatotest.New(t, plugin, aviatotest.WithRecords("customers", aviato.Record{"id": "42", "name": "Acme"}))
//	result, err := h.Execute(ctx, "customers", "Refund last invoice", aviatotest.Target{RecordIDs: []string{"42"}})
//	require.NoError(t, err)
//	assert.Equal(t, aviato.Success("Refunded 42", "invoices"), result)
//
// Handlers are called directly: signatures and protocol encoding are skipped, but values go
// through a JSON round trip like on the wire (numbers arrive as float64). Handlers reading or
// writing records through the Data API hit an in-memory fake seeded with [WithRecords].
package aviatotest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	aviato "github.com/getaviato/aviato-go"
	"github.com/getaviato/aviato-go/internal/bridge"
)

type dispatcher interface {
	Invocation(caller aviato.Caller, collection string) aviato.Invocation
	Execute(ctx context.Context, name string, a *aviato.ActionContext) (aviato.Result, error)
	ResolveForm(ctx context.Context, name string, f *aviato.FormContext) (aviato.FormResolution, error)
	Compute(ctx context.Context, inv *aviato.Invocation, fields []string, records []aviato.Record) ([]aviato.Record, error)
	Write(ctx context.Context, inv *aviato.Invocation, field string, value any, record aviato.Record) (map[string]any, error)
	Segment(ctx context.Context, inv *aviato.Invocation, name string) (aviato.SegmentResult, error)
	Hooks(ctx context.Context, h *aviato.HookContext) (aviato.HookResult, error)
	Search(ctx context.Context, inv *aviato.Invocation, query string) (map[string]any, error)
	Chart(ctx context.Context, caller aviato.Caller, name string, filter map[string]any) (map[string]any, error)
	DatasourceList(ctx context.Context, c *aviato.DatasourceContext, query aviato.DatasourceQuery) (aviato.DatasourcePage, error)
	DatasourceGet(ctx context.Context, c *aviato.DatasourceContext, id string) (aviato.Record, error)
	DatasourceCreate(ctx context.Context, c *aviato.DatasourceContext, values aviato.Record) (aviato.Record, error)
	DatasourceUpdate(ctx context.Context, c *aviato.DatasourceContext, id string, values aviato.Record) (aviato.Record, error)
	DatasourceDelete(ctx context.Context, c *aviato.DatasourceContext, id string) error
	ListChanges(ctx context.Context, collection, cursor string) (aviato.DatasourceChanges, error)
}

// Harness calls a plugin's handlers as the agent would.
type Harness struct {
	dispatcher dispatcher
	caller     aviato.Caller
	data       *FakeData
}

// Option configures a [Harness].
type Option func(*Harness)

// WithCaller replaces the default caller ([DefaultCaller]). Its DataURL and InvocationToken
// are overridden to point at the fake Data API.
func WithCaller(caller aviato.Caller) Option {
	return func(h *Harness) { h.caller = caller }
}

// WithRecords seeds the fake Data API with records of collection. Records are identified by
// their "id" field.
func WithRecords(collection string, records ...aviato.Record) Option {
	return func(h *Harness) { h.data.Seed(collection, records...) }
}

// DefaultCaller is the caller used unless [WithCaller] is given: an admin user.
func DefaultCaller() aviato.Caller {
	return aviato.Caller{
		UserID:         "u_test",
		Name:           "Test User",
		ActorType:      "human",
		Role:           "admin",
		OrganizationID: "o_test",
		ProjectID:      "p_test",
		Environment:    "test",
		RequestID:      "r_test",
	}
}

// New returns a harness for plugin. The fake Data API it starts is stopped when the test ends.
func New(tb testing.TB, plugin *aviato.Plugin, options ...Option) *Harness {
	tb.Helper()
	h := &Harness{
		dispatcher: bridge.Dispatcher(plugin).(dispatcher),
		caller:     DefaultCaller(),
		data:       newFakeData(tb),
	}
	for _, option := range options {
		option(h)
	}
	h.caller.DataURL = h.data.URL()
	h.caller.InvocationToken = h.data.token
	return h
}

// Caller is the caller handlers receive.
func (h *Harness) Caller() aviato.Caller { return h.caller }

// Data is the fake Data API, to inspect what handlers wrote.
func (h *Harness) Data() *FakeData { return h.data }

// Target is what an action runs on, and its form values.
type Target struct {
	RecordIDs []string
	Filter    map[string]any
	Segment   string
	Search    string
	Values    map[string]any
}

func (h *Harness) actionContext(collection string, target Target) (*aviato.ActionContext, error) {
	values, err := normalize(target.Values)
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = map[string]any{}
	}
	filter, err := normalize(target.Filter)
	if err != nil {
		return nil, err
	}
	return &aviato.ActionContext{
		Invocation: h.dispatcher.Invocation(h.caller, collection),
		RecordIDs:  target.RecordIDs,
		Filter:     filter,
		Segment:    target.Segment,
		Search:     target.Search,
		Values:     values,
	}, nil
}

// Execute runs an action and returns its result (file results carry their content).
func (h *Harness) Execute(ctx context.Context, collection, action string, target Target) (aviato.Result, error) {
	a, err := h.actionContext(collection, target)
	if err != nil {
		return nil, err
	}
	return h.dispatcher.Execute(ctx, action, a)
}

// ResolveForm resolves an action form after changedField changed ("" when the form opens).
func (h *Harness) ResolveForm(ctx context.Context, collection, action string, target Target, changedField string) (aviato.FormResolution, error) {
	a, err := h.actionContext(collection, target)
	if err != nil {
		return aviato.FormResolution{}, err
	}
	return h.dispatcher.ResolveForm(ctx, action, &aviato.FormContext{ActionContext: *a, ChangedField: changedField})
}

// Compute computes one field for records, returning one value per record.
func (h *Harness) Compute(ctx context.Context, collection, field string, records ...aviato.Record) ([]any, error) {
	normalized := make([]aviato.Record, 0, len(records))
	for _, record := range records {
		value, err := normalize(record)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, value)
	}
	inv := h.dispatcher.Invocation(h.caller, collection)
	computed, err := h.dispatcher.Compute(ctx, &inv, []string{field}, normalized)
	if err != nil {
		return nil, err
	}
	values := make([]any, 0, len(computed))
	for _, value := range computed {
		normalizedValue, err := normalizeValue(value[field])
		if err != nil {
			return nil, err
		}
		values = append(values, normalizedValue)
	}
	return values, nil
}

// Hook describes the operation a hook runs for.
type Hook struct {
	RecordIDs []string
	Values    map[string]any
	Action    string
}

// RunHook runs the hooks registered for an operation, as the agent does.
func (h *Harness) RunHook(ctx context.Context, collection string, timing aviato.HookTiming, operation aviato.HookOperation, hook Hook) (aviato.HookResult, error) {
	values, err := normalize(hook.Values)
	if err != nil {
		return aviato.HookResult{}, err
	}
	return h.dispatcher.Hooks(ctx, &aviato.HookContext{
		Invocation: h.dispatcher.Invocation(h.caller, collection),
		Timing:     timing,
		Operation:  operation,
		RecordIDs:  hook.RecordIDs,
		Values:     values,
		Action:     hook.Action,
	})
}

// WriteField runs the write override of a field.
func (h *Harness) WriteField(ctx context.Context, collection, field string, value any, record aviato.Record) (map[string]any, error) {
	normalizedValue, err := normalizeValue(value)
	if err != nil {
		return nil, err
	}
	normalizedRecord, err := normalize(record)
	if err != nil {
		return nil, err
	}
	inv := h.dispatcher.Invocation(h.caller, collection)
	return h.dispatcher.Write(ctx, &inv, field, normalizedValue, normalizedRecord)
}

// Segment resolves a code segment.
func (h *Harness) Segment(ctx context.Context, collection, name string) (aviato.SegmentResult, error) {
	inv := h.dispatcher.Invocation(h.caller, collection)
	return h.dispatcher.Segment(ctx, &inv, name)
}

// Search builds the search filter of a collection.
func (h *Harness) Search(ctx context.Context, collection, query string) (map[string]any, error) {
	inv := h.dispatcher.Invocation(h.caller, collection)
	return h.dispatcher.Search(ctx, &inv, query)
}

// Chart computes a chart.
func (h *Harness) Chart(ctx context.Context, name string, filter map[string]any) (map[string]any, error) {
	normalized, err := normalize(filter)
	if err != nil {
		return nil, err
	}
	return h.dispatcher.Chart(ctx, h.caller, name, normalized)
}

func (h *Harness) datasourceContext(collection string) *aviato.DatasourceContext {
	caller := h.caller
	return &aviato.DatasourceContext{Collection: collection, Caller: &caller}
}

// ListCustomRecords lists the records of a custom collection, as the agent does with the
// translation strategy.
func (h *Harness) ListCustomRecords(ctx context.Context, collection string, query aviato.DatasourceQuery) (aviato.DatasourcePage, error) {
	filter, err := normalize(query.Filter)
	if err != nil {
		return aviato.DatasourcePage{}, err
	}
	query.Filter = filter
	page, err := h.dispatcher.DatasourceList(ctx, h.datasourceContext(collection), query)
	if err != nil {
		return aviato.DatasourcePage{}, err
	}
	if page.Records, err = normalizeRecords(page.Records); err != nil {
		return aviato.DatasourcePage{}, err
	}
	return page, nil
}

// GetCustomRecord returns one record of a custom collection (an error with Connect code
// not_found when there is none).
func (h *Harness) GetCustomRecord(ctx context.Context, collection, id string) (aviato.Record, error) {
	record, err := h.dispatcher.DatasourceGet(ctx, h.datasourceContext(collection), id)
	if err != nil {
		return nil, err
	}
	return normalize(record)
}

// CreateCustomRecord creates a record of a custom collection and returns it as stored.
func (h *Harness) CreateCustomRecord(ctx context.Context, collection string, values aviato.Record) (aviato.Record, error) {
	normalized, err := normalize(values)
	if err != nil {
		return nil, err
	}
	record, err := h.dispatcher.DatasourceCreate(ctx, h.datasourceContext(collection), normalized)
	if err != nil {
		return nil, err
	}
	return normalize(record)
}

// UpdateCustomRecord updates a record of a custom collection and returns it as stored.
func (h *Harness) UpdateCustomRecord(ctx context.Context, collection, id string, values aviato.Record) (aviato.Record, error) {
	normalized, err := normalize(values)
	if err != nil {
		return nil, err
	}
	record, err := h.dispatcher.DatasourceUpdate(ctx, h.datasourceContext(collection), id, normalized)
	if err != nil {
		return nil, err
	}
	return normalize(record)
}

// DeleteCustomRecord deletes a record of a custom collection.
func (h *Harness) DeleteCustomRecord(ctx context.Context, collection, id string) error {
	return h.dispatcher.DatasourceDelete(ctx, h.datasourceContext(collection), id)
}

// ListChanges returns the changes of a replicated collection since cursor, as the agent pulls
// them (without a caller).
func (h *Harness) ListChanges(ctx context.Context, collection, cursor string) (aviato.DatasourceChanges, error) {
	changes, err := h.dispatcher.ListChanges(ctx, collection, cursor)
	if err != nil {
		return aviato.DatasourceChanges{}, err
	}
	if changes.Records, err = normalizeRecords(changes.Records); err != nil {
		return aviato.DatasourceChanges{}, err
	}
	return changes, nil
}

func normalizeRecords(records []aviato.Record) ([]aviato.Record, error) {
	normalized := make([]aviato.Record, 0, len(records))
	for _, record := range records {
		value, err := normalize(record)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

// normalize sends a JSON object through a JSON round trip, as the protocol does.
func normalize(value map[string]any) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("aviatotest: value is not JSON-serializable: %w", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func normalizeValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("aviatotest: value is not JSON-serializable: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
