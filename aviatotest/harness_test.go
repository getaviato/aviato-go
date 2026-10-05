package aviatotest_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	aviato "github.com/getaviato/aviato-go"
	"github.com/getaviato/aviato-go/aviatotest"
)

var secret = "whsec_" + base64.StdEncoding.EncodeToString([]byte("aviato-harness-test-secret-bytes"))

func plugin() *aviato.Plugin {
	return aviato.MustNew(aviato.Options{Secret: secret}).
		Action("customers", "Refund last invoice", aviato.ScopeSingle, func(_ context.Context, a *aviato.ActionContext) (aviato.Result, error) {
			if a.StringValue("reason") == "" {
				return a.Error("A reason is required"), nil
			}
			return a.Success("Refunded "+a.RecordID(), "invoices"), nil
		}).
		Action("customers", "Archive", aviato.ScopeBulk, func(ctx context.Context, a *aviato.ActionContext) (aviato.Result, error) {
			records, err := a.Records(ctx)
			if err != nil {
				return nil, err
			}
			for _, record := range records {
				if _, err := a.Data.Update(ctx, a.Collection, fmt.Sprint(record["id"]), aviato.Record{"archived": true, "archived_by": a.Caller.UserID}); err != nil {
					return nil, err
				}
			}
			return a.Success(fmt.Sprintf("Archived %d", len(records))), nil
		}).
		Action("customers", "Export", aviato.ScopeGlobal, func(ctx context.Context, a *aviato.ActionContext) (aviato.Result, error) {
			records, err := a.Records(ctx, aviato.RecordsOptions{Fields: []string{"name"}})
			if err != nil {
				return nil, err
			}
			content := "name\n"
			for _, record := range records {
				content += fmt.Sprint(record["name"]) + "\n"
			}
			return a.File("customers.csv", "text/csv", []byte(content)), nil
		}).
		Action("customers", "Double", aviato.ScopeSingle, func(_ context.Context, a *aviato.ActionContext) (aviato.Result, error) {
			amount, ok := a.Values["amount"].(float64)
			if !ok {
				return nil, errors.New("amount is not a JSON number")
			}
			return a.Success(fmt.Sprint(amount * 2)), nil
		}).
		Action("customers", "Plan change", aviato.ScopeSingle, func(context.Context, *aviato.ActionContext) (aviato.Result, error) {
			return aviato.Success("Plan changed"), nil
		}, aviato.WithFormResolver(func(_ context.Context, f *aviato.FormContext) (aviato.FormResolution, error) {
			if f.ChangedField == "plan" && f.Values["plan"] == "team" {
				return aviato.FormResolution{Values: map[string]any{"confirm": true}}, nil
			}
			return aviato.FormResolution{}, nil
		})).
		ComputedField("customers", "fullName", "string", []string{"first_name", "last_name"}, func(_ context.Context, _ *aviato.Invocation, records []aviato.Record) ([]any, error) {
			values := make([]any, len(records))
			for index, record := range records {
				values[index] = fmt.Sprintf("%v %v", record["first_name"], record["last_name"])
			}
			return values, nil
		}).
		Hook("customers", aviato.Before, aviato.HookUpdate, func(_ context.Context, h *aviato.HookContext) (aviato.HookResult, error) {
			if h.Values["plan"] == "enterprise" {
				return aviato.HookResult{Reject: "Enterprise plans are set by sales"}, nil
			}
			return aviato.HookResult{}, nil
		}).
		Segment("customers", "vip", func(context.Context, *aviato.Invocation) (aviato.SegmentResult, error) {
			return aviato.SegmentFilter(map[string]any{"plan": "team"}), nil
		}).
		Search("customers", func(_ context.Context, _ *aviato.Invocation, query string) (map[string]any, error) {
			return map[string]any{"name": map[string]any{"$ilike": "%" + query + "%"}}, nil
		}).
		WriteOverride("customers", "fullName", func(_ context.Context, _ *aviato.Invocation, value any, _ aviato.Record) (map[string]any, error) {
			return map[string]any{"first_name": value}, nil
		}).
		Chart("customersByPlan", "customers", func(_ context.Context, inv *aviato.Invocation, _ map[string]any) (map[string]any, error) {
			return map[string]any{"mark": "bar", "role": inv.Caller.Role}, nil
		})
}

