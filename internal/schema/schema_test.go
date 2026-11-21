package schema

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/denglertai/gonfig/internal/filter"
	"github.com/denglertai/gonfig/internal/general"
	"github.com/stretchr/testify/assert"
)

// writeSchema writes the schema into a temporary directory and loads it
func writeSchema(t *testing.T, content string) *Schema {
	t.Helper()

	path := filepath.Join(t.TempDir(), "schema.json")
	assert.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	loaded, err := Load(path)
	assert.NoError(t, err)
	return loaded
}

// allowValuesInErrors allows values in errors for the rest of the test
func allowValuesInErrors(t *testing.T) {
	t.Helper()

	filter.SetValuesInErrors(true)
	t.Cleanup(func() { filter.SetValuesInErrors(false) })
}

// configSchema requires a database with a host and a port and allows nothing else
const configSchema = `{
  "type": "object",
  "required": ["database"],
  "properties": {
    "database": {
      "type": "object",
      "required": ["host", "port"],
      "properties": {
        "host": {"type": "string"},
        "port": {"type": "integer", "maximum": 65535}
      },
      "additionalProperties": false
    }
  }
}`

// TestValidateFileTypes validates a valid and an invalid document of each supported file type
func TestValidateFileTypes(t *testing.T) {
	s := writeSchema(t, configSchema)

	testCases := []struct {
		fileType general.FileType
		valid    string
		invalid  string
	}{
		{general.JSON, `{"database": {"host": "db", "port": 5432}}`, `{"database": {"host": "db", "port": "5432", "user": "app"}}`},
		{general.YAML, "database:\n  host: db\n  port: 5432\n", "database:\n  host: db\n  port: '5432'\n  user: app\n"},
		{general.YML, "database: {host: db, port: 5432}\n", "database: {host: db, port: '5432', user: app}\n"},
		{general.TOML, "[database]\nhost = \"db\"\nport = 5432\n", "[database]\nhost = \"db\"\nport = \"5432\"\nuser = \"app\"\n"},
	}
	for _, tC := range testCases {
		t.Run(string(tC.fileType), func(t *testing.T) {
			violations, err := s.Validate(tC.fileType, []byte(tC.valid))
			assert.NoError(t, err)
			assert.Empty(t, violations)

			violations, err = s.Validate(tC.fileType, []byte(tC.invalid))
			assert.NoError(t, err)
			assert.Equal(t, []Violation{
				{Path: "database", Message: "additional properties 'user' not allowed"},
				{Path: "database.port", Message: "got string, want integer"},
			}, violations)
		})
	}
}

// TestValidateYamlDocuments checks that every document of a YAML file is validated and named in the paths,
// and that an empty file is validated as null
func TestValidateYamlDocuments(t *testing.T) {
	s := writeSchema(t, configSchema)

	violations, err := s.Validate(general.YAML, []byte("database: {host: db, port: 1}\n---\ndatabase: {host: db}\n---\nother: 1\n"))
	assert.NoError(t, err)
	assert.Equal(t, []string{
		"document 2: database: missing property 'port'",
		"document 3: (root): missing property 'database'",
	}, violationStrings(violations))

	violations, err = s.Validate(general.YAML, nil)
	assert.NoError(t, err)
	assert.Equal(t, []string{"(root): got null, want object"}, violationStrings(violations))
}

// violationStrings returns the violations as they are printed
func violationStrings(violations []Violation) []string {
	var lines []string
	for _, violation := range violations {
		lines = append(lines, violation.String())
	}
	return lines
}

// TestValidateNormalizes checks that YAML mappings with other keys than strings and TOML dates and times are validated
// like their text, and that the BOM of a processed file is ignored. Local times have no offset, which the format time
// requires, so their text is compared.
func TestValidateNormalizes(t *testing.T) {
	s := writeSchema(t, `{
  "properties": {
    "ports": {"type": "object", "required": ["8080"]},
    "dates": {"type": "array", "items": {"type": "string"}, "prefixItems": [
      {"format": "date-time"}, {"format": "date-time"}, {"format": "date"}, {"const": "07:32:00"}
    ]}
  }
}`)

	violations, err := s.Validate(general.YAML, []byte("\xEF\xBB\xBFports:\n  8080: http\n"))
	assert.NoError(t, err)
	assert.Empty(t, violations)

	toml := "\xEF\xBB\xBFdates = [1979-05-27T07:32:00Z, 1979-05-27T07:32:00.5-07:00, 1979-05-27, 07:32:00]\n"
	violations, err = s.Validate(general.TOML, []byte(toml))
	assert.NoError(t, err)
	assert.Empty(t, violations)

	// A local date-time has no offset, which the format date-time requires
	violations, err = s.Validate(general.TOML, []byte("dates = [1979-05-27T07:32:00]\n"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"dates.0: value is not valid date-time"}, violationStrings(violations))
}

