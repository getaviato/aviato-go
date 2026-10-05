package aviato_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	aviato "github.com/getaviato/aviato-go"
	pluginv1 "github.com/getaviato/aviato-go/pluginv1"
	"github.com/getaviato/aviato-go/pluginv1/pluginv1connect"
)

type seenList struct {
	collection string
	caller     string
	query      aviato.DatasourceQuery
}

// datasourcePlugin serves "tickets" (translation, list and create only) and "rates"
// (replication).
func datasourcePlugin(t *testing.T) (*aviato.Plugin, func() seenList) {
	t.Helper()
	var (
		mu      sync.Mutex
		seen    seenList
		tickets = []aviato.Record{
			{"id": 1, "subject": "Login broken", "status": "open"},
			{"id": 2, "subject": "Invoice copy", "status": "closed"},
		}
	)
	plugin := aviato.MustNew(aviato.Options{Secret: secret, BasePath: "/aviato"}).
		Datasource(aviato.Datasource{
			Collections: []aviato.CustomCollection{{
				Name: "tickets",
				Fields: []aviato.CustomField{
					{Name: "id", Type: "number", ReadOnly: true, NotNull: true},
					{Name: "subject", Type: "string"},
					{Name: "status", Type: "enum", EnumValues: []string{"open", "closed"}},
				},
				Capabilities: aviato.Capabilities{FilterOperators: []string{"eq"}, Sort: true},
				Relations:    []aviato.RelationHint{{Name: "customer", Kind: aviato.BelongsTo, ForeignKey: []string{"customer_id"}, Target: "customers", TargetKey: []string{"id"}}},
			}},
			List: func(_ context.Context, d *aviato.DatasourceContext, query aviato.DatasourceQuery) (aviato.DatasourcePage, error) {
				mu.Lock()
				defer mu.Unlock()
				seen = seenList{collection: d.Collection, query: query}
				if d.Caller != nil {
					seen.caller = d.Caller.UserID
				}
				status, _ := query.Filter["status"].(map[string]any)
				var records []aviato.Record
				for _, ticket := range tickets {
					if status == nil || ticket["status"] == status["$eq"] {
						records = append(records, ticket)
					}
				}
				if len(query.Sort) > 0 && query.Sort[0].Desc {
					for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
						records[left], records[right] = records[right], records[left]
					}
				}
				return aviato.DatasourcePage{Records: records}, nil
			},
			Create: func(_ context.Context, _ *aviato.DatasourceContext, values aviato.Record) (aviato.Record, error) {
				if values["subject"] == nil {
					return nil, aviato.NewDatasourceError(aviato.DatasourceInvalid, "A subject is required")
				}
				mu.Lock()
				defer mu.Unlock()
				ticket := aviato.Record{"id": len(tickets) + 1, "status": "open"}
				for key, value := range values {
					ticket[key] = value
				}
				tickets = append(tickets, ticket)
				return ticket, nil
			},
		}).
		Datasource(aviato.Datasource{
			Strategy: aviato.ReplicationStrategy,
			Collections: []aviato.CustomCollection{{
				Name:                "rates",
				PrimaryKey:          []string{"currency"},
				Fields:              []aviato.CustomField{{Name: "currency", Type: "string"}, {Name: "rate", Type: "number"}},
				ReplicationInterval: time.Minute,
			}},
			List: func(context.Context, *aviato.DatasourceContext, aviato.DatasourceQuery) (aviato.DatasourcePage, error) {
				return aviato.DatasourcePage{}, nil
			},
			ListChanges: func(_ context.Context, d *aviato.DatasourceContext, cursor string) (aviato.DatasourceChanges, error) {
				if d.Caller != nil {
					return aviato.DatasourceChanges{}, errors.New("replication calls have no caller")
				}
				if cursor == "" {
					return aviato.DatasourceChanges{Records: []aviato.Record{{"currency": "EUR", "rate": 1}}, NextCursor: "1", HasMore: true}, nil
				}
				return aviato.DatasourceChanges{DeletedIDs: []string{"USD"}, NextCursor: "2"}, nil
			},
		})
	return plugin, func() seenList {
		mu.Lock()
		defer mu.Unlock()
		return seen
	}
}

func datasourceClient(t *testing.T, plugin *aviato.Plugin) (pluginv1connect.DatasourceServiceClient, *fixture) {
	t.Helper()
	f := newFixture(t, plugin)
	return pluginv1connect.NewDatasourceServiceClient(f.signed, f.baseURL, connect.WithProtoJSON()), f
}

