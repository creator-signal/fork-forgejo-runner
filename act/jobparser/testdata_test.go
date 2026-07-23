package jobparser

import (
	"bytes"
	"embed"
	"path"
	"testing"

	"code.forgejo.org/forgejo/runner/v13/act/model"

	"github.com/stretchr/testify/require"
)

//go:embed testdata
var testdata embed.FS

func ReadTestdata(t *testing.T, name string, skipValidation bool) []byte {
	t.Helper()
	filename := path.Join("testdata", name)
	content, err := testdata.ReadFile(filename)
	require.NoError(t, err, filename)
	if !skipValidation {
		_, err = model.ReadWorkflow(bytes.NewReader(content), true)
		require.NoError(t, err, filename)
	}
	return content
}
