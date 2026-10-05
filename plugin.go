// Package aviato extends an Aviato agent with custom actions, computed fields, hooks, segments,
// search, write overrides, charts and custom datasources written in Go.
//
// The agent calls the plugin over HTTP (ConnectRPC, JSON encoding) and signs every request
// with the Standard Webhooks scheme; mount [Plugin.Handler] in your application under
// [Options.BasePath]. Plugin code reads and writes records through the agent's Data API
// ([DataClient]) with the caller's permissions, never behind its back.
package aviato

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	pluginv1 "github.com/getaviato/aviato-go/pluginv1"
)

const (
	// ProtocolVersion is the plugin protocol version this SDK speaks.
	ProtocolVersion = 1
	// SDKName is reported to the agent in the manifest.
	SDKName = "github.com/getaviato/aviato-go"
	// SDKVersion is reported to the agent in the manifest.
	SDKVersion = "0.1.0"
)

// Options configure a [Plugin].
type Options struct {
	// Secret is shared with the agent, in the Standard Webhooks format (whsec_<base64>).
	// Requests with a missing, invalid or stale (over five minutes) signature are rejected.
	Secret string
	// BasePath is the mount path of the plugin in your application, for example /aviato.
	BasePath string
	// HTTPClient is used to call the agent's Data API; defaults to http.DefaultClient.
	HTTPClient *http.Client
	// FileTTL is how long file results stay downloadable; defaults to five minutes.
	FileTTL time.Duration
	// MaxRequestBytes bounds request bodies; defaults to 10 MiB.
	MaxRequestBytes int64
}

type (
	// ExecuteFunc runs an action.
	ExecuteFunc func(ctx context.Context, a *ActionContext) (Result, error)
	// FormResolverFunc recomputes an action form when it opens and after each change.
	FormResolverFunc func(ctx context.Context, f *FormContext) (FormResolution, error)
	// ComputeFunc computes a field for a page of records, returning one value per record in
	// the same order.
	ComputeFunc func(ctx context.Context, inv *Invocation, records []Record) ([]any, error)
	// HookFunc runs a hook.
	HookFunc func(ctx context.Context, h *HookContext) (HookResult, error)
	// SegmentFunc resolves a code segment.
	SegmentFunc func(ctx context.Context, inv *Invocation) (SegmentResult, error)
	// SearchFunc turns a search query into a filter (MongoDB query syntax).
	SearchFunc func(ctx context.Context, inv *Invocation, query string) (map[string]any, error)
	// WriteFunc turns a value written to a virtual field into column values.
	WriteFunc func(ctx context.Context, inv *Invocation, value any, record Record) (map[string]any, error)
	// ChartFunc returns a Vega-Lite specification with its data inlined.
	ChartFunc func(ctx context.Context, inv *Invocation, filter map[string]any) (map[string]any, error)
)

type actionDef struct {
	collection    string
	name          string
	scope         Scope
	description   string
	form          *Form
	resolveForm   FormResolverFunc
	generatesFile bool
	execute       ExecuteFunc
}

// ActionOption configures an action.
type ActionOption func(*actionDef)

// WithDescription describes the action to users and AI agents.
func WithDescription(description string) ActionOption {
	return func(a *actionDef) { a.description = description }
}

// WithForm gives the action a form, as a JSON Schema describing the data.
func WithForm(schema map[string]any) ActionOption {
	return func(a *actionDef) {
		if a.form == nil {
			a.form = &Form{}
		}
		a.form.Schema = schema
	}
}

// WithFormUI lays the form out with a JSON Forms UI schema (layout, rules).
func WithFormUI(uiSchema map[string]any) ActionOption {
	return func(a *actionDef) {
		if a.form == nil {
			a.form = &Form{}
		}
		a.form.UISchema = uiSchema
	}
}

// WithFormResolver makes the form dynamic: resolve is called when the form opens and after
// each change to compute visibility, defaults, choices…
func WithFormResolver(resolve FormResolverFunc) ActionOption {
	return func(a *actionDef) { a.resolveForm = resolve }
}

// WithGeneratesFile declares that the action produces a file, downloaded instead of shown.
func WithGeneratesFile() ActionOption {
	return func(a *actionDef) { a.generatesFile = true }
}

type computedDef struct {
	collection        string
	name              string
	fieldType         string
	dependencies      []string
	enumValues        []string
	emulateFilterSort bool
	compute           ComputeFunc
}

// FieldOption configures a computed field.
type FieldOption func(*computedDef)

// WithEnumValues lists the possible values of an enum computed field.
func WithEnumValues(values ...string) FieldOption {
	return func(c *computedDef) { c.enumValues = values }
}

// WithEmulatedFilterSort lets the agent sort and filter on the field by computing it in
// memory, within a bounded number of records.
func WithEmulatedFilterSort() FieldOption {
	return func(c *computedDef) { c.emulateFilterSort = true }
}

type hookDef struct {
	collection string
	timing     HookTiming
	operation  HookOperation
	run        HookFunc
}

