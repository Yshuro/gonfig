package file

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/denglertai/gonfig/internal/general"
	"github.com/stretchr/testify/assert"
	"go.yaml.in/yaml/v4"
)

// processedKeys processes the content as a file of the type and returns the processor, whose RedactKeys is checked
func processedKeys(t *testing.T, fileType general.FileType, content string) *FileProcessor {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config."+string(fileType))
	assert.NoError(t, os.WriteFile(file, []byte(content), 0o644))
	processor := NewFileProcessor(file, general.Undefined, &bytes.Buffer{})
	processor.Validated = true
	if !assert.NoError(t, processor.Process()) {
		t.FailNow()
	}
	return processor
}

// TestRedactKeysLocations checks the locations of keys in the written documents of every file type: merge keys, nested
// aliases, documents, keys with separators and quotes, array tables, dotted keys, inline tables and deep nesting
func TestRedactKeysLocations(t *testing.T) {
	t.Setenv("KEYS_LOC_P", "made-up-p")
	t.Setenv("KEYS_LOC_Q", "made-up-q")
	t.Setenv("KEYS_LOC_DOT", `a.b"c`)

	deepYaml, deepJson, deepClose := "", "", ""
	for i := 0; i < 40; i++ {
		deepYaml += strings.Repeat("  ", i) + "n:\n"
		deepJson += `{"n":`
		deepClose += "}"
	}
	deepLocation := slices.Concat(strings.Split(strings.Repeat("n,", 40), ",")[:40], []string{"made-up-p"})
	deepMasked := slices.Concat(strings.Split(strings.Repeat("n,", 40), ",")[:40], []string{"***"})

	testCases := []struct {
		desc     string
		fileType general.FileType
		content  string
		document int
		location []string
		want     []string
	}{
		{"yaml merge of a merge", general.YAML,
			"b1: &b1 {\"${KEYS_LOC_P}\": 1}\nb2: &b2\n  <<: *b1\n  pre_${KEYS_LOC_Q}: 2\no:\n  <<: [*b2]\n",
			0, []string{"o", "made-up-p"}, []string{"o", "***"}},
		{"yaml merge of a merge, own key", general.YAML,
			"b1: &b1 {\"${KEYS_LOC_P}\": 1}\nb2: &b2\n  <<: *b1\n  pre_${KEYS_LOC_Q}: 2\no:\n  <<: [*b2]\n",
			0, []string{"o", "pre_made-up-q"}, []string{"o", "pre_***"}},
		{"yaml merge in a list of merges, static key", general.YAML,
			"b1: &b1 {\"${KEYS_LOC_P}\": 1}\nb2: &b2 {made-up-p: 2}\no:\n  <<: [*b1, *b2]\n",
			0, []string{"b2", "made-up-p"}, []string{"b2", "made-up-p"}},
		{"yaml nested alias value", general.YAML,
			"a: &a {\"${KEYS_LOC_P}\": 1}\nb: {c: *a}\nd: [*a]\n",
			0, []string{"d", "0", "made-up-p"}, []string{"d", "0", "***"}},
		{"yaml second document", general.YAML, "x: 1\n---\n\"${KEYS_LOC_P}\": 1\n",
			1, []string{"made-up-p"}, []string{"***"}},
		{"yaml other document stays visible", general.YAML, "made-up-p: 1\n---\n\"${KEYS_LOC_P}\": 1\n",
			0, []string{"made-up-p"}, []string{"made-up-p"}},
		{"yaml key with a separator and quote", general.YAML, "\"${KEYS_LOC_DOT}\": {x: 1}\n",
			0, []string{`a.b"c`, "x"}, []string{"***", "x"}},
		{"yaml static keys do not run into a processed key", general.YAML, "\"${KEYS_LOC_DOT}\": 1\na: {b\"c: 1}\n",
			0, []string{"a", `b"c`}, []string{"a", `b"c`}},
		{"yaml deep nesting", general.YAML, deepYaml + strings.Repeat("  ", 40) + "\"${KEYS_LOC_P}\": 1\n",
			0, deepLocation, deepMasked},
		{"yaml alias cycle", general.YAML, "a: &a\n  \"${KEYS_LOC_P}\": 1\n  b: *a\n",
			0, []string{"a", "made-up-p"}, []string{"a", "***"}},
		{"yaml sequence alias cycle", general.YAML, "a: &a [*a, 1]\n\"${KEYS_LOC_P}\": 1\n",
			0, []string{"made-up-p"}, []string{"***"}},
		{"yaml merge cycle", general.YAML, "a: &a\n  <<: *a\n  \"${KEYS_LOC_P}\": 1\n",
			0, []string{"a", "made-up-p"}, []string{"a", "***"}},
		{"yaml location beyond the document", general.YAML, "\"${KEYS_LOC_P}\": 1\n",
			0, []string{"made-up-p", "x", "y"}, []string{"***", "***", "***"}},
		{"yaml no location", general.YAML, "\"${KEYS_LOC_P}\": 1\n", 0, nil, nil},

		{"json array of objects", general.JSON, `{"l": [{"${KEYS_LOC_P}": 1}, {"${KEYS_LOC_P}": 2, "made-up-p": 3, "s": 4}]}`,
			0, []string{"l", "1", "made-up-p"}, []string{"l", "1", "***"}},
		{"json array of objects, first", general.JSON, `{"l": [{"${KEYS_LOC_P}": 1}, {"s": 2}]}`,
			0, []string{"l", "0", "made-up-p"}, []string{"l", "0", "***"}},
		{"json array index equal to a key", general.JSON, `{"${KEYS_LOC_P}": [1]}`,
			0, []string{"made-up-p", "0"}, []string{"***", "0"}},
		{"json key with a separator", general.JSON, `{"${KEYS_LOC_DOT}": {"x": 1}}`,
			0, []string{`a.b"c`, "x"}, []string{"***", "x"}},
		{"json deep nesting", general.JSON, deepJson + `{"${KEYS_LOC_P}": 1}` + deepClose,
			0, deepLocation, deepMasked},

		{"toml array tables", general.TOML, "[[\"${KEYS_LOC_P}\"]]\nx = 1\n[[\"${KEYS_LOC_P}\"]]\n\"${KEYS_LOC_Q}\" = 2\n",
			0, []string{"made-up-p", "1", "made-up-q"}, []string{"***", "1", "***"}},
		{"toml array tables, first", general.TOML, "[[\"${KEYS_LOC_P}\"]]\nx = 1\n[[\"${KEYS_LOC_P}\"]]\nx = 2\n",
			0, []string{"made-up-p", "0", "x"}, []string{"***", "0", "x"}},
		{"toml dotted header", general.TOML, "[a.\"${KEYS_LOC_P}\".b]\nx = 1\n",
			0, []string{"a", "made-up-p", "b", "x"}, []string{"a", "***", "b", "x"}},
		{"toml dotted key", general.TOML, "a.\"${KEYS_LOC_P}\".c = 1\na.\"pre_${KEYS_LOC_Q}\" = 2\n",
			0, []string{"a", "pre_made-up-q"}, []string{"a", "pre_***"}},
		{"toml dotted key, static part", general.TOML, "a.\"${KEYS_LOC_P}\".c = 1\n",
			0, []string{"a", "made-up-p", "c"}, []string{"a", "***", "c"}},
		{"toml inline table", general.TOML, "t = { \"${KEYS_LOC_P}\" = { \"${KEYS_LOC_Q}\" = 1 } }\n",
			0, []string{"t", "made-up-p", "made-up-q"}, []string{"t", "***", "***"}},
		{"toml array of inline tables", general.TOML, "l = [1, { \"${KEYS_LOC_P}\" = 1 }]\n",
			0, []string{"l", "1", "made-up-p"}, []string{"l", "1", "***"}},
		{"toml key with a separator and quote", general.TOML, "\"${KEYS_LOC_DOT}\" = { x = 1 }\n",
			0, []string{`a.b"c`, "x"}, []string{"***", "x"}},
		{"toml static table with the same name", general.TOML, "\"${KEYS_LOC_P}\" = 1\n[made-up-q]\nmade-up-p = 1\n",
			0, []string{"made-up-q", "made-up-p"}, []string{"made-up-q", "made-up-p"}},
		{"toml table after an array of tables", general.TOML, "[[a]]\nx = 1\n[a.t]\n\"${KEYS_LOC_P}\" = 1\n",
			0, []string{"a", "0", "t", "made-up-p"}, []string{"a", "0", "t", "***"}},
		{"toml bom and crlf", general.TOML, "\ufeff[t]\r\n\"${KEYS_LOC_P}\" = 1\r\n",
			0, []string{"t", "made-up-p"}, []string{"t", "***"}},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			processor := processedKeys(t, tC.fileType, tC.content)
			assert.Equal(t, tC.want, processor.RedactKeys(tC.document, strings.Count(tC.content, "\n---\n")+1, tC.location, digitIndexes(tC.location)))
		})
	}
}

