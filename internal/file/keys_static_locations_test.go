package file

import (
	"fmt"
	"strings"
	"testing"

	"github.com/denglertai/gonfig/internal/general"
	"github.com/stretchr/testify/assert"
	"go.yaml.in/yaml/v4"
)

// TestIsYamlMergeScalar checks which scalars the decoder drops from sequences as merge keys
func TestIsYamlMergeScalar(t *testing.T) {
	for desc, tC := range map[string]struct {
		node *yaml.Node
		want bool
	}{
		"plain untagged": {&yaml.Node{Kind: yaml.ScalarNode, Value: "<<"}, true},
		"tagged merge":   {&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!merge", Value: "x"}, true},
		"double quoted":  {&yaml.Node{Kind: yaml.ScalarNode, Value: "<<", Style: yaml.DoubleQuotedStyle}, false},
		"single quoted":  {&yaml.Node{Kind: yaml.ScalarNode, Value: "<<", Style: yaml.SingleQuotedStyle}, false},
		"literal":        {&yaml.Node{Kind: yaml.ScalarNode, Value: "<<", Style: yaml.LiteralStyle}, false},
		"folded":         {&yaml.Node{Kind: yaml.ScalarNode, Value: "<<", Style: yaml.FoldedStyle}, false},
		"other plain":    {&yaml.Node{Kind: yaml.ScalarNode, Value: "x"}, false},
		"tagged string":  {&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "<<"}, false},
		"mapping":        {&yaml.Node{Kind: yaml.MappingNode}, false},
		"alias":          {&yaml.Node{Kind: yaml.AliasNode, Value: "<<"}, false},
	} {
		assert.Equal(t, tC.want, isYamlMergeScalar(tC.node), desc)
	}
}

// TestRedactKeysStaticLocations checks which keys are shown when the locator knows the static keys: a key is shown
// only at the location of a static key, whatever other keys have the same text
func TestRedactKeysStaticLocations(t *testing.T) {
	t.Setenv("KEYS_SL_P", "made-up-same")
	t.Setenv("KEYS_SL_FR", `[&k "${KEYS_SL_P}"]`)

	testCases := []struct {
		desc     string
		fileType general.FileType
		content  string
		schema   string
		want     []string
	}{
		{"json static and processed key of the same text at different locations", general.JSON,
			`{"a": {"made-up-same": 1}, "b": {"${KEYS_SL_P}": 1}}`,
			`{"properties": {"a": {"additionalProperties": false}, "b": {"additionalProperties": false}}}`,
			[]string{"a: additional properties 'made-up-same' not allowed", "b: additional properties '***' not allowed"}},
		{"toml static and processed key of the same text at different locations", general.TOML,
			"[a]\nmade-up-same = 1\n[b]\n\"${KEYS_SL_P}\" = 1\n",
			`{"properties": {"a": {"additionalProperties": false}, "b": {"additionalProperties": false}}}`,
			[]string{"a: additional properties 'made-up-same' not allowed", "b: additional properties '***' not allowed"}},
		{"toml without processed keys falls back to the text of the static keys", general.TOML,
			"[a]\nmade-up-same = 1\n", `{"properties": {"a": {"additionalProperties": false}}}`,
			[]string{"a: additional properties 'made-up-same' not allowed"}},
		{"json without processed keys falls back to the text of the static keys", general.JSON,
			`{"a": {"made-up-same": 1}}`, `{"properties": {"a": {"additionalProperties": false}}}`,
			[]string{"a: additional properties 'made-up-same' not allowed"}},
		{"yaml without processed keys falls back to the text of the static keys", general.YAML,
			"a:\n  made-up-same: 1\n", `{"properties": {"a": {"additionalProperties": false}}}`,
			[]string{"a: additional properties 'made-up-same' not allowed"}},
		{"yaml alias redefined several times resolves to the last anchor", general.YAML,
			"a: &k first\nb: &k \"${KEYS_SL_P}\"\nc: &k second\nm: {*k : 1}\nmade-up-same: 1\n",
			`{"properties": {"m": {"additionalProperties": false}}}`,
			[]string{"m: additional properties 'second' not allowed"}},
		{"yaml alias redefined, the processed anchor is the last", general.YAML,
			"a: &k first\nb: &k \"${KEYS_SL_P}\"\nm: {*k : 1}\nmade-up-same: 1\n",
			`{"properties": {"m": {"additionalProperties": false}}}`,
			[]string{"m: additional properties '***' not allowed"}},
		{"yaml merge key plain and tagged in a sequence keep the indexes", general.YAML,
			"l:\n  - <<\n  - {made-up-same: 1}\n  - !!merge x\n  - {\"${KEYS_SL_P}\": 1}\n",
			`{"properties": {"l": {"items": {"additionalProperties": false}}}}`,
			[]string{"l.0: additional properties 'made-up-same' not allowed", "l.1: additional properties '***' not allowed"}},
		{"yaml nested anchors in a fragment are masked", general.YAML,
			"f: ${KEYS_SL_FR2 | fragment}\nmade-up-same: 1\n",
			`{"properties": {"f": {"properties": {"m": {"additionalProperties": false}}}}}`,
			[]string{"f.m: additional properties '***' not allowed"}},
	}
	t.Setenv("KEYS_SL_FR2", "{o: &o {v: &a made-up-same, w: &b made-up-same}, m: {*a : 1}}")
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			assert.Equal(t, tC.want, redactedViolations(t, tC.fileType, tC.content, tC.schema))
		})
	}
}

