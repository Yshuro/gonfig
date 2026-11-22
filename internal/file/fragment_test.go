package file

import (
	"bytes"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/denglertai/gonfig/internal/filter"
	"github.com/denglertai/gonfig/internal/general"
	"github.com/stretchr/testify/assert"
)

// processFragment processes the content as a file of the given type with the environment variable FRAGMENT set to the fragment
func processFragment(t *testing.T, fileType general.FileType, content string, fragment string) (string, error) {
	t.Helper()
	t.Setenv("FRAGMENT", fragment)
	t.Setenv("FRAGMENT_SECRET", "s3cret")
	t.Setenv("FRAGMENT_PORT", "8080")

	file := path.Join(t.TempDir(), "config."+string(fileType))
	assert.NoError(t, os.WriteFile(file, []byte(content), 0o644))

	var output bytes.Buffer
	err := NewFileProcessor(file, fileType, &output).Process()
	return output.String(), err
}

// TestFragments checks inserting and merging fragments for each file type, including lists, empty fragments and errors
func TestFragments(t *testing.T) {
	testCases := []struct {
		desc     string
		fileType general.FileType
		content  string
		fragment string
		want     string
		wantErr  string
	}{
		// YAML values
		{
			desc:     "yaml value replaced by a JSON fragment in block style",
			fileType: general.YAML,
			content:  "logging:\n  level: info\n  appenders: ${FRAGMENT | fragment} # appenders\n",
			fragment: `[{"type": "console"}, {"type": "file", "path": "/var/log/app.log", "port": "123"}]`,
			want:     "logging:\n  level: info\n  appenders: # appenders\n    - type: console\n    - type: file\n      path: /var/log/app.log\n      port: \"123\"\n",
		},
		{
			desc:     "yaml value replaced by an empty sequence keeps its line comment",
			fileType: general.YAML,
			content:  "appenders: ${FRAGMENT | fragment} # appenders\nlevel: info\n",
			fragment: "[]",
			want:     "appenders: [] # appenders\nlevel: info\n",
		},
		{
			desc:     "yaml value replaced by an empty mapping keeps its line comment",
			fileType: general.YAML,
			content:  "pool: ${FRAGMENT | fragment} # pool\nlevel: info\n",
			fragment: "{}",
			want:     "pool: {} # pool\nlevel: info\n",
		},
		{
			desc:     "yaml sequence item replaced by an empty sequence keeps its line comment",
			fileType: general.YAML,
			content:  "- ${FRAGMENT | fragment} # item\n- b\n",
			fragment: "[]",
			want:     "- [] # item\n- b\n",
		},
		{
			desc:     "yaml value replaced by a YAML fragment keeping literal scalars",
			fileType: general.YAML,
			content:  "script: ${FRAGMENT | fragment}\n",
			fragment: "run: |\n  echo a\n  echo b\n",
			want:     "script:\n  run: |\n    echo a\n    echo b\n",
		},
		{
			desc:     "yaml value replaced by a scalar fragment",
			fileType: general.YAML,
			content:  "key: ${FRAGMENT | fragment}\n",
			fragment: "42",
			want:     "key: 42\n",
		},
		{
			desc:     "yaml value replaced by null for an empty fragment",
			fileType: general.YAML,
			content:  "key: ${FRAGMENT | fragment}\n",
			fragment: "",
			want:     "key:\n",
		},
		{
			desc:     "placeholders within yaml fragments",
			fileType: general.YAML,
			content:  "db: ${FRAGMENT | fragment}\n",
			fragment: "password: ${FRAGMENT_SECRET | base64}\nport: ${FRAGMENT_PORT | to_int}\n${FRAGMENT_PORT}: key\n",
			want:     "db:\n  password: czNjcmV0\n  port: 8080\n  \"8080\": key\n",
		},
		{
			desc:     "yaml list from split",
			fileType: general.YAML,
			content:  "a: \"val\"\nb: \"${FRAGMENT | split(,) | fragment}\"\n",
			fragment: "alpha,beta,gamma",
			want:     "a: \"val\"\nb:\n  - alpha\n  - beta\n  - gamma\n",
		},
		{
			desc:     "yaml empty list from split",
			fileType: general.YAML,
			content:  "b: ${FRAGMENT | split | fragment}\n",
			fragment: "",
			want:     "b: []\n",
		},
		{
			desc:     "yaml list elements are strings",
			fileType: general.YAML,
			content:  "b: ${FRAGMENT | split | trim | fragment}\n",
			fragment: "1, true ,null",
			want:     "b:\n  - \"1\"\n  - \"true\"\n  - \"null\"\n",
		},
		// YAML keys
		{
			desc:     "yaml key deep merged into the mapping",
			fileType: general.YAML,
			content:  "database:\n  host: localhost # dev\n  port: 5432\n  pool:\n    timeout: 30\n  hosts: [a, b]\n  ${FRAGMENT | fragment}:\nother: x\n",
			fragment: "host: db.prod\npool:\n  size: 20\nhosts: [c]\nssl: true\n",
			want:     "database:\n  host: db.prod # dev\n  port: 5432\n  pool:\n    timeout: 30\n    size: 20\n  hosts:\n    - c\n  ssl: true\nother: x\n",
		},
		{
			desc:     "yaml key merged into the first of duplicate keys",
			fileType: general.YAML,
			content:  "a:\n  x: 1\na:\n  y: 2\n${FRAGMENT | fragment}:\n",
			fragment: "a:\n  z: 3\nb: 1\nb: 2\n",
			want:     "a:\n  x: 1\n  z: 3\na:\n  y: 2\nb: 2\n",
		},
		{
			desc:     "yaml key removed for an empty fragment",
			fileType: general.YAML,
			content:  "database:\n  host: localhost\n  ${FRAGMENT | fragment}:\n",
			fragment: "",
			want:     "database:\n  host: localhost\n",
		},
		{
			desc:     "yaml key replacing the value of a later placeholder key",
			fileType: general.YAML,
			content:  "${FRAGMENT | fragment}:\n${FRAGMENT_SECRET}: 1\n",
			fragment: `{"${FRAGMENT_SECRET}": {"y": 2}}`,
			want:     "s3cret:\n  y: 2\n",
		},
		{
			desc:     "yaml key replacing the value of a nested placeholder key",
			fileType: general.YAML,
			content:  "${FRAGMENT | fragment}:\no:\n  ${FRAGMENT_SECRET}: 1\n",
			fragment: `{"o": {"${FRAGMENT_SECRET}": 2}}`,
			want:     "o:\n  s3cret: 2\n",
		},
		{
			desc:     "yaml key replacing a later key fragment by a value",
			fileType: general.YAML,
			content:  "${FRAGMENT | fragment}:\n${FRAGMENT_PORT | fragment}:\n",
			fragment: `{"${FRAGMENT_PORT | fragment}": 1}`,
			wantErr:  "a key with a fragment must not have a value",
		},
		{
			desc:     "yaml key with a value",
			fileType: general.YAML,
			content:  "${FRAGMENT | fragment}: value\n",
			fragment: "a: 1",
			wantErr:  "a key with a fragment must not have a value",
		},
		{
			desc:     "yaml key with a fragment other than a mapping",
			fileType: general.YAML,
			content:  "${FRAGMENT | fragment}:\n",
			fragment: "[1, 2]",
			wantErr:  "a fragment used as a key has to be a mapping",
		},
		{
			desc:     "invalid yaml fragment",
			fileType: general.YAML,
			content:  "key: ${FRAGMENT | fragment}\n",
			fragment: "a: [1",
			wantErr:  "invalid YAML fragment",
		},
		{
			desc:     "nested fragments",
			fileType: general.YAML,
			content:  "key: ${FRAGMENT | fragment}\n",
			fragment: "inner: ${FRAGMENT | fragment}",
			wantErr:  "fragments cannot contain further fragments",
		},
		{
			desc:     "embedded fragment",
			fileType: general.YAML,
			content:  "key: x ${FRAGMENT | fragment}\n",
			fragment: "a: 1",
			wantErr:  "has to make up the whole value",
		},
		// JSON values
		{
			desc:     "json value replaced by a fragment",
			fileType: general.JSON,
			content:  `{"appenders": "${FRAGMENT | fragment}", "level": "info"}`,
			fragment: `[{"type": "console", "port": "${FRAGMENT_PORT | to_int}"}]`,
			want:     "{\n  \"appenders\": [\n    {\n      \"type\": \"console\",\n      \"port\": 8080\n    }\n  ],\n  \"level\": \"info\"\n}\n",
		},
		{
			desc:     "json value replaced by null for an empty fragment",
			fileType: general.JSON,
			content:  `{"key": "${FRAGMENT | fragment}"}`,
			fragment: " ",
			want:     "{\n  \"key\": null\n}\n",
		},
		{
			desc:     "invalid json fragment",
			fileType: general.JSON,
			content:  `{"key": "${FRAGMENT | fragment}"}`,
			fragment: `[1,`,
			wantErr:  "invalid JSON fragment",
		},
		{
			desc:     "json fragment with trailing data",
			fileType: general.JSON,
			content:  `{"key": "${FRAGMENT | fragment}"}`,
			fragment: `1 2`,
			wantErr:  "unexpected data after the top-level JSON value",
		},
		{
			desc:     "json list from split",
			fileType: general.JSON,
			content:  `{"b": "${FRAGMENT | split | fragment}"}`,
			fragment: `alpha,"quoted"`,
			want:     "{\n  \"b\": [\n    \"alpha\",\n    \"\\\"quoted\\\"\"\n  ]\n}\n",
		},
		// JSON keys
		{
			desc:     "json key deep merged into the object",
			fileType: general.JSON,
			content:  `{"database": {"host": "localhost", "pool": {"timeout": 30}, "${FRAGMENT | fragment}": null, "port": 5432}}`,
			fragment: `{"host": "db.prod", "pool": {"size": 20}, "${FRAGMENT_PORT}_key": "${FRAGMENT_SECRET}"}`,
			want:     "{\n  \"database\": {\n    \"host\": \"db.prod\",\n    \"pool\": {\n      \"timeout\": 30,\n      \"size\": 20\n    },\n    \"port\": 5432,\n    \"8080_key\": \"s3cret\"\n  }\n}\n",
		},
		{
			desc:     "json key merged into the first of duplicate keys",
			fileType: general.JSON,
			content:  `{"a": {"x": 1}, "a": {"y": 2}, "${FRAGMENT | fragment}": null}`,
			fragment: `{"a": {"z": 3}, "b": 1, "b": 2}`,
			want:     "{\n  \"a\": {\n    \"x\": 1,\n    \"z\": 3\n  },\n  \"a\": {\n    \"y\": 2\n  },\n  \"b\": 2\n}\n",
		},
		{
			desc:     "json key removed for an empty fragment",
			fileType: general.JSON,
			content:  `{"a": 1, "${FRAGMENT | fragment}": null}`,
			fragment: "",
			want:     "{\n  \"a\": 1\n}\n",
		},
		{
			desc:     "json key replacing the value of a later placeholder key",
			fileType: general.JSON,
			content:  `{"${FRAGMENT | fragment}": null, "${FRAGMENT_SECRET}": 1}`,
			fragment: `{"${FRAGMENT_SECRET}": {"y": 2}}`,
			want:     "{\n  \"s3cret\": {\n    \"y\": 2\n  }\n}\n",
		},
		{
			desc:     "json key replacing the scalar of a later placeholder key",
			fileType: general.JSON,
			content:  `{"${FRAGMENT | fragment}": null, "${FRAGMENT_SECRET}": 1}`,
			fragment: `{"${FRAGMENT_SECRET}": 2}`,
			want:     "{\n  \"s3cret\": 2\n}\n",
		},
		{
			desc:     "json key in a nested object replacing the value of a later placeholder key",
			fileType: general.JSON,
			content:  `{"o": {"${FRAGMENT | fragment}": null, "${FRAGMENT_SECRET}": 1}}`,
			fragment: `{"${FRAGMENT_SECRET}": 3}`,
			want:     "{\n  \"o\": {\n    \"s3cret\": 3\n  }\n}\n",
		},
		{
			desc:     "json key replacing the value of a nested placeholder key",
			fileType: general.JSON,
			content:  `{"${FRAGMENT | fragment}": null, "o": {"${FRAGMENT_SECRET}": 1}}`,
			fragment: `{"o": {"${FRAGMENT_SECRET}": 2}}`,
			want:     "{\n  \"o\": {\n    \"s3cret\": 2\n  }\n}\n",
		},
		{
			// Pins the current first-occurrence matching only; how duplicate keys should be handled is tracked separately
			desc:     "json key replacing the first of duplicate placeholder keys",
			fileType: general.JSON,
			content:  `{"${FRAGMENT | fragment}": null, "${FRAGMENT_SECRET}": 1, "${FRAGMENT_SECRET}": 5}`,
			fragment: `{"${FRAGMENT_SECRET}": 2}`,
			want:     "{\n  \"s3cret\": 2,\n  \"s3cret\": 5\n}\n",
		},
		{
			desc:     "json key replacing a later key fragment by a value",
			fileType: general.JSON,
			content:  `{"${FRAGMENT | fragment}": null, "${FRAGMENT_PORT | fragment}": null}`,
			fragment: `{"${FRAGMENT_PORT | fragment}": 1}`,
			wantErr:  "a key with a fragment must have the value null",
		},
		{
			desc:     "json key with a value",
			fileType: general.JSON,
			content:  `{"${FRAGMENT | fragment}": 1}`,
			fragment: `{}`,
			wantErr:  "a key with a fragment must have the value null",
		},
		{
			desc:     "json key with a fragment other than an object",
			fileType: general.JSON,
			content:  `{"${FRAGMENT | fragment}": null}`,
			fragment: `[1]`,
			wantErr:  "a fragment used as a key has to be an object",
		},
		{
			desc:     "json key with a placeholder",
			fileType: general.JSON,
			content:  `{"${FRAGMENT_PORT | to_int}": "${FRAGMENT_PORT | to_int}"}`,
			fragment: "",
			want:     "{\n  \"8080\": 8080\n}\n",
		},
		{
			desc:     "json key fragment replacing an edited scalar with an object",
			fileType: general.JSON,
			content:  `{"port": "${FRAGMENT_PORT}", "${FRAGMENT | fragment}": null}`,
			fragment: `{"port": {"y": 2}}`,
			want:     "{\n  \"port\": {\n    \"y\": 2\n  }\n}\n",
		},
		{
			desc:     "json key fragment replacing an edited scalar with a string",
			fileType: general.JSON,
			content:  `{"port": "${FRAGMENT_PORT}", "${FRAGMENT | fragment}": null}`,
			fragment: `{"port": "replacement"}`,
			want:     "{\n  \"port\": \"replacement\"\n}\n",
		},
		{
			desc:     "json key fragment replacing an object with a nested fragment key",
			fileType: general.JSON,
			content:  `{"${FRAGMENT | fragment}": null, "outer": {"${FRAGMENT | fragment}": null}}`,
			fragment: `{"outer": "replacement"}`,
			want:     "{\n  \"outer\": \"replacement\"\n}\n",
		},
		// XML
		{
			desc:     "xml element content replaced by a fragment",
			fileType: general.XML,
			content:  "<server><resources>${FRAGMENT | fragment}</resources></server>",
			fragment: `<?xml version="1.0"?><datasource url="${FRAGMENT_SECRET}&amp;x"/>text<password>${FRAGMENT_SECRET | upper}</password>`,
			want:     `<server><resources><datasource url="s3cret&amp;x"/>text<password>S3CRET</password></resources></server>`,
		},
		{
			desc:     "xml element content with child elements replaced in the order of the fragment",
			fileType: general.XML,
			content:  "<server><resources>${FRAGMENT | fragment}<a/>text<b/></resources><keep/></server>",
			fragment: "<x/>1<y>2</y><z/>3",
			want:     "<server><resources><x/>1<y>2</y><z/>3</resources><keep/></server>",
		},
		{
			desc:     "xml fragment embedded into text after a child element",
			fileType: general.XML,
			content:  "<server><resources><old/>text ${FRAGMENT | fragment}</resources></server>",
			fragment: "",
			wantErr:  "has to make up the whole value",
		},
		{
			desc:     "xml fragment in a later text node replaces the whole content",
			fileType: general.XML,
			content:  "<server><resources><old/>${FRAGMENT | fragment}<other/></resources><keep/></server>",
			fragment: "<new/>",
			want:     "<server><resources><new/></resources><keep/></server>",
		},
		{
			desc:     "xml element content removed for an empty fragment",
			fileType: general.XML,
			content:  "<server><resources>${FRAGMENT | fragment}</resources></server>",
			fragment: "",
			want:     "<server><resources/></server>",
		},
		{
			desc:     "invalid xml fragment",
			fileType: general.XML,
			content:  "<server><resources>${FRAGMENT | fragment}</resources></server>",
			fragment: "<a>",
			wantErr:  "invalid XML fragment",
		},
		{
			desc:     "xml list from split",
			fileType: general.XML,
			content:  "<r><b>${FRAGMENT | split | fragment}</b></r>",
			fragment: "alpha,beta",
			wantErr:  "lists can only be inserted into YAML, JSON and TOML files",
		},
		{
			desc:     "xml attribute",
			fileType: general.XML,
			content:  `<server><resources name="${FRAGMENT | fragment}"/></server>`,
			fragment: "<a/>",
			wantErr:  "fragments are not supported here",
		},
		// File types without structure
		{
			desc:     "properties",
			fileType: general.PROPERTIES,
			content:  "key=${FRAGMENT | fragment}\n",
			fragment: "a=1",
			wantErr:  "fragments are not supported here",
		},
		{
			desc:     "plain text",
			fileType: general.PLAIN,
			content:  "${FRAGMENT | fragment}\n",
			fragment: "a",
			wantErr:  "fragments are not supported here",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			output, err := processFragment(t, tC.fileType, tC.content, tC.fragment)
			if tC.wantErr != "" {
				assert.ErrorContains(t, err, tC.wantErr)
				assert.Empty(t, output)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tC.want, output)
		})
	}
}

