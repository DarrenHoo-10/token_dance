package teammetrics

import (
	"encoding/json"
	"time"
)

const RuleVersion = "6"

const (
	KindUsage    = "usage"
	KindActivity = "activity"
	KindCost     = "cost"
	KindSkill    = "skill"
)

// AfterPersonalBeforeTeam is a test failpoint invoked after occupancy is confirmed
// and before static rows are written. Returning an error rolls back the caller tx.
var AfterPersonalBeforeTeam func() error

type Contributor struct {
	TeamID         string
	UserID         string
	ContributorKey string
	MembershipID   *string
	RetiredAtMs    *int64
	CreatedAtMs    int64
	UpdatedAtMs    int64
}

type DayRow struct {
	TeamID          string
	ContributorKey  string
	MetricDate      string
	RowKey          string
	MetricKind      string
	AgentID         *string
	ProviderID      *string
	ModelID         *string
	Currency        *string
	SkillID         *int64
	PublicName      string
	TokenExact      string
	TokenDerived    string
	UsageEventCount string
	ReportedCost    string
	EstimatedCost   string
	SkillUseCount   string
	SkillStats      json.RawMessage
	Resources       json.RawMessage
	Activity        json.RawMessage
	Hourly          json.RawMessage
	Quality         json.RawMessage
	MaxReceivedAt   *time.Time
	RuleVersion     string
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

func decOrZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}
