package aviato

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	pluginv1 "github.com/getaviato/aviato-go/pluginv1"
)

// DatasourceStrategy is how the agent serves a custom collection.
type DatasourceStrategy int

const (
	// TranslationStrategy (the default) forwards every read and write to the plugin when it
	// happens.
	TranslationStrategy DatasourceStrategy = iota + 1
	// ReplicationStrategy makes the agent copy the collection with [Datasource.ListChanges] and
	// serve reads from its copy; writes still go to the plugin.
	ReplicationStrategy
)

func (s DatasourceStrategy) proto() pluginv1.DatasourceStrategy {
	if s == ReplicationStrategy {
		return pluginv1.DatasourceStrategy_DATASOURCE_STRATEGY_REPLICATION
	}
	return pluginv1.DatasourceStrategy_DATASOURCE_STRATEGY_TRANSLATION
}

// CustomField is a field of a custom collection. Fields are nullable unless NotNull is set.
type CustomField struct {
	Name string
	// Type is a protocol field type: string, number, boolean, date, dateonly, time, json, uuid,
	// enum or binary.
	Type       string
	EnumValues []string
	NotNull    bool
	ReadOnly   bool
	HasDefault bool
}

// Capabilities is what a collection's List handler does itself. The agent evaluates everything
// else in memory, over at most 2,000 records, and always checks the records you return against
// the caller's permissions.
type Capabilities struct {
	// FilterOperators are the MongoDB operators List applies, without `$`: eq, ne, gt, gte, lt,
	// lte, in, nin, exists, regex, like, ilike, and the logical or and nor (negations arrive as
	// $nor; $and is always understood).
	FilterOperators []string
	// Sort is set when List sorts on any field.
	Sort bool
	// Count is set when List returns the total number of matching records.
	Count bool
}

// CustomCollection declares a collection served by a [Datasource].
type CustomCollection struct {
	Name   string
	Fields []CustomField
	// PrimaryKey defaults to ["id"]. Composite keys arrive as "a|b" in record ids.
	PrimaryKey []string
	// Relations to other collections (custom or database ones). Their Collection is set to Name.
	Relations    []RelationHint
	Capabilities Capabilities
	// Strategy overrides the datasource's strategy.
	Strategy DatasourceStrategy
	// ReplicationInterval is the time between two ListChanges pulls of a replicated collection
	// (whole seconds; zero: on demand only, see [Plugin.RefreshReplica]).
	ReplicationInterval time.Duration
}

// SortField is one key of a [DatasourceQuery] sort.
type SortField struct {
	Field string
	Desc  bool
}

// DatasourceQuery is what the agent asks a List handler for.
type DatasourceQuery struct {
	// Filter in MongoDB query syntax, using only the declared operators (row scopes included).
	// Nil when there is none.
	Filter map[string]any
	// Sort is empty unless the collection declares Capabilities.Sort.
	Sort []SortField
	// Limit is 0 for every matching record.
	Limit  uint32
	Offset uint32
	// Fields to return (empty: all); returning more is fine.
	Fields []string
	// IncludeTotal asks for [DatasourcePage.Total] (when the collection declares Count).
	IncludeTotal bool
}

// DatasourcePage is what a List handler returns.
type DatasourcePage struct {
	Records []Record
	// Total is the number of matching records; it defaults to len(Records).
	Total uint64
}

// DatasourceChanges is what a ListChanges handler returns.
type DatasourceChanges struct {
	// Records created or updated since the cursor (full records).
	Records []Record
	// DeletedIDs are the ids of the records deleted since the cursor (composite keys as "a|b").
	DeletedIDs []string
	NextCursor string
	// HasMore is set when more changes are waiting: the agent calls again right away.
	HasMore bool
}

// DatasourceContext is what every datasource handler receives about the call.
type DatasourceContext struct {
	// Collection is the name of the custom collection, as you declared it.
	Collection string
	// Caller is who the agent acts for; nil for the agent's own calls (replication).
	Caller *Caller
}

