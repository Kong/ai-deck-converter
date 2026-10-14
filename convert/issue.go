package convert

import (
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/Kong/ai-deck-converter/internal/aimap"
	"github.com/Kong/ai-deck-converter/internal/kong"
)

// ConversionIssueCode identifies the class of a ConversionIssue. Codes are
// public API. Callers must handle an unknown code by its severity.
type ConversionIssueCode string

// Conversion issue codes.
const (
	MissingRequiredConfigField ConversionIssueCode = "MISSING_REQUIRED_CONFIG_FIELD"
	UndefinedReference         ConversionIssueCode = "UNDEFINED_REFERENCE"
	ConfigFieldOverridden      ConversionIssueCode = "CONFIG_FIELD_OVERRIDDEN"
	ConfigFieldDropped         ConversionIssueCode = "CONFIG_FIELD_DROPPED"
	ConfigFieldIgnored         ConversionIssueCode = "CONFIG_FIELD_IGNORED"
	ConfigFieldInvalid         ConversionIssueCode = "CONFIG_FIELD_INVALID"
	BehaviorMayDiffer          ConversionIssueCode = "BEHAVIOR_MAY_DIFFER"
	InternalError              ConversionIssueCode = "INTERNAL_ERROR"

	UnsupportedInMinRuntimeVersion ConversionIssueCode = "UNSUPPORTED_IN_MIN_RUNTIME_VERSION"
)

// ConversionIssueSeverity is the level of a ConversionIssue.
type ConversionIssueSeverity string

// Conversion issue severities.
const (
	// Error means the converter could not work around the issue. The output
	// is invalid or diverges from the user intent.
	Error ConversionIssueSeverity = "ERROR"
	// Warning means the converter worked around the issue. The output is
	// valid but may not fully behave as the user intended.
	Warning ConversionIssueSeverity = "WARNING"
)

// ConversionIssueSource describes the source of an issue found during
// conversion.
type ConversionIssueSource struct {
	// Kind names what caused the issue, for example "model" or "agent". It
	// is usually the singular form of a top-level document key. A kind can
	// also be something else, for example "option".
	Kind string `json:"kind" yaml:"kind"`

	// Name is the entity name that caused the issue, for example, "gpt-5".
	Name string `json:"name" yaml:"name"`

	// Fields are the paths of the fields that caused the issue. Each path is
	// relative to the entity, for example, ["targets[0].provider", "type"].
	Fields []string `json:"fields,omitempty" yaml:"fields,omitempty"`
}

// ConversionIssue describes one issue found during conversion.
type ConversionIssue struct {
	// Code identifies the class of the issue.
	Code ConversionIssueCode `json:"code" yaml:"code"`

	// Reason tells why the converter reported the issue. It is not API.
	Reason string `json:"reason" yaml:"reason"`

	// Source is the entity that caused the issue.
	Source ConversionIssueSource `json:"source" yaml:"source"`

	// Severity is the level of the issue.
	Severity ConversionIssueSeverity `json:"severity" yaml:"severity"`
}

// String formats the issue as one line for logs and the CLI.
func (i ConversionIssue) String() string {
	s := fmt.Sprintf("%s %s %s %q", i.Severity, i.Code, i.Source.Kind, i.Source.Name)
	if len(i.Source.Fields) > 0 {
		s += fmt.Sprintf(" %v", i.Source.Fields)
	}
	return s + ": " + i.Reason
}

// ConversionResult is the outcome of a conversion run.
type ConversionResult struct {
	// Output is the Kong document that the conversion generates. It is never
	// nil. When every entity fails, Output is an empty document.
	Output *kong.Document

	// Metadata identifies the AI Gateway source of the generated entities.
	Metadata ConversionMetadata

	// Issues holds every issue found during conversion.
	// The caller decides how to handle them.
	Issues []ConversionIssue
}

// HasErrors reports whether any issue has the Error severity.
func (r *ConversionResult) HasErrors() bool {
	return slices.ContainsFunc(r.Issues, func(i ConversionIssue) bool { return i.Severity == Error })
}

// Source.Kind values. The values are public API.
const (
	kindModel            = "model"
	kindMCPServer        = "mcp_server"
	kindAgent            = "agent"
	kindPolicy           = "policy"
	kindCustomPolicy     = "custom_policy"
	kindAuthStrategy     = "auth_strategy"
	kindConsumer         = "consumer"
	kindConsumerGroup    = "consumer_group"
	kindSNI              = "sni"
	kindConversionOption = "option"
)

