package jobparser

import (
	"bytes"
	"embed"
	"path/filepath"
	"testing"

	"code.forgejo.org/forgejo/runner/v11/act/model"

	"github.com/stretchr/testify/require"
)

//go:embed testdata
var testdata embed.FS

func ReadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	filename := filepath.Join("testdata", name)
	content, err := testdata.ReadFile(filename)
	require.NoError(t, err, filename)
	_, err = model.ReadWorkflow(bytes.NewReader(content), true)
	require.NoError(t, err, filename)
	return content
}