type segmentDef struct {
	collection string
	name       string
	resolve    SegmentFunc
}

type searchDef struct {
	collection string
	replace    bool
	search     SearchFunc
}

// SearchOption configures a search.
type SearchOption func(*searchDef)

// WithReplacedSearch makes the plugin search replace the agent's search entirely instead of
// being combined with it.
func WithReplacedSearch() SearchOption {
	return func(s *searchDef) { s.replace = true }
}

type writeDef struct {
	collection string
	field      string
	write      WriteFunc
}

type chartDef struct {
	name       string
	collection string
	compute    ChartFunc
}

// registry keeps definitions in registration order; re-registering a key replaces the
// definition in place.
type registry[T any] struct {
	items []T
	index map[string]int
}

func (r *registry[T]) set(key string, item T) {
	if r.index == nil {
		r.index = map[string]int{}
	}
	if position, ok := r.index[key]; ok {
		r.items[position] = item
		return
	}
	r.index[key] = len(r.items)
	r.items = append(r.items, item)
}

func (r *registry[T]) get(key string) (T, bool) {
	position, ok := r.index[key]
	if !ok {
		var zero T
		return zero, false
	}
	return r.items[position], true
}

// Plugin extends an Aviato agent. Register handlers with its methods, which return the plugin
// for chaining, then serve [Plugin.Handler]. Registration methods panic on invalid
// definitions (a missing handler), like http.ServeMux does.
type Plugin struct {
	options  Options
	basePath string
	webhook  *standardwebhooks.Webhook
	files    *fileStore

	mu        sync.RWMutex
	actions   registry[actionDef]
	computed  registry[computedDef]
	hooks     []hookDef
	segments  registry[segmentDef]
	searches  registry[searchDef]
	writes    registry[writeDef]
	charts    registry[chartDef]
	summaries registry[summaryDef]
	relations []RelationHint
	// collections are the custom collections of the registered datasources, by name.
	collections registry[customCollectionDef]

	handlerOnce sync.Once
	handler     http.Handler
}

// New returns a plugin. It fails when the secret is missing or not in the whsec_<base64>
// format.
func New(options Options) (*Plugin, error) {
	if options.Secret == "" {
		return nil, errors.New("aviato: Options.Secret is required")
	}
	webhook, err := standardwebhooks.NewWebhook(options.Secret)
	if err != nil {
		return nil, fmt.Errorf("aviato: invalid secret: %w", err)
	}
	if options.FileTTL <= 0 {
		options.FileTTL = 5 * time.Minute
	}
	if options.MaxRequestBytes <= 0 {
		options.MaxRequestBytes = 10 << 20
	}
	return &Plugin{
		options:  options,
		basePath: strings.TrimRight(options.BasePath, "/"),
		webhook:  webhook,
		files:    newFileStore(options.FileTTL),
	}, nil
}

// MustNew is like [New] but panics on error.
func MustNew(options Options) *Plugin {
	plugin, err := New(options)
	if err != nil {
		panic(err)
	}
	return plugin
}

// BasePath is the mount path of the plugin, without trailing slash.
func (p *Plugin) BasePath() string { return p.basePath }

func key(collection, name string) string { return collection + "\x00" + name }

