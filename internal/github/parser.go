package github

import (
	"crypto/sha256"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tiendv89/workspace-github-adapter/internal/domain"
)

// timestampFormats lists the timestamp formats found in task/feature YAML files.
// Task YAML files use offsets without colons (e.g. +0700, +0000); RFC3339 requires
// colons (e.g. +07:00). We try both.
var timestampFormats = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05-0700", // no colon in offset
	"2006-01-02T15:04:05.999999999-0700",
	"2006-01-02T15:04:05Z0700", // Z or numeric offset
}

// parseTimestamp attempts to parse a timestamp string using known formats.
// Returns zero time if parsing fails — callers treat zero as "unknown".
func parseTimestamp(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, f := range timestampFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// workspaceYAML mirrors the relevant fields from workspace.yaml.
type workspaceYAML struct {
	Name           string     `yaml:"name"`
	Repos          []repoYAML `yaml:"repos"`
	Git            gitYAML    `yaml:"git"`
	ManagementRepo string     `yaml:"management_repo"`
}

type repoYAML struct {
	ID         string `yaml:"id"`
	BaseBranch string `yaml:"base_branch"`
}

type gitYAML struct {
	BranchPattern string `yaml:"branch_pattern"`
}

// featureStatusYAML mirrors the relevant fields from docs/features/*/status.yaml.
type featureStatusYAML struct {
	Feature       string                 `yaml:"feature"`
	FeatureID     string                 `yaml:"feature_id"`
	Title         string                 `yaml:"title"`
	Status        string                 `yaml:"status"`
	FeatureStatus string                 `yaml:"feature_status"`
	Stage         string                 `yaml:"stage"`
	CurrentStage  string                 `yaml:"current_stage"`
	NextAction    string                 `yaml:"next_action"`
	History       []activityYAML         `yaml:"history"`
	Stages        map[string]interface{} `yaml:"stages"`
}

type activityYAML struct {
	Action string `yaml:"action"`
	By     string `yaml:"by"`
	At     string `yaml:"at"`
	Note   string `yaml:"note"`
}

// taskYAML mirrors the task YAML files (docs/features/*/tasks/T*.yaml).
type taskYAML struct {
	ID            string                 `yaml:"id"`
	Title         string                 `yaml:"title"`
	Repo          string                 `yaml:"repo"`
	Status        string                 `yaml:"status"`
	DependsOn     []string               `yaml:"depends_on"`
	BlockedReason interface{}            `yaml:"blocked_reason"` // string or null
	Branch        string                 `yaml:"branch"`
	Execution     map[string]interface{} `yaml:"execution"`
	PR            map[string]interface{} `yaml:"pr"`
	WorkspacePR   map[string]interface{} `yaml:"workspace_pr"`
	Log           []activityYAML         `yaml:"log"`
}

// taskIDPattern matches task YAML filenames like T1.yaml, T23.yaml.
var taskIDPattern = regexp.MustCompile(`^T\d+$`)

// parseWorkspaceYAML parses the raw bytes of workspace.yaml.
// Returns a SourceError if the content is invalid YAML.
func parseWorkspaceYAML(data []byte, sourcePath string) (*workspaceYAML, *domain.SourceError) { //nolint:unparam
	var ws workspaceYAML
	if err := yaml.Unmarshal(data, &ws); err != nil {
		se := domain.NewParserInvalidYAMLError(sourcePath, err.Error())
		return nil, &se
	}
	return &ws, nil
}

// parseFeatureStatusYAML parses the raw bytes of a feature status.yaml.
func parseFeatureStatusYAML(data []byte, sourcePath string) (*featureStatusYAML, *domain.SourceError) {
	var fs featureStatusYAML
	if err := yaml.Unmarshal(data, &fs); err != nil {
		se := domain.NewParserInvalidYAMLError(sourcePath, err.Error())
		return nil, &se
	}
	return &fs, nil
}

// parseTaskYAML parses the raw bytes of a task YAML file.
func parseTaskYAML(data []byte, sourcePath string) (*taskYAML, *domain.SourceError) {
	var t taskYAML
	if err := yaml.Unmarshal(data, &t); err != nil {
		se := domain.NewParserInvalidYAMLError(sourcePath, err.Error())
		return nil, &se
	}
	return &t, nil
}

func (f *featureStatusYAML) featureID() string {
	return firstNonEmpty(f.FeatureID, f.Feature)
}

func (f *featureStatusYAML) featureStatus() string {
	return firstNonEmpty(f.FeatureStatus, f.Status)
}

func (f *featureStatusYAML) currentStage() string {
	return firstNonEmpty(f.CurrentStage, f.Stage)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// hashContent returns a hex SHA-256 of the given bytes for change detection.
func hashContent(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// extractFeatureID returns the feature ID from a recognized feature document path.
// Path formats:
//   - docs/features/<feature-id>/status.yaml
//   - docs/features/<feature-id>/product-spec.md
//   - docs/features/<feature-id>/technical-design.md
func extractFeatureID(featureDocPath string) (string, bool) {
	parts := strings.Split(featureDocPath, "/")
	if len(parts) != 4 || parts[0] != "docs" || parts[1] != "features" || parts[2] == "" {
		return "", false
	}
	switch parts[3] {
	case "status.yaml", "product-spec.md", "technical-design.md":
		return parts[2], true
	default:
		return "", false
	}
}

// featureDocPaths returns the expected paths for all feature documents.
func featureDocPaths(featureID string) map[string]string {
	base := "docs/features/" + featureID
	return map[string]string{
		"product_spec":     base + "/product-spec.md",
		"technical_design": base + "/technical-design.md",
		"tasks_md":         base + "/tasks.md",
		"status_yaml":      base + "/status.yaml",
	}
}

// taskFileBase extracts the task ID base (e.g. "T1") from a path like
// "docs/features/<id>/tasks/T1.yaml". Returns empty string if not a task file.
func taskFileBase(p string) string {
	base := path.Base(p)
	if !strings.HasSuffix(base, ".yaml") {
		return ""
	}
	id := strings.TrimSuffix(base, ".yaml")
	if !taskIDPattern.MatchString(id) {
		return ""
	}
	return id
}

// mapActivityLog converts a slice of activityYAML to domain.ActivityEvent.
func mapActivityLog(log []activityYAML, scope, featureID, taskID string) []domain.ActivityEvent {
	events := make([]domain.ActivityEvent, 0, len(log))
	for _, e := range log {
		events = append(events, domain.ActivityEvent{
			Action:     e.Action,
			Scope:      scope,
			Actor:      e.By,
			OccurredAt: parseTimestamp(e.At),
			Note:       e.Note,
			FeatureID:  featureID,
			TaskID:     taskID,
		})
	}
	return events
}

// blockedReasonString converts the YAML blocked_reason (string or null) to a Go string.
func blockedReasonString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// slugify converts a workspace name to a URL-safe slug.
func slugify(name string) string {
	lower := strings.ToLower(name)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	slug := re.ReplaceAllString(lower, "-")
	return strings.Trim(slug, "-")
}