// TestValidateErrors checks the errors for unsupported file types and content that cannot be decoded
func TestValidateErrors(t *testing.T) {
	s := writeSchema(t, `{}`)

	for fileType, content := range map[general.FileType]string{
		general.XML:  "<a/>",
		general.JSON: "{",
		general.YAML: "a: [",
		general.TOML: "a =",
	} {
		_, err := s.Validate(fileType, []byte(content))
		assert.Error(t, err, fileType)
	}

	_, err := s.Validate(general.PROPERTIES, nil)
	assert.EqualError(t, err, "files of type properties cannot be validated, only JSON, YAML, TOML, dotenv and INI files")
}

// madeUpSecret is a made-up processed value, which decode errors must not contain unless values are allowed in errors
const madeUpSecret = "made-up-s3cret"

// decodeErrorCases are contents of each format that cannot be decoded and quote the secret in the parser's message
var decodeErrorCases = []struct {
	fileType general.FileType
	content  string
	redacted string
}{
	{general.JSON, `{"a": "x"` + madeUpSecret + `}`, "invalid JSON at offset 10"},
	{general.TOML, "a = 1\nb = " + madeUpSecret + "\n", "invalid TOML on line 2"},
	{general.YAML, "a: 1\nb: [" + madeUpSecret + "\n", "invalid YAML on line 3"},
	{general.YAML, "a: 1\nx: !!int " + madeUpSecret + "\n", "invalid YAML on line 2, a value does not match its tag !!int"},
	{general.YAML, "a: 1\n---\nb:\n  c: !!bool " + madeUpSecret + "\n", "invalid YAML on line 4, a value does not match its tag !!bool"},
	{general.YML, "x: !!timestamp " + madeUpSecret + "\n", "invalid YAML on line 1, a value does not match its tag !!timestamp"},
}

// TestDecodeErrorsWithoutValues checks that decode errors name the format and location but not the content
func TestDecodeErrorsWithoutValues(t *testing.T) {
	s := writeSchema(t, `{}`)

	for _, c := range decodeErrorCases {
		_, err := s.Validate(c.fileType, []byte(c.content))
		assert.EqualError(t, err, c.redacted, c.content)
		assert.NotContains(t, err.Error(), madeUpSecret)
	}
}

// TestDecodeErrorsWithValues checks that decode errors wrap the parser's error if values are allowed in errors
func TestDecodeErrorsWithValues(t *testing.T) {
	allowValuesInErrors(t)
	s := writeSchema(t, `{}`)

	for _, c := range decodeErrorCases {
		_, err := s.Validate(c.fileType, []byte(c.content))
		parserError := errors.Unwrap(err)
		if assert.NotNil(t, parserError, c.content) {
			format, _, _ := strings.Cut(c.redacted[len("invalid "):], " ")
			assert.EqualError(t, err, "invalid "+format+": "+parserError.Error(), c.content)
		}
	}

	// The parser's message of a value that doesn't match its tag quotes the value
	_, err := s.Validate(general.YAML, []byte("x: !!int "+madeUpSecret+"\n"))
	assert.ErrorContains(t, err, madeUpSecret)
}

// TestTagMismatch checks the search for values that don't match their explicit tag, which YAML errors have no line for
func TestTagMismatch(t *testing.T) {
	line, tag := tagMismatch([]byte("a: !!str 1\nb: !!int 2\nc: !!float abc\n"))
	assert.Equal(t, 3, line)
	assert.Equal(t, "!!float", tag)

	line, _ = tagMismatch([]byte("a: !!int 1\nb: !custom abc\n"))
	assert.Zero(t, line)

	line, _ = tagMismatch([]byte("a: [\n"))
	assert.Zero(t, line)

	assert.Empty(t, yamlLocation([]byte("a: 1\n"), assert.AnError))
}

