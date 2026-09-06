package charts

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// rootKeyRE matches a key definition at the root level (column 0) of a YAML
// document. Nested keys and block scalar contents are always indented, so
// they never match.
var rootKeyRE = regexp.MustCompile(`^([A-Za-z0-9_.-]+):`)

// sortExceptions lists root-level keys that are allowed to appear out of
// alphabetical order, e.g. keys defining YAML anchors that must be declared
// before their aliases.
var sortExceptions = map[string][]string{
	// The &kubeapi anchor must be defined before it is referenced by
	// identity.kubeAPI.
	"linkerd-control-plane/values.yaml": {"kubeAPI"},
}

// TestValuesFilesRootKeysSorted enforces the convention that root-level keys
// in the charts' values files are sorted alphabetically (case-insensitively).
func TestValuesFilesRootKeysSorted(t *testing.T) {
	valuesFiles := []string{
		"linkerd-control-plane/values.yaml",
		"linkerd-control-plane/values-ha.yaml",
		"linkerd-crds/values.yaml",
		"linkerd2-cni/values.yaml",
	}

	for _, file := range valuesFiles {
		file := file
		t.Run(file, func(t *testing.T) {
			data, err := Templates.ReadFile(file)
			if err != nil {
				t.Fatalf("failed to read %s: %v", file, err)
			}

			exempt := map[string]struct{}{}
			for _, key := range sortExceptions[file] {
				exempt[key] = struct{}{}
			}

			keys := []string{}
			scanner := bufio.NewScanner(bytes.NewReader(data))
			for scanner.Scan() {
				match := rootKeyRE.FindStringSubmatch(scanner.Text())
				if match == nil {
					continue
				}
				if _, ok := exempt[match[1]]; ok {
					continue
				}
				keys = append(keys, match[1])
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("failed to scan %s: %v", file, err)
			}

			for i := 1; i < len(keys); i++ {
				if strings.ToLower(keys[i-1]) > strings.ToLower(keys[i]) {
					t.Errorf("root-level keys must be sorted alphabetically: %q appears after %q", keys[i], keys[i-1])
				}
			}
		})
	}
}
