package configmgmt_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mywebsite/construction-ai-gateway/internal/configmgmt"
)

func TestManifestRequiresInputLimitsForEveryCapabilityAndRank(t *testing.T) {
	for name, refs := range validManifest().CapabilityModels {
		for index := range refs {
			t.Run(fmt.Sprintf("%s/rank-%d", name, index), func(t *testing.T) {
				m := validManifest()
				m.CapabilityModels[name][index].MaxInputChars = 0
				data, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				// Remove the field entirely, as in an old manifest.
				data = []byte(strings.Replace(string(data), `,"max_input_chars":0`, "", 1))
				_, err = configmgmt.Parse(data)
				want := fmt.Sprintf("capability %s has not defined max_input_chars please see documentation", name)
				if err == nil || err.Error() != want {
					t.Fatalf("Parse() error = %v, want %q", err, want)
				}
			})
		}
	}
}

func TestManifestInputLimitValues(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"null", false}, {"0", false}, {"-1", false}, {"199", false},
		{"200", true}, {"8000", true}, {"500000", true}, {"500001", false},
		{"200.5", false}, {`"200"`, false}, {"true", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			data, err := json.Marshal(validManifest())
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.ReplaceAll(string(data), `"max_input_chars":200`, `"max_input_chars":`+tc.value))
			m, err := configmgmt.Parse(data)
			if !tc.valid {
				if err == nil {
					t.Fatal("Parse() accepted an invalid limit")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			snap, err := configmgmt.Resolve(m)
			if err != nil {
				t.Fatal(err)
			}
			if snap.Bindings["routing"].MaxInputChars != m.CapabilityModels["routing"][0].MaxInputChars {
				t.Fatal("Resolve changed the configured input limit")
			}
		})
	}
}