// TestRedactKeysSpareBudget checks that a document whose aliases of mappings without candidates expand exponentially
// is walked within the spare budget: the locator terminates, keys at locations it did not reach are masked, and
// the keys produced by placeholders stay masked
func TestRedactKeysSpareBudget(t *testing.T) {
	t.Setenv("KEYS_SL_P", "made-up-s3cret")
	var content strings.Builder
	content.WriteString("a0: &a0 {k: 1}\n")
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&content, "a%d: &a%d {<<: [*a%d, *a%d]}\n", i, i, i-1, i-1)
	}
	content.WriteString("\"${KEYS_SL_P}\": 1\n")

	processor := processedKeys(t, general.YAML, content.String())
	assert.Equal(t, []string{"***"}, processor.RedactKeys(0, 1, []string{"made-up-s3cret"}, []bool{false}))
	// The first mapping is walked before the spare is used up, the key within the last one is not reached
	assert.Equal(t, []string{"a0", "k"}, processor.RedactKeys(0, 1, []string{"a0", "k"}, []bool{false, false}))
	assert.Equal(t, []string{"a40", "***"}, processor.RedactKeys(0, 1, []string{"a40", "k"}, []bool{false, false}))
}

// TestRedactKeysAliasWithoutAnchor checks that a key that is an alias of no anchor and a merge of such an alias
// are skipped by the locator
func TestRedactKeysAliasWithoutAnchor(t *testing.T) {
	var document yaml.Node
	assert.NoError(t, yaml.Unmarshal([]byte("a: &a {k: 1}\nm: {*a : 1}\nn: {<<: *a}\n"), &document))
	// Dropping the anchor leaves the aliases without a target, as the written document would have it
	document.Content[0].Content[1].Anchor = ""

	handler := &YamlConfigFileHandler{documents: []*yaml.Node{&document}}
	locations, sure := handler.keyLocator(map[ConfigEntry]string{}).locateKeys(1)
	assert.True(t, sure)
	assert.Equal(t, keyLocations{
		locationKey(0, []string{"a"}):      "",
		locationKey(0, []string{"a", "k"}): "",
		locationKey(0, []string{"m"}):      "",
		locationKey(0, []string{"n"}):      "",
	}, locations)
}
