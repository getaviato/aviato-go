package aviato_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	aviato "github.com/getaviato/aviato-go"
)

type testIntegration []aviato.WidgetDefinition

func (i testIntegration) Widgets() []aviato.WidgetDefinition { return i }

func TestStripePlugin(t *testing.T) {
	stripe, err := aviato.NewStripePlugin(aviato.StripePluginOptions{CredentialEnv: "STRIPE_WIDGET_KEY", ReadOnly: true})
	require.NoError(t, err)
	customer := aviato.StripeCustomerWidget{Collection: "customers", Name: "billing", CustomerID: aviato.RecordBinding{Field: "stripe_id", Relation: "account"}}
	stripe.Customer(customer)
	p := aviato.MustNew(aviato.Options{Secret: secret})
	require.NoError(t, p.Use(stripe))
	stripe.Invoices(customer)
	manifest, err := p.Manifest()
	require.NoError(t, err)
	require.Equal(t, "customer", manifest.Widgets[0].Resource)
	require.NoError(t, p.Use(stripe))
	manifest, err = p.Manifest()
	require.NoError(t, err)
	require.Len(t, manifest.Widgets, 1)
	require.Equal(t, "account", manifest.Widgets[0].BindingRelation)
	require.Equal(t, "invoices", manifest.Widgets[0].Resource)
	snapshot := stripe.Widgets()
	snapshot[0].BindingField = "mutated"
	require.Equal(t, "stripe_id", stripe.Widgets()[0].BindingField)

	customer.Name = "subscriptions"
	stripe.Subscriptions(customer)
	stripe.Subscription(aviato.StripeSubscriptionWidget{Collection: "subscriptions", Name: "one", SubscriptionID: aviato.RecordBinding{Field: "subscription_id"}})
	stripe.Invoice(aviato.StripeInvoiceWidget{Collection: "invoices", Name: "one", InvoiceID: aviato.RecordBinding{Field: "invoice_id"}})
	require.NoError(t, p.Use(stripe))
	manifest, err = p.Manifest()
	require.NoError(t, err)
	require.Equal(t, "subscription_id", manifest.Widgets[2].BindingField)
	require.Equal(t, "invoice_id", manifest.Widgets[3].BindingField)
}

func TestIntegrationValidation(t *testing.T) {
	_, err := aviato.NewStripePlugin(aviato.StripePluginOptions{CredentialEnv: "rk_test_private", ReadOnly: true})
	require.Error(t, err)
	_, err = aviato.NewStripePlugin(aviato.StripePluginOptions{CredentialEnv: "STRIPE_WIDGET_KEY"})
	require.Error(t, err)
	valid := aviato.WidgetDefinition{Collection: "customers", Name: "billing", Provider: "custom", Resource: "tickets", BindingField: "external_id", CredentialEnv: "CUSTOM_KEY", ReadOnly: true}
	invalid := valid
	invalid.ReadOnly = false
	p := aviato.MustNew(aviato.Options{Secret: secret})
	require.Error(t, p.Use(testIntegration{valid, invalid}))
	manifest, err := p.Manifest()
	require.NoError(t, err)
	require.Empty(t, manifest.Widgets)
	require.NoError(t, p.Use(testIntegration{valid}))
}

func TestStripeCredentialReferences(t *testing.T) {
	for _, credential := range []string{"", "rk_test_secret", "STRIPE-KEY", "1STRIPE_KEY", " STRIPE_KEY", "STRIPE_KEY\n", "STRIPE_KEY\r\n", strings.Repeat("A", 129)} {
		t.Run(credential, func(t *testing.T) {
			_, err := aviato.NewStripePlugin(aviato.StripePluginOptions{CredentialEnv: credential, ReadOnly: true})
			require.Error(t, err)
		})
	}
}

func TestIntegrationFailedReplacementIsAtomic(t *testing.T) {
	original := aviato.WidgetDefinition{Collection: "customers", Name: "billing", Provider: "stripe", Resource: "invoices", BindingField: "stripe_id", CredentialEnv: "STRIPE_KEY", ReadOnly: true}
	for name, invalidate := range map[string]func(*aviato.WidgetDefinition){
		"read-only":        func(w *aviato.WidgetDefinition) { w.ReadOnly = false },
		"credential":       func(w *aviato.WidgetDefinition) { w.CredentialEnv = "rk_live_secret" },
		"empty collection": func(w *aviato.WidgetDefinition) { w.Collection = "" },
		"reserved name":    func(w *aviato.WidgetDefinition) { w.Name = "constructor" },
		"reserved field":   func(w *aviato.WidgetDefinition) { w.BindingField = "__proto__" },
		"long field":       func(w *aviato.WidgetDefinition) { w.BindingField = strings.Repeat("f", 129) },
		"long relation":    func(w *aviato.WidgetDefinition) { w.BindingRelation = strings.Repeat("r", 129) },
		"long title":       func(w *aviato.WidgetDefinition) { w.Title = strings.Repeat("t", 161) },
		"provider":         func(w *aviato.WidgetDefinition) { w.Provider = "https://stripe.com" },
		"resource":         func(w *aviato.WidgetDefinition) { w.Resource = "../customers" },
	} {
		t.Run(name, func(t *testing.T) {
			host := aviato.MustNew(aviato.Options{Secret: secret})
			require.NoError(t, host.Use(testIntegration{original}))
			replacement := original
			replacement.Resource = "customer"
			sibling := original
			sibling.Name = "new"
			invalid := original
			invalidate(&invalid)
			require.Error(t, host.Use(testIntegration{replacement, sibling, invalid}))
			manifest, err := host.Manifest()
			require.NoError(t, err)
			require.Len(t, manifest.Widgets, 1)
			require.Equal(t, "invoices", manifest.Widgets[0].Resource)
		})
	}
}

