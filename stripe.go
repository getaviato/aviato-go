package aviato

import (
	"fmt"
	"sync"
)

// StripePluginOptions refers to a restricted key in the customer agent environment.
// ReadOnly confirms the operator has configured Read/None permissions in Stripe.
type StripePluginOptions struct {
	CredentialEnv string
	ReadOnly      bool
}

// StripeCustomerWidget binds a customer detail or list widget to a Stripe customer ID.
type StripeCustomerWidget struct {
	Collection string
	Name       string
	Title      string
	CustomerID RecordBinding
}

// StripeSubscriptionWidget binds a single subscription by its Stripe subscription ID.
type StripeSubscriptionWidget struct {
	Collection     string
	Name           string
	Title          string
	SubscriptionID RecordBinding
}

// StripeInvoiceWidget binds a single invoice by its Stripe invoice ID.
type StripeInvoiceWidget struct {
	Collection string
	Name       string
	Title      string
	InvoiceID  RecordBinding
}

// StripePlugin implements IntegrationPlugin with typed Stripe resources and bindings.
type StripePlugin struct {
	mu      sync.RWMutex
	options StripePluginOptions
	widgets registry[WidgetDefinition]
}

var _ IntegrationPlugin = (*StripePlugin)(nil)

// NewStripePlugin configures a typed integration with an agent-local credential reference.
func NewStripePlugin(options StripePluginOptions) (*StripePlugin, error) {
	if !options.ReadOnly || !credentialEnvPattern.MatchString(options.CredentialEnv) {
		return nil, fmt.Errorf("stripe requires a read-only credential environment variable")
	}
	return &StripePlugin{options: options}, nil
}

// Customer adds customer details.
func (s *StripePlugin) Customer(widget StripeCustomerWidget) *StripePlugin {
	return s.add("customer", widget.Collection, widget.Name, widget.Title, widget.CustomerID)
}

// Subscriptions adds a customer’s subscription preview, including canceled subscriptions.
func (s *StripePlugin) Subscriptions(widget StripeCustomerWidget) *StripePlugin {
	return s.add("subscriptions", widget.Collection, widget.Name, widget.Title, widget.CustomerID)
}

// Invoices adds a customer’s invoice preview.
func (s *StripePlugin) Invoices(widget StripeCustomerWidget) *StripePlugin {
	return s.add("invoices", widget.Collection, widget.Name, widget.Title, widget.CustomerID)
}

// Subscription adds one subscription, bound by subscription ID.
func (s *StripePlugin) Subscription(widget StripeSubscriptionWidget) *StripePlugin {
	return s.add("subscription", widget.Collection, widget.Name, widget.Title, widget.SubscriptionID)
}

// Invoice adds one invoice, bound by invoice ID.
func (s *StripePlugin) Invoice(widget StripeInvoiceWidget) *StripePlugin {
	return s.add("invoice", widget.Collection, widget.Name, widget.Title, widget.InvoiceID)
}

func (s *StripePlugin) add(resource, collection, name, title string, binding RecordBinding) *StripePlugin {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.widgets.set(collection+"\x00"+name, WidgetDefinition{
		Collection: collection, Name: name, Title: title, Provider: "stripe", Resource: resource,
		BindingField: binding.Field, BindingRelation: binding.Relation,
		CredentialEnv: s.options.CredentialEnv, ReadOnly: s.options.ReadOnly,
	})
	return s
}

// Widgets returns a copy of the provider declarations for host registration.
func (s *StripePlugin) Widgets() []WidgetDefinition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]WidgetDefinition(nil), s.widgets.items...)
}