// TestFragmentErrorsWithoutValues checks that errors of invalid fragments only name the format and location, since the
// messages of the parsers may quote parts of the fragment, unless values are allowed in errors
func TestFragmentErrorsWithoutValues(t *testing.T) {
	testCases := []struct {
		desc     string
		fileType general.FileType
		content  string
		fragment string
		want     string
		// wantWithValues is the error if values are allowed in errors
		wantWithValues string
	}{
		{
			desc:           "unknown YAML anchor",
			fileType:       general.YAML,
			content:        "a: ${FRAGMENT | fragment}\n",
			fragment:       "pw: *s3cret",
			want:           "invalid YAML fragment on line 1",
			wantWithValues: "invalid YAML fragment: yaml: line 1: unknown anchor 's3cret' referenced",
		},
		{
			desc:           "YAML syntax error",
			fileType:       general.YAML,
			content:        "a: ${FRAGMENT | fragment}\n",
			fragment:       "a: 1\nb: 2\n  pw: s3cret",
			want:           "invalid YAML fragment on line 3",
			wantWithValues: "invalid YAML fragment: yaml: line 3: mapping values are not allowed in this context",
		},
		{
			desc:           "JSON syntax error",
			fileType:       general.JSON,
			content:        `{"a": "${FRAGMENT | fragment}"}`,
			fragment:       `{"pw": s3cret}`,
			want:           "invalid JSON fragment at offset 8",
			wantWithValues: "invalid JSON fragment: invalid character 's' looking for beginning of value",
		},
		{
			desc:           "JSON key replacing a later key fragment by a value",
			fileType:       general.JSON,
			content:        `{"${FRAGMENT | fragment}": null, "${FRAGMENT_PORT | fragment}": null}`,
			fragment:       `{"${FRAGMENT_PORT | fragment}": "s3cret"}`,
			want:           "config.json: a key with a fragment must have the value null",
			wantWithValues: "a key with a fragment must have the value null",
		},
		{
			desc:           "unknown XML entity",
			fileType:       general.XML,
			content:        "<r><a>${FRAGMENT | fragment}</a></r>",
			fragment:       "<pw>\n&s3cret;</pw>",
			want:           "invalid XML fragment on line 2",
			wantWithValues: "invalid XML fragment: XML syntax error on line 2: invalid character entity &s3cret;",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			_, err := processFragment(t, tC.fileType, tC.content, tC.fragment)
			assert.ErrorContains(t, err, tC.want)
			assert.NotContains(t, err.Error(), "s3cret")
			assert.True(t, strings.HasSuffix(err.Error(), tC.want), err.Error())

			filter.SetValuesInErrors(true)
			t.Cleanup(func() { filter.SetValuesInErrors(false) })

			_, err = processFragment(t, tC.fileType, tC.content, tC.fragment)
			assert.ErrorContains(t, err, tC.wantWithValues)
		})
	}
}