func TestIntegrationNamespaceAndSnapshot(t *testing.T) {
	host := aviato.MustNew(aviato.Options{Secret: secret})
	base := aviato.WidgetDefinition{Provider: "stripe", Resource: "invoices", BindingField: "stripe_id", CredentialEnv: "STRIPE_KEY", ReadOnly: true}
	pairs := [][2]string{{"orders.account", "billing"}, {"orders", "account.billing"}, {"customers", "billing"}, {"customers", "invoices"}}
	definitions := make(testIntegration, 0, len(pairs))
	for _, pair := range pairs {
		widget := base
		widget.Collection, widget.Name = pair[0], pair[1]
		definitions = append(definitions, widget)
	}
	require.NoError(t, host.Use(definitions))
	definitions[0].CredentialEnv = "OTHER_ACCOUNT"
	require.NoError(t, host.Use(testIntegration{}))
	manifest, err := host.Manifest()
	require.NoError(t, err)
	require.Len(t, manifest.Widgets, len(pairs))
	for index, pair := range pairs {
		require.Equal(t, pair[0], manifest.Widgets[index].Collection)
		require.Equal(t, pair[1], manifest.Widgets[index].Name)
		require.Equal(t, "STRIPE_KEY", manifest.Widgets[index].CredentialEnv)
	}
}

func TestStripeBuildersAreIndependent(t *testing.T) {
	first, err := aviato.NewStripePlugin(aviato.StripePluginOptions{CredentialEnv: "FIRST_KEY", ReadOnly: true})
	require.NoError(t, err)
	second, err := aviato.NewStripePlugin(aviato.StripePluginOptions{CredentialEnv: "SECOND_KEY", ReadOnly: true})
	require.NoError(t, err)
	binding := aviato.StripeCustomerWidget{Collection: "customers", Name: "billing", Title: "Billing", CustomerID: aviato.RecordBinding{Field: "stripe_id", Relation: "account"}}
	first.Customer(binding)
	binding.CustomerID.Field = "changed"
	second.Invoices(binding)
	require.Equal(t, "stripe_id", first.Widgets()[0].BindingField)
	require.Equal(t, "FIRST_KEY", first.Widgets()[0].CredentialEnv)
	require.Equal(t, "changed", second.Widgets()[0].BindingField)
	require.Equal(t, "SECOND_KEY", second.Widgets()[0].CredentialEnv)
	require.Equal(t, "Billing", first.Widgets()[0].Title)
}

func TestStripeResourceBindings(t *testing.T) {
	customer := aviato.StripeCustomerWidget{Collection: "records", Name: "billing", Title: "Billing", CustomerID: aviato.RecordBinding{Field: "customer_id", Relation: "account"}}
	cases := []struct {
		resource string
		field    string
		relation string
		build    func(*aviato.StripePlugin)
	}{
		{"customer", "customer_id", "account", func(p *aviato.StripePlugin) { p.Customer(customer) }},
		{"subscriptions", "customer_id", "account", func(p *aviato.StripePlugin) { p.Subscriptions(customer) }},
		{"invoices", "customer_id", "account", func(p *aviato.StripePlugin) { p.Invoices(customer) }},
		{"subscription", "subscription_id", "", func(p *aviato.StripePlugin) {
			p.Subscription(aviato.StripeSubscriptionWidget{Collection: "records", Name: "billing", Title: "Billing", SubscriptionID: aviato.RecordBinding{Field: "subscription_id"}})
		}},
		{"invoice", "invoice_id", "", func(p *aviato.StripePlugin) {
			p.Invoice(aviato.StripeInvoiceWidget{Collection: "records", Name: "billing", Title: "Billing", InvoiceID: aviato.RecordBinding{Field: "invoice_id"}})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.resource, func(t *testing.T) {
			stripe, err := aviato.NewStripePlugin(aviato.StripePluginOptions{CredentialEnv: "STRIPE_KEY", ReadOnly: true})
			require.NoError(t, err)
			tc.build(stripe)
			require.Equal(t, []aviato.WidgetDefinition{{Collection: "records", Name: "billing", Title: "Billing", Provider: "stripe", Resource: tc.resource, BindingField: tc.field, BindingRelation: tc.relation, CredentialEnv: "STRIPE_KEY", ReadOnly: true}}, stripe.Widgets())
		})
	}
}