func TestExecute(t *testing.T) {
	h := aviatotest.New(t, plugin())
	ctx := context.Background()

	result, err := h.Execute(ctx, "customers", "Refund last invoice", aviatotest.Target{RecordIDs: []string{"42"}, Values: map[string]any{"reason": "Duplicate"}})
	require.NoError(t, err)
	assert.Equal(t, aviato.Success("Refunded 42", "invoices"), result)

	result, err = h.Execute(ctx, "customers", "Refund last invoice", aviatotest.Target{RecordIDs: []string{"42"}})
	require.NoError(t, err)
	assert.Equal(t, aviato.Error("A reason is required"), result)

	result, err = h.Execute(ctx, "customers", "Double", aviatotest.Target{RecordIDs: []string{"1"}, Values: map[string]any{"amount": 21}})
	require.NoError(t, err)
	assert.Equal(t, aviato.Success("42"), result, "values go through JSON like on the wire")

	_, err = h.Execute(ctx, "customers", "Nope", aviatotest.Target{})
	require.ErrorIs(t, err, aviato.ErrNotFound)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestFakeDataAPI(t *testing.T) {
	h := aviatotest.New(t, plugin(),
		aviatotest.WithCaller(aviato.Caller{UserID: "u_42", Role: "editor"}),
		aviatotest.WithRecords("customers", aviato.Record{"id": "1", "name": "Acme"}, aviato.Record{"id": "2", "name": "Globex"}),
	)
	ctx := context.Background()

	result, err := h.Execute(ctx, "customers", "Archive", aviatotest.Target{RecordIDs: []string{"1"}})
	require.NoError(t, err)
	assert.Equal(t, aviato.Success("Archived 1"), result)
	records := h.Data().Records("customers")
	assert.Equal(t, aviato.Record{"id": "1", "name": "Acme", "archived": true, "archived_by": "u_42"}, records[0])
	assert.Equal(t, aviato.Record{"id": "2", "name": "Globex"}, records[1])

	result, err = h.Execute(ctx, "customers", "Export", aviatotest.Target{Filter: map[string]any{}})
	require.NoError(t, err)
	assert.Equal(t, aviato.File("customers.csv", "text/csv", []byte("name\nAcme\nGlobex\n")), result, "file results carry their content")

	_, err = h.Execute(ctx, "customers", "Archive", aviatotest.Target{RecordIDs: []string{"404"}})
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestOtherHandlers(t *testing.T) {
	h := aviatotest.New(t, plugin())
	ctx := context.Background()

	resolved, err := h.ResolveForm(ctx, "customers", "Plan change", aviatotest.Target{RecordIDs: []string{"7"}, Values: map[string]any{"plan": "team"}}, "plan")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"confirm": true}, resolved.Values)

	values, err := h.Compute(ctx, "customers", "fullName", aviato.Record{"first_name": "Ada", "last_name": "Lovelace"}, aviato.Record{"first_name": "Alan", "last_name": "Turing"})
	require.NoError(t, err)
	assert.Equal(t, []any{"Ada Lovelace", "Alan Turing"}, values)

	hook, err := h.RunHook(ctx, "customers", aviato.Before, aviato.HookUpdate, aviatotest.Hook{RecordIDs: []string{"1"}, Values: map[string]any{"plan": "enterprise"}})
	require.NoError(t, err)
	assert.Equal(t, "Enterprise plans are set by sales", hook.Reject)
	hook, err = h.RunHook(ctx, "customers", aviato.Before, aviato.HookUpdate, aviatotest.Hook{Values: map[string]any{"plan": "pro"}})
	require.NoError(t, err)
	assert.Equal(t, aviato.HookResult{Values: map[string]any{"plan": "pro"}}, hook)

	segment, err := h.Segment(ctx, "customers", "vip")
	require.NoError(t, err)
	filter, ok := segment.Filter()
	assert.True(t, ok)
	assert.Equal(t, map[string]any{"plan": "team"}, filter)

	search, err := h.Search(ctx, "customers", "acme")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": map[string]any{"$ilike": "%acme%"}}, search)

	patch, err := h.WriteField(ctx, "customers", "fullName", "Grace", nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"first_name": "Grace"}, patch)

	chart, err := h.Chart(ctx, "customersByPlan", nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"mark": "bar", "role": "admin"}, chart, "the default caller is an admin")
}