func TestDatasourceIsAnnouncedAndDescribed(t *testing.T) {
	plugin, _ := datasourcePlugin(t)
	client, f := datasourceClient(t, plugin)
	ctx := context.Background()

	manifest, err := f.client.GetManifest(ctx, connect.NewRequest(&pluginv1.GetManifestRequest{AgentProtocolVersion: 1}))
	require.NoError(t, err)
	assert.True(t, manifest.Msg.GetManifest().GetDatasource())
	withoutDatasource, err := testPlugin(t).Manifest()
	require.NoError(t, err)
	assert.False(t, withoutDatasource.GetDatasource())

	response, err := client.DescribeCollections(ctx, connect.NewRequest(&pluginv1.DescribeCollectionsRequest{}))
	require.NoError(t, err)
	collections := response.Msg.GetCollections()
	require.Len(t, collections, 2)
	tickets := collections[0]
	assert.Equal(t, "tickets", tickets.GetName())
	assert.Equal(t, []string{"id"}, tickets.GetPrimaryKey())
	assert.Equal(t, pluginv1.DatasourceStrategy_DATASOURCE_STRATEGY_TRANSLATION, tickets.GetStrategy())
	assert.Equal(t, []string{"eq"}, tickets.GetCapabilities().GetFilterOperators())
	assert.True(t, tickets.GetCapabilities().GetSort())
	assert.False(t, tickets.GetCapabilities().GetCount())
	assert.True(t, tickets.GetCapabilities().GetCreate())
	assert.False(t, tickets.GetCapabilities().GetUpdate())
	assert.False(t, tickets.GetCapabilities().GetDelete())
	require.Len(t, tickets.GetRelations(), 1)
	assert.Equal(t, "tickets", tickets.GetRelations()[0].GetCollection())
	assert.Equal(t, "customers", tickets.GetRelations()[0].GetTarget())
	assert.False(t, tickets.GetFields()[0].GetNullable())
	assert.True(t, tickets.GetFields()[0].GetReadOnly())
	assert.True(t, tickets.GetFields()[2].GetNullable())
	assert.Equal(t, []string{"open", "closed"}, tickets.GetFields()[2].GetEnumValues())

	rates := collections[1]
	assert.Equal(t, []string{"currency"}, rates.GetPrimaryKey())
	assert.Equal(t, pluginv1.DatasourceStrategy_DATASOURCE_STRATEGY_REPLICATION, rates.GetStrategy())
	assert.EqualValues(t, 60, rates.GetReplicationIntervalSeconds())
}

func TestDatasourceList(t *testing.T) {
	plugin, seen := datasourcePlugin(t)
	client, f := datasourceClient(t, plugin)
	filter, err := structpb.NewStruct(map[string]any{"status": map[string]any{"$eq": "open"}})
	require.NoError(t, err)
	response, err := client.List(context.Background(), connect.NewRequest(&pluginv1.DatasourceServiceListRequest{Caller: f.caller, Collection: "tickets", Filter: filter, Sort: "-id", Limit: 10, IncludeTotal: true}))
	require.NoError(t, err)
	require.Len(t, response.Msg.GetRecords(), 1)
	assert.Equal(t, map[string]any{"id": 1.0, "subject": "Login broken", "status": "open"}, response.Msg.GetRecords()[0].AsMap())
	assert.EqualValues(t, 1, response.Msg.GetTotal(), "the total defaults to the number of records")
	assert.Equal(t, seenList{
		collection: "tickets",
		caller:     "u1",
		query:      aviato.DatasourceQuery{Filter: map[string]any{"status": map[string]any{"$eq": "open"}}, Sort: []aviato.SortField{{Field: "id", Desc: true}}, Limit: 10, IncludeTotal: true},
	}, seen())
}

