package aviato

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// Invocation is what every handler receives about the call in progress.
type Invocation struct {
	Caller Caller
	// Collection concerned by the call (empty for charts not bound to one).
	Collection string
	// Data reads and writes records through the agent with the caller's permissions.
	Data *DataClient
}

// ActionContext is passed to action handlers.
type ActionContext struct {
	Invocation
	// RecordIDs are the selected record ids (single and bulk actions). Composite keys are
	// encoded "a|b".
	RecordIDs []string
	// Filter is the current filter of global actions (MongoDB query syntax).
	Filter map[string]any
	// Segment and Search are the current segment and search of global actions.
	Segment string
	Search  string
	// Values are the form values.
	Values map[string]any
}

// RecordID returns the first selected record id, or "" (convenient for single actions).
func (a *ActionContext) RecordID() string {
	if len(a.RecordIDs) == 0 {
		return ""
	}
	return a.RecordIDs[0]
}

// StringValue returns the form value key when it is a string, or "".
func (a *ActionContext) StringValue(key string) string {
	value, _ := a.Values[key].(string)
	return value
}

// RecordsOptions narrows [ActionContext.Records].
type RecordsOptions struct {
	// Fields to read; all readable fields when empty (global actions only).
	Fields []string
	// Limit on the number of records of global actions; defaults to 200.
	Limit uint32
}

// Records reads the records the action runs on through the agent: the selected records for
// single and bulk actions, the records matching the current filter for global actions.
func (a *ActionContext) Records(ctx context.Context, options ...RecordsOptions) ([]Record, error) {
	var opts RecordsOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if len(a.RecordIDs) > 0 {
		records := make([]Record, len(a.RecordIDs))
		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(8)
		for index, id := range a.RecordIDs {
			group.Go(func() error {
				record, err := a.Data.Get(groupCtx, a.Collection, id)
				records[index] = record
				return err
			})
		}
		if err := group.Wait(); err != nil {
			return nil, err
		}
		found := records[:0]
		for _, record := range records {
			if record != nil {
				found = append(found, record)
			}
		}
		return found, nil
	}
	limit := opts.Limit
	if limit == 0 {
		limit = 200
	}
	page, err := a.Data.List(ctx, a.Collection, ListOptions{Filter: a.Filter, Fields: opts.Fields, Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Records, nil
}

// Success reports a successful action; see [Success].
func (a *ActionContext) Success(message string, invalidated ...string) Result {
	return Success(message, invalidated...)
}

// Error reports a business error; see [Error].
func (a *ActionContext) Error(message string) Result { return Error(message) }

// HTML shows HTML to the user; see [HTML].
func (a *ActionContext) HTML(html string) Result { return HTML(html) }

// File makes the user download content; see [File].
func (a *ActionContext) File(name, mimeType string, content []byte) Result {
	return File(name, mimeType, content)
}

// Redirect sends the user to a URL or dashboard path; see [Redirect].
func (a *ActionContext) Redirect(target string) Result { return Redirect(target) }

// Webhook makes the user's browser perform a request; see [Webhook].
func (a *ActionContext) Webhook(url, method string, headers map[string]string, body string) Result {
	return Webhook(url, method, headers, body)
}

// FormContext is passed to form resolvers.
type FormContext struct {
	ActionContext
	// ChangedField is the field that just changed, empty when the form opens.
	ChangedField string
}

// FormResolution is what a form resolver returns. A nil Form keeps the declared form.
type FormResolution struct {
	Form *Form
	// Values to set in the form (defaults, recomputed values).
	Values map[string]any
}

// HookContext is passed to hooks.
type HookContext struct {
	Invocation
	Timing    HookTiming
	Operation HookOperation
	RecordIDs []string
	// Values being written (create and update).
	Values map[string]any
	// Action is the action name, for action hooks.
	Action string
}

// HookResult is what a hook returns. The zero value lets the operation through unchanged.
type HookResult struct {
	// Reject rejects the operation with this message (before hooks only).
	Reject string
	// Values replace the values being written (before hooks on create and update).
	Values map[string]any
}

// SegmentResult is what a segment resolves to; build it with [SegmentFilter] or [SegmentIDs].
type SegmentResult struct {
	filter map[string]any
	ids    []string
	byIDs  bool
}

// SegmentFilter resolves a segment to a filter the agent applies (preferred).
func SegmentFilter(filter map[string]any) SegmentResult {
	return SegmentResult{filter: filter}
}

// SegmentIDs resolves a segment to explicit record ids.
func SegmentIDs(ids ...string) SegmentResult {
	return SegmentResult{ids: ids, byIDs: true}
}

// Filter returns the segment filter, if the segment resolves to one.
func (s SegmentResult) Filter() (map[string]any, bool) { return s.filter, !s.byIDs }

// IDs returns the record ids, if the segment resolves to explicit ids.
func (s SegmentResult) IDs() ([]string, bool) { return s.ids, s.byIDs }