func TestDatasources(t *testing.T) {
	ctx := context.Background()
	tickets := map[string]aviato.Record{"1": {"id": 1, "subject": "Login broken"}}
	p := aviato.MustNew(aviato.Options{Secret: secret}).
		Datasource(aviato.Datasource{
			Collections: []aviato.CustomCollection{{Name: "tickets", Fields: []aviato.CustomField{{Name: "id", Type: "number"}, {Name: "subject", Type: "string"}}}},
			List: func(_ context.Context, d *aviato.DatasourceContext, _ aviato.DatasourceQuery) (aviato.DatasourcePage, error) {
				records := make([]aviato.Record, 0, len(tickets))
				for _, ticket := range tickets {
					records = append(records, aviato.Record{"id": ticket["id"], "subject": ticket["subject"], "seen_by": d.Caller.UserID})
				}
				return aviato.DatasourcePage{Records: records}, nil
			},
			Create: func(_ context.Context, _ *aviato.DatasourceContext, values aviato.Record) (aviato.Record, error) {
				if values["subject"] == nil {
					return nil, aviato.NewDatasourceError(aviato.DatasourceInvalid, "A subject is required")
				}
				record := aviato.Record{"id": len(tickets) + 1, "subject": values["subject"]}
				tickets[fmt.Sprint(record["id"])] = record
				return record, nil
			},
			Delete: func(_ context.Context, _ *aviato.DatasourceContext, id string) error {
				delete(tickets, id)
				return nil
			},
		}).
		Datasource(aviato.Datasource{
			Strategy:    aviato.ReplicationStrategy,
			Collections: []aviato.CustomCollection{{Name: "plans", PrimaryKey: []string{"code"}}},
			List: func(context.Context, *aviato.DatasourceContext, aviato.DatasourceQuery) (aviato.DatasourcePage, error) {
				return aviato.DatasourcePage{}, nil
			},
			ListChanges: func(_ context.Context, d *aviato.DatasourceContext, cursor string) (aviato.DatasourceChanges, error) {
				return aviato.DatasourceChanges{Records: []aviato.Record{{"code": "pro", "replicated_without_caller": d.Caller == nil}}, NextCursor: cursor + "1"}, nil
			},
		})
	h := aviatotest.New(t, p)

	page, err := h.ListCustomRecords(ctx, "tickets", aviato.DatasourceQuery{})
	require.NoError(t, err)
	assert.Equal(t, aviato.DatasourcePage{Records: []aviato.Record{{"id": 1.0, "subject": "Login broken", "seen_by": "u_test"}}, Total: 1}, page)

	created, err := h.CreateCustomRecord(ctx, "tickets", aviato.Record{"subject": "Refund"})
	require.NoError(t, err)
	assert.Equal(t, aviato.Record{"id": 2.0, "subject": "Refund"}, created)
	_, err = h.CreateCustomRecord(ctx, "tickets", aviato.Record{})
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	record, err := h.GetCustomRecord(ctx, "tickets", "2")
	require.NoError(t, err)
	assert.Equal(t, "Refund", record["subject"])

	require.NoError(t, h.DeleteCustomRecord(ctx, "tickets", "2"))
	_, err = h.GetCustomRecord(ctx, "tickets", "2")
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = h.UpdateCustomRecord(ctx, "tickets", "1", aviato.Record{"subject": "x"})
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))

	changes, err := h.ListChanges(ctx, "plans", "")
	require.NoError(t, err)
	assert.Equal(t, aviato.DatasourceChanges{Records: []aviato.Record{{"code": "pro", "replicated_without_caller": true}}, NextCursor: "1"}, changes)
}
