package aviato

import (
	"fmt"
	"sync"
)

// IntercomPluginOptions references a connector-local access token and the Intercom data region.
// Region is exactly "us", "eu" or "au", never a URL or host. The connector checks that the token
// works for the region (GET /me). Intercom offers no API to confirm a token is read-only, so that
// depends on the Intercom app's permissions (see docs.getaviato.com/integrations/intercom).
type IntercomPluginOptions struct {
	Region        string
	CredentialEnv string
	ReadOnly      bool
}

// IntercomContactWidget binds a contact card to a record. Set exactly one of ExternalID
// (matched on the Intercom external_id) or ContactID (the Intercom contact ID).
type IntercomContactWidget struct {
	Collection string
	Name       string
	Title      string
	ExternalID RecordBinding
	ContactID  RecordBinding
}

// IntercomPlugin implements IntegrationPlugin with a typed, read-only Intercom contact widget.
type IntercomPlugin struct {
	mu      sync.RWMutex
	options IntercomPluginOptions
	widgets registry[WidgetDefinition]
}

var _ IntegrationPlugin = (*IntercomPlugin)(nil)

// NewIntercomPlugin configures a region and a connector-local access token reference.
func NewIntercomPlugin(options IntercomPluginOptions) (*IntercomPlugin, error) {
	if !options.ReadOnly || !credentialEnvPattern.MatchString(options.CredentialEnv) {
		return nil, fmt.Errorf("intercom requires a read-only access token environment variable")
	}
	if options.Region != "us" && options.Region != "eu" && options.Region != "au" {
		return nil, fmt.Errorf("intercom requires region us, eu or au, not a URL")
	}
	return &IntercomPlugin{options: options}, nil
}

// Contact adds an Intercom contact card. It returns an error unless exactly one of
// ExternalID or ContactID is set.
func (i *IntercomPlugin) Contact(widget IntercomContactWidget) (*IntercomPlugin, error) {
	hasExternal := widget.ExternalID != RecordBinding{}
	hasContact := widget.ContactID != RecordBinding{}
	if hasExternal == hasContact {
		return nil, fmt.Errorf("intercom contact requires exactly one of ExternalID or ContactID")
	}
	resource, binding := "contact", widget.ContactID
	if hasExternal {
		resource, binding = "external-contact", widget.ExternalID
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.widgets.set(widget.Collection+"\x00"+widget.Name, WidgetDefinition{
		Collection: widget.Collection, Name: widget.Name, Title: widget.Title, Provider: "intercom", Resource: resource,
		BindingField: binding.Field, BindingRelation: binding.Relation,
		CredentialEnv: i.options.CredentialEnv, ReadOnly: i.options.ReadOnly, Account: i.options.Region,
	})
	return i, nil
}

// Widgets returns a copy of the declarations for host registration.
func (i *IntercomPlugin) Widgets() []WidgetDefinition {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]WidgetDefinition(nil), i.widgets.items...)
}
