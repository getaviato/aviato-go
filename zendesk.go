package aviato

import (
	"fmt"
	"regexp"
	"sync"
)

// ZendeskPluginOptions references an agent-local OAuth token and a Zendesk subdomain label.
// The agent verifies that the token has only read scopes before loading data.
type ZendeskPluginOptions struct {
	Subdomain     string
	CredentialEnv string
	ReadOnly      bool
}

// ZendeskUserWidget binds a profile or requested-ticket list to a Zendesk user ID.
type ZendeskUserWidget struct {
	Collection string
	Name       string
	Title      string
	UserID     RecordBinding
}

// ZendeskTicketWidget binds a single ticket by ID.
type ZendeskTicketWidget struct {
	Collection string
	Name       string
	Title      string
	TicketID   RecordBinding
}

// ZendeskPlugin implements IntegrationPlugin with typed Zendesk resources.
type ZendeskPlugin struct {
	mu      sync.RWMutex
	options ZendeskPluginOptions
	widgets registry[WidgetDefinition]
}

var _ IntegrationPlugin = (*ZendeskPlugin)(nil)
var zendeskSubdomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// NewZendeskPlugin configures a tenant and an agent-local OAuth credential reference.
func NewZendeskPlugin(options ZendeskPluginOptions) (*ZendeskPlugin, error) {
	if !options.ReadOnly || !credentialEnvPattern.MatchString(options.CredentialEnv) || !zendeskSubdomainPattern.MatchString(options.Subdomain) {
		return nil, fmt.Errorf("zendesk requires a subdomain label and read-only OAuth credential environment variable")
	}
	return &ZendeskPlugin{options: options}, nil
}

// User adds a Zendesk user profile.
func (z *ZendeskPlugin) User(widget ZendeskUserWidget) *ZendeskPlugin {
	return z.add("user", widget.Collection, widget.Name, widget.Title, widget.UserID)
}

// Tickets adds a preview of tickets requested by the bound user.
// Ticket adds one ticket, bound by ticket ID.
func (z *ZendeskPlugin) Tickets(widget ZendeskUserWidget) *ZendeskPlugin {
	return z.add("tickets", widget.Collection, widget.Name, widget.Title, widget.UserID)
}

// Ticket adds one ticket, bound by ticket ID.
func (z *ZendeskPlugin) Ticket(widget ZendeskTicketWidget) *ZendeskPlugin {
	return z.add("ticket", widget.Collection, widget.Name, widget.Title, widget.TicketID)
}

func (z *ZendeskPlugin) add(resource, collection, name, title string, binding RecordBinding) *ZendeskPlugin {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.widgets.set(collection+"\x00"+name, WidgetDefinition{
		Collection: collection, Name: name, Title: title, Provider: "zendesk", Resource: resource,
		BindingField: binding.Field, BindingRelation: binding.Relation,
		CredentialEnv: z.options.CredentialEnv, ReadOnly: z.options.ReadOnly, Account: z.options.Subdomain,
	})
	return z
}

// Widgets returns a copy of the declarations for host registration.
func (z *ZendeskPlugin) Widgets() []WidgetDefinition {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return append([]WidgetDefinition(nil), z.widgets.items...)
}
