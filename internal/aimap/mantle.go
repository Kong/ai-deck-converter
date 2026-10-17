package aimap

import "strings"

// MantlePathPrefixAuto is the mantle_openai_path_prefix value for a model
// that has no fixed base path.
const MantlePathPrefixAuto = "auto_detect"

// mantlePathRules are checked in order. The first matching name prefix wins.
var mantlePathRules = []struct{ namePrefix, pathPrefix string }{
	{"google.gemma-4-", "/openai/v1"},
	{"google.gemma-3-", "/v1"},
	{"openai.gpt-oss", "/v1"},
	{"openai.gpt-", "/openai/v1"},
	{"xai.", "/openai/v1"},
	{"deepseek.", "/v1"},
	{"minimax.", "/v1"},
	{"mistral.", "/v1"},
	{"moonshotai.", "/v1"},
	{"nvidia.", "/v1"},
	{"qwen.", "/v1"},
	{"zai.", "/v1"},
}

// MantlePathPrefix returns the mantle_openai_path_prefix value for a backend
// model name. Unmatched models get MantlePathPrefixAuto.
func MantlePathPrefix(modelName string) string {
	for _, r := range mantlePathRules {
		if strings.HasPrefix(modelName, r.namePrefix) {
			return r.pathPrefix
		}
	}
	return MantlePathPrefixAuto
}
