package aviato

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
)

// ErrNotFound is wrapped by the errors returned for unknown actions, fields, segments, charts,
// searches and write overrides (reported to the agent as Connect code not_found).
var ErrNotFound = errors.New("not found")

func notFound(format string, args ...any) error {
	return connect.NewError(connect.CodeNotFound, fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrNotFound))
}

// The dispatch methods run handlers with Go values. The Connect service converts to and from
// the protocol messages around them, and the aviatotest harness calls them directly.

func (p *Plugin) lookupAction(collection, name string) (actionDef, error) {
	p.mu.RLock()
	action, ok := p.actions.get(key(collection, name))
	p.mu.RUnlock()
	if !ok {
		return actionDef{}, notFound("unknown action %s.%s", collection, name)
	}
	return action, nil
}

func (p *Plugin) dispatchAction(ctx context.Context, name string, a *ActionContext) (Result, error) {
	action, err := p.lookupAction(a.Collection, name)
	if err != nil {
		return nil, err
	}
	result, err := action.execute(ctx, a)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return SuccessResult{}, nil
	}
	return result, nil
}

func (p *Plugin) dispatchForm(ctx context.Context, name string, f *FormContext) (FormResolution, error) {
	action, err := p.lookupAction(f.Collection, name)
	if err != nil {
		return FormResolution{}, err
	}
	if action.resolveForm == nil {
		return FormResolution{Form: action.form}, nil
	}
	resolved, err := action.resolveForm(ctx, f)
	if err != nil {
		return FormResolution{}, err
	}
	if resolved.Form == nil {
		resolved.Form = action.form
	}
	return resolved, nil
}

func (p *Plugin) dispatchCompute(ctx context.Context, inv *Invocation, fields []string, records []Record) ([]Record, error) {
	values := make([]Record, len(records))
	for index := range values {
		values[index] = Record{}
	}
	for _, name := range fields {
		p.mu.RLock()
		field, ok := p.computed.get(key(inv.Collection, name))
		p.mu.RUnlock()
		if !ok {
			return nil, notFound("unknown computed field %s.%s", inv.Collection, name)
		}
		computed, err := field.compute(ctx, inv, records)
		if err != nil {
			return nil, err
		}
		if len(computed) != len(records) {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("computed field %s.%s returned %d values for %d records", inv.Collection, name, len(computed), len(records)))
		}
		for index, value := range computed {
			values[index][name] = value
		}
	}
	return values, nil
}

func (p *Plugin) dispatchWrite(ctx context.Context, inv *Invocation, field string, value any, record Record) (map[string]any, error) {
	p.mu.RLock()
	write, ok := p.writes.get(key(inv.Collection, field))
	p.mu.RUnlock()
	if !ok {
		return nil, notFound("no write override for %s.%s", inv.Collection, field)
	}
	if record == nil {
		record = Record{}
	}
	return write.write(ctx, inv, value, record)
}

func (p *Plugin) dispatchSegment(ctx context.Context, inv *Invocation, name string) (SegmentResult, error) {
	p.mu.RLock()
	segment, ok := p.segments.get(key(inv.Collection, name))
	p.mu.RUnlock()
	if !ok {
		return SegmentResult{}, notFound("unknown segment %s.%s", inv.Collection, name)
	}
	return segment.resolve(ctx, inv)
}

func (p *Plugin) dispatchHooks(ctx context.Context, h *HookContext) (HookResult, error) {
	p.mu.RLock()
	hooks := make([]hookDef, 0, len(p.hooks))
	for _, hook := range p.hooks {
		if hook.collection == h.Collection && hook.timing == h.Timing && hook.operation == h.Operation {
			hooks = append(hooks, hook)
		}
	}
	p.mu.RUnlock()
	for _, hook := range hooks {
		outcome, err := hook.run(ctx, h)
		if err != nil {
			return HookResult{}, err
		}
		if outcome.Reject != "" {
			return HookResult{Reject: outcome.Reject}, nil
		}
		if outcome.Values != nil {
			h.Values = outcome.Values
		}
	}
	return HookResult{Values: h.Values}, nil
}

func (p *Plugin) dispatchSearch(ctx context.Context, inv *Invocation, query string) (map[string]any, error) {
	p.mu.RLock()
	search, ok := p.searches.get(inv.Collection)
	p.mu.RUnlock()
	if !ok {
		return nil, notFound("no search for %s", inv.Collection)
	}
	return search.search(ctx, inv, query)
}

func (p *Plugin) dispatchChart(ctx context.Context, caller Caller, name string, filter map[string]any) (map[string]any, error) {
	p.mu.RLock()
	chart, ok := p.charts.get(name)
	p.mu.RUnlock()
	if !ok {
		return nil, notFound("unknown chart %s", name)
	}
	inv := p.invocation(caller, chart.collection)
	return chart.compute(ctx, &inv, filter)
}