// TestKeyFragmentReplacingKeyFragment checks that a key fragment whose fragment replaces a later key fragment by null
// keeps the later one a key fragment, which is merged afterwards, for YAML and JSON
func TestKeyFragmentReplacingKeyFragment(t *testing.T) {
	t.Setenv("FRAGMENT_SECOND", `{"q": 1}`)
	for fileType, testCase := range map[general.FileType]struct{ content, want string }{
		general.YAML: {"${FRAGMENT | fragment}:\n${FRAGMENT_SECOND | fragment}:\n", "q: 1\n"},
		general.JSON: {`{"${FRAGMENT | fragment}": null, "${FRAGMENT_SECOND | fragment}": null}`, "{\n  \"q\": 1\n}\n"},
	} {
		output, err := processFragment(t, fileType, testCase.content, `{"${FRAGMENT_SECOND | fragment}": null}`)
		assert.NoError(t, err, fileType)
		assert.Equal(t, testCase.want, output, fileType)
	}
}

// TestJsonKeyFragmentChain checks that the rename of a placeholder key survives several key fragments replacing its
// value one after the other, so the key is renamed once, to its processed name, and the last value stays
func TestJsonKeyFragmentChain(t *testing.T) {
	t.Setenv("FRAGMENT_FIRST", `{"${FRAGMENT_SECRET}": {"a": "${FRAGMENT_PORT}"}, "${FRAGMENT_PORT}": 1}`)
	t.Setenv("FRAGMENT_SECOND", `{"${FRAGMENT_SECRET}": "${FRAGMENT_PORT}"}`)
	t.Setenv("FRAGMENT_THIRD", `{"${FRAGMENT_SECRET}": 3}`)
	content := `{"${FRAGMENT_FIRST | fragment}": null, "${FRAGMENT_SECOND | fragment}": null, "${FRAGMENT_THIRD | fragment}": null, "${FRAGMENT_SECRET}": 0}`

	output, err := processFragment(t, general.JSON, content, "")
	assert.NoError(t, err)
	assert.Equal(t, "{\n  \"s3cret\": 3,\n  \"8080\": 1\n}\n", output)
}