// TestRedactKeysDuplicateKeys checks that two keys that are equal after processing are both masked at their location
func TestRedactKeysDuplicateKeys(t *testing.T) {
	t.Setenv("KEYS_LOC_P", "made-up-p")
	t.Setenv("KEYS_LOC_Q", "made-up-p")

	processor := processedKeys(t, general.JSON, `{"${KEYS_LOC_P}": 1, "${KEYS_LOC_Q}": 2, "static": 3}`)
	assert.Equal(t, []string{"***"}, processor.RedactKeys(0, 1, []string{"made-up-p"}, digitIndexes([]string{"made-up-p"})))
	assert.Equal(t, []string{"static"}, processor.RedactKeys(0, 1, []string{"static"}, digitIndexes([]string{"static"})))

	processor = processedKeys(t, general.JSON, `{"${KEYS_LOC_P}": 1, "made-up-p": 2}`)
	assert.Equal(t, []string{"***"}, processor.RedactKeys(0, 1, []string{"made-up-p"}, digitIndexes([]string{"made-up-p"})),
		"a static key equal to a processed one is indistinguishable in the written document")
}

// TestRedactKeysAliasCycleGuard checks that walking a document with cyclic aliases terminates and still locates the
// candidate within the cycle, which the YAML parser accepts although the validator cannot decode such a document. The
// candidate makes the walk enter the cycle through aliases and merges, where the guard stops it, so the result is sure
// and not the fail-safe of the exhausted budget.
func TestRedactKeysAliasCycleGuard(t *testing.T) {
	var document yaml.Node
	assert.NoError(t, yaml.Unmarshal([]byte("a: &a\n  k: 1\n  b: *a\n  c: [*a]\n  <<: *a\n  <<: [*a]\n"), &document))

	anchored := document.Content[0].Content[1]
	candidate := anchored.Content[0]
	assert.Equal(t, "k", candidate.Value)

	handler := &YamlConfigFileHandler{documents: []*yaml.Node{&document}}
	locations, sure := handler.keyLocator(map[ConfigEntry]string{&YamlConfigEntry{node: candidate}: "***"}).locateKeys(1)
	assert.True(t, sure)
	assert.Equal(t, keyLocations{
		locationKey(0, []string{"a", "k"}):      "***",
		locationKey(0, []string{"a", "b", "k"}): "***",
		// The sequence is computed while a is, so it must not be skipped as without candidates
		locationKey(0, []string{"a", "c", "0", "k"}): "***",
		// The static keys are located as well, up to where the guard stops the walk
		locationKey(0, []string{"a"}):                "",
		locationKey(0, []string{"a", "b"}):           "",
		locationKey(0, []string{"a", "c"}):           "",
		locationKey(0, []string{"a", "b", "b"}):      "",
		locationKey(0, []string{"a", "b", "c"}):      "",
		locationKey(0, []string{"a", "c", "0", "b"}): "",
		locationKey(0, []string{"a", "c", "0", "c"}): "",
	}, locations)
}

