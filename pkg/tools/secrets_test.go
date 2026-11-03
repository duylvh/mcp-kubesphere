package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every value in the fixture contains "S3CRET" (plain or base64), so any leak
// into the output is detectable.
const secretListFixture = `{
 "items": [
  {
   "kind": "Secret",
   "type": "Opaque",
   "metadata": {
    "name": "db",
    "namespace": "p1",
    "labels": {"app": "fund"},
    "annotations": {
     "kubesphere.io/creator": "alice",
     "objectset.rio.cattle.io/applied": "{\"data\":{\"password\":\"S3CRET-planted\"}}",
     "kubectl.kubernetes.io/last-applied-configuration": "{\"data\":{\"password\":\"UzNDUkVULXB3\"},\"stringData\":{\"token\":\"S3CRET-token\"}}"
    },
    "managedFields": [{"manager": "kubectl", "fieldsV1": {"f:data": {"f:password": {}}}}]
   },
   "data": {"password": "UzNDUkVULXB3", "username": "UzNDUkVULXVzZXI="},
   "stringData": {"token": "S3CRET-token"}
  },
  {
   "kind": "Secret",
   "type": "kubernetes.io/service-account-token",
   "metadata": {"name": "empty", "namespace": "p1"}
  }
 ],
 "totalItems": 2
}`

func assertNoSecretValues(t *testing.T, out string) {
	t.Helper()
	for _, leak := range []string{"S3CRET", "UzNDUkVU", "last-applied-configuration", "managedFields", "objectset.rio.cattle.io"} {
		assert.NotContains(t, out, leak)
	}
	var list struct {
		Items []struct {
			Type     string   `json:"type"`
			DataKeys []string `json:"dataKeys"`
			Metadata struct {
				Name        string            `json:"name"`
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Data       any `json:"data"`
			StringData any `json:"stringData"`
		} `json:"items"`
		TotalItems int `json:"totalItems"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &list))
	require.Len(t, list.Items, 2)
	assert.Equal(t, 2, list.TotalItems)
	first := list.Items[0]
	assert.Equal(t, []string{"password", "token", "username"}, first.DataKeys)
	assert.Nil(t, first.Data)
	assert.Nil(t, first.StringData)
	assert.Equal(t, "db", first.Metadata.Name)
	assert.Equal(t, "Opaque", first.Type)
	assert.Equal(t, map[string]string{"app": "fund"}, first.Metadata.Labels)
	assert.Equal(t, map[string]string{"kubesphere.io/creator": "alice"}, first.Metadata.Annotations)
	assert.Equal(t, []string{}, list.Items[1].DataKeys)
}

func TestRedactSecretList(t *testing.T) {
	out, err := redactSecretList([]byte(secretListFixture))
	require.NoError(t, err)
	assertNoSecretValues(t, string(out))
}

func TestRedactSecretListWithholdsUnexpectedShapes(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":          "S3CRET",
		"array":             `["S3CRET"]`,
		"no items":          `{"data":{"password":"S3CRET"}}`,
		"items not a list":  `{"items":{"data":{"password":"S3CRET"}}}`,
		"data not a map":    `{"items":[{"data":"S3CRET"}]}`,
		"stringData a list": `{"items":[{"stringData":["S3CRET"]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := redactSecretList([]byte(body))
			require.ErrorIs(t, err, errSecretListShape)
			assert.Empty(t, out)
			assert.False(t, strings.Contains(err.Error(), "S3CRET"))
		})
	}

	out, err := redactSecretList([]byte(`{"items":null,"totalItems":0}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"items":null,"totalItems":0}`, string(out))
}
