package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// actionStep mirrors just enough of a composite action step to check its
// condition and run its script in isolation.
type actionStep struct {
	Name string `yaml:"name"`
	ID   string `yaml:"id"`
	If   string `yaml:"if"`
	Run  string `yaml:"run"`
}

func loadRootActionSteps(t *testing.T) map[string]actionStep {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "action.yml"))
	require.NoError(t, err)
	var action struct {
		Runs struct {
			Steps []actionStep `yaml:"steps"`
		} `yaml:"runs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &action))
	steps := make(map[string]actionStep, len(action.Runs.Steps))
	for _, st := range action.Runs.Steps {
		steps[st.Name] = st
	}
	return steps
}

// runDetectSandboxHost runs the "Detect sandbox host" step script with only
// pathDir on PATH and returns the outputs it wrote to GITHUB_OUTPUT. When
// endpoint is nil, OPENSHELL_GATEWAY_ENDPOINT is left unset.
func runDetectSandboxHost(t *testing.T, script string, endpoint *string, pathDir string) map[string]string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	require.NoError(t, err)

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "step.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	outputPath := filepath.Join(dir, "github_output")
	require.NoError(t, os.WriteFile(outputPath, nil, 0o600))

	env := []string{"PATH=" + pathDir, "GITHUB_OUTPUT=" + outputPath}
	if endpoint != nil {
		env = append(env, "OPENSHELL_GATEWAY_ENDPOINT="+*endpoint)
	}
	cmd := exec.Command(bash, scriptPath)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	raw, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	outputs := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			outputs[k] = v
		}
	}
	return outputs
}

func TestActionDetectSandboxHostStep(t *testing.T) {
	steps := loadRootActionSteps(t)
	detect, ok := steps["Detect sandbox host"]
	require.True(t, ok, "action.yml must have a Detect sandbox host step")
	require.Equal(t, "sandbox-host", detect.ID)
	require.NotEmpty(t, detect.Run)

	emptyPath := t.TempDir()
	withCLI := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(withCLI, "openshell"), []byte("#!/bin/sh\nexit 0\n"), 0o700))

	empty := ""
	remote := "https://gateway.example:8080"

	cases := []struct {
		name             string
		endpoint         *string
		pathDir          string
		wantLocal        string
		wantInstallShell string
	}{
		{name: "endpoint unset sets up a local sandbox host", pathDir: withCLI, wantLocal: "true", wantInstallShell: "true"},
		{name: "empty endpoint sets up a local sandbox host", endpoint: &empty, pathDir: withCLI, wantLocal: "true", wantInstallShell: "true"},
		{name: "endpoint set with preinstalled CLI skips all local setup", endpoint: &remote, pathDir: withCLI, wantLocal: "false", wantInstallShell: "false"},
		{name: "endpoint set without CLI still installs the CLI", endpoint: &remote, pathDir: emptyPath, wantLocal: "false", wantInstallShell: "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outputs := runDetectSandboxHost(t, detect.Run, tc.endpoint, tc.pathDir)
			assert.Equal(t, tc.wantLocal, outputs["local"])
			assert.Equal(t, tc.wantInstallShell, outputs["install-openshell"])
		})
	}
}

func TestActionLocalSandboxHostStepsAreGated(t *testing.T) {
	steps := loadRootActionSteps(t)
	for _, name := range []string{"Install Podman", "Configure rootless Podman", "Configure OpenShell gateway"} {
		st, ok := steps[name]
		require.True(t, ok, "action.yml must have a %s step", name)
		assert.Equal(t, "steps.sandbox-host.outputs.local == 'true'", st.If, name)
	}

	install, ok := steps["Install OpenShell CLI"]
	require.True(t, ok, "action.yml must have an Install OpenShell CLI step")
	assert.Equal(t, "steps.sandbox-host.outputs.install-openshell == 'true'", install.If)

	run, ok := steps["Run fullsend"]
	require.True(t, ok, "action.yml must have a Run fullsend step")
	assert.NotContains(t, run.If, "sandbox-host", "Run fullsend must run whichever sandbox host the runner uses")
}
