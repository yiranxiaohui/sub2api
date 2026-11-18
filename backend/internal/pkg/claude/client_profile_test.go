package claude

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultHeadersCapturedNativeProfile(t *testing.T) {
	raw, err := os.ReadFile("testdata/native-linux-x64-2.1.327.json")
	require.NoError(t, err)
	var capture struct {
		Capture struct {
			Version string `json:"cli_version"`
		} `json:"capture"`
		Headers map[string]string `json:"headers"`
	}
	require.NoError(t, json.Unmarshal(raw, &capture))
	profile := DefaultClientProfile()
	require.Equal(t, capture.Capture.Version, profile.CLIVersion)
	require.Equal(t, profile.CLIVersion, CLICurrentVersion)
	headers := DefaultHeaders()
	for key, value := range capture.Headers {
		require.Equal(t, value, headers[key], key)
	}
	headers["X-Stainless-Arch"] = "changed"
	require.Equal(t, "x64", DefaultHeaders()["X-Stainless-Arch"])
	profile.StainlessArch = "changed"
	require.Equal(t, "x64", DefaultClientProfile().StainlessArch)
}
