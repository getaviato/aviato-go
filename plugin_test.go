package aviato_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	aviato "github.com/getaviato/aviato-go"
	pluginv1 "github.com/getaviato/aviato-go/pluginv1"
	"github.com/getaviato/aviato-go/pluginv1/pluginv1connect"
)

var secret = "whsec_" + base64.StdEncoding.EncodeToString([]byte("aviato-plugin-test-secret-32bytes!"))

func mustStruct(t *testing.T, value map[string]any) *structpb.Struct {
	t.Helper()
	result, err := structpb.NewStruct(value)
	require.NoError(t, err)
	return result
}

// testPlugin registers one handler of each kind.
func testPlugin(t *testing.T) *aviato.Plugin {
	t.Helper()
	plugin, err := aviato.New(aviato.Options{Secret: secret, BasePath: "/aviato/"})
	require.NoError(t, err)
	return plugin.
		Action("customers", "Refund last invoice", aviato.ScopeSingle, func(_ context.Context, a *aviato.ActionContext) (aviato.Result, error) {
			if a.StringValue("reason") == "" {
				return a.Error("A reason is required"), nil
			}
			return a.Success("Refunded "+a.RecordID(), "invoices"), nil
		}, aviato.WithForm(map[string]any{"type": "object", "properties": map[string]any{"reason": map[string]any{"type": "string"}}, "required": []string{"reason"}}), aviato.WithDescription("Refunds the last invoice")).
		Action("customers", "Export", aviato.ScopeGlobal, func(_ context.Context, a *aviato.ActionContext) (aviato.Result, error) {
			return a.File("customers.csv", "text/csv", []byte("id,name\n1,Acme\n")), nil
		}, aviato.WithGeneratesFile()).
		Action("customers", "Summary", aviato.ScopeSingle, func(_ context.Context, a *aviato.ActionContext) (aviato.Result, error) {
			return a.HTML("<p>Customer " + a.RecordID() + "</p>"), nil
		}).
		Action("customers", "Open in Stripe", aviato.ScopeSingle, func(_ context.Context, a *aviato.ActionContext) (aviato.Result, error) {
			return a.Redirect("https://dashboard.stripe.com/customers/" + a.RecordID()), nil
		}).
		Action("customers", "Notify", aviato.ScopeBulk, func(_ context.Context, a *aviato.ActionContext) (aviato.Result, error) {
			return a.Webhook("https://hooks.example.com/notify", "", map[string]string{"x-count": fmt.Sprint(len(a.RecordIDs))}, "{}"), nil
		}).
		Action("customers", "Noop", aviato.ScopeSingle, func(context.Context, *aviato.ActionContext) (aviato.Result, error) {
			return nil, nil
		}).
		Action("customers", "Fail", aviato.ScopeSingle, func(context.Context, *aviato.ActionContext) (aviato.Result, error) {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("nope"))
		}).
		Action("customers", "Count", aviato.ScopeBulk, func(ctx context.Context, a *aviato.ActionContext) (aviato.Result, error) {
			records, err := a.Records(ctx)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, len(records))
			for _, record := range records {
				names = append(names, fmt.Sprint(record["name"]))
			}
			return a.Success(strings.Join(names, ",")), nil
		}).
		Action("customers", "Plan change", aviato.ScopeSingle, func(_ context.Context, a *aviato.ActionContext) (aviato.Result, error) {
			return a.Success("Plan changed"), nil
		}, aviato.WithForm(map[string]any{"type": "object"}), aviato.WithFormResolver(func(_ context.Context, f *aviato.FormContext) (aviato.FormResolution, error) {
			if f.ChangedField == "plan" && f.Values["plan"] == "team" {
				return aviato.FormResolution{Values: map[string]any{"confirm": true}}, nil
			}
			return aviato.FormResolution{Values: map[string]any{}}, nil
		})).
		ComputedField("customers", "fullName", "string", []string{"first_name", "last_name"}, func(_ context.Context, _ *aviato.Invocation, records []aviato.Record) ([]any, error) {
			values := make([]any, 0, len(records))
			for _, record := range records {
				values = append(values, fmt.Sprintf("%v %v", record["first_name"], record["last_name"]))
			}
			return values, nil
		}).
		ComputedField("customers", "broken", "number", nil, func(context.Context, *aviato.Invocation, []aviato.Record) ([]any, error) {
			return []any{1}, nil
		}, aviato.WithEmulatedFilterSort(), aviato.WithEnumValues("a")).
		Hook("customers", aviato.Before, aviato.HookUpdate, func(_ context.Context, h *aviato.HookContext) (aviato.HookResult, error) {
			if h.Values["plan"] == "enterprise" {
				return aviato.HookResult{Reject: "Enterprise plans are set by sales"}, nil
			}
			return aviato.HookResult{}, nil
		}).
		Hook("customers", aviato.Before, aviato.HookUpdate, func(_ context.Context, h *aviato.HookContext) (aviato.HookResult, error) {
			if h.Values["plan"] == "legacy" {
				return aviato.HookResult{Values: map[string]any{"plan": "pro"}}, nil
			}
			return aviato.HookResult{}, nil
		}).
		Segment("customers", "vip", func(context.Context, *aviato.Invocation) (aviato.SegmentResult, error) {
			return aviato.SegmentFilter(map[string]any{"plan": "team"}), nil
		}).
		Segment("customers", "firsts", func(context.Context, *aviato.Invocation) (aviato.SegmentResult, error) {
			return aviato.SegmentIDs("1", "2"), nil
		}).
		Search("customers", func(_ context.Context, _ *aviato.Invocation, query string) (map[string]any, error) {
			return map[string]any{"name": map[string]any{"$ilike": "%" + query + "%"}}, nil
		}).
		WriteOverride("customers", "fullName", func(_ context.Context, _ *aviato.Invocation, value any, _ aviato.Record) (map[string]any, error) {
			first, last, _ := strings.Cut(fmt.Sprint(value), " ")
			return map[string]any{"first_name": first, "last_name": last}, nil
		}).
		Chart("customersByPlan", "customers", func(_ context.Context, inv *aviato.Invocation, filter map[string]any) (map[string]any, error) {
			return map[string]any{"mark": "bar", "collection": inv.Collection, "filter": filter}, nil
		}).
		RelationHints(aviato.RelationHint{Collection: "invoices", Name: "customer", Kind: aviato.BelongsTo, ForeignKey: []string{"customer_id"}, Target: "customers", TargetKey: []string{"id"}})
}