// TestRedactKeysProcessAgain checks that the keys of an earlier Process are forgotten
func TestRedactKeysProcessAgain(t *testing.T) {
	t.Setenv("KEYS_LOC_P", "made-up-p")
	file := filepath.Join(t.TempDir(), "config.json")
	assert.NoError(t, os.WriteFile(file, []byte(`{"${KEYS_LOC_P}": 1}`), 0o644))
	processor := NewFileProcessor(file, general.Undefined, &bytes.Buffer{})
	processor.Validated = true
	assert.NoError(t, processor.Process())
	assert.Equal(t, []string{"***"}, processor.RedactKeys(0, 1, []string{"made-up-p"}, digitIndexes([]string{"made-up-p"})))

	assert.NoError(t, os.WriteFile(file, []byte(`{"made-up-p": 1}`), 0o644))
	assert.NoError(t, processor.Process())
	assert.Equal(t, []string{"made-up-p"}, processor.RedactKeys(0, 1, []string{"made-up-p"}, digitIndexes([]string{"made-up-p"})))
}

// digitIndexes returns which parts of the location are array indexes for RedactKeys, assuming that every number is one,
// which holds for the locations of these tests
func digitIndexes(location []string) []bool {
	indexes := make([]bool, len(location))
	for i, part := range location {
		_, err := strconv.Atoi(part)
		indexes[i] = err == nil
	}
	return indexes
}