type (
	// DatasourceListFunc returns a page of the records matching query.
	DatasourceListFunc func(ctx context.Context, d *DatasourceContext, query DatasourceQuery) (DatasourcePage, error)
	// DatasourceGetFunc returns one record, or nil when there is none.
	DatasourceGetFunc func(ctx context.Context, d *DatasourceContext, id string) (Record, error)
	// DatasourceCreateFunc creates a record and returns it as stored, with its primary key.
	DatasourceCreateFunc func(ctx context.Context, d *DatasourceContext, values Record) (Record, error)
	// DatasourceUpdateFunc updates a record and returns it as stored.
	DatasourceUpdateFunc func(ctx context.Context, d *DatasourceContext, id string, values Record) (Record, error)
	// DatasourceDeleteFunc deletes a record.
	DatasourceDeleteFunc func(ctx context.Context, d *DatasourceContext, id string) error
	// DatasourceChangesFunc returns the changes since cursor (empty for a full copy).
	DatasourceChangesFunc func(ctx context.Context, d *DatasourceContext, cursor string) (DatasourceChanges, error)
)

// Datasource serves collections from your code: an internal API, a SaaS (Stripe, HubSpot), a
// file. The agent applies roles, row scopes, field masking and the audit trail as for its
// databases. Register it with [Plugin.Datasource].
type Datasource struct {
	Collections []CustomCollection
	// Strategy defaults to [TranslationStrategy].
	Strategy DatasourceStrategy
	// List is required.
	List DatasourceListFunc
	// Get defaults to looking the record up with List.
	Get DatasourceGetFunc
	// Create, Update and Delete are optional; without any of them the collections are
	// read-only.
	Create DatasourceCreateFunc
	Update DatasourceUpdateFunc
	Delete DatasourceDeleteFunc
	// ListChanges is required by replicated collections.
	ListChanges DatasourceChangesFunc
}

// DatasourceErrorKind is the meaning of a [DatasourceError].
type DatasourceErrorKind string

// Datasource error kinds, shown to the user as HTTP 404, 422, 409 and 403.
const (
	DatasourceNotFound  DatasourceErrorKind = "not_found"
	DatasourceInvalid   DatasourceErrorKind = "invalid"
	DatasourceConflict  DatasourceErrorKind = "conflict"
	DatasourceForbidden DatasourceErrorKind = "forbidden"
)

func (k DatasourceErrorKind) code() connect.Code {
	switch k {
	case DatasourceNotFound:
		return connect.CodeNotFound
	case DatasourceInvalid:
		return connect.CodeInvalidArgument
	case DatasourceConflict:
		return connect.CodeAlreadyExists
	case DatasourceForbidden:
		return connect.CodePermissionDenied
	default:
		return connect.CodeUnknown
	}
}

// DatasourceError is a business error of a datasource handler, shown to the user with its
// meaning. Return it (wrapped or not) from a handler:
//
//	return nil, aviato.NewDatasourceError(aviato.DatasourceInvalid, "A subject is required")
type DatasourceError struct {
	Kind    DatasourceErrorKind
	Message string
}

// NewDatasourceError returns a [DatasourceError].
func NewDatasourceError(kind DatasourceErrorKind, message string) *DatasourceError {
	return &DatasourceError{Kind: kind, Message: message}
}

func (e *DatasourceError) Error() string { return e.Message }

// datasourceErr turns a DatasourceError returned by a handler into its Connect error.
func datasourceErr(err error) error {
	var business *DatasourceError
	if errors.As(err, &business) {
		return connect.NewError(business.Kind.code(), errors.New(business.Message))
	}
	return err
}

type customCollectionDef struct {
	collection CustomCollection
	datasource *Datasource
	strategy   DatasourceStrategy
}

// Datasource registers collections served by your code, browsed and edited like database
// collections under the same permissions and audit trail. It panics when List is missing, when
// a replicated collection has no ListChanges, or when a collection name is declared twice.
func (p *Plugin) Datasource(datasource Datasource) *Plugin {
	if datasource.List == nil {
		panic("aviato: datasource has no List function")
	}
	definition := datasource
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, collection := range datasource.Collections {
		if _, exists := p.collections.get(collection.Name); exists {
			panic(fmt.Sprintf("aviato: custom collection %q is declared twice", collection.Name))
		}
		strategy := collection.Strategy
		if strategy == 0 {
			strategy = datasource.Strategy
		}
		if strategy == 0 {
			strategy = TranslationStrategy
		}
		if strategy == ReplicationStrategy && datasource.ListChanges == nil {
			panic(fmt.Sprintf("aviato: custom collection %q is replicated: its datasource needs ListChanges", collection.Name))
		}
		p.collections.set(collection.Name, customCollectionDef{collection: collection, datasource: &definition, strategy: strategy})
	}
	return p
}