type fixture struct {
	server  *httptest.Server
	baseURL string
	client  pluginv1connect.PluginServiceClient
	signed  *http.Client
	caller  *pluginv1.Caller
}

func newFixture(t *testing.T, plugin *aviato.Plugin) *fixture {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/aviato/", plugin.Handler())
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	signed, err := aviato.NewSigningClient(secret)
	require.NoError(t, err)
	baseURL := server.URL + "/aviato"
	return &fixture{
		server:  server,
		baseURL: baseURL,
		signed:  signed,
		client:  pluginv1connect.NewPluginServiceClient(signed, baseURL, connect.WithProtoJSON()),
		caller:  &pluginv1.Caller{UserId: "u1", Name: "Sarah", ActorType: "human", Role: "admin", InvocationToken: "tok", DataUrl: "http://127.0.0.1:9/data"},
	}
}

func TestNewRejectsInvalidSecrets(t *testing.T) {
	_, err := aviato.New(aviato.Options{})
	require.Error(t, err)
	_, err = aviato.New(aviato.Options{Secret: "whsec_not base64!"})
	require.Error(t, err)
	assert.Panics(t, func() { aviato.MustNew(aviato.Options{}) })
}

func TestRegistrationRequiresHandlers(t *testing.T) {
	plugin := aviato.MustNew(aviato.Options{Secret: secret})
	assert.Panics(t, func() { plugin.Action("customers", "x", aviato.ScopeSingle, nil) })
	assert.Panics(t, func() { plugin.ComputedField("customers", "x", "string", nil, nil) })
}

