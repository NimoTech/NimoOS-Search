package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/NimoTech/NimoOS-Search/common"
	"github.com/stretchr/testify/require"
)

func TestBinaryRespondsToDashV(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "-v").CombinedOutput()
	require.NoError(t, err)
	require.True(t, strings.Contains(string(out), common.Version),
		"expected version string, got: %s", string(out))
}