// DescribeCollections describes the custom collections, as sent to the agent.
func (p *Plugin) DescribeCollections() *pluginv1.DescribeCollectionsResponse {
	p.mu.RLock()
	defer p.mu.RUnlock()
	response := &pluginv1.DescribeCollectionsResponse{}
	for _, definition := range p.collections.items {
		collection := definition.collection
		described := &pluginv1.CustomCollection{
			Name:       collection.Name,
			PrimaryKey: primaryKey(collection),
			Capabilities: &pluginv1.CollectionCapabilities{
				FilterOperators: collection.Capabilities.FilterOperators,
				Sort:            collection.Capabilities.Sort,
				Count:           collection.Capabilities.Count,
				Create:          definition.datasource.Create != nil,
				Update:          definition.datasource.Update != nil,
				Delete:          definition.datasource.Delete != nil,
			},
			Strategy:                   definition.strategy.proto(),
			ReplicationIntervalSeconds: uint32(collection.ReplicationInterval / time.Second), //nolint:gosec // an interval of over 136 years is not a concern
		}
		for _, field := range collection.Fields {
			described.Fields = append(described.Fields, &pluginv1.CustomField{
				Name:       field.Name,
				Type:       field.Type,
				EnumValues: field.EnumValues,
				Nullable:   !field.NotNull,
				ReadOnly:   field.ReadOnly,
				HasDefault: field.HasDefault,
			})
		}
		for _, relation := range collection.Relations {
			described.Relations = append(described.Relations, &pluginv1.RelationHint{
				Collection: collection.Name,
				Name:       relation.Name,
				Kind:       string(relation.Kind),
				ForeignKey: relation.ForeignKey,
				Target:     relation.Target,
				TargetKey:  relation.TargetKey,
			})
		}
		response.Collections = append(response.Collections, described)
	}
	return response
}

func primaryKey(collection CustomCollection) []string {
	if len(collection.PrimaryKey) == 0 {
		return []string{"id"}
	}
	return collection.PrimaryKey
}

// encodeID encodes the primary key of record as the agent does ("a|b" for composite keys).
func encodeID(record Record, key []string) string {
	parts := make([]string, len(key))
	for index, field := range key {
		switch value := record[field].(type) {
		case nil:
			parts[index] = ""
		case float64:
			parts[index] = strconv.FormatFloat(value, 'f', -1, 64)
		default:
			parts[index] = fmt.Sprint(value)
		}
	}
	if len(parts) > 1 && strings.Contains(strings.Join(parts, ""), "|") {
		for index, part := range parts {
			parts[index] = hex.EncodeToString([]byte(part))
		}
		return "~" + strings.Join(parts, ".")
	}
	return strings.Join(parts, "|")
}

// parseSort turns "field,-other" into sort fields.
func parseSort(sort string) []SortField {
	var fields []SortField
	for part := range strings.SplitSeq(sort, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if name, desc := strings.CutPrefix(part, "-"); desc {
			fields = append(fields, SortField{Field: name, Desc: true})
		} else {
			fields = append(fields, SortField{Field: part})
		}
	}
	return fields
}

func (p *Plugin) lookupCollection(name string) (customCollectionDef, error) {
	p.mu.RLock()
	definition, ok := p.collections.get(name)
	p.mu.RUnlock()
	if !ok {
		return customCollectionDef{}, notFound("unknown custom collection %s", name)
	}
	return definition, nil
}

func unimplemented(format string, args ...any) error {
	return connect.NewError(connect.CodeUnimplemented, fmt.Errorf(format, args...))
}

func (p *Plugin) dispatchDatasourceList(ctx context.Context, d *DatasourceContext, query DatasourceQuery) (DatasourcePage, error) {
	definition, err := p.lookupCollection(d.Collection)
	if err != nil {
		return DatasourcePage{}, err
	}
	page, err := definition.datasource.List(ctx, d, query)
	if err != nil {
		return DatasourcePage{}, datasourceErr(err)
	}
	page.Total = max(page.Total, uint64(len(page.Records)))
	return page, nil
}

func (p *Plugin) dispatchDatasourceGet(ctx context.Context, d *DatasourceContext, id string) (Record, error) {
	definition, err := p.lookupCollection(d.Collection)
	if err != nil {
		return nil, err
	}
	var record Record
	if definition.datasource.Get != nil {
		if record, err = definition.datasource.Get(ctx, d, id); err != nil {
			return nil, datasourceErr(err)
		}
	} else {
		page, err := definition.datasource.List(ctx, d, DatasourceQuery{})
		if err != nil {
			return nil, datasourceErr(err)
		}
		key := primaryKey(definition.collection)
		for _, candidate := range page.Records {
			if encodeID(candidate, key) == id {
				record = candidate
				break
			}
		}
	}
	if record == nil {
		return nil, notFound("no record %s in %s", id, d.Collection)
	}
	return record, nil
}

