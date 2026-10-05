package echoadapter_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	aviato "github.com/getaviato/aviato-go"
	"github.com/getaviato/aviato-go/echoadapter"
	pluginv1 "github.com/getaviato/aviato-go/pluginv1"
	"github.com/getaviato/aviato-go/pluginv1/pluginv1connect"
)

func TestMount(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("aviato-echo-adapter-test-secret!"))
	plugin := aviato.MustNew(aviato.Options{Secret: secret, BasePath: "/aviato"}).
		Action("customers", "Export", aviato.ScopeGlobal, func(context.Context, *aviato.ActionContext) (aviato.Result, error) {
			return aviato.File("a.csv", "text/csv", []byte("a\n")), nil
		})
	e := echo.New()
	e.GET("/health", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") })
	echoadapter.Mount(e, plugin)
	server := httptest.NewServer(e)
	t.Cleanup(server.Close)

	signed, err := aviato.NewSigningClient(secret)
	require.NoError(t, err)
	client := pluginv1connect.NewPluginServiceClient(signed, server.URL+"/aviato", connect.WithProtoJSON())
	response, err := client.ExecuteAction(context.Background(), connect.NewRequest(&pluginv1.ExecuteActionRequest{Caller: &pluginv1.Caller{UserId: "u1"}, Action: "Export", Target: &pluginv1.ActionTarget{Collection: "customers"}}))
	require.NoError(t, err)
	ref := response.Msg.GetResult().GetFile().GetRef()
	require.NotEmpty(t, ref)

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/aviato/files/"+ref, nil)
	require.NoError(t, err)
	download, err := signed.Do(request)
	require.NoError(t, err)
	download.Body.Close()
	assert.Equal(t, http.StatusOK, download.StatusCode, "file downloads are routed too")

	unsigned := pluginv1connect.NewPluginServiceClient(http.DefaultClient, server.URL+"/aviato", connect.WithProtoJSON())
	_, err = unsigned.GetManifest(context.Background(), connect.NewRequest(&pluginv1.GetManifestRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
