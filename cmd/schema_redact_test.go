package cmd

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/denglertai/gonfig/internal/file"
	"github.com/denglertai/gonfig/internal/filter"
)

// leafSchema accepts objects, arrays and integers at every level, so every string leaf is a violation at its path
const leafSchema = `{"$id": "leaf", "type": ["object", "array", "integer"], "additionalProperties": {"$ref": "#"}, "items": {"$ref": "#"}}`

// TestSchemaViolationPathsHideKeyEdgeCases checks which keys the paths of violations mask for edge cases of keys, in
// process and check: nested placeholder keys, filters, defaults, escapes, arrays, quoted and dotted TOML keys, flow
// mappings and non-string keys. Keys are masked by their location, so a static key that equals the result of a
// placeholder elsewhere stays visible, since masking it would reveal that the two are equal.
func TestSchemaViolationPathsHideKeyEdgeCases(t *testing.T) {
	const secretA, secretB = "made-up-key-a-s3cret", "made-up-key-b-s3cret"
	t.Setenv("SCHEMA_EDGE_A", secretA)
	t.Setenv("SCHEMA_EDGE_B", secretB)
	t.Setenv("SCHEMA_EDGE_NUM", "42")
	t.Cleanup(func() {
		file.SetLogValues(false)
		filter.SetValuesInErrors(false)
	})

	testCases := []struct {
		name    string
		file    string
		content string
		want    []string // texts that must be reported
		hidden  []string // texts that must not be in the output
	}{
		{name: "two keys", file: "c.yaml", content: "${SCHEMA_EDGE_A}: x\n${SCHEMA_EDGE_B}: x\n",
			want: []string{"***: got string"}, hidden: []string{secretA, secretB}},
		{name: "nested keys", file: "c.yaml", content: "${SCHEMA_EDGE_A}:\n  pre_${SCHEMA_EDGE_B}:\n    static: x\n",
			want: []string{"***.pre_***.static: got string"}, hidden: []string{secretA, secretB}},
		{name: "composed key", file: "c.yaml", content: "${SCHEMA_EDGE_A}.${SCHEMA_EDGE_B}: x\n",
			want: []string{"***.***: got string"}, hidden: []string{secretA, secretB}},
		{name: "filter and default", file: "c.yaml", content: "\"${SCHEMA_EDGE_A | upper}\": x\n\"${SCHEMA_EDGE_MISSING | default(dflt-key)}\": x\n",
			want: []string{"***: got string"}, hidden: []string{secretA, strings.ToUpper(secretA)}},
		{name: "escape stays visible", file: "c.yaml", content: "$${SCHEMA_EDGE_A}: x\n",
			want: []string{"${SCHEMA_EDGE_A}: got string"}},
		{name: "escape and placeholder", file: "c.yaml", content: "\"$${LIT}_${SCHEMA_EDGE_A}\": x\n",
			want: []string{"${LIT}_***: got string"}, hidden: []string{secretA}},
		{name: "array below key", file: "c.yaml", content: "${SCHEMA_EDGE_A}:\n  - x\n  - [y]\n",
			want: []string{"***.0: got string", "***.1.0: got string"}, hidden: []string{secretA}},
		{name: "flow mapping", file: "c.yaml", content: "{\"${SCHEMA_EDGE_A}\": {\"pre_${SCHEMA_EDGE_B}\": x}}\n",
			want: []string{"***.pre_***: got string"}, hidden: []string{secretA, secretB}},
		{name: "complex key", file: "c.yaml", content: "? ${SCHEMA_EDGE_A}\n: x\n",
			want: []string{"***: got string"}, hidden: []string{secretA}},
		{name: "non string key", file: "c.yaml", content: "${SCHEMA_EDGE_NUM | to_int}: x\n",
			want: []string{"***: got string"}, hidden: []string{"42"}},
		{name: "static key equal to a processed key", file: "c.yaml", content: "${SCHEMA_EDGE_NUM}: x\nother:\n  \"42\": x\n",
			want: []string{"***: got string", "other.42: got string"}},
		{name: "json keys", file: "c.json", content: `{"${SCHEMA_EDGE_A}": {"pre_${SCHEMA_EDGE_B}": ["x"]}, "$${X}": "x"}`,
			want:   []string{"***.pre_***.0: got string", "${X}: got string"},
			hidden: []string{secretA, secretB}},
		{name: "toml dotted and quoted keys", file: "c.toml",
			content: "[\"${SCHEMA_EDGE_A}\"]\nstatic.\"pre_${SCHEMA_EDGE_B}\".k = \"x\"\n",
			want:    []string{"***.static.pre_***.k: got string"}, hidden: []string{secretA, secretB}},
		{name: "toml inline table", file: "c.toml", content: "t = { \"${SCHEMA_EDGE_A}\" = \"x\", n = { \"${SCHEMA_EDGE_B}\" = \"x\" } }\n",
			want: []string{"t.***: got string", "t.n.***: got string"}, hidden: []string{secretA, secretB}},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			t.Setenv("GONFIG_LOG_VALUES", "")
			writeSchemaFiles(t, map[string]string{tC.file: tC.content, "leaf.json": leafSchema})
			for _, args := range [][]string{
				{"config", "process", "-f", tC.file},
				{"config", "check", "-f", tC.file},
				{"config", "check", "-o", "json", "-f", tC.file},
			} {
				args = append(args, "--schema", "leaf.json")
				output, err := runSchemaCommand(t, args...)
				if !assert.Error(t, err, args) {
					continue
				}
				all := output + err.Error()
				for _, want := range tC.want {
					// The JSON report has the path and the message in separate fields
					path, _, _ := strings.Cut(want, ": got")
					assert.True(t, strings.Contains(all, want) || strings.Contains(all, `"path": "`+path+`"`), "%s in %v", want, args)
				}
				for _, text := range tC.hidden {
					assert.NotContains(t, all, text, args)
				}
			}
		})
	}
}