func TestManifest(t *testing.T) {
	f := newFixture(t, testPlugin(t))
	response, err := f.client.GetManifest(context.Background(), connect.NewRequest(&pluginv1.GetManifestRequest{AgentProtocolVersion: 1}))
	require.NoError(t, err)
	manifest := response.Msg.GetManifest()
	assert.EqualValues(t, aviato.ProtocolVersion, manifest.GetProtocolVersion())
	assert.Equal(t, aviato.SDKName, manifest.GetSdkName())

	names := make([]string, 0)
	for _, action := range manifest.GetActions() {
		names = append(names, action.GetName())
	}
	assert.Equal(t, []string{"Refund last invoice", "Export", "Summary", "Open in Stripe", "Notify", "Noop", "Fail", "Count", "Plan change"}, names, "actions keep registration order")
	refund := manifest.GetActions()[0]
	assert.Equal(t, pluginv1.ActionScope_ACTION_SCOPE_SINGLE, refund.GetScope())
	assert.Equal(t, "Refunds the last invoice", refund.GetDescription())
	assert.Equal(t, []any{"reason"}, refund.GetFormSchema().AsMap()["required"])
	assert.True(t, manifest.GetActions()[1].GetGeneratesFile())
	assert.Equal(t, pluginv1.ActionScope_ACTION_SCOPE_GLOBAL, manifest.GetActions()[1].GetScope())
	assert.True(t, manifest.GetActions()[8].GetDynamicForm())
	assert.False(t, refund.GetDynamicForm())

	require.Len(t, manifest.GetComputedFields(), 2)
	assert.Equal(t, []string{"first_name", "last_name"}, manifest.GetComputedFields()[0].GetDependencies())
	assert.True(t, manifest.GetComputedFields()[1].GetEmulateFilterSort())
	assert.Equal(t, []string{"a"}, manifest.GetComputedFields()[1].GetEnumValues())
	require.Len(t, manifest.GetHooks(), 2)
	assert.Equal(t, pluginv1.HookTiming_HOOK_TIMING_BEFORE, manifest.GetHooks()[0].GetTiming())
	assert.Equal(t, pluginv1.HookOperation_HOOK_OPERATION_UPDATE, manifest.GetHooks()[0].GetOperation())
	assert.Len(t, manifest.GetSegments(), 2)
	require.Len(t, manifest.GetSearches(), 1)
	assert.False(t, manifest.GetSearches()[0].GetReplace())
	assert.Equal(t, "fullName", manifest.GetWriteOverrides()[0].GetField())
	assert.Equal(t, "customersByPlan", manifest.GetCharts()[0].GetName())
	require.Len(t, manifest.GetRelationHints(), 1)
	assert.Equal(t, "belongsTo", manifest.GetRelationHints()[0].GetKind())
	assert.Equal(t, []string{"customer_id"}, manifest.GetRelationHints()[0].GetForeignKey())
}

func TestReregisteringReplacesInPlace(t *testing.T) {
	plugin := aviato.MustNew(aviato.Options{Secret: secret})
	noop := func(context.Context, *aviato.ActionContext) (aviato.Result, error) { return nil, nil }
	plugin.Action("c", "a", aviato.ScopeSingle, noop).Action("c", "b", aviato.ScopeSingle, noop).Action("c", "a", aviato.ScopeBulk, noop)
	manifest, err := plugin.Manifest()
	require.NoError(t, err)
	require.Len(t, manifest.GetActions(), 2)
	assert.Equal(t, "a", manifest.GetActions()[0].GetName())
	assert.Equal(t, pluginv1.ActionScope_ACTION_SCOPE_BULK, manifest.GetActions()[0].GetScope())
}

