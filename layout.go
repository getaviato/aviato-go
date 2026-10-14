package aviato

type layoutDef struct {
	collection string
	layout     map[string]any
}

// Layout declares how a collection looks in the back-office: label, icon, fieldOrder, fields
// (label, description, hidden, readOnly, widget, currency), searchFields, defaultSort and
// relations, as in the dashboard's layout editor. It replaces the layout edited in the dashboard,
// which then shows it read-only. Re-registering a collection replaces its layout. The connector
// validates the layout and ignores an invalid one.
func (p *Plugin) Layout(collection string, layout map[string]any) *Plugin {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.layouts.set(collection, layoutDef{collection: collection, layout: layout})
	return p
}
