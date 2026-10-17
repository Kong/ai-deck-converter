package aigw

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestTargetModelConfigBedrockEndpointType(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"empty":         {in: `{type: bedrock, region: us-east-1}`, want: ""},
		"runtime":       {in: `{type: bedrock, endpoint_type: runtime}`, want: "runtime"},
		"mantle":        {in: `{type: bedrock, endpoint_type: mantle}`, want: "mantle"},
		"unknown value": {in: `{type: bedrock, endpoint_type: other}`, want: ""},
		"not bedrock":   {in: `{type: openai, endpoint_type: mantle}`, want: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var c TargetModelConfig
			require.NoError(t, yaml.Unmarshal([]byte(tc.in), &c))
			require.Equal(t, tc.want, c.BedrockEndpointType)
		})
	}
}