func TestSignatureVerification(t *testing.T) {
	f := newFixture(t, testPlugin(t))
	url := f.baseURL + "/aviato.plugin.v1.PluginService/GetManifest"
	body := []byte(`{"agentProtocolVersion":1}`)

	post := func(t *testing.T, client *http.Client, headers map[string]string, payload []byte) (int, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(payload))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, err := client.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		content, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		return response.StatusCode, string(content)
	}

	t.Run("valid", func(t *testing.T) {
		status, content := post(t, f.signed, nil, body)
		assert.Equal(t, http.StatusOK, status, content)
		assert.Contains(t, content, `"protocolVersion":1`)
	})
	t.Run("unsigned", func(t *testing.T) {
		status, content := post(t, http.DefaultClient, nil, body)
		assert.Equal(t, http.StatusUnauthorized, status)
		assert.JSONEq(t, `{"code":"unauthenticated","message":"Invalid plugin request signature"}`, content)
	})
	t.Run("wrong secret", func(t *testing.T) {
		other, err := aviato.NewSigningClient("whsec_" + base64.StdEncoding.EncodeToString([]byte("not-the-plugin-secret-at-all!!!!")))
		require.NoError(t, err)
		status, _ := post(t, other, nil, body)
		assert.Equal(t, http.StatusUnauthorized, status)
	})
	t.Run("stale", func(t *testing.T) {
		transport, err := aviato.NewSigningTransport(secret)
		require.NoError(t, err)
		transport.Now = func() time.Time { return time.Now().Add(-10 * time.Minute) }
		status, _ := post(t, &http.Client{Transport: transport}, nil, body)
		assert.Equal(t, http.StatusUnauthorized, status)
	})
	t.Run("from the future", func(t *testing.T) {
		transport, err := aviato.NewSigningTransport(secret)
		require.NoError(t, err)
		transport.Now = func() time.Time { return time.Now().Add(10 * time.Minute) }
		status, _ := post(t, &http.Client{Transport: transport}, nil, body)
		assert.Equal(t, http.StatusUnauthorized, status)
	})
	t.Run("tampered body", func(t *testing.T) {
		transport, err := aviato.NewSigningTransport(secret)
		require.NoError(t, err)
		transport.Base = roundTripFunc(func(request *http.Request) (*http.Response, error) {
			request.Body = io.NopCloser(strings.NewReader(`{"agentProtocolVersion":2}`))
			request.ContentLength = -1
			return http.DefaultTransport.RoundTrip(request)
		})
		status, _ := post(t, &http.Client{Transport: transport}, nil, body)
		assert.Equal(t, http.StatusUnauthorized, status)
	})
	t.Run("unknown path is not found", func(t *testing.T) {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.baseURL+"/nope", nil)
		require.NoError(t, err)
		response, err := f.signed.Do(request)
		require.NoError(t, err)
		response.Body.Close()
		assert.Equal(t, http.StatusNotFound, response.StatusCode)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func (f *fixture) execute(t *testing.T, action string, target *pluginv1.ActionTarget, values map[string]any) (*pluginv1.ActionResult, error) {
	t.Helper()
	request := &pluginv1.ExecuteActionRequest{Caller: f.caller, Action: action, Target: target}
	if values != nil {
		request.Values = mustStruct(t, values)
	}
	response, err := f.client.ExecuteAction(context.Background(), connect.NewRequest(request))
	if err != nil {
		return nil, err
	}
	return response.Msg.GetResult(), nil
}

func TestExecuteAction(t *testing.T) {
	f := newFixture(t, testPlugin(t))
	single := &pluginv1.ActionTarget{Collection: "customers", RecordIds: []string{"42"}}

	result, err := f.execute(t, "Refund last invoice", single, map[string]any{"reason": "Duplicate charge"})
	require.NoError(t, err)
	assert.Equal(t, "Refunded 42", result.GetSuccess().GetMessage())
	assert.Equal(t, []string{"invoices"}, result.GetSuccess().GetInvalidated())

	result, err = f.execute(t, "Refund last invoice", single, nil)
	require.NoError(t, err)
	assert.Equal(t, "A reason is required", result.GetError().GetMessage())

	result, err = f.execute(t, "Summary", single, nil)
	require.NoError(t, err)
	assert.Equal(t, "<p>Customer 42</p>", result.GetHtml().GetHtml())

	result, err = f.execute(t, "Open in Stripe", single, nil)
	require.NoError(t, err)
	assert.Equal(t, "https://dashboard.stripe.com/customers/42", result.GetRedirect().GetUrl())
	assert.Empty(t, result.GetRedirect().GetPath())

	result, err = f.execute(t, "Notify", &pluginv1.ActionTarget{Collection: "customers", RecordIds: []string{"1", "2"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "POST", result.GetWebhook().GetMethod())
	assert.Equal(t, map[string]string{"x-count": "2"}, result.GetWebhook().GetHeaders())

	result, err = f.execute(t, "Noop", single, nil)
	require.NoError(t, err)
	assert.NotNil(t, result.GetSuccess(), "a nil result is a success")

	_, err = f.execute(t, "Fail", single, nil)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = f.execute(t, "Does not exist", single, nil)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = f.client.ExecuteAction(context.Background(), connect.NewRequest(&pluginv1.ExecuteActionRequest{Action: "Summary", Target: single}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "the caller is required")
}

func TestFileDownloadsAreOneTimeAndSigned(t *testing.T) {
	f := newFixture(t, testPlugin(t))
	result, err := f.execute(t, "Export", &pluginv1.ActionTarget{Collection: "customers", Filter: mustStruct(t, map[string]any{"plan": "team"})}, nil)
	require.NoError(t, err)
	file := result.GetFile()
	require.NotNil(t, file)
	assert.Equal(t, "customers.csv", file.GetName())
	assert.Equal(t, "text/csv", file.GetMimeType())
	require.NotEmpty(t, file.GetRef())

	get := func(client *http.Client) (int, http.Header, string) {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.baseURL+"/files/"+file.GetRef(), nil)
		require.NoError(t, err)
		response, err := client.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		content, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		return response.StatusCode, response.Header, string(content)
	}

	status, _, _ := get(http.DefaultClient)
	assert.Equal(t, http.StatusUnauthorized, status, "unsigned downloads are rejected")

	status, headers, content := get(f.signed)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "id,name\n1,Acme\n", content)
	assert.Equal(t, "text/csv", headers.Get("Content-Type"))
	assert.Equal(t, `attachment; filename=customers.csv`, headers.Get("Content-Disposition"))

	status, _, _ = get(f.signed)
	assert.Equal(t, http.StatusNotFound, status, "files are served once")
}

func TestFileDownloadsExpire(t *testing.T) {
	plugin, err := aviato.New(aviato.Options{Secret: secret, BasePath: "/aviato", FileTTL: time.Nanosecond})
	require.NoError(t, err)
	plugin.Action("customers", "Export", aviato.ScopeGlobal, func(context.Context, *aviato.ActionContext) (aviato.Result, error) {
		return aviato.File("a.txt", "text/plain", []byte("a")), nil
	})
	f := newFixture(t, plugin)
	result, err := f.execute(t, "Export", &pluginv1.ActionTarget{Collection: "customers"}, nil)
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.baseURL+"/files/"+result.GetFile().GetRef(), nil)
	require.NoError(t, err)
	response, err := f.signed.Do(request)
	require.NoError(t, err)
	response.Body.Close()
	assert.Equal(t, http.StatusNotFound, response.StatusCode)
}

func TestResolveForm(t *testing.T) {
	f := newFixture(t, testPlugin(t))
	target := &pluginv1.ActionTarget{Collection: "customers", RecordIds: []string{"7"}}
	response, err := f.client.ResolveForm(context.Background(), connect.NewRequest(&pluginv1.ResolveFormRequest{Caller: f.caller, Action: "Plan change", Target: target, Values: mustStruct(t, map[string]any{"plan": "team"}), ChangedField: "plan"}))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"confirm": true}, response.Msg.GetValues().AsMap())
	assert.Equal(t, map[string]any{"type": "object"}, response.Msg.GetFormSchema().AsMap(), "the declared form is kept")

	response, err = f.client.ResolveForm(context.Background(), connect.NewRequest(&pluginv1.ResolveFormRequest{Caller: f.caller, Action: "Plan change", Target: target, Values: mustStruct(t, map[string]any{"plan": "pro"}), ChangedField: "plan"}))
	require.NoError(t, err)
	assert.Empty(t, response.Msg.GetValues().AsMap())

	response, err = f.client.ResolveForm(context.Background(), connect.NewRequest(&pluginv1.ResolveFormRequest{Caller: f.caller, Action: "Refund last invoice", Target: target}))
	require.NoError(t, err)
	assert.Equal(t, "object", response.Msg.GetFormSchema().AsMap()["type"], "static forms are returned as declared")
}