// TestJsonKeyFragmentDetachedEntries checks that entries beneath a member replaced by a fragment, whose parent is no
// longer part of the document, neither fail nor write into the output
func TestJsonKeyFragmentDetachedEntries(t *testing.T) {
	for desc, testCase := range map[string]struct{ content, fragment, want string }{
		"key beneath a replaced object": {
			`{"${FRAGMENT | fragment}": null, "o": {"${FRAGMENT_SECRET}": 1}}`, `{"o": 5}`, "{\n  \"o\": 5\n}\n"},
		"value beneath a replaced object": {
			`{"${FRAGMENT | fragment}": null, "o": {"k": "${FRAGMENT_SECRET}"}}`, `{"o": "x"}`, "{\n  \"o\": \"x\"\n}\n"},
		"value beneath a replaced list": {
			`{"${FRAGMENT | fragment}": null, "o": ["${FRAGMENT_SECRET}", {"${FRAGMENT_PORT}": 1}]}`, `{"o": null}`, "{\n  \"o\": null\n}\n"},
		"key and value beneath a replaced placeholder key": {
			`{"${FRAGMENT | fragment}": null, "${FRAGMENT_PORT}": {"${FRAGMENT_SECRET}": "${FRAGMENT_SECRET}"}}`, `{"${FRAGMENT_PORT}": true}`, "{\n  \"8080\": true\n}\n"},
	} {
		output, err := processFragment(t, general.JSON, testCase.content, testCase.fragment)
		assert.NoError(t, err, desc)
		assert.Equal(t, testCase.want, output, desc)
		assert.NotContains(t, output, "s3cret", desc)
	}
}