// Action registers an action on collection. Its name is unique within the collection.
func (p *Plugin) Action(collection, name string, scope Scope, execute ExecuteFunc, options ...ActionOption) *Plugin {
	if execute == nil {
		panic(fmt.Sprintf("aviato: action %s.%s has no execute function", collection, name))
	}
	definition := actionDef{collection: collection, name: name, scope: scope, execute: execute}
	for _, option := range options {
		option(&definition)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.actions.set(key(collection, name), definition)
	return p
}

// ComputedField registers a field computed from the dependencies of each record. fieldType is
// a protocol field type: string, number, boolean, date, dateonly, json, enum…
func (p *Plugin) ComputedField(collection, name, fieldType string, dependencies []string, compute ComputeFunc, options ...FieldOption) *Plugin {
	if compute == nil {
		panic(fmt.Sprintf("aviato: computed field %s.%s has no compute function", collection, name))
	}
	definition := computedDef{collection: collection, name: name, fieldType: fieldType, dependencies: dependencies, compute: compute}
	for _, option := range options {
		option(&definition)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.computed.set(key(collection, name), definition)
	return p
}

// Hook registers a hook run before or after an operation on collection. Several hooks may be
// registered for the same operation; they run in registration order.
func (p *Plugin) Hook(collection string, timing HookTiming, operation HookOperation, run HookFunc) *Plugin {
	if run == nil {
		panic(fmt.Sprintf("aviato: hook on %s has no run function", collection))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hooks = append(p.hooks, hookDef{collection: collection, timing: timing, operation: operation, run: run})
	return p
}

// Segment registers a code segment of collection.
func (p *Plugin) Segment(collection, name string, resolve SegmentFunc) *Plugin {
	if resolve == nil {
		panic(fmt.Sprintf("aviato: segment %s.%s has no resolve function", collection, name))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.segments.set(key(collection, name), segmentDef{collection: collection, name: name, resolve: resolve})
	return p
}

// Search registers the search of collection: it turns the query into a filter.
func (p *Plugin) Search(collection string, search SearchFunc, options ...SearchOption) *Plugin {
	if search == nil {
		panic(fmt.Sprintf("aviato: search on %s has no search function", collection))
	}
	definition := searchDef{collection: collection, search: search}
	for _, option := range options {
		option(&definition)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.searches.set(collection, definition)
	return p
}

// WriteOverride registers custom write behaviour for a field (for example splitting a full
// name into first and last names).
func (p *Plugin) WriteOverride(collection, field string, write WriteFunc) *Plugin {
	if write == nil {
		panic(fmt.Sprintf("aviato: write override %s.%s has no write function", collection, field))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes.set(key(collection, field), writeDef{collection: collection, field: field, write: write})
	return p
}

// Chart registers a chart computed by the plugin.
func (p *Plugin) Chart(name, collection string, compute ChartFunc) *Plugin {
	if compute == nil {
		panic(fmt.Sprintf("aviato: chart %s has no compute function", name))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.charts.set(name, chartDef{name: name, collection: collection, compute: compute})
	return p
}

// RelationHints declares relations the agent cannot introspect from the database. See the
// gormhints package to derive them from GORM models.
func (p *Plugin) RelationHints(hints ...RelationHint) *Plugin {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.relations = append(p.relations, hints...)
	return p
}

// Manifest declares what the plugin provides, as sent to the agent.
func (p *Plugin) Manifest() (*pluginv1.Manifest, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	manifest := &pluginv1.Manifest{ProtocolVersion: ProtocolVersion, SdkName: SDKName, SdkVersion: SDKVersion, Datasource: len(p.collections.items) > 0}
	for _, action := range p.actions.items {
		definition := &pluginv1.ActionDefinition{
			Collection:    action.collection,
			Name:          action.name,
			Scope:         action.scope.proto(),
			Description:   action.description,
			DynamicForm:   action.resolveForm != nil,
			GeneratesFile: action.generatesFile,
		}
		if action.form != nil {
			var err error
			if definition.FormSchema, err = toStruct(action.form.Schema); err != nil {
				return nil, fmt.Errorf("action %s.%s form: %w", action.collection, action.name, err)
			}
			if definition.FormUiSchema, err = toStruct(action.form.UISchema); err != nil {
				return nil, fmt.Errorf("action %s.%s UI schema: %w", action.collection, action.name, err)
			}
		}
		manifest.Actions = append(manifest.Actions, definition)
	}
	for _, field := range p.computed.items {
		manifest.ComputedFields = append(manifest.ComputedFields, &pluginv1.ComputedFieldDefinition{
			Collection:        field.collection,
			Name:              field.name,
			Type:              field.fieldType,
			Dependencies:      field.dependencies,
			EmulateFilterSort: field.emulateFilterSort,
			EnumValues:        field.enumValues,
		})
	}
	for _, hook := range p.hooks {
		manifest.Hooks = append(manifest.Hooks, &pluginv1.HookDefinition{Collection: hook.collection, Timing: hook.timing.proto(), Operation: hook.operation.proto()})
	}
	for _, segment := range p.segments.items {
		manifest.Segments = append(manifest.Segments, &pluginv1.SegmentDefinition{Collection: segment.collection, Name: segment.name})
	}
	for _, search := range p.searches.items {
		manifest.Searches = append(manifest.Searches, &pluginv1.SearchDefinition{Collection: search.collection, Replace: search.replace})
	}
	for _, summary := range p.summaries.items {
		document, err := toStruct(summary.document)
		if err != nil {
			return nil, fmt.Errorf("summary %s: %w", summary.collection, err)
		}
		manifest.Summaries = append(manifest.Summaries, &pluginv1.SummaryDefinition{Collection: summary.collection, Document: document})
	}
	for _, chart := range p.charts.items {
		manifest.Charts = append(manifest.Charts, &pluginv1.ChartDefinition{Name: chart.name, Collection: chart.collection})
	}
	for _, write := range p.writes.items {
		manifest.WriteOverrides = append(manifest.WriteOverrides, &pluginv1.WriteOverrideDefinition{Collection: write.collection, Field: write.field})
	}
	for _, hint := range p.relations {
		manifest.RelationHints = append(manifest.RelationHints, &pluginv1.RelationHint{
			Collection: hint.Collection,
			Name:       hint.Name,
			Kind:       string(hint.Kind),
			ForeignKey: hint.ForeignKey,
			Target:     hint.Target,
			TargetKey:  hint.TargetKey,
		})
	}
	return manifest, nil
}

// invocation builds the per-call context shared by every handler.
func (p *Plugin) invocation(caller Caller, collection string) Invocation {
	return Invocation{Caller: caller, Collection: collection, Data: NewDataClient(caller, p.options.HTTPClient)}
}