func TestComputeFields(t *testing.T) {
	f := newFixture(t, testPlugin(t))
	response, err := f.client.ComputeFields(context.Background(), connect.NewRequest(&pluginv1.ComputeFieldsRequest{
		Caller:     f.caller,
		Collection: "customers",
		Fields:     []string{"fullName"},
		Records:    []*structpb.Struct{mustStruct(t, map[string]any{"first_name": "Ada", "last_name": "Lovelace"}), mustStruct(t, map[string]any{"first_name": "Alan", "last_name": "Turing"})},
	}))
	require.NoError(t, err)
	require.Len(t, response.Msg.GetValues(), 2)
	assert.Equal(t, map[string]any{"fullName": "Ada Lovelace"}, response.Msg.GetValues()[0].AsMap())
	assert.Equal(t, map[string]any{"fullName": "Alan Turing"}, response.Msg.GetValues()[1].AsMap())

	_, err = f.client.ComputeFields(context.Background(), connect.NewRequest(&pluginv1.ComputeFieldsRequest{Caller: f.caller, Collection: "customers", Fields: []string{"nope"}}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	_, err = f.client.ComputeFields(context.Background(), connect.NewRequest(&pluginv1.ComputeFieldsRequest{Caller: f.caller, Collection: "customers", Fields: []string{"broken"}, Records: []*structpb.Struct{{}, {}}}))
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err), "a compute function must return one value per record")
}