func (p *Plugin) dispatchDatasourceCreate(ctx context.Context, d *DatasourceContext, values Record) (Record, error) {
	definition, err := p.lookupCollection(d.Collection)
	if err != nil {
		return nil, err
	}
	if definition.datasource.Create == nil {
		return nil, unimplemented("%s does not support creating records", d.Collection)
	}
	if values == nil {
		values = Record{}
	}
	record, err := definition.datasource.Create(ctx, d, values)
	return record, datasourceErr(err)
}

func (p *Plugin) dispatchDatasourceUpdate(ctx context.Context, d *DatasourceContext, id string, values Record) (Record, error) {
	definition, err := p.lookupCollection(d.Collection)
	if err != nil {
		return nil, err
	}
	if definition.datasource.Update == nil {
		return nil, unimplemented("%s does not support updating records", d.Collection)
	}
	if values == nil {
		values = Record{}
	}
	record, err := definition.datasource.Update(ctx, d, id, values)
	return record, datasourceErr(err)
}

func (p *Plugin) dispatchDatasourceDelete(ctx context.Context, d *DatasourceContext, id string) error {
	definition, err := p.lookupCollection(d.Collection)
	if err != nil {
		return err
	}
	if definition.datasource.Delete == nil {
		return unimplemented("%s does not support deleting records", d.Collection)
	}
	return datasourceErr(definition.datasource.Delete(ctx, d, id))
}

func (p *Plugin) dispatchListChanges(ctx context.Context, collection, cursor string) (DatasourceChanges, error) {
	definition, err := p.lookupCollection(collection)
	if err != nil {
		return DatasourceChanges{}, err
	}
	if definition.datasource.ListChanges == nil {
		return DatasourceChanges{}, unimplemented("%s is not replicated", collection)
	}
	changes, err := definition.datasource.ListChanges(ctx, &DatasourceContext{Collection: collection}, cursor)
	if err != nil {
		return DatasourceChanges{}, datasourceErr(err)
	}
	return changes, nil
}

// ReplicaRefresh is the agent's answer to [Plugin.RefreshReplica].
type ReplicaRefresh struct {
	Collection string `json:"collection"`
	Upserted   int    `json:"upserted"`
	Deleted    int    `json:"deleted"`
	Cursor     string `json:"cursor"`
}

// RefreshReplica asks the agent to pull the changes of a replicated collection now, for
// example from a webhook of the source. dataURL is the agent's Data API
// ([Caller.DataURL], <agent URL>/plugin-data). The request is signed with the plugin secret.
func (p *Plugin) RefreshReplica(ctx context.Context, collection, dataURL string) (ReplicaRefresh, error) {
	body := []byte("{}")
	endpoint := strings.TrimRight(dataURL, "/") + "/replicas/" + url.PathEscape(collection) + "/refresh"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ReplicaRefresh{}, fmt.Errorf("aviato: refreshing the replica of %s: %w", collection, err)
	}
	id := "msg_" + randomID()
	timestamp := time.Now()
	signature, err := p.webhook.Sign(id, timestamp, body)
	if err != nil {
		return ReplicaRefresh{}, fmt.Errorf("aviato: signing the replica refresh: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(standardwebhooks.HeaderWebhookID, id)
	request.Header.Set(standardwebhooks.HeaderWebhookTimestamp, strconv.FormatInt(timestamp.Unix(), 10))
	request.Header.Set(standardwebhooks.HeaderWebhookSignature, signature)
	client := p.options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return ReplicaRefresh{}, fmt.Errorf("aviato: refreshing the replica of %s: %w", collection, err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return ReplicaRefresh{}, fmt.Errorf("aviato: refreshing the replica of %s: %w", collection, err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return ReplicaRefresh{}, fmt.Errorf("aviato: refreshing the replica of %s failed: %d %s", collection, response.StatusCode, content)
	}
	var result ReplicaRefresh
	if err := json.Unmarshal(content, &result); err != nil {
		return ReplicaRefresh{}, fmt.Errorf("aviato: refreshing the replica of %s: %w", collection, err)
	}
	return result, nil
}