func TestDatasourceGetFallsBackToList(t *testing.T) {
	plugin, _ := datasourcePlugin(t)
	client, f := datasourceClient(t, plugin)
	ctx := context.Background()
	response, err := client.Get(ctx, connect.NewRequest(&pluginv1.DatasourceServiceGetRequest{Caller: f.caller, Collection: "tickets", Id: "2"}))
	require.NoError(t, err)
	assert.Equal(t, "Invoice copy", response.Msg.GetRecord().AsMap()["subject"])

	_, err = client.Get(ctx, connect.NewRequest(&pluginv1.DatasourceServiceGetRequest{Caller: f.caller, Collection: "tickets", Id: "99"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = client.Get(ctx, connect.NewRequest(&pluginv1.DatasourceServiceGetRequest{Caller: f.caller, Collection: "nope", Id: "1"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestDatasourceWritesAndBusinessErrors(t *testing.T) {
	plugin, _ := datasourcePlugin(t)
	client, f := datasourceClient(t, plugin)
	ctx := context.Background()
	values, err := structpb.NewStruct(map[string]any{"subject": "New"})
	require.NoError(t, err)
	created, err := client.Create(ctx, connect.NewRequest(&pluginv1.DatasourceServiceCreateRequest{Caller: f.caller, Collection: "tickets", Values: values}))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"id": 3.0, "status": "open", "subject": "New"}, created.Msg.GetRecord().AsMap())

	_, err = client.Create(ctx, connect.NewRequest(&pluginv1.DatasourceServiceCreateRequest{Caller: f.caller, Collection: "tickets", Values: &structpb.Struct{}}))
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
	assert.Equal(t, "A subject is required", connectErr.Message())

	_, err = client.Update(ctx, connect.NewRequest(&pluginv1.DatasourceServiceUpdateRequest{Caller: f.caller, Collection: "tickets", Id: "1", Values: values}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
	_, err = client.Delete(ctx, connect.NewRequest(&pluginv1.DatasourceServiceDeleteRequest{Caller: f.caller, Collection: "tickets", Id: "1"}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

func TestDatasourceErrorKinds(t *testing.T) {
	kinds := map[aviato.DatasourceErrorKind]connect.Code{
		aviato.DatasourceNotFound:  connect.CodeNotFound,
		aviato.DatasourceInvalid:   connect.CodeInvalidArgument,
		aviato.DatasourceConflict:  connect.CodeAlreadyExists,
		aviato.DatasourceForbidden: connect.CodePermissionDenied,
	}
	for kind, code := range kinds {
		plugin := aviato.MustNew(aviato.Options{Secret: secret, BasePath: "/aviato"}).Datasource(aviato.Datasource{
			Collections: []aviato.CustomCollection{{Name: "x"}},
			List: func(context.Context, *aviato.DatasourceContext, aviato.DatasourceQuery) (aviato.DatasourcePage, error) {
				return aviato.DatasourcePage{}, nil
			},
			Delete: func(context.Context, *aviato.DatasourceContext, string) error {
				return fmt.Errorf("deleting: %w", aviato.NewDatasourceError(kind, "because"))
			},
		})
		client, f := datasourceClient(t, plugin)
		_, err := client.Delete(context.Background(), connect.NewRequest(&pluginv1.DatasourceServiceDeleteRequest{Caller: f.caller, Collection: "x", Id: "1"}))
		assert.Equal(t, code, connect.CodeOf(err), kind)
	}
}

func TestDatasourceListChanges(t *testing.T) {
	plugin, _ := datasourcePlugin(t)
	client, _ := datasourceClient(t, plugin)
	ctx := context.Background()
	first, err := client.ListChanges(ctx, connect.NewRequest(&pluginv1.ListChangesRequest{Collection: "rates"}))
	require.NoError(t, err)
	require.Len(t, first.Msg.GetRecords(), 1)
	assert.Equal(t, map[string]any{"currency": "EUR", "rate": 1.0}, first.Msg.GetRecords()[0].AsMap())
	assert.Equal(t, "1", first.Msg.GetNextCursor())
	assert.True(t, first.Msg.GetHasMore())

	second, err := client.ListChanges(ctx, connect.NewRequest(&pluginv1.ListChangesRequest{Collection: "rates", Cursor: "1"}))
	require.NoError(t, err)
	assert.Empty(t, second.Msg.GetRecords())
	assert.Equal(t, []string{"USD"}, second.Msg.GetDeletedIds())
	assert.Equal(t, "2", second.Msg.GetNextCursor())
	assert.False(t, second.Msg.GetHasMore())

	_, err = client.ListChanges(ctx, connect.NewRequest(&pluginv1.ListChangesRequest{Collection: "tickets"}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

func TestDatasourceRegistrationIsValidated(t *testing.T) {
	list := func(context.Context, *aviato.DatasourceContext, aviato.DatasourceQuery) (aviato.DatasourcePage, error) {
		return aviato.DatasourcePage{}, nil
	}
	assert.PanicsWithValue(t, "aviato: datasource has no List function", func() {
		aviato.MustNew(aviato.Options{Secret: secret}).Datasource(aviato.Datasource{Collections: []aviato.CustomCollection{{Name: "x"}}})
	})
	assert.PanicsWithValue(t, `aviato: custom collection "x" is replicated: its datasource needs ListChanges`, func() {
		aviato.MustNew(aviato.Options{Secret: secret}).Datasource(aviato.Datasource{Strategy: aviato.ReplicationStrategy, Collections: []aviato.CustomCollection{{Name: "x"}}, List: list})
	})
	assert.PanicsWithValue(t, `aviato: custom collection "x" is declared twice`, func() {
		aviato.MustNew(aviato.Options{Secret: secret}).
			Datasource(aviato.Datasource{Collections: []aviato.CustomCollection{{Name: "x"}}, List: list}).
			Datasource(aviato.Datasource{Collections: []aviato.CustomCollection{{Name: "x"}}, List: list})
	})
}

func TestRefreshReplicaSignsTheRequest(t *testing.T) {
	plugin, _ := datasourcePlugin(t)
	webhook, err := standardwebhooks.NewWebhook(secret)
	require.NoError(t, err)
	type received struct {
		path     string
		verified bool
	}
	var calls []received
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, received{path: r.URL.Path, verified: webhook.Verify(body, r.Header) == nil})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"collection": "rates", "upserted": 1, "deleted": 0, "cursor": "1"})
	}))
	t.Cleanup(agent.Close)

	result, err := plugin.RefreshReplica(context.Background(), "rates", agent.URL+"/plugin-data/")
	require.NoError(t, err)
	assert.Equal(t, aviato.ReplicaRefresh{Collection: "rates", Upserted: 1, Cursor: "1"}, result)
	assert.Equal(t, []received{{path: "/plugin-data/replicas/rates/refresh", verified: true}}, calls)

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	t.Cleanup(failing.Close)
	_, err = plugin.RefreshReplica(context.Background(), "rates", failing.URL)
	require.ErrorContains(t, err, "401")
}