// TestJsonKeyEntryGuards checks the internal errors for a key whose value is no longer part of its object, which
// mergeObjects prevents by moving the key entries
func TestJsonKeyEntryGuards(t *testing.T) {
	newEntry := func(value *jsonNode) *JsonConfigEntry {
		parent := &jsonNode{kind: jsonObject, keys: []string{"${X}"}, children: []*jsonNode{{kind: jsonScalar}}}
		return &JsonConfigEntry{node: value, key: "${X}", isKey: true, parent: parent, handler: NewJsonConfigFileHandler()}
	}

	assert.PanicsWithValue(t, "internal error: a JSON key containing a placeholder is no longer part of its object",
		func() { newEntry(&jsonNode{kind: jsonScalar}).SetValue("made-up") })

	entry := newEntry(&jsonNode{kind: jsonScalar})
	_, err := entry.SetFragment(filter.Fragment{Text: `{"a": 1}`})
	assert.EqualError(t, err, "internal error: the key with the fragment is no longer part of its object")
	assert.Len(t, entry.parent.keys, 1, "the object must stay unchanged")

	// Entries of the object are found by the identity of the value
	entry = newEntry(nil)
	entry.node = entry.parent.children[0]
	entry.SetValue("made-up")
	assert.Equal(t, []string{"made-up"}, entry.parent.keys)
}

// TestListFragmentWithDel checks that a list element containing DEL, which YAML and TOML only allow escaped, is inserted
// into every file type supporting lists
func TestListFragmentWithDel(t *testing.T) {
	for fileType, testCase := range map[general.FileType]struct{ content, want string }{
		general.YAML: {"b: ${FRAGMENT | split | fragment}\n", "b:\n  - \"a\\x7Fb\"\n  - c\n"},
		general.JSON: {`{"b": "${FRAGMENT | split | fragment}"}`, "{\n  \"b\": [\n    \"a\x7fb\",\n    \"c\"\n  ]\n}\n"},
		general.TOML: {"b = \"${FRAGMENT | split | fragment}\"\n", "b = [\"a\\u007fb\",\"c\"]\n"},
	} {
		output, err := processFragment(t, fileType, testCase.content, "a\x7fb,c")
		assert.NoError(t, err, fileType)
		assert.Equal(t, testCase.want, output, fileType)
	}
}
