package runner

import "code.forgejo.org/forgejo/runner/v12/act/container"

// runnerContext maps a back-end's platform and paths to the GitHub-Actions
// `runner` context. It is the only place OCI os/arch is translated to the
// Actions format.
func runnerContext(c container.ExecutionsEnvironment) map[string]any {
	p := c.GetPlatform()
	return map[string]any{
		"os":         actionsOS(p.OS),
		"arch":       actionsArch(p.Architecture),
		"temp":       c.GetTempDir(),
		"tool_cache": c.GetToolCache(),
	}
}

// Only values sanctioned by GitHub are returned; unsupported platforms map to
// "undefined" to keep the `runner` context compatible with GitHub Actions.
// https://docs.github.com/en/actions/learn-github-actions/contexts#runner-context
func actionsOS(os string) string {
	switch os {
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	default:
		return "undefined"
	}
}

func actionsArch(arch string) string {
	switch arch {
	case "386":
		return "X86"
	case "amd64":
		return "X64"
	case "arm":
		return "ARM"
	case "arm64":
		return "ARM64"
	default:
		return "undefined"
	}
}
