package aviato_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	aviato "github.com/getaviato/aviato-go"
	"github.com/getaviato/aviato-go/pluginv1"
)

func TestLayoutWireParity(t *testing.T) {
	content, err := os.ReadFile("testdata/layout-v1.json")
	require.NoError(t, err)
	var layout map[string]any
	require.NoError(t, json.Unmarshal(content, &layout))
	p, err := aviato.New(aviato.Options{Secret: secret})
	require.NoError(t, err)
	p.Layout("customers", layout)
	manifest, err := p.Manifest()
	require.NoError(t, err)
	encoded, err := protojson.Marshal(manifest)
	require.NoError(t, err)
	var decoded pluginv1.Manifest
	require.NoError(t, protojson.Unmarshal(encoded, &decoded))
	require.Len(t, decoded.Layouts, 1)
	require.Equal(t, "customers", decoded.Layouts[0].Collection)
	require.Equal(t, layout, decoded.Layouts[0].Layout.AsMap())

	p.Layout("customers", map[string]any{"label": "Clients"})
	manifest, err = p.Manifest()
	require.NoError(t, err)
	require.Len(t, manifest.Layouts, 1)
	require.Equal(t, map[string]any{"label": "Clients"}, manifest.Layouts[0].Layout.AsMap())
}