// TestRegisterDecoder checks that registered decoders validate their file type, with their errors returned
func TestRegisterDecoder(t *testing.T) {
	const fileType general.FileType = "test"
	RegisterDecoder(fileType, func(data []byte) (any, error) {
		if len(data) == 0 {
			return nil, assert.AnError
		}
		return map[string]any{"value": string(data)}, nil
	})
	t.Cleanup(func() { delete(decoders, fileType) })

	s := writeSchema(t, `{"properties": {"value": {"const": "a"}}}`)
	violations, err := s.Validate(fileType, []byte("b"))
	assert.NoError(t, err)
	assert.Equal(t, []string{"value: value must be 'a'"}, violationStrings(violations))

	_, err = s.Validate(fileType, nil)
	assert.ErrorIs(t, err, assert.AnError)
}

// TestDescribe checks that values are only part of the messages if values are allowed in errors
func TestDescribe(t *testing.T) {
	s := writeSchema(t, `{
  "properties": {
    "pattern": {"pattern": "^[a-z]+$"},
    "format": {"format": "email"},
    "minimum": {"minimum": 10},
    "maximum": {"maximum": 1.5},
    "exclusiveMinimum": {"exclusiveMinimum": 10},
    "exclusiveMaximum": {"exclusiveMaximum": 0},
    "multipleOf": {"multipleOf": 0.5},
    "enum": {"enum": ["a", "b"]}
  }
}`)
	document := []byte(`{"pattern": "S3cret", "format": "secret", "minimum": 5, "maximum": 100000, "exclusiveMinimum": 10,` +
		` "exclusiveMaximum": 0.25, "multipleOf": 0.3, "enum": "c"}`)

	violations, err := s.Validate(general.JSON, document)
	assert.NoError(t, err)
	assert.Equal(t, []string{
		"enum: value must be one of 'a', 'b'",
		"exclusiveMaximum: value must be < 0",
		"exclusiveMinimum: value must be > 10",
		"format: value is not valid email",
		"maximum: value must be <= 1.5",
		"minimum: value must be >= 10",
		"multipleOf: value must be a multiple of 0.5",
		"pattern: value does not match pattern '^[a-z]+$'",
	}, violationStrings(violations))

	allowValuesInErrors(t)
	violations, err = s.Validate(general.JSON, document)
	assert.NoError(t, err)
	assert.Equal(t, []string{
		"enum: value must be one of 'a', 'b'",
		"exclusiveMaximum: value must be < 0, got 0.25",
		"exclusiveMinimum: value must be > 10, got 10",
		"format: 'secret' is not valid email: missing @",
		"maximum: value must be <= 1.5, got 100000",
		"minimum: value must be >= 10, got 5",
		"multipleOf: value must be a multiple of 0.5, got 0.3",
		"pattern: 'S3cret' does not match pattern '^[a-z]+$'",
	}, violationStrings(violations))
}

// TestCheck checks the error of Check for a file that does not match the schema and for content that cannot be decoded
func TestCheck(t *testing.T) {
	s := writeSchema(t, configSchema)

	assert.NoError(t, s.Check("app.json", general.JSON, []byte(`{"database": {"host": "db", "port": 1}}`)))

	err := s.Check("app.json", general.JSON, []byte(`{"database": {"port": 70000}}`))
	var schemaError *Error
	if assert.ErrorAs(t, err, &schemaError) {
		assert.Len(t, schemaError.Violations, 2)
	}
	assert.Regexp(t, `^app\.json does not match the schema \S+schema\.json:
  - database: missing property 'host'
  - database\.port: value must be <= 65535$`, err.Error())

	assert.ErrorContains(t, s.Check("app.json", general.JSON, []byte("{")), "failed to validate app.json against the schema ")
}

// TestLoadErrors checks the errors of schemas that cannot be loaded, including remote references, which are never fetched
func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"invalid.json": "{",
		"type.json":    `{"type": 5}`,
		"remote.json":  `{"$ref": "https://example.com/schema.json"}`,
		"missing.json": `{"$ref": "missing-ref.json"}`,
	} {
		assert.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}

	_, err := Load(filepath.Join(dir, "invalid.json"))
	assert.ErrorContains(t, err, "invalid.json: invalid JSON: ")
	_, err = Load(filepath.Join(dir, "type.json"))
	assert.ErrorContains(t, err, "is not valid against metaschema")
	_, err = Load(filepath.Join(dir, "remote.json"))
	assert.ErrorContains(t, err, `failing loading "https://example.com/schema.json"`)
	_, err = Load(filepath.Join(dir, "missing.json"))
	assert.ErrorContains(t, err, "missing-ref.json")
	_, err = Load(filepath.Join(dir, "none.json"))
	assert.ErrorContains(t, err, "failed to load the schema: open ")
}

