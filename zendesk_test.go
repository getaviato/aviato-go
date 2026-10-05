package aviato_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	aviato "github.com/getaviato/aviato-go"
)

func TestZendeskTypedWidgets(t *testing.T) {
	options := aviato.ZendeskPluginOptions{Subdomain: "northstar", CredentialEnv: "ZENDESK_TOKEN", ReadOnly: true}
	integration, err := aviato.NewZendeskPlugin(options)
	require.NoError(t, err)
	options.Subdomain = "other"
	user := aviato.ZendeskUserWidget{Collection: "customers", Name: "user", UserID: aviato.RecordBinding{Field: "zendesk_id", Relation: "account"}}
	integration.User(user)
	user.Name = "tickets"
	integration.Tickets(user).Ticket(aviato.ZendeskTicketWidget{Collection: "issues", Name: "ticket", TicketID: aviato.RecordBinding{Field: "ticket_id"}})
	host := aviato.MustNew(aviato.Options{Secret: secret})
	require.NoError(t, host.Use(integration))
	manifest, err := host.Manifest()
	require.NoError(t, err)
	require.Len(t, manifest.Widgets, 3)
	for i, resource := range []string{"user", "tickets", "ticket"} {
		widget := manifest.Widgets[i]
		require.Equal(t, resource, widget.Resource)
		require.Equal(t, "zendesk", widget.Provider)
		require.Equal(t, "northstar", widget.Account)
		require.Equal(t, "ZENDESK_TOKEN", widget.CredentialEnv)
		require.True(t, widget.ReadOnly)
	}
	require.Equal(t, "account", manifest.Widgets[0].BindingRelation)
	require.Equal(t, "ticket_id", manifest.Widgets[2].BindingField)
	integration.Widgets()[0].Account = "mutated"
	manifest.Widgets[0].Account = "mutated"
	manifest, err = host.Manifest()
	require.NoError(t, err)
	require.Equal(t, "northstar", manifest.Widgets[0].Account)
	require.Equal(t, "northstar", integration.Widgets()[0].Account)
	user.Name = "user"
	integration.Tickets(user)
	require.Equal(t, "user", manifest.Widgets[0].Resource)
	require.NoError(t, host.Use(integration))
	manifest, err = host.Manifest()
	require.NoError(t, err)
	require.Equal(t, "tickets", manifest.Widgets[0].Resource)
}

func TestZendeskValidation(t *testing.T) {
	for _, subdomain := range []string{"", "https://northstar.zendesk.com", "northstar.zendesk.com", "host@evil.test", "../evil", "-bad", "bad-", "bad\n", strings.Repeat("a", 64)} {
		t.Run(subdomain, func(t *testing.T) {
			_, err := aviato.NewZendeskPlugin(aviato.ZendeskPluginOptions{Subdomain: subdomain, CredentialEnv: "ZENDESK_TOKEN", ReadOnly: true})
			require.Error(t, err)
		})
	}
	_, err := aviato.NewZendeskPlugin(aviato.ZendeskPluginOptions{Subdomain: "northstar", CredentialEnv: "actual-token", ReadOnly: true})
	require.Error(t, err)
	_, err = aviato.NewZendeskPlugin(aviato.ZendeskPluginOptions{Subdomain: "northstar", CredentialEnv: "ZENDESK_TOKEN"})
	require.Error(t, err)
	integration, err := aviato.NewZendeskPlugin(aviato.ZendeskPluginOptions{Subdomain: "northstar", CredentialEnv: "ZENDESK_TOKEN", ReadOnly: true})
	require.NoError(t, err)
	host := aviato.MustNew(aviato.Options{Secret: secret})
	integration.User(aviato.ZendeskUserWidget{Collection: "customers", Name: "support", UserID: aviato.RecordBinding{Field: "id"}})
	require.NoError(t, host.Use(integration))
	integration.Tickets(aviato.ZendeskUserWidget{Collection: "customers", Name: "support", UserID: aviato.RecordBinding{Field: "__proto__"}})
	require.Error(t, host.Use(integration))
	manifest, err := host.Manifest()
	require.NoError(t, err)
	require.Equal(t, "user", manifest.Widgets[0].Resource)
}
