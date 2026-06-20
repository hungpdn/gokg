package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionCommandPlainText(t *testing.T) {
	var stdout bytes.Buffer
	cmd := newVersionCommand()
	cmd.SetOut(&stdout)

	require.NoError(t, cmd.Execute())
	assert.Contains(t, stdout.String(), "gokg ")
	assert.Contains(t, stdout.String(), "commit:")
	assert.Contains(t, stdout.String(), "go:")
	assert.Contains(t, stdout.String(), "platform:")
}

func TestVersionCommandJSON(t *testing.T) {
	var stdout bytes.Buffer
	cmd := newVersionCommand()
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--json"})

	require.NoError(t, cmd.Execute())

	var payload map[string]string
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &payload))
	assert.NotEmpty(t, payload["version"])
	assert.NotEmpty(t, payload["commit"])
	assert.NotEmpty(t, payload["go_version"])
	assert.NotEmpty(t, payload["platform"])
}
