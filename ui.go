package aviato

// UINode is a serializable native UI component. The agent and dashboard validate UI v1.
// Use UIBind for live data; strings are always literal text, never expressions or HTML.
type UINode = map[string]any

type summaryDef struct {
	collection string
	document   map[string]any
}

// Summary registers a declarative record overview. Re-registering replaces the definition.
func (p *Plugin) Summary(collection string, document map[string]any) *Plugin {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.summaries.set(collection, summaryDef{collection: collection, document: document})
	return p
}

// UIBind reads an own-property JSON Pointer from record, form, state, props, or item.
func UIBind(path string) map[string]any { return map[string]any{"$bind": path} }

// UIComponent creates a native node. Allowed types and props are documented in UI v1.
func UIComponent(kind string, props map[string]any, children ...UINode) UINode {
	node := UINode{"type": kind}
	if len(props) > 0 {
		node["props"] = props
	}
	if len(children) > 0 {
		node["children"] = children
	}
	return node
}

// UIDocument wraps a root in the versioned UI protocol. Optional state and reusable
// components can be set on the returned document before registering it.
func UIDocument(root UINode) map[string]any { return map[string]any{"version": 1, "root": root} }

// UIForm embeds a document in an action's JSON Forms UI schema (WithFormUI).
func UIForm(document map[string]any) map[string]any {
	return map[string]any{"type": "AviatoUI", "document": document}
}