// errSkipEntity tells an entity wrapper to leave out the current entity.
// The issue that explains the skip is already recorded.
var errSkipEntity = errors.New("entity skipped")

func issueSource(kind, name string) ConversionIssueSource {
	return ConversionIssueSource{Kind: kind, Name: name}
}

// at returns a copy of s with fields as its field paths.
func (s ConversionIssueSource) at(fields ...string) ConversionIssueSource {
	s.Fields = fields
	return s
}

// report records an issue. An issue equal to a recorded issue is not
// recorded again.
func (c *Converter) report(
	src ConversionIssueSource, severity ConversionIssueSeverity, code ConversionIssueCode,
	format string, args ...any,
) {
	issue := ConversionIssue{
		Code:     code,
		Reason:   fmt.Sprintf(format, args...),
		Source:   src,
		Severity: severity,
	}
	for _, existing := range c.issues {
		if existing.Code == issue.Code && existing.Severity == issue.Severity &&
			existing.Reason == issue.Reason && existing.Source.Kind == issue.Source.Kind &&
			existing.Source.Name == issue.Source.Name && slices.Equal(existing.Source.Fields, issue.Source.Fields) {
			return
		}
	}
	c.issues = append(c.issues, issue)
}

// warn records a Warning issue.
func (c *Converter) warn(src ConversionIssueSource, code ConversionIssueCode, format string, args ...any) {
	c.report(src, Warning, code, format, args...)
}

// reportError records an Error issue. The conversion of the entity continues.
func (c *Converter) reportError(src ConversionIssueSource, code ConversionIssueCode, format string, args ...any) {
	c.report(src, Error, code, format, args...)
}

// fail records an Error issue and returns errSkipEntity.
func (c *Converter) fail(src ConversionIssueSource, code ConversionIssueCode, format string, args ...any) error {
	c.report(src, Error, code, format, args...)
	return errSkipEntity
}

// supports reports whether the minimum runtime version has f.
func (c *Converter) supports(f aimap.Feature) bool {
	return aimap.Supports(f, c.minRuntimeVersion)
}

// reportUnsupported records an UnsupportedInMinRuntimeVersion issue for the
// field of src that needs f.
func (c *Converter) reportUnsupported(
	src ConversionIssueSource, severity ConversionIssueSeverity, f aimap.Feature, field string,
) {
	c.report(src, severity, UnsupportedInMinRuntimeVersion, "%s",
		unsupportedReason(f, field, c.minRuntimeVersion))
}

// failUnsupported records an Error UnsupportedInMinRuntimeVersion issue and
// returns errSkipEntity.
func (c *Converter) failUnsupported(src ConversionIssueSource, f aimap.Feature, field string) error {
	c.reportUnsupported(src, Error, f, field)
	return errSkipEntity
}

func unsupportedReason(f aimap.Feature, field string, version aimap.Version) string {
	return fmt.Sprintf("%s requires minimum runtime version %q, but the configured minimum is %q",
		field, aimap.Since(f), version)
}

// guard runs fn for the entity src. It changes a panic or an unexpected error
// into an InternalError issue. It returns errSkipEntity when fn fails.
func (c *Converter) guard(src ConversionIssueSource, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			c.reportError(src.at(), InternalError, "converter panic: %v | %s", r, oneLine(debug.Stack()))
			err = errSkipEntity
		}
	}()
	if c.beforeEntity != nil {
		c.beforeEntity(src)
	}
	if err := fn(); err != nil {
		if !errors.Is(err, errSkipEntity) {
			c.reportError(src.at(), InternalError, "%v", err)
		}
		return errSkipEntity
	}
	return nil
}

// convertEntity runs fn for the entity src. When fn fails, the output
// document returns to its state before fn. The copy of the document is
// shallow, so fn must not change an element of a c.out slice in place before
// a step that can fail.
func (c *Converter) convertEntity(src ConversionIssueSource, fn func() error) {
	snapshot := *c.out
	if err := c.guard(src, fn); err != nil {
		*c.out = snapshot
	}
}

// oneLine joins a multi-line stack trace into one line with " | ". A
// ConversionIssue prints as one stderr line; an embedded stack trace must
// not break that.
func oneLine(stack []byte) string {
	lines := strings.Split(strings.TrimSpace(string(stack)), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, " | ")
}