func TestWriteField(t *testing.T) {
	f := newFixture(t, testPlugin(t))
	response, err := f.client.WriteField(context.Background(), connect.NewRequest(&pluginv1.WriteFieldRequest{Caller: f.caller, Collection: "customers", Field: "fullName", Value: structpb.NewStringValue("Grace Hopper"), Record: &structpb.Struct{}}))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"first_name": "Grace", "last_name": "Hopper"}, response.Msg.GetPatch().AsMap())

	_, err = f.client.WriteField(context.Background(), connect.NewRequest(&pluginv1.WriteFieldRequest{Caller: f.caller, Collection: "customers", Field: "nope"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestRunHook(t *testing.T) {
	f := newFixture(t, testPlugin(t))
	run := func(timing pluginv1.HookTiming, values map[string]any) *pluginv1.RunHookResponse {
		response, err := f.client.RunHook(context.Background(), connect.NewRequest(&pluginv1.RunHookRequest{Caller: f.caller, Collection: "customers", Timing: timing, Operation: pluginv1.HookOperation_HOOK_OPERATION_UPDATE, RecordIds: []string{"1"}, Values: mustStruct(t, values)}))
		require.NoError(t, err)
		return response.Msg
	}
	rejected := run(pluginv1.HookTiming_HOOK_TIMING_BEFORE, map[string]any{"plan": "enterprise"})
	assert.Equal(t, "Enterprise plans are set by sales", rejected.GetRejectMessage())
	assert.Nil(t, rejected.GetValues())

	through := run(pluginv1.HookTiming_HOOK_TIMING_BEFORE, map[string]any{"plan": "pro"})
	assert.Empty(t, through.GetRejectMessage())
	assert.Equal(t, map[string]any{"plan": "pro"}, through.GetValues().AsMap())

	replaced := run(pluginv1.HookTiming_HOOK_TIMING_BEFORE, map[string]any{"plan": "legacy"})
	assert.Equal(t, map[string]any{"plan": "pro"}, replaced.GetValues().AsMap(), "hooks chain their values")

	after := run(pluginv1.HookTiming_HOOK_TIMING_AFTER, map[string]any{"plan": "enterprise"})
	assert.Empty(t, after.GetRejectMessage(), "hooks only run for their timing")
}

func TestSegmentsSearchAndCharts(t *testing.T) {
	f := newFixture(t, testPlugin(t))
	ctx := context.Background()

	segment, err := f.client.ResolveSegment(ctx, connect.NewRequest(&pluginv1.ResolveSegmentRequest{Caller: f.caller, Collection: "customers", Segment: "vip"}))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"plan": "team"}, segment.Msg.GetFilter().AsMap())

	segment, err = f.client.ResolveSegment(ctx, connect.NewRequest(&pluginv1.ResolveSegmentRequest{Caller: f.caller, Collection: "customers", Segment: "firsts"}))
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, segment.Msg.GetRecordIds().GetIds())

	_, err = f.client.ResolveSegment(ctx, connect.NewRequest(&pluginv1.ResolveSegmentRequest{Caller: f.caller, Collection: "customers", Segment: "nope"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	search, err := f.client.Search(ctx, connect.NewRequest(&pluginv1.SearchRequest{Caller: f.caller, Collection: "customers", Query: "acme"}))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": map[string]any{"$ilike": "%acme%"}}, search.Msg.GetFilter().AsMap())

	_, err = f.client.Search(ctx, connect.NewRequest(&pluginv1.SearchRequest{Caller: f.caller, Collection: "invoices", Query: "acme"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	chart, err := f.client.ComputeChart(ctx, connect.NewRequest(&pluginv1.ComputeChartRequest{Caller: f.caller, Chart: "customersByPlan", Filter: mustStruct(t, map[string]any{"plan": "pro"})}))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"mark": "bar", "collection": "customers", "filter": map[string]any{"plan": "pro"}}, chart.Msg.GetVegaLite().AsMap())

	_, err = f.client.ComputeChart(ctx, connect.NewRequest(&pluginv1.ComputeChartRequest{Caller: f.caller, Chart: "nope"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// dataService is a DataService checking the invocation token.
type dataService struct {
	pluginv1connect.UnimplementedDataServiceHandler
	records map[string]*structpb.Struct
}

func (d *dataService) Get(_ context.Context, request *connect.Request[pluginv1.GetRequest]) (*connect.Response[pluginv1.GetResponse], error) {
	if request.Header().Get("Authorization") != "Bearer tok" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("bad token"))
	}
	return connect.NewResponse(&pluginv1.GetResponse{Record: d.records[request.Msg.GetId()]}), nil
}

func (d *dataService) List(_ context.Context, request *connect.Request[pluginv1.ListRequest]) (*connect.Response[pluginv1.ListResponse], error) {
	if request.Header().Get("Authorization") != "Bearer tok" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("bad token"))
	}
	if request.Msg.GetLimit() != 200 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("limit %d", request.Msg.GetLimit()))
	}
	return connect.NewResponse(&pluginv1.ListResponse{Records: []*structpb.Struct{d.records["1"]}, Total: 1}), nil
}

func TestActionRecordsGoThroughTheDataAPI(t *testing.T) {
	data := &dataService{records: map[string]*structpb.Struct{"1": mustStruct(t, map[string]any{"name": "Acme"}), "2": mustStruct(t, map[string]any{"name": "Globex"})}}
	path, handler := pluginv1connect.NewDataServiceHandler(data)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	var (
		mu           sync.Mutex
		contentTypes []string
	)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		contentTypes = append(contentTypes, r.Header.Get("Content-Type"))
		mu.Unlock()
		http.StripPrefix("/data", mux).ServeHTTP(w, r)
	}))
	t.Cleanup(agent.Close)

	f := newFixture(t, testPlugin(t))
	f.caller.DataUrl = agent.URL + "/data"
	result, err := f.execute(t, "Count", &pluginv1.ActionTarget{Collection: "customers", RecordIds: []string{"1", "2", "3"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "Acme,Globex", result.GetSuccess().GetMessage(), "missing records are skipped")
	assert.Contains(t, contentTypes, "application/json", "the Data API is called with the JSON codec")

	result, err = f.execute(t, "Count", &pluginv1.ActionTarget{Collection: "customers", Filter: mustStruct(t, map[string]any{"plan": "team"})}, nil)
	require.NoError(t, err)
	assert.Equal(t, "Acme", result.GetSuccess().GetMessage())

	f.caller.InvocationToken = "wrong"
	_, err = f.execute(t, "Count", &pluginv1.ActionTarget{Collection: "customers", RecordIds: []string{"1"}}, nil)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestResultHelpers(t *testing.T) {
	assert.Equal(t, aviato.RedirectResult{URL: "https://example.com/a"}, aviato.Redirect("https://example.com/a"))
	assert.Equal(t, aviato.RedirectResult{Path: "/customers/1"}, aviato.Redirect("/customers/1"))
	assert.Equal(t, aviato.SuccessResult{Message: "ok", Invalidated: []string{"invoices"}}, aviato.Success("ok", "invoices"))
	assert.Equal(t, "POST", aviato.Webhook("https://example.com", "", nil, "").Method)
	assert.Equal(t, "PUT", aviato.Webhook("https://example.com", "PUT", nil, "").Method)
}

func TestValuesAcceptAnyJSONSerializableType(t *testing.T) {
	type tag struct {
		Name string `json:"name"`
	}
	plugin := aviato.MustNew(aviato.Options{Secret: secret, BasePath: "/aviato"}).
		Search("customers", func(context.Context, *aviato.Invocation, string) (map[string]any, error) {
			return map[string]any{"id": map[string]any{"$in": []string{"1", "2"}}, "tag": tag{Name: "vip"}}, nil
		})
	f := newFixture(t, plugin)
	response, err := f.client.Search(context.Background(), connect.NewRequest(&pluginv1.SearchRequest{Caller: f.caller, Collection: "customers", Query: "x"}))
	require.NoError(t, err)
	encoded, err := json.Marshal(response.Msg.GetFilter().AsMap())
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":{"$in":["1","2"]},"tag":{"name":"vip"}}`, string(encoded))
}

func TestPluginWithoutBasePath(t *testing.T) {
	plugin := aviato.MustNew(aviato.Options{Secret: secret})
	server := httptest.NewServer(plugin)
	t.Cleanup(server.Close)
	signed, err := aviato.NewSigningClient(secret)
	require.NoError(t, err)
	client := pluginv1connect.NewPluginServiceClient(signed, server.URL, connect.WithProtoJSON())
	response, err := client.GetManifest(context.Background(), connect.NewRequest(&pluginv1.GetManifestRequest{}))
	require.NoError(t, err)
	assert.EqualValues(t, 1, response.Msg.GetManifest().GetProtocolVersion())
}
