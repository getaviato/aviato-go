package aviato_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	aviato "github.com/getaviato/aviato-go"
)

func TestIntercomTypedWidgets(t *testing.T) {
	options := aviato.IntercomPluginOptions{Region: "eu", CredentialEnv: "INTERCOM_TOKEN", ReadOnly: true}
	integration, err := aviato.NewIntercomPlugin(options)
	require.NoError(t, err)
	options.Region = "au"
	_, err = integration.Contact(aviato.IntercomContactWidget{Collection: "customers", Name: "intercom", Title: "Intercom", ExternalID: aviato.RecordBinding{Field: "id", Relation: "account"}})
	require.NoError(t, err)
	_, err = integration.Contact(aviato.IntercomContactWidget{Collection: "customers", Name: "by-id", ContactID: aviato.RecordBinding{Field: "intercom_id"}})
	require.NoError(t, err)
	host := aviato.MustNew(aviato.Options{Secret: secret})
	require.NoError(t, host.Use(integration))
	manifest, err := host.Manifest()
	require.NoError(t, err)
	require.Len(t, manifest.Widgets, 2)
	for i, resource := range []string{"external-contact", "contact"} {
		widget := manifest.Widgets[i]
		require.Equal(t, resource, widget.Resource)
		require.Equal(t, "intercom", widget.Provider)
		require.Equal(t, "eu", widget.Account)
		require.Equal(t, "INTERCOM_TOKEN", widget.CredentialEnv)
		require.True(t, widget.ReadOnly)
	}
	require.Equal(t, "account", manifest.Widgets[0].BindingRelation)
	require.Equal(t, "intercom_id", manifest.Widgets[1].BindingField)
	integration.Widgets()[0].Account = "mutated"
	require.Equal(t, "eu", integration.Widgets()[0].Account)
}

func TestIntercomSameCollectionAndNameReplaces(t *testing.T) {
	integration, err := aviato.NewIntercomPlugin(aviato.IntercomPluginOptions{Region: "us", CredentialEnv: "INTERCOM_TOKEN", ReadOnly: true})
	require.NoError(t, err)
	_, err = integration.Contact(aviato.IntercomContactWidget{Collection: "customers", Name: "intercom", ExternalID: aviato.RecordBinding{Field: "id"}})
	require.NoError(t, err)
	_, err = integration.Contact(aviato.IntercomContactWidget{Collection: "customers", Name: "intercom", ContactID: aviato.RecordBinding{Field: "intercom_id"}})
	require.NoError(t, err)
	widgets := integration.Widgets()
	require.Len(t, widgets, 1)
	require.Equal(t, "contact", widgets[0].Resource)
}

func TestIntercomValidation(t *testing.T) {
	for _, region := range []string{"", "US", "https://api.intercom.io", "api.eu.intercom.io", "eu ", "us\n", "ca"} {
		t.Run(region, func(t *testing.T) {
			_, err := aviato.NewIntercomPlugin(aviato.IntercomPluginOptions{Region: region, CredentialEnv: "INTERCOM_TOKEN", ReadOnly: true})
			require.Error(t, err)
		})
	}
	_, err := aviato.NewIntercomPlugin(aviato.IntercomPluginOptions{Region: "us", CredentialEnv: "actual-token", ReadOnly: true})
	require.Error(t, err)
	_, err = aviato.NewIntercomPlugin(aviato.IntercomPluginOptions{Region: "us", CredentialEnv: "INTERCOM_TOKEN"})
	require.Error(t, err)

	integration, err := aviato.NewIntercomPlugin(aviato.IntercomPluginOptions{Region: "us", CredentialEnv: "INTERCOM_TOKEN", ReadOnly: true})
	require.NoError(t, err)
	binding := aviato.RecordBinding{Field: "id"}
	_, err = integration.Contact(aviato.IntercomContactWidget{Collection: "customers", Name: "both", ExternalID: binding, ContactID: binding})
	require.Error(t, err)
	_, err = integration.Contact(aviato.IntercomContactWidget{Collection: "customers", Name: "neither"})
	require.Error(t, err)
	require.Empty(t, integration.Widgets())

	_, err = integration.Contact(aviato.IntercomContactWidget{Collection: "customers", Name: "bad", ExternalID: aviato.RecordBinding{Field: "__proto__"}})
	require.NoError(t, err)
	host := aviato.MustNew(aviato.Options{Secret: secret})
	require.Error(t, host.Use(integration))
}