// TestSchemaViolationKeysNotLeakedBetweenFiles checks that the keys produced in one file don't mask the same static
// text in another file of the same run, since every file has its own processor
func TestSchemaViolationKeysNotLeakedBetweenFiles(t *testing.T) {
	t.Setenv("SCHEMA_EDGE_A", "shared-key")
	t.Setenv("GONFIG_LOG_VALUES", "")
	t.Cleanup(func() {
		file.SetLogValues(false)
		filter.SetValuesInErrors(false)
	})
	writeSchemaFiles(t, map[string]string{
		"a.yaml":    "${SCHEMA_EDGE_A}: x\n",
		"b.yaml":    "shared-key: x\n",
		"leaf.json": leafSchema,
	})
	// process stops at the first file not matching the schema, so only check reports both
	output, err := runSchemaCommand(t, "config", "check", "-f", "*.yaml", "--schema", "leaf.json")
	assert.Error(t, err)
	assert.Contains(t, output, "schema: ***: got string")
	assert.Contains(t, output, "schema: shared-key: got string")
}

// TestSchemaViolationsHideKeysOfAliasesTagsAndInvalidUTF8 checks that keys produced by placeholders are masked in
// process and check, in text and JSON reports, when they are YAML aliases of processed values, have YAML tags or
// contain invalid UTF-8, and that static keys and indexes equal to a processed key stay visible, unless --log-values
// is set
func TestSchemaViolationsHideKeysOfAliasesTagsAndInvalidUTF8(t *testing.T) {
	const closed = `{"properties": {"a": {}}, "additionalProperties": false}`
	testCases := []struct {
		name    string
		env     map[string]string
		file    string
		content string
		schema  string
		want    []string // texts that must be reported, the path and message are separate fields in the JSON report
		hidden  []string // texts that must not be in the output without --log-values
	}{
		{name: "alias as key", env: map[string]string{"SCHEMA_KEY_PW": "made-up-alias-s3cret"}, file: "c.yaml",
			content: "a: &v ${SCHEMA_KEY_PW}\n*v : 1\n", schema: closed,
			want: []string{"additional properties '***' not allowed"}, hidden: []string{"made-up-alias-s3cret"}},
		{name: "alias in path", env: map[string]string{"SCHEMA_KEY_PW": "made-up-alias-s3cret"}, file: "c.yaml",
			content: "a: &v ${SCHEMA_KEY_PW}\nb:\n  *v : {c: x}\n", schema: leafSchema,
			want: []string{"b.***.c"}, hidden: []string{"made-up-alias-s3cret"}},
		{name: "tagged keys", env: map[string]string{"SCHEMA_KEY_N": "1e3", "SCHEMA_KEY_TM": "2001-12-14", "SCHEMA_KEY_PIN": "1234"},
			file: "c.yaml", content: "!!float ${SCHEMA_KEY_N}: 1\n!!timestamp ${SCHEMA_KEY_TM}: 2\n!!int ${SCHEMA_KEY_PIN}: 3\n",
			schema: closed, want: []string{"additional properties '***', '***', '***' not allowed"},
			hidden: []string{"1000", "2001", "1234"}},
		{name: "invalid UTF-8 in JSON", env: map[string]string{"SCHEMA_KEY_PW": "made-up-\xffs3cret"}, file: "c.json",
			content: `{"${SCHEMA_KEY_PW}": 1}`, schema: closed,
			want: []string{"additional properties '***' not allowed"}, hidden: []string{"s3cret"}},
		{name: "invalid UTF-8 in TOML", env: map[string]string{"SCHEMA_KEY_PW": "made-up-\xffs3cret"}, file: "c.toml",
			content: "\"${SCHEMA_KEY_PW}\" = 1\n", schema: closed,
			want: []string{"additional properties '***' not allowed"}, hidden: []string{"s3cret"}},
		{name: "static key equal to a processed key", env: map[string]string{"SCHEMA_KEY_PW": "admin"}, file: "c.yaml",
			content: "${SCHEMA_KEY_PW}: 1\nusers: {admin: {x: s}}\n", schema: leafSchema,
			want: []string{"users.admin.x"}},
		{name: "dependency", env: map[string]string{"SCHEMA_KEY_PW": "admin"}, file: "c.yaml",
			content: "${SCHEMA_KEY_PW}: 1\n", schema: `{"dependentRequired": {"admin": ["pw"]}}`,
			want: []string{"properties 'pw' required, if '***' exists"}, hidden: []string{"'admin'"}},
		{name: "index equal to a processed key", env: map[string]string{"SCHEMA_KEY_PW": "0"}, file: "c.yaml",
			content: "${SCHEMA_KEY_PW}: 1\nlist: [{x: s}]\n", schema: leafSchema,
			want: []string{"list.0.x"}},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			t.Setenv("GONFIG_LOG_VALUES", "")
			for name, value := range tC.env {
				t.Setenv(name, value)
			}
			t.Cleanup(func() {
				file.SetLogValues(false)
				filter.SetValuesInErrors(false)
			})
			writeSchemaFiles(t, map[string]string{tC.file: tC.content, "schema.json": tC.schema})
			for _, args := range [][]string{
				{"config", "process", "-f", tC.file},
				{"config", "check", "-f", tC.file},
				{"config", "check", "-o", "json", "-f", tC.file},
			} {
				args = append(args, "--schema", "schema.json")
				output, err := runSchemaCommand(t, args...)
				if !assert.Error(t, err, args) {
					continue
				}
				all := output + err.Error()
				for _, want := range tC.want {
					assert.Contains(t, all, want, args)
				}
				for _, text := range tC.hidden {
					assert.NotContains(t, all, text, args)
				}

				output, err = runSchemaCommand(t, append(args, "--log-values")...)
				assert.Error(t, err, args)
				for _, text := range tC.hidden {
					assert.Contains(t, output+err.Error(), text, args)
				}
			}
		})
	}
}

