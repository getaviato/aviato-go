package aviato

import (
	"context"

	"github.com/getaviato/aviato-go/internal/bridge"
)

func init() {
	bridge.Dispatcher = func(plugin any) any { return dispatcher{plugin.(*Plugin)} }
}

// dispatcher exposes the dispatch methods to the aviatotest harness through the bridge.
type dispatcher struct{ p *Plugin }

func (d dispatcher) Invocation(caller Caller, collection string) Invocation {
	return d.p.invocation(caller, collection)
}

func (d dispatcher) Execute(ctx context.Context, name string, a *ActionContext) (Result, error) {
	return d.p.dispatchAction(ctx, name, a)
}

func (d dispatcher) ResolveForm(ctx context.Context, name string, f *FormContext) (FormResolution, error) {
	return d.p.dispatchForm(ctx, name, f)
}

func (d dispatcher) Compute(ctx context.Context, inv *Invocation, fields []string, records []Record) ([]Record, error) {
	return d.p.dispatchCompute(ctx, inv, fields, records)
}

func (d dispatcher) Write(ctx context.Context, inv *Invocation, field string, value any, record Record) (map[string]any, error) {
	return d.p.dispatchWrite(ctx, inv, field, value, record)
}

func (d dispatcher) Segment(ctx context.Context, inv *Invocation, name string) (SegmentResult, error) {
	return d.p.dispatchSegment(ctx, inv, name)
}

func (d dispatcher) Hooks(ctx context.Context, h *HookContext) (HookResult, error) {
	return d.p.dispatchHooks(ctx, h)
}

func (d dispatcher) Search(ctx context.Context, inv *Invocation, query string) (map[string]any, error) {
	return d.p.dispatchSearch(ctx, inv, query)
}

func (d dispatcher) Chart(ctx context.Context, caller Caller, name string, filter map[string]any) (map[string]any, error) {
	return d.p.dispatchChart(ctx, caller, name, filter)
}

func (d dispatcher) DatasourceList(ctx context.Context, c *DatasourceContext, query DatasourceQuery) (DatasourcePage, error) {
	return d.p.dispatchDatasourceList(ctx, c, query)
}

func (d dispatcher) DatasourceGet(ctx context.Context, c *DatasourceContext, id string) (Record, error) {
	return d.p.dispatchDatasourceGet(ctx, c, id)
}

func (d dispatcher) DatasourceCreate(ctx context.Context, c *DatasourceContext, values Record) (Record, error) {
	return d.p.dispatchDatasourceCreate(ctx, c, values)
}

func (d dispatcher) DatasourceUpdate(ctx context.Context, c *DatasourceContext, id string, values Record) (Record, error) {
	return d.p.dispatchDatasourceUpdate(ctx, c, id, values)
}

func (d dispatcher) DatasourceDelete(ctx context.Context, c *DatasourceContext, id string) error {
	return d.p.dispatchDatasourceDelete(ctx, c, id)
}

func (d dispatcher) ListChanges(ctx context.Context, collection, cursor string) (DatasourceChanges, error) {
	return d.p.dispatchListChanges(ctx, collection, cursor)
}