// TestTagMismatchLocations checks the line reported for values that don't match their explicit tag in different
// positions of the content, and that valid or ignored tags are not reported
func TestTagMismatchLocations(t *testing.T) {
	s := writeSchema(t, `{}`)
	const secret = madeUpSecret
	mismatch := func(line int, tag string) string {
		return "invalid YAML on line " + strconv.Itoa(line) + ", a value does not match its tag " + tag
	}

	for _, c := range []struct {
		name    string
		content string
		want    string
	}{
		{"flow mapping", "a: 1\nb: {x: !!int " + secret + "}\n", mismatch(2, "!!int")},
		{"flow sequence", "a: [1,\n  !!float " + secret + "]\n", mismatch(2, "!!float")},
		{"block sequence", "a:\n  - 1\n  - !!bool " + secret + "\n", mismatch(3, "!!bool")},
		{"nested", "a:\n  b:\n    - c: !!timestamp " + secret + "\n", mismatch(3, "!!timestamp")},
		{"after valid tagged value", "a: !!int 1\nb: !!float 1.5\nc: !!bool true\nd: !!null ~\ne: !!int " + secret + "\n", mismatch(5, "!!int")},
		{"null", "a: !!null " + secret + "\n", mismatch(1, "!!null")},
		{"anchor", "a: &x !!int " + secret + "\nb: *x\n", mismatch(1, "!!int")},
		{"CRLF", "a: 1\r\nb: !!int " + secret + "\r\n", mismatch(2, "!!int")},
		{"BOM", "\ufeffa: 1\nb: !!int " + secret + "\n", mismatch(2, "!!int")},
		{"comments and blank lines", "# c\n\na: 1 # x\n\nb: !!int " + secret + "\n", mismatch(5, "!!int")},
		{"second document", "a: 1\n---\nb: 2\n---\nc: !!int " + secret + "\n", mismatch(5, "!!int")},
		{"multiline value", "a: |\n  x\n  y\nb: !!int " + secret + "\n", mismatch(4, "!!int")},
		{"first of two", "a: !!int " + secret + "\nb: !!int " + secret + "\n", mismatch(1, "!!int")},
	} {
		_, err := s.Validate(general.YAML, []byte(c.content))
		assert.EqualError(t, err, c.want, c.name)
	}

	// Values that match their tag, ignored tags and tags the parser doesn't resolve are not decode errors
	for _, content := range []string{
		"a: !!int 12\nb: !!float 1e3\nc: !!bool true\nd: !!null null\ne: !!timestamp 2024-01-02\n",
		"a: !!str " + secret + "\nb: !custom " + secret + "\n",
		"a: !!int 1\n---\nb: !!str 2\n",
		"a: &x !!int 1\nb: *x\n",
	} {
		_, err := s.Validate(general.YAML, []byte(content))
		assert.NoError(t, err, content)
	}
}

// TestTagMismatchKeys documents that explicit tags on keys are searched as well as tags on values
func TestTagMismatchKeys(t *testing.T) {
	line, tag := tagMismatch([]byte("a: 1\n!!int " + madeUpSecret + ": 1\n"))
	assert.Equal(t, 2, line)
	assert.Equal(t, "!!int", tag)

	line, tag = tagMismatch([]byte("{!!int " + madeUpSecret + ": 1}\n"))
	assert.Equal(t, 1, line)
	assert.Equal(t, "!!int", tag)
}

// TestTagMismatchNoFalsePositives checks that values the parser accepts for their tag are not reported
func TestTagMismatchNoFalsePositives(t *testing.T) {
	s := writeSchema(t, `{}`)
	for _, content := range []string{
		"a: !!bool true\nb: !!bool False\nc: !!int 0x1F\nd: !!int 0o17\ne: !!int 1_000\nf: !!float .inf\ng: !!float .nan\nh: !!int -5\n",
		"a: !!timestamp 2024-01-02T03:04:05Z\nb: !!timestamp 2024-01-02 03:04:05\nc: !!null \"\"\nd: !!null\n",
		"a: !!int '7'\nb: !!float \"1.5\"\nc: !!bool 'true'\n",
	} {
		_, err := s.Validate(general.YAML, []byte(content))
		assert.NoError(t, err, content)
		line, _ := tagMismatch([]byte(content))
		assert.Zero(t, line, content)
	}
}
