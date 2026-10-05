# Aviato Go SDK

Extend an Aviato agent with custom **actions**, **computed fields**, **hooks**, **segments**,
**search**, **write overrides**, **charts** and **custom datasources** written in Go.

The agent calls your plugin over HTTP (ConnectRPC, JSON encoding) and signs every request with
[Standard Webhooks](https://www.standardwebhooks.com/). Your code reads and writes records
through the agent's Data API, under the caller's permissions and audit trail. It never queries
the database directly.

## Install

```sh
go get github.com/getaviato/aviato-go
```

Requires Go 1.25+. Connect the plugin to an existing Aviato agent. The SDK does not
include the agent binary; automatic downloads require a working release URL.

## Define a plugin

```go
import aviato "github.com/getaviato/aviato-go"

plugin := aviato.MustNew(aviato.Options{
    Secret:   os.Getenv("AVIATO_PLUGIN_SECRET"), // whsec_…, shared with the agent
    BasePath: "/aviato",
})

plugin.Action("customers", "Refund last invoice", aviato.ScopeSingle,
    func(ctx context.Context, a *aviato.ActionContext) (aviato.Result, error) {
        if a.StringValue("reason") == "" {
            return a.Error("A reason is required"), nil
        }
        customers, err := a.Records(ctx) // read through the agent with the caller's permissions
        if err != nil {
            return nil, err
        }
        // … refund customers[0] …
        return a.Success("Refunded "+a.RecordID(), "invoices"), nil
    },
    aviato.WithForm(map[string]any{
        "type":       "object",
        "properties": map[string]any{"reason": map[string]any{"type": "string"}},
        "required":   []string{"reason"},
    }),
)

plugin.ComputedField("customers", "fullName", "string", []string{"first_name", "last_name"},
    func(ctx context.Context, inv *aviato.Invocation, records []aviato.Record) ([]any, error) {
        values := make([]any, len(records))
        for i, r := range records {
            values[i] = fmt.Sprintf("%v %v", r["first_name"], r["last_name"])
        }
        return values, nil
    })

plugin.Hook("customers", aviato.Before, aviato.HookUpdate,
    func(ctx context.Context, h *aviato.HookContext) (aviato.HookResult, error) {
        if h.Values["plan"] == "enterprise" {
            return aviato.HookResult{Reject: "Enterprise plans are set by sales"}, nil
        }
        return aviato.HookResult{}, nil // let it through unchanged
    })
```

The other registrations are `Segment`, `Search`, `WriteOverride`, `Chart` and `RelationHints`.
Actions can return `Success`, `Error`, `HTML`, `File` (served once to the agent from
`GET <basePath>/files/{ref}`), `Redirect` or `Webhook`. They take the options `WithDescription`,
`WithFormUI`, `WithGeneratesFile` and `WithFormResolver`, which makes the form dynamic.
`a.Data` is the full Data API client (`List`, `Get`, `Create`, `Update`, `Delete`).

If your relations are declared in GORM models rather than as foreign keys in the database:

```go
hints, err := gormhints.RelationHintsFromGORM(&Customer{}, &Invoice{})
plugin.RelationHints(hints...)
```

## Serve custom collections

Data the agent cannot reach through a database driver (an internal API, a SaaS, a file) can be
served by your code as custom collections, under the same roles, row scopes, masking and audit
trail as database collections:

```go
plugin.Datasource(aviato.Datasource{
    Collections: []aviato.CustomCollection{{
        Name: "tickets",
        Fields: []aviato.CustomField{
            {Name: "id", Type: "number", ReadOnly: true, NotNull: true},
            {Name: "subject", Type: "string", NotNull: true},
        },
        // What List does itself; the agent does the rest in memory.
        Capabilities: aviato.Capabilities{FilterOperators: []string{"eq", "in"}, Sort: true, Count: true},
    }},
    List: func(ctx context.Context, d *aviato.DatasourceContext, q aviato.DatasourceQuery) (aviato.DatasourcePage, error) {
        records, total, err := helpdesk.Search(ctx, q.Filter, q.Sort, q.Limit, q.Offset)
        return aviato.DatasourcePage{Records: records, Total: total}, err
    },
    Create: func(ctx context.Context, d *aviato.DatasourceContext, values aviato.Record) (aviato.Record, error) {
        if values["subject"] == nil {
            return nil, aviato.NewDatasourceError(aviato.DatasourceInvalid, "A subject is required")
        }
        return helpdesk.Create(ctx, values)
    },
})
```

`Get` (defaults to a lookup through `List`), `Create`, `Update` and `Delete` are optional. For slow
sources, `Strategy: aviato.ReplicationStrategy` with a `ListChanges` function lets the agent keep
a copy, refreshed every `ReplicationInterval` and on demand with
`plugin.RefreshReplica(ctx, "tickets", dataURL)`. See
[the custom datasources guide](../../docs/guides/custom-datasources.md).

## Mount it

The handler expects the full request path, including `BasePath`.

```go
// net/http
http.Handle("/aviato/", plugin.Handler())

// gin
import "github.com/getaviato/aviato-go/ginadapter"
ginadapter.Mount(router, plugin) // *gin.Engine or *gin.RouterGroup

// echo (v5)
import "github.com/getaviato/aviato-go/echoadapter"
echoadapter.Mount(e, plugin) // *echo.Echo or *echo.Group
```

Requests without a valid signature, or with one older than five minutes, get HTTP 401 before
their body is decoded.

## Run the agent

Download the agent binary for your platform and point it at your plugin with the same secret:

```sh
go run github.com/getaviato/aviato-go/cmd/aviato-agent-install@latest -out bin/aviato-agent
```

Use `-version v1.2.3` to pin a release, `-sha256` to verify the download, and `-url` (or
`AVIATO_AGENT_URL_TEMPLATE`) to download from a mirror. The URL template takes the placeholders
`{release}`, `{version}`, `{os}`, `{arch}` and `{ext}`.

## Test your handlers

`aviatotest` calls your handlers with a fake caller, without an agent. It also serves an
in-memory Data API that you seed with records:

```go
func TestRefund(t *testing.T) {
    h := aviatotest.New(t, plugin,
        aviatotest.WithRecords("customers", aviato.Record{"id": "42", "name": "Acme"}))

    result, err := h.Execute(context.Background(), "customers", "Refund last invoice",
        aviatotest.Target{RecordIDs: []string{"42"}, Values: map[string]any{"reason": "Duplicate"}})
    require.NoError(t, err)
    assert.Equal(t, aviato.Success("Refunded 42", "invoices"), result)
    // h.Data().Records("customers") shows what the handler wrote.
}
```

The harness also offers `ResolveForm`, `Compute`, `RunHook`, `WriteField`, `Segment`, `Search`,
`Chart`, and for custom datasources `ListCustomRecords`, `GetCustomRecord`, `CreateCustomRecord`,
`UpdateCustomRecord`, `DeleteCustomRecord` and `ListChanges`. Values go through JSON as they do on the wire, so numbers arrive as `float64`.

## Custom summaries and forms

`plugin.Summary(collection, document)` registers a native record overview. Use
`UIDocument`, `UIComponent`, and `UIBind` to build the shared JSON format, or load
the same document used by the TypeScript and Laravel SDKs. For action forms, keep
the data schema in `WithForm(schema)` and pass the layout through
`WithFormUI(UIForm(document))`.

The [custom UI guide](https://docs.getaviato.com/guides/custom-ui) covers metric
strips, property rows, reusable components, local state, field presets, and action
buttons. Rendering uses Aviato's native design system without customer scripts or
iframes. The agent still enforces permissions, validation, approvals, and auditing.

## Development

The protocol bindings in `pluginv1/` are generated from `packages/protocol/proto`. Regenerate
them with `buf generate` (you need `protoc-gen-go` and `protoc-gen-connect-go` on your `PATH`).

```sh
pnpm exec nx run @aviato/sdk-go:test               # go test ./...
pnpm exec nx run @aviato/sdk-go:lint               # go vet + golangci-lint when installed
pnpm exec nx run @aviato/fixture-gin:conformance   # the conformance suite against the gin fixture
```

## License

MIT. See [LICENSE](LICENSE).