// TestSchemaViolationsHideKeysAfterEmptyDocuments checks that keys of YAML files with empty documents are masked in
// process and check, in text and JSON reports, since the written content has no document for a first empty document
func TestSchemaViolationsHideKeysAfterEmptyDocuments(t *testing.T) {
	const secret = "made-up-doc-s3cret"
	t.Setenv("SCHEMA_DOC_PW", secret)
	t.Setenv("GONFIG_LOG_VALUES", "")
	t.Cleanup(func() {
		file.SetLogValues(false)
		filter.SetValuesInErrors(false)
	})

	for _, content := range []string{
		"---\n---\n${SCHEMA_DOC_PW}: 1\n",
		"---\n# only comment\n---\n${SCHEMA_DOC_PW}: 1\n",
		"---\n...\n---\n${SCHEMA_DOC_PW}: 1\n",
		"--- \n--- \n--- {\"${SCHEMA_DOC_PW}\": 1}\n",
		"---\n---\n---\n${SCHEMA_DOC_PW}: 1\n---\n${SCHEMA_DOC_PW}x: 1\n",
		"---\n~\n---\n${SCHEMA_DOC_PW}: 1\n",
	} {
		writeSchemaFiles(t, map[string]string{"c.yaml": content, "schema.json": `{"additionalProperties": false}`})
		for _, args := range [][]string{
			{"config", "process", "-f", "c.yaml"},
			{"config", "check", "-f", "c.yaml"},
			{"config", "check", "-o", "json", "-f", "c.yaml"},
		} {
			args = append(args, "--schema", "schema.json")
			output, err := runSchemaCommand(t, args...)
			if !assert.Error(t, err, args) {
				continue
			}
			all := output + err.Error()
			assert.Contains(t, all, "additional properties '***' not allowed", content, args)
			assert.NotContains(t, all, secret, content, args)
		}
	}
}

// TestProcessAliasExpansionWithoutSchema checks that processing a YAML file whose aliases expand exponentially and
// that has keys with placeholders takes no time without a schema, which locating the keys would take
func TestProcessAliasExpansionWithoutSchema(t *testing.T) {
	t.Setenv("SCHEMA_DOC_PW", "made-up-doc-s3cret")
	t.Setenv("GONFIG_LOG_VALUES", "")
	content := "p: &p \"${SCHEMA_DOC_PW}\"\n\"${SCHEMA_DOC_PW}\": 1\na0: &a0 {\"${SCHEMA_DOC_PW}\": [x,x,x,x,x,x,x,x,x,x]}\n"
	for i := 1; i <= 7; i++ {
		item := "*a" + strconv.Itoa(i-1)
		content += "a" + strconv.Itoa(i) + ": &a" + strconv.Itoa(i) + " [" + strings.TrimSuffix(strings.Repeat(item+",", 10), ",") + "]\n"
	}
	writeSchemaFiles(t, map[string]string{"c.yaml": content, "schema.json": `{}`})

	output, err := runSchemaCommand(t, "config", "process", "-f", "c.yaml")
	assert.NoError(t, err)
	assert.Contains(t, output, "a7: &a7 [*a6")

	// The validator rejects the file, since it expands too much, so the keys are never located
	_, err = runSchemaCommand(t, "config", "process", "-f", "c.yaml", "--schema", "schema.json")
	assert.ErrorContains(t, err, "invalid YAML")
}
