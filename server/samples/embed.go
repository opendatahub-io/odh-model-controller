package samples

import "embed"

//go:embed *.yaml
var content embed.FS

// ByType returns the embedded YAML sample for the given config-type.
// The configType maps to a file named "<configType>.yaml".
// Returns an *fs.PathError when the type is unknown.
func ByType(configType string) ([]byte, error) {
	return content.ReadFile(configType + ".yaml")
}
